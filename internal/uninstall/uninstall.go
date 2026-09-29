// Package uninstall removes what ach itself put on the machine: shared
// settings links, the files it keeps in its config directory and its binary.
// It never touches account homes, credentials, sessions, project
// .agent-handoffs directories, the user's status line, or the backups
// sharing made of each account's original settings.
package uninstall

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"

	"github.com/tsai41/agent-conversation-handoff/internal/registry"
)

const (
	registryName  = "accounts.json"
	authCacheName = "auth-cache.json"
	usageName     = "usage"
	asideSuffix   = ".uninstall-link"
)

type Options struct {
	RegistryPath string
	// Execute is false for the default dry-run, which prints the plan and
	// changes nothing.
	Execute bool
	Out     io.Writer
	// Executable and TempDir are injectable so tests never point at the test
	// binary or the real temp directory.
	Executable func() (string, error)
	TempDir    string
}

type registryState int

const (
	registryOK registryState = iota
	registryMissing
	registryUnreadable
)

type run struct {
	Options
	dir, regPath string
	failed       int
	manual       []string
	// keepRegistry leaves the registry, and the binary that reads it, in
	// place so a re-run can still find the accounts.
	keepRegistry bool
	// configDone is false when step 3 did not get as far as deciding which
	// files to delete, so the directory must not be finished off either.
	configDone bool
	planned    map[string]bool
	// restored holds entry paths a dry-run plans to put back from an aside
	// link, so the preview can still show them being replaced by a copy.
	restored map[string]bool
}

// rename and removeFile are swapped in tests to inject failures.
var (
	rename     = os.Rename
	removeFile = os.Remove
)

// Refused is returned by Run when it declined to touch anything, as opposed
// to a count of items that failed.
const Refused = -1

// Run performs (or previews) the uninstall and returns how many items
// failed, or Refused. A failure never stops the remaining items.
func Run(o Options) int {
	if o.Executable == nil {
		o.Executable = invokedPath
	}
	if o.TempDir == "" {
		o.TempDir = os.TempDir()
	}
	u := &run{Options: o, planned: map[string]bool{}, restored: map[string]bool{}}

	if filepath.Base(o.RegistryPath) != registryName {
		u.say("拒絕執行：--registry 必須指向 %s，收到的是 %s", registryName, o.RegistryPath)
		return Refused
	}
	dir, err := filepath.Abs(filepath.Dir(o.RegistryPath))
	if err != nil {
		u.say("拒絕執行：無法解析 %s：%s", o.RegistryPath, err)
		return Refused
	}
	u.dir = dir
	u.regPath = filepath.Join(dir, registryName)

	_, statErr := os.Stat(dir)
	if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
		u.say("拒絕執行：無法確認設定目錄 %s：%s。沒有做任何變更。", dir, statErr)
		return Refused
	}
	if statErr == nil {
		if reason := u.configDirRefusal(); reason != "" {
			u.say("拒絕執行：不處理 %s，因為%s。沒有做任何變更。", dir, reason)
			return Refused
		}
	}

	if o.Execute {
		u.say("執行解除安裝：")
	} else {
		u.say("預覽模式：以下動作都不會執行，確認後加上 --yes 才會實際進行。")
	}

	if o.Execute && statErr == nil {
		if err := registry.WithLock(u.regPath, func() error { u.steps(); return nil }); err != nil {
			u.fail("無法鎖定 registry：%s", err)
			u.keepRegistry = true
		}
	} else {
		u.steps()
	}
	u.finishConfigDir()

	u.say("\n步驟 4：刪除 ach 執行檔")
	if u.keepRegistry {
		u.say("  略過：registry 保留時也保留執行檔，才能重跑")
	} else {
		u.binary()
	}

	u.footer()
	return u.failed
}

// configDirRefusal returns why the config directory must not be touched:
// it is the home directory, an ancestor of it, or holds an account home. It
// runs before anything else, because even the registry lock creates a file
// in that directory.
func (u *run) configDirRefusal() string {
	dir := u.dir
	if home, err := os.UserHomeDir(); err == nil && (dir == "/" || dir == home || inside(home, dir)) {
		return "它是家目錄或其上層"
	}
	if reg, state, _ := readRegistry(u.regPath); state == registryOK {
		for _, account := range resolveHomes(reg).Accounts {
			if inside(account.Home, dir) {
				return "帳號目錄 " + account.Home + " 在裡面"
			}
		}
	}
	return ""
}

