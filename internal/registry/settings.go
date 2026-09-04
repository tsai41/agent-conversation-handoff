package registry

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// EntryKind distinguishes a shared file from a shared directory. A plain
// file sitting where a shared directory belongs, or a directory sitting
// where a shared file belongs, is refused the same way a foreign symlink
// is -- whoever put it there did so on purpose, and moving it aside would
// describe the wrong thing to the user.
type EntryKind int

const (
	EntryFile EntryKind = iota
	EntryDir
)

// SettingsEntry is one file or directory shared between accounts of a
// provider.
type SettingsEntry struct {
	Name string
	Kind EntryKind
}

// sharedEntries lists what each provider's accounts share, in the order
// ShareSettings processes them. Neither provider keeps credentials in any
// of these -- Claude uses .credentials.json and the Keychain, Codex uses
// auth.json -- which is what makes sharing them across accounts safe: the
// accounts share preferences without sharing a login.
var sharedEntries = map[string][]SettingsEntry{
	"claude": {
		{Name: "settings.json", Kind: EntryFile},
		{Name: "skills", Kind: EntryDir},
		{Name: "commands", Kind: EntryDir},
		{Name: "agents", Kind: EntryDir},
	},
	"codex": {
		{Name: "config.toml", Kind: EntryFile},
		{Name: "skills", Kind: EntryDir},
	},
}

// SharedEntries returns what a provider's accounts share, or nil for a
// provider with nothing known to share, which callers must treat as
// "nothing to share" rather than as an empty list to iterate.
func SharedEntries(provider string) []SettingsEntry {
	return sharedEntries[provider]
}

// EntryPath is where one shared entry lives inside an account home.
func EntryPath(account Account, entry SettingsEntry) string {
	return filepath.Join(account.Home, entry.Name)
}

// SettingsPath is an account's settings document -- the one shared file
// entry, as opposed to the shared directories. It is empty for a provider
// with no known file entry, which callers must treat as "nothing to share"
// rather than as a path.
func SettingsPath(account Account) string {
	for _, entry := range sharedEntries[account.Provider] {
		if entry.Kind == EntryFile {
			return EntryPath(account, entry)
		}
	}
	return ""
}

// EntryState is what a shared entry already is at the target, judged
// against the account it would be sharing with.
type EntryState int

const (
	EntryMissing EntryState = iota
	EntryShared
	EntryOwn
	// EntryForeign is a symlink to something other than the source --
	// somebody else's arrangement, which sharing must not overrule.
	EntryForeign
	// EntryWrongKind is a file where a shared directory belongs, or a
	// directory where a shared file belongs.
	EntryWrongKind
)

func (s EntryState) String() string {
	switch s {
	case EntryMissing:
		return "missing"
	case EntryShared:
		return "shared"
	case EntryOwn:
		return "own"
	case EntryWrongKind:
		return "the wrong kind"
	default:
		return "linked elsewhere"
	}
}

// EntryShare reports what ShareSettings found and did for one shared entry.
type EntryShare struct {
	Name   string
	Before EntryState
	// Backup is where an existing entry was moved, empty when nothing was
	// moved or, for a file entry, when what was moved turned out to be
	// redundant. Directory backups are never treated as redundant: nothing
	// compares their contents, so a directory backup is always kept.
	Backup string
	// Linked is true only when this call created the symlink.
	Linked bool
	// Err is set when this entry was refused or a step failed. It never
	// stops the other entries from being tried.
	Err error
}

// ShareSettings points every one of the target account's shared entries at
// the source account's, so both accounts read and write one copy of each
// and never drift. See SharedEntries for what is shared per provider.
//
// Each entry is judged and linked independently -- one entry's refusal or
// failure is recorded on its own EntryShare and does not stop the others.
// The returned error is non-nil only for a problem with the pair of
// accounts as a whole (same account, same physical home, or a provider
// with nothing known to share), checked before any entry is touched.
//
// Per entry: a target with nothing there yet is linked outright. A target
// that already points at the source is left alone. A target with an entry
// of its own is only touched when replace is true, and then the original
// is moved aside first and its path reported, never deleted -- unless it
// is a file entry that is byte-for-byte the file the link now points at,
// in which case the copy preserves nothing and is cleaned up. A target
// pointing at some third place is always refused: that link was somebody's
// deliberate choice and this is not the code to overrule it.
func ShareSettings(source, target Account, replace bool) ([]EntryShare, error) {
	entries := sharedEntries[source.Provider]
	if len(entries) == 0 {
		return nil, fmt.Errorf("provider has nothing known to share: %s", source.Provider)
	}
	// Compare the homes and not just the ids: two registry entries can
	// name one physical directory, and linking an entry to itself destroys
	// it while looking like success.
	if source.ID == target.ID || sameFile(source.Home, target.Home) {
		return nil, fmt.Errorf("%s and %s are the same account home", source.ID, target.ID)
	}

	shares := make([]EntryShare, len(entries))
	for i, entry := range entries {
		shares[i] = shareEntry(source, target, entry, replace)
	}
	return shares, nil
}

