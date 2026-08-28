package registry

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// settingsFiles names each provider's settings document inside an account
// home. Neither provider keeps credentials there -- Claude uses
// .credentials.json and the Keychain, Codex uses auth.json -- which is what
// makes one settings document shared across several accounts safe: the
// accounts share preferences without sharing a login.
var settingsFiles = map[string]string{
	"claude": "settings.json",
	"codex":  "config.toml",
}

// SettingsPath is an account's settings document. It is empty for a
// provider with no known settings file, which callers must treat as
// "nothing to share" rather than as a path.
func SettingsPath(account Account) string {
	name, known := settingsFiles[account.Provider]
	if !known {
		return ""
	}
	return filepath.Join(account.Home, name)
}

// SettingsState is what an account's settings document already is, judged
// against the account it would be sharing with.
type SettingsState int

const (
	SettingsMissing SettingsState = iota
	SettingsShared
	SettingsOwn
	// SettingsForeign is a symlink to something other than the source --
	// somebody else's arrangement, which sharing must not overrule.
	SettingsForeign
	// SettingsDirectory is a directory sitting where the document belongs.
	// Moving one aside and calling it a backed-up settings file would
	// describe the wrong thing to the user.
	SettingsDirectory
)

func (s SettingsState) String() string {
	switch s {
	case SettingsMissing:
		return "missing"
	case SettingsShared:
		return "shared"
	case SettingsOwn:
		return "own"
	case SettingsDirectory:
		return "a directory"
	default:
		return "linked elsewhere"
	}
}

// SettingsShare reports what ShareSettings found and what it did about it.
type SettingsShare struct {
	Before SettingsState
	// Backup is where an existing document was moved, empty when nothing
	// was moved or when what was moved turned out to be redundant.
	Backup string
	// Linked is true only when this call created the symlink.
	Linked bool
}

// ShareSettings points the target account's settings document at the source
// account's, so both accounts read and write one file and never drift.
//
// A target with no document yet is linked outright. A target that already
// points at the source is left alone. A target with a document of its own
// is only touched when replace is true, and then the original is moved
// aside first and its path reported, never deleted -- unless it is
// byte-for-byte the file the link now points at, in which case the copy
// preserves nothing and is cleaned up. A target pointing at some third
// place is always refused: that link was somebody's deliberate choice and
// this is not the code to overrule it.
func ShareSettings(source, target Account, replace bool) (SettingsShare, error) {
	sourcePath := SettingsPath(source)
	targetPath := SettingsPath(target)
	if sourcePath == "" || targetPath == "" {
		return SettingsShare{}, fmt.Errorf("provider has no known settings file: %s", target.Provider)
	}
	// Compare the homes and not just the ids: two registry entries can
	// name one physical directory through a symlink, and linking a
	// document to itself destroys it while looking like success.
	if source.ID == target.ID || sameFile(source.Home, target.Home) {
		return SettingsShare{}, fmt.Errorf("%s and %s are the same account home", source.ID, target.ID)
	}
	// A symlink to a file that is not there yet would silently become a
	// broken link, so require the source document to exist.
	if _, err := os.Stat(sourcePath); err != nil {
		return SettingsShare{}, fmt.Errorf("%s has no settings document to share: %w", source.ID, err)
	}

	state, err := settingsState(sourcePath, targetPath)
	if err != nil {
		return SettingsShare{}, err
	}
	share := SettingsShare{Before: state}
	switch state {
	case SettingsShared:
		return share, nil
	case SettingsForeign:
		link, _ := os.Readlink(targetPath)
		return share, fmt.Errorf("%s already links its settings elsewhere (%s)", target.ID, link)
	case SettingsDirectory:
		return share, fmt.Errorf("%s has a directory where its settings document belongs", target.ID)
	case SettingsOwn:
		if !replace {
			return share, fmt.Errorf("%s keeps its own settings document", target.ID)
		}
	}

	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return share, err
	}
	redundant := false
	if state == SettingsOwn {
		redundant = sameContent(targetPath, sourcePath)
		backup, err := freeBackupPath(targetPath)
		if err != nil {
			return share, err
		}
		if err := os.Rename(targetPath, backup); err != nil {
			return share, err
		}
		share.Backup = backup
	}
	if err := os.Symlink(sourcePath, targetPath); err != nil {
		// An account with no settings document at all is worse than one
		// that simply is not sharing, so put the original back.
		if share.Backup != "" && os.Rename(share.Backup, targetPath) == nil {
			share.Backup = ""
		}
		return share, err
	}
	if redundant && os.Remove(share.Backup) == nil {
		share.Backup = ""
	}
	share.Linked = true
	return share, nil
}

// freeBackupPath picks a .bak name nothing occupies. The timestamp has
// second resolution and os.Rename overwrites silently, so two shares within
// one second would otherwise destroy the backup made moments earlier.
func freeBackupPath(path string) (string, error) {
	stamp := time.Now().Format("20060102-150405")
	candidate := fmt.Sprintf("%s.bak-%s", path, stamp)
	for attempt := 2; attempt < 100; attempt++ {
		_, err := os.Lstat(candidate)
		if os.IsNotExist(err) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
		candidate = fmt.Sprintf("%s.bak-%s-%d", path, stamp, attempt)
	}
	return "", fmt.Errorf("no free backup name beside %s", path)
}

// settingsState classifies the target path without following it: Stat would
// report a symlink as whatever it points at, which is exactly the
// distinction being made here.
func settingsState(sourcePath, targetPath string) (SettingsState, error) {
	info, err := os.Lstat(targetPath)
	if os.IsNotExist(err) {
		return SettingsMissing, nil
	}
	if err != nil {
		return SettingsMissing, err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		if info.IsDir() {
			return SettingsDirectory, nil
		}
		return SettingsOwn, nil
	}
	link, err := os.Readlink(targetPath)
	if err != nil {
		return SettingsForeign, err
	}
	// A relative link is relative to the directory holding it.
	if !filepath.IsAbs(link) {
		link = filepath.Join(filepath.Dir(targetPath), link)
	}
	if sameFile(link, sourcePath) {
		return SettingsShared, nil
	}
	return SettingsForeign, nil
}

// sameFile compares two paths through any further symlinks, so a file or
// home reached by a different route is not reported as a different one.
// It fails to false, which callers turn into a refusal rather than a write.
func sameFile(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	resolvedA, errA := filepath.EvalSymlinks(a)
	resolvedB, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && resolvedA == resolvedB
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