// steps runs steps 1 to 3, under the registry lock when executing.
func (u *run) steps() {
	reg, state, err := readRegistry(u.regPath)
	switch state {
	case registryMissing:
		u.say("\n沒有 registry，沒有共用設定要處理")
	case registryUnreadable:
		u.say("\n無法讀取 registry（%s），略過步驟 1、2。", err)
		u.manual = append(u.manual, "共用設定的 symlink 與共用前的原始設定備份未處理，因為 registry 無法讀取")
		u.keepRegistry = true
	default:
		reg = resolveHomes(reg)
		u.say("\n步驟 1：把共用設定換回實體複本")
		before := u.failed
		u.leftovers(reg)
		u.unshare(reg)
		if u.failed > before {
			u.keepRegistry = true
		}
		u.say("\n步驟 2：列出共用前的原始設定備份（不刪除）")
		u.backups(reg)
	}

	u.say("\n步驟 3：刪除設定目錄裡 ach 自己的檔案")
	u.configDir(state)
}

func readRegistry(path string) (registry.Registry, registryState, error) {
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return registry.Registry{}, registryMissing, nil
	}
	reg, err := registry.Peek(path)
	if err != nil {
		return registry.Registry{}, registryUnreadable, err
	}
	return reg, registryOK, nil
}

// notRunningError reports that the path ach was invoked by is not the file
// that is running, so neither can be trusted as the thing to delete.
type notRunningError struct{ invoked, running string }

func (e *notRunningError) Error() string {
	return e.invoked + " is not the running executable " + e.running
}

func invokedPath() (string, error) {
	argv0 := ""
	if len(os.Args) > 0 {
		argv0 = os.Args[0]
	}
	return resolveInvoked(argv0, exec.LookPath, os.Executable)
}

// resolveInvoked returns the absolute path argv0 names, but only when it is
// the same file as the running executable, following symlinks on both.
func resolveInvoked(argv0 string, lookPath func(string) (string, error), executable func() (string, error)) (string, error) {
	running, err := executable()
	if err != nil {
		return "", err
	}
	if argv0 == "" {
		return running, nil
	}
	name := argv0
	if !strings.ContainsRune(name, filepath.Separator) {
		found, err := lookPath(name)
		if err != nil {
			return running, nil
		}
		name = found
	}
	invoked, err := filepath.Abs(name)
	if err != nil {
		return "", err
	}
	invokedInfo, invokedErr := os.Stat(invoked)
	runningInfo, runningErr := os.Stat(running)
	if invokedErr != nil || runningErr != nil || !os.SameFile(invokedInfo, runningInfo) {
		return "", &notRunningError{invoked: invoked, running: running}
	}
	return invoked, nil
}

func (u *run) say(format string, args ...any) {
	fmt.Fprintf(u.Out, format+"\n", args...)
}

// act runs fn when executing, and only announces it in a dry-run.
func (u *run) act(desc string, fn func() error) bool {
	if !u.Execute {
		u.say("  將會：%s", desc)
		return true
	}
	if err := fn(); err != nil {
		u.failed++
		u.say("  ✗ %s 失敗：%s", desc, err)
		return false
	}
	u.say("  ✓ %s", desc)
	return true
}

func (u *run) fail(format string, args ...any) {
	u.failed++
	u.say("  ✗ "+format, args...)
}

func resolveHomes(reg registry.Registry) registry.Registry {
	accounts := make([]registry.Account, len(reg.Accounts))
	for i, account := range reg.Accounts {
		if home, err := registry.ResolveHome(account.Home); err == nil {
			account.Home = home
		}
		accounts[i] = account
	}
	reg.Accounts = accounts
	return reg
}