func shareEntry(source, target Account, entry SettingsEntry, replace bool) EntryShare {
	share := EntryShare{Name: entry.Name}
	sourcePath := EntryPath(source, entry)
	targetPath := EntryPath(target, entry)

	// A symlink to an entry that is not there yet would silently become a
	// broken link, so require the source entry to exist and be the kind
	// this entry claims to be.
	sourceInfo, err := os.Stat(sourcePath)
	if err != nil {
		share.Err = fmt.Errorf("%s has no %s to share: %w", source.ID, entry.Name, err)
		return share
	}
	if sourceInfo.IsDir() != (entry.Kind == EntryDir) {
		share.Err = kindMismatchError(source.ID, entry, sourceInfo.IsDir())
		return share
	}

	state, err := entryState(sourcePath, targetPath, entry.Kind)
	if err != nil {
		share.Err = err
		return share
	}
	share.Before = state
	switch state {
	case EntryShared:
		return share
	case EntryForeign:
		link, _ := os.Readlink(targetPath)
		share.Err = fmt.Errorf("%s already links its %s elsewhere (%s)", target.ID, entry.Name, link)
		return share
	case EntryWrongKind:
		share.Err = kindMismatchError(target.ID, entry, entry.Kind == EntryFile)
		return share
	case EntryOwn:
		if !replace {
			share.Err = fmt.Errorf("%s keeps its own %s", target.ID, entry.Name)
			return share
		}
		// An entry at the target that is already the source entry -- two
		// homes on one directory, a hard link, a source file symlinked
		// onto this one -- would be moved aside and then replaced by a
		// link to itself, destroying the only copy.
		if sameFile(sourcePath, targetPath) {
			share.Err = fmt.Errorf("%s and %s already share one %s", source.ID, target.ID, entry.Name)
			return share
		}
	}

	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		share.Err = err
		return share
	}
	redundant := false
	if state == EntryOwn {
		if entry.Kind == EntryFile {
			redundant = sameContent(targetPath, sourcePath)
		}
		backup, err := freeBackupPath(targetPath, entry.Kind)
		if err != nil {
			share.Err = err
			return share
		}
		if err := renameEntry(targetPath, backup, entry.Kind); err != nil {
			// The name was claimed by creating it; an empty placeholder left
			// beside the entry would only confuse the next run.
			os.Remove(backup)
			share.Err = err
			return share
		}
		share.Backup = backup
	}
	if err := os.Symlink(sourcePath, targetPath); err != nil {
		// An account with nothing there at all is worse than one that
		// simply is not sharing, so put the original back.
		if share.Backup != "" && renameEntry(share.Backup, targetPath, entry.Kind) == nil {
			share.Backup = ""
		}
		share.Err = err
		return share
	}
	if redundant && os.Remove(share.Backup) == nil {
		share.Backup = ""
	}
	share.Linked = true
	return share
}

// kindMismatchError describes a file or directory sitting where an entry
// of the other kind belongs.
func kindMismatchError(accountID string, entry SettingsEntry, foundDir bool) error {
	if foundDir {
		return fmt.Errorf("%s has a directory where its %s belongs", accountID, entry.Name)
	}
	return fmt.Errorf("%s has a file where its %s directory belongs", accountID, entry.Name)
}

