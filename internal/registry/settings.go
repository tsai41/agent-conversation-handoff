package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// settingsFile is the per-account settings document both provider CLIs keep
// in the account home, keyed by CLAUDE_CONFIG_DIR / CODEX_HOME.
const settingsFile = "settings.json"

// readSettings loads an account's settings document. A home that has never
// been launched has no settings.json at all, which is an empty document
// rather than an error. Values stay raw, so a copied key's nested content
// survives verbatim; the top-level object is still re-encoded on write,
// which sorts the keys and re-indents the file.
func readSettings(path string) (map[string]json.RawMessage, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]json.RawMessage{}, nil
	}
	if err != nil {
		return nil, err
	}
	settings := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &settings); err != nil {
		return nil, fmt.Errorf("%s is not a JSON object: %w", path, err)
	}
	// A document of `null` unmarshals into a nil map without erroring, so
	// the check above lets it through and every later write to the map
	// panics. It is no more an object than `{not json` is.
	if settings == nil {
		return nil, fmt.Errorf("%s is not a JSON object", path)
	}
	return settings, nil
}

// CopySettingsKey copies one top-level key from the source account's
// settings into the target account's, and reports whether it wrote.
//
// A key the target already sets is left alone: this seeds an account that
// has nothing, it does not push one account's preference onto another. A
// key the source does not set copies nothing, which is not a failure --
// there is simply no default to inherit.
//
// Writing replaces the target path, so a settings.json that is a symlink
// to another home's becomes a regular file. Callers should only aim this
// at a home they know to be freshly created.
func CopySettingsKey(sourceHome, targetHome, key string) (bool, error) {
	source, err := readSettings(filepath.Join(sourceHome, settingsFile))
	if err != nil {
		return false, err
	}
	value, present := source[key]
	if !present {
		return false, nil
	}
	targetPath := filepath.Join(targetHome, settingsFile)
	target, err := readSettings(targetPath)
	if err != nil {
		return false, err
	}
	if _, exists := target[key]; exists {
		return false, nil
	}
	target[key] = value
	data, err := json.MarshalIndent(target, "", "  ")
	if err != nil {
		return false, err
	}
	if err := writeFileAtomic(targetPath, append(data, '\n'), 0o600); err != nil {
		return false, err
	}
	return true, nil
}

// PrimaryAccount returns the lowest-numbered account registered for a
// provider -- the one a new account of that provider inherits defaults
// from.
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
// so a crash mid-write cannot leave a half-written settings or registry
// document in place of a good one.
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