// leftovers finishes or undoes a directory swap an earlier run left halfway:
// the link moved aside to <entry>.uninstall-link, and temp copies named
// .<entry>.uninstall-<n>.
//
// Only non-primary homes are searched: sharing never replaces the primary
// account's entries, so a match there is not ach's.
func (u *run) leftovers(reg registry.Registry) {
	temp := regexp.MustCompile(`^\.(.+)\.uninstall-\d+$`)
	for _, account := range reg.Accounts {
		primary, ok := registry.PrimaryAccount(reg, account.Provider)
		if !ok || primary.ID == account.ID || registry.SameFile(primary.Home, account.Home) {
			continue
		}
		names, err := os.ReadDir(account.Home)
		if err != nil {
			if !os.IsNotExist(err) {
				u.fail("%s：%s", account.Home, err)
			}
			continue
		}
		shared := map[string]bool{}
		for _, entry := range registry.SharedEntries(account.Provider) {
			shared[entry.Name] = true
			u.leftoverAside(registry.EntryPath(account, entry))
		}
		for _, name := range names {
			m := temp.FindStringSubmatch(name.Name())
			if m == nil {
				continue
			}
			path := filepath.Join(account.Home, name.Name())
			if !shared[m[1]] {
				u.say("  略過 %s：%s 不是共用設定的項目", path, m[1])
				u.manual = append(u.manual, path+" 看似中斷留下的暫存複本，但不是共用設定的項目，確認後自行處理")
				continue
			}
			u.act("刪除上次中斷留下的暫存複本 "+path, func() error { return forceRemoveAll(path) })
		}
	}
}

func (u *run) leftoverAside(path string) {
	aside := path + asideSuffix
	asideInfo, err := os.Lstat(aside)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		u.fail("%s：%s", aside, err)
		return
	}
	if asideInfo.Mode()&os.ModeSymlink == 0 {
		u.say("  略過 %s：不是連結", aside)
		u.manual = append(u.manual, aside+" 不是 ach 留下的連結，確認後自行處理")
		return
	}
	info, err := os.Lstat(path)
	switch {
	case os.IsNotExist(err):
		if u.act(fmt.Sprintf("把上次中斷時移開的連結 %s 放回 %s", aside, path), func() error { return os.Rename(aside, path) }) && !u.Execute {
			u.restored[path] = true
		}
	case err != nil:
		u.fail("%s：%s", path, err)
	case info.Mode()&os.ModeSymlink == 0:
		u.act("刪除上次換成實體複本後沒刪掉的連結 "+aside, func() error { return os.Remove(aside) })
	default:
		u.say("  略過 %s：%s 也還是連結", aside, path)
		u.manual = append(u.manual, fmt.Sprintf("%s 與 %s 都是連結，確認後自行刪除多的那個", aside, path))
	}
}

func (u *run) unshare(reg registry.Registry) {
	for _, account := range reg.Accounts {
		primary, ok := registry.PrimaryAccount(reg, account.Provider)
		if !ok || primary.ID == account.ID || registry.SameFile(primary.Home, account.Home) {
			continue
		}
		for _, entry := range registry.SharedEntries(account.Provider) {
			u.unshareEntry(primary, account, entry)
		}
	}
}

func (u *run) unshareEntry(primary, account registry.Account, entry registry.SettingsEntry) {
	path := registry.EntryPath(account, entry)
	info, err := os.Lstat(path)
	primaryPath := registry.EntryPath(primary, entry)
	switch {
	case os.IsNotExist(err) && u.restored[path]:
		u.say("  將會：放回連結後，把 %s 換成 %s 的實體複本", path, primaryPath)
		return
	case os.IsNotExist(err):
		u.say("  略過 %s：不存在", path)
		return
	case err != nil:
		u.fail("%s：%s", path, err)
		return
	case info.Mode()&os.ModeSymlink == 0:
		u.say("  略過 %s：已是實體檔案", path)
		return
	}

	primaryInfo, err := os.Stat(primaryPath)
	if err != nil && !os.IsNotExist(err) {
		u.fail("%s：%s", primaryPath, err)
		return
	}
	if err != nil {
		link, linkErr := os.Readlink(path)
		if linkErr != nil {
			u.fail("%s：%s", path, linkErr)
			return
		}
		if !filepath.IsAbs(link) {
			link = filepath.Join(filepath.Dir(path), link)
		}
		if filepath.Clean(link) != filepath.Clean(primaryPath) {
			u.say("  略過 %s：連到別處，保持原狀", path)
			return
		}
		u.say("  略過 %s：主要帳號沒有對應的 %s，保留連結", path, primaryPath)
		u.manual = append(u.manual, fmt.Sprintf("%s 仍指向不存在的 %s，需自行處理", path, primaryPath))
		return
	}
	state, err := registry.ClassifyEntry(primary, account, entry)
	if err != nil {
		u.fail("%s：%s", path, err)
		return
	}
	if state != registry.EntryShared {
		u.say("  略過 %s：連到別處，保持原狀", path)
		return
	}
	if primaryInfo.IsDir() != (entry.Kind == registry.EntryDir) {
		u.fail("%s：主要帳號的 %s 種類不符，保留連結", path, primaryPath)
		return
	}
	u.act(fmt.Sprintf("把 %s 換成 %s 的實體複本", path, primaryPath), func() error {
		return replaceWithCopy(primaryPath, path, entry.Kind)
	})
}