// freeBackupPath claims a .bak name for an entry of the given kind. The
// timestamp has second resolution and os.Rename overwrites silently, so two
// shares within one second would otherwise destroy the backup made moments
// earlier. The name is claimed by creating it rather than by testing and
// then using it, so a second process racing for the same name loses
// instead of tying; the rename that follows replaces the empty
// placeholder. A directory placeholder must itself be an empty directory --
// os.Rename onto an existing plain file at that path fails -- so the two
// kinds claim the name differently.
func freeBackupPath(path string, kind EntryKind) (string, error) {
	stamp := time.Now().Format("20060102-150405")
	candidate := fmt.Sprintf("%s.bak-%s", path, stamp)
	for attempt := 2; attempt < 100; attempt++ {
		claimed, err := claimBackupName(candidate, kind)
		if claimed {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
		candidate = fmt.Sprintf("%s.bak-%s-%d", path, stamp, attempt)
	}
	return "", fmt.Errorf("no free backup name beside %s", path)
}

// renameEntry moves an entry aside. os.Rename refuses to replace an
// existing directory even when it is empty, as a guard against silently
// swallowing a populated one; freeBackupPath's directory placeholder is
// verified empty by construction, so a directory entry bypasses that guard
// and renames directly through the syscall.
func renameEntry(oldpath, newpath string, kind EntryKind) error {
	if kind == EntryDir {
		return syscall.Rename(oldpath, newpath)
	}
	return os.Rename(oldpath, newpath)
}

func claimBackupName(candidate string, kind EntryKind) (bool, error) {
	if kind == EntryDir {
		err := os.Mkdir(candidate, 0o700)
		if err == nil {
			return true, nil
		}
		if os.IsExist(err) {
			return false, nil
		}
		return false, err
	}
	claim, err := os.OpenFile(candidate, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err == nil {
		claim.Close()
		return true, nil
	}
	if os.IsExist(err) {
		return false, nil
	}
	return false, err
}

// entryState classifies the target path without following it: Stat would
// report a symlink as whatever it points at, which is exactly the
// distinction being made here.
func entryState(sourcePath, targetPath string, kind EntryKind) (EntryState, error) {
	info, err := os.Lstat(targetPath)
	if os.IsNotExist(err) {
		return EntryMissing, nil
	}
	if err != nil {
		return EntryMissing, err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		if info.IsDir() != (kind == EntryDir) {
			return EntryWrongKind, nil
		}
		return EntryOwn, nil
	}
	link, err := os.Readlink(targetPath)
	if err != nil {
		return EntryForeign, err
	}
	// A relative link is relative to the directory holding it.
	if !filepath.IsAbs(link) {
		link = filepath.Join(filepath.Dir(targetPath), link)
	}
	if sameFile(link, sourcePath) {
		return EntryShared, nil
	}
	return EntryForeign, nil
}

// sameFile asks the filesystem whether two paths are one file, rather than
// comparing the strings. Path comparison misses every route that does not
// change the spelling: a case-insensitive volume, a macOS firmlink, a hard
// link. Getting this wrong is not a missed optimisation -- the caller
// renames one path and symlinks the other, so two spellings of one file
// means destroying it.
//
// It fails to false, which callers turn into a refusal rather than a write.
func sameFile(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	infoA, errA := os.Stat(a)
	infoB, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(infoA, infoB)
}

func sameContent(a, b string) bool {
	rawA, err := os.ReadFile(a)
	if err != nil {
		return false
	}
	rawB, err := os.ReadFile(b)
	if err != nil {
		return false
	}
	return bytes.Equal(rawA, rawB)
}

// PrimaryAccount returns the lowest-numbered account registered for a
// provider -- the one the other accounts of that provider share settings
// with.
func PrimaryAccount(r Registry, provider string) (Account, bool) {
	var primary Account
	found := false
	for _, account := range r.Accounts {
		if account.Provider != provider {
			continue
		}
		if !found || account.Number < primary.Number {
			primary = account
			found = true
		}
	}
	return primary, found
}

// writeFileAtomic writes through a temp file in the destination directory
// so a crash mid-write cannot leave a half-written registry document in
// place of a good one.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	cleanup := func(err error) error {
		temp.Close()
		os.Remove(tempName)
		return err
	}
	if err := temp.Chmod(perm); err != nil {
		return cleanup(err)
	}
	if _, err := temp.Write(data); err != nil {
		return cleanup(err)
	}
	if err := temp.Sync(); err != nil {
		return cleanup(err)
	}
	if err := temp.Close(); err != nil {
		os.Remove(tempName)
		return err
	}
	if err := os.Rename(tempName, path); err != nil {
		os.Remove(tempName)
		return err
	}
	return nil
}