// replaceWithCopy builds the copy beside path and swaps it in. A file swap
// is one rename over the link. A directory cannot be renamed over a link,
// so the link is first renamed aside to path+".uninstall-link", which keeps
// the entry present at every point: an interrupted run leaves the link at
// path or aside, and the next run's leftovers pass puts it back or removes
// it.
func replaceWithCopy(src, path string, kind registry.EntryKind) error {
	dir := filepath.Dir(path)
	pattern := "." + filepath.Base(path) + ".uninstall-*"
	if kind == registry.EntryFile {
		temp, err := os.CreateTemp(dir, pattern)
		if err != nil {
			return err
		}
		tempName := temp.Name()
		temp.Close()
		if err := copyFile(src, tempName); err != nil {
			os.Remove(tempName)
			return err
		}
		if err := rename(tempName, path); err != nil {
			os.Remove(tempName)
			return err
		}
		return nil
	}

	aside := path + asideSuffix
	if _, err := os.Lstat(aside); !os.IsNotExist(err) {
		return fmt.Errorf("%s already exists", aside)
	}
	tempName, err := os.MkdirTemp(dir, pattern)
	if err != nil {
		return err
	}
	if err := copyTree(src, tempName); err != nil {
		forceRemoveAll(tempName)
		return err
	}
	if err := rename(path, aside); err != nil {
		forceRemoveAll(tempName)
		return err
	}
	if err := rename(tempName, path); err != nil {
		forceRemoveAll(tempName)
		if restoreErr := os.Rename(aside, path); restoreErr != nil {
			return fmt.Errorf("%w; restoring the link from %s also failed: %v", err, aside, restoreErr)
		}
		return err
	}
	return os.Remove(aside)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, info.Mode().Perm())
}

type dirMode struct {
	path string
	mode fs.FileMode
}

// copyTree copies src into the existing directory dst. Directories stay
// 0700 while copying so a failed copy can always be removed; the source
// modes are applied only after everything is copied, deepest first. A
// relative symlink is rewritten to an absolute target resolved against its
// source directory, so the copy points at the same file.
func copyTree(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}
	dirs := []dirMode{{dst, srcInfo.Mode().Perm()}}
	if err := copyEntries(src, dst, &dirs); err != nil {
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := os.Chmod(dirs[i].path, dirs[i].mode); err != nil {
			return err
		}
	}
	return nil
}

func copyEntries(src, dst string, dirs *[]dirMode) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		from := filepath.Join(src, e.Name())
		to := filepath.Join(dst, e.Name())
		info, err := e.Info()
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(from)
			if err != nil {
				return err
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(from), target)
			}
			if err := os.Symlink(target, to); err != nil {
				return err
			}
		case info.IsDir():
			if err := os.Mkdir(to, 0o700); err != nil {
				return err
			}
			*dirs = append(*dirs, dirMode{to, info.Mode().Perm()})
			if err := copyEntries(from, to, dirs); err != nil {
				return err
			}
		case info.Mode().IsRegular():
			if err := copyFile(from, to); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%s is not a regular file, directory or symlink", from)
		}
	}
	return nil
}

// forceRemoveAll removes a tree this package created, first making every
// directory in it writable in case its final modes were already applied.
func forceRemoveAll(path string) error {
	filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			os.Chmod(p, 0o700)
		}
		return nil
	})
	return os.RemoveAll(path)
}

// backups lists the .bak-<time> copies share-settings and trust sync made.
// They hold each account's own settings from before sharing, so they are
// the user's data and are never deleted.
func (u *run) backups(reg registry.Registry) {
	type target struct{ dir, base string }
	var targets []target
	for _, account := range reg.Accounts {
		for _, entry := range registry.SharedEntries(account.Provider) {
			targets = append(targets, target{account.Home, entry.Name})
		}
		if account.Provider == "claude" {
			if file, err := registry.ClaudeConfigFile(account); err == nil {
				targets = append(targets, target{filepath.Dir(file), filepath.Base(file)})
			}
		}
	}

	listed := map[string][]os.DirEntry{}
	seen := map[string]bool{}
	var found []string
	for _, t := range targets {
		names, ok := listed[t.dir]
		if !ok {
			var err error
			names, err = os.ReadDir(t.dir)
			if err != nil && !os.IsNotExist(err) {
				u.fail("%s：%s", t.dir, err)
			}
			listed[t.dir] = names
		}
		for _, name := range names {
			path := filepath.Join(t.dir, name.Name())
			if seen[path] || !registry.IsBackupName(t.base, name.Name()) {
				continue
			}
			seen[path] = true
			found = append(found, path)
			u.say("  保留 %s", path)
		}
	}
	if len(found) == 0 {
		u.say("  沒有備份")
		return
	}
	u.manual = append(u.manual, "共用設定前的原始設定備份（帳號在共用前自己的設定，ach 不會刪除，確認不需要後再自行刪除）：\n      "+strings.Join(found, "\n      "))
}

// configDir deletes only the files ach keeps in the registry's directory,
// so a directory shared with anything else loses nothing but those.
func (u *run) configDir(state registryState) {
	dir := u.dir
	if _, err := os.Lstat(dir); os.IsNotExist(err) {
		u.say("  設定目錄不存在，略過：%s", dir)
		return
	}
	names, err := os.ReadDir(dir)
	if err != nil {
		u.fail("%s：%s", dir, err)
		return
	}
	u.configDone = true

	for _, name := range names {
		if !strings.HasPrefix(name.Name(), registryName+".corrupt-") {
			continue
		}
		path := filepath.Join(dir, name.Name())
		if u.keepRegistry {
			u.say("  保留損壞 registry 的備份 %s：registry 保留時一併保留", path)
			u.manual = append(u.manual, "損壞 registry 的備份 "+path+"（修復 registry 時可能用得到，之後再自行刪除）")
			continue
		}
		u.remove(path, "刪除損壞 registry 的備份", os.Remove)
	}
	if authCache := filepath.Join(dir, authCacheName); exists(authCache) {
		u.remove(authCache, "刪除登入狀態快取", os.Remove)
	}

	defaultUsage := filepath.Join(dir, usageName)
	usageDir := registry.PeekUsageDir(u.regPath)
	switch {
	case state == registryMissing:
	case usageDir != "" && !inside(usageDir, dir):
		u.say("  用量資料目錄在設定目錄之外，不刪除：%s", usageDir)
		u.manual = append(u.manual, "用量資料目錄 "+usageDir+"（不在設定目錄內，需要的話自行刪除）")
	case usageDir != "" && filepath.Clean(usageDir) != defaultUsage:
		u.say("  用量資料目錄不是預設位置，不刪除：%s", usageDir)
	}
	if state != registryMissing && (usageDir == "" || filepath.Clean(usageDir) == defaultUsage) && exists(defaultUsage) {
		switch ok, err := onlySnapshots(defaultUsage); {
		case err != nil:
			u.fail("%s：%s", defaultUsage, err)
		case !ok:
			u.say("  用量資料目錄裡有不是 ach 快照的東西，不刪除：%s", defaultUsage)
			u.manual = append(u.manual, "用量資料目錄 "+defaultUsage+"（裡面有 ach 以外的東西，確認後自行刪除）")
		default:
			u.remove(defaultUsage, "刪除用量資料目錄", os.RemoveAll)
		}
	}

	switch {
	case state == registryMissing:
	case u.keepRegistry:
		u.say("  保留 registry %s，修正後重跑才找得到帳號", u.regPath)
	default:
		if !u.remove(u.regPath, "刪除 registry", removeFile) {
			u.keepRegistry = true
		}
	}
}

// onlySnapshots reports whether dir holds nothing but the usage snapshots
// ach writes and their temp files.
func onlySnapshots(dir string) (bool, error) {
	names, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	for _, name := range names {
		n := name.Name()
		isSnapshot := strings.HasSuffix(n, ".json") || (strings.HasPrefix(n, ".snapshot-") && strings.HasSuffix(n, ".tmp"))
		if name.IsDir() || !isSnapshot {
			return false, nil
		}
	}
	return true, nil
}

func (u *run) remove(path, desc string, fn func(string) error) bool {
	u.planned[filepath.Base(path)] = true
	return u.act(desc+" "+path, func() error { return fn(path) })
}

// finishConfigDir runs after the registry lock is released, because the lock
// file is one of the things it deletes.
func (u *run) finishConfigDir() {
	if !u.configDone {
		return
	}
	if lock := u.regPath + ".lock"; exists(lock) {
		u.remove(lock, "刪除 registry 鎖定檔", os.Remove)
	}
	names, err := os.ReadDir(u.dir)
	if err != nil {
		u.fail("%s：%s", u.dir, err)
		return
	}
	var rest []string
	for _, name := range names {
		if u.Execute || !u.planned[name.Name()] {
			rest = append(rest, name.Name())
		}
	}
	if len(rest) > 0 {
		sort.Strings(rest)
		u.say("  保留設定目錄 %s：裡面還有其他東西", u.dir)
		u.manual = append(u.manual, fmt.Sprintf("設定目錄 %s 未刪除，裡面還有：%s", u.dir, strings.Join(rest, "、")))
		return
	}
	u.act("刪除已清空的設定目錄 "+u.dir, func() error { return os.Remove(u.dir) })
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// inside reports whether path is parent or lies under it, comparing resolved
// paths when both resolve.
func inside(path, parent string) bool {
	if p, err := filepath.EvalSymlinks(path); err == nil {
		path = p
	}
	if p, err := filepath.EvalSymlinks(parent); err == nil {
		parent = p
	}
	rel, err := filepath.Rel(parent, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// binary deletes the executable at the path it was invoked by. When that
// path is a symlink only the link goes: its target may be a build artifact
// in a checkout, which ach did not install.
func (u *run) binary() {
	exe, err := u.Executable()
	var notRunning *notRunningError
	if errors.As(err, &notRunning) {
		u.say("  略過 %s：與執行中的 %s 不是同一個檔案", notRunning.invoked, notRunning.running)
		u.manual = append(u.manual, fmt.Sprintf("執行檔 %s 與執行中的 %s 不是同一個檔案，都沒有刪除，確認後自行刪除", notRunning.invoked, notRunning.running))
		return
	}
	if err != nil {
		u.fail("找不到執行檔位置：%s", err)
		return
	}
	dir := filepath.Dir(exe)
	if inside(dir, u.TempDir) || strings.Contains(exe, "go-build") {
		u.say("  略過 %s：看起來是 go run 的暫存建置", exe)
		return
	}
	if syscall.Access(dir, 2) != nil {
		u.say("  略過 %s：%s 不可寫入", exe, dir)
		u.manual = append(u.manual, "執行檔 "+exe+" 需自行刪除")
		return
	}
	info, err := os.Lstat(exe)
	if os.IsNotExist(err) {
		u.say("  略過 %s：不存在", exe)
		return
	}
	if err != nil {
		u.fail("%s：%s", exe, err)
		return
	}

	pattern := regexp.MustCompile("^" + regexp.QuoteMeta(filepath.Base(exe)) + `\.bak-\d{8}-\d{6}$`)
	names, err := os.ReadDir(dir)
	if err != nil {
		u.fail("%s：%s", dir, err)
	}
	for _, name := range names {
		if pattern.MatchString(name.Name()) {
			path := filepath.Join(dir, name.Name())
			u.act("刪除舊版備份 "+path, func() error { return os.Remove(path) })
		}
	}

	if info.Mode()&os.ModeSymlink == 0 {
		u.act("刪除執行檔 "+exe, func() error { return os.Remove(exe) })
		return
	}
	target, err := os.Readlink(exe)
	if err != nil {
		u.fail("%s：%s", exe, err)
		return
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(dir, target)
	}
	if u.act("刪除指向執行檔的連結 "+exe, func() error { return os.Remove(exe) }) {
		u.manual = append(u.manual, fmt.Sprintf("%s 是連結，它指向的 %s 沒有刪除，需要的話自行處理", exe, target))
	}
}

func (u *run) footer() {
	u.say("\n不會動的東西：帳號目錄本身、認證資料、session、共用前的原始設定備份、各專案的 .agent-handoffs，以及你的 status line。")
	u.say("\n仍需手動處理：")
	u.say("  1. 各專案的 .agent-handoffs 目錄（含完整對話副本），可用這行找出來：")
	u.say(`     find "$HOME" -type d -name .agent-handoffs -prune 2>/dev/null`)
	u.say("  2. 如果 status line 腳本有加過 `ach usage record`，請自行移除那一行。")
	for i, item := range u.manual {
		u.say("  %d. %s", i+3, item)
	}
	if u.keepRegistry {
		u.say("\nregistry 與執行檔已保留：修正上面的問題後重跑 ach uninstall --yes")
	}
	if !u.Execute {
		u.say("\n以上只是預覽，沒有任何檔案被改動。確認無誤後執行：ach uninstall --yes")
	}
}
