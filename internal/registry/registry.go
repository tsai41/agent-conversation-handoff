// Package registry manages the account registry (accounts.json): schema
// validation, locked atomic reads/writes, and account CRUD.
package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
)

var ProviderNames = map[string]string{"claude": "Claude", "codex": "Codex"}

type Account struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Number   int    `json:"number"`
	Home     string `json:"home"`
	Alias    string `json:"alias"`
}

type Registry struct {
	Version    int            `json:"version"`
	NextNumber map[string]int `json:"next_number"`
	Accounts   []Account      `json:"accounts"`
	// UsageDir is the usage view's snapshot directory, set from the menu's
	// account-settings action. Empty means the caller's built-in default
	// applies; a registry saved before this field existed loads the same
	// way, so no schema version bump is needed for it.
	UsageDir string `json:"usage_dir,omitempty"`
}

func Empty() Registry {
	return Registry{Version: 1, NextNumber: map[string]int{"claude": 1, "codex": 1}, Accounts: []Account{}}
}

// SchemaProblem returns a human-readable description of the first schema
// violation found, or "" if the registry is valid.
func SchemaProblem(r Registry) string {
	if r.Version != 1 {
		return "version must be 1"
	}
	if r.NextNumber == nil {
		return "next_number must contain claude and codex"
	}
	for provider := range ProviderNames {
		value, ok := r.NextNumber[provider]
		if !ok {
			return "next_number must contain claude and codex"
		}
		if value < 1 {
			return fmt.Sprintf("next_number.%s must be a positive integer", provider)
		}
	}
	if len(r.NextNumber) != len(ProviderNames) {
		return "next_number must contain claude and codex"
	}

	accountIDs := map[string]bool{}
	providerNumbers := map[string]bool{}
	accountHomes := map[string]bool{}
	highest := map[string]int{"claude": 0, "codex": 0}
	for index, account := range r.Accounts {
		prefix := fmt.Sprintf("accounts[%d]", index)
		if _, ok := ProviderNames[account.Provider]; !ok {
			return fmt.Sprintf("%s.provider is unsupported", prefix)
		}
		if account.Number < 1 {
			return fmt.Sprintf("%s.number must be a positive integer", prefix)
		}
		if account.ID != fmt.Sprintf("%s-%d", account.Provider, account.Number) {
			return fmt.Sprintf("%s.id does not match provider and number", prefix)
		}
		if account.Home == "" {
			return fmt.Sprintf("%s.home must be a non-empty string", prefix)
		}
		resolvedHome, err := ResolveHome(account.Home)
		if err != nil {
			return fmt.Sprintf("%s.home is invalid", prefix)
		}
		if accountIDs[account.ID] {
			return fmt.Sprintf("duplicate account id: %s", account.ID)
		}
		key := account.Provider + "-" + fmt.Sprint(account.Number)
		if providerNumbers[key] {
			return fmt.Sprintf("duplicate provider account number: %s", key)
		}
		if accountHomes[resolvedHome] {
			return fmt.Sprintf("duplicate account home: %s", resolvedHome)
		}
		accountIDs[account.ID] = true
		providerNumbers[key] = true
		accountHomes[resolvedHome] = true
		if account.Number > highest[account.Provider] {
			highest[account.Provider] = account.Number
		}
	}
	for provider, max := range highest {
		if r.NextNumber[provider] <= max {
			return fmt.Sprintf("next_number.%s must be greater than every registered number", provider)
		}
	}
	return ""
}

// ResolveHome expands a leading ~ and returns the absolute, cleaned form of
// home. It is the one normalisation shared by account homes and snapshot
// config_dir values.
func ResolveHome(home string) (string, error) {
	expanded, err := expandUser(home)
	if err != nil {
		return "", err
	}
	return filepath.Abs(expanded)
}

func expandUser(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if path == "~" {
			return home, nil
		}
		return filepath.Join(home, path[2:]), nil
	}
	return path, nil
}

func Load(path string) (Registry, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Registry{}, fmt.Errorf("account registry does not exist: %s", path)
	}
	if err != nil {
		return Registry{}, err
	}
	var r Registry
	if err := json.Unmarshal(raw, &r); err != nil {
		backup, backupErr := backupInvalid(path)
		if backupErr != nil {
			return Registry{}, fmt.Errorf("account registry is invalid JSON: %s: %w", path, err)
		}
		return Registry{}, fmt.Errorf("account registry is invalid JSON: %s: %w; backup: %s", path, err, backup)
	}
	if problem := SchemaProblem(r); problem != "" {
		backup, backupErr := backupInvalid(path)
		if backupErr != nil {
			return Registry{}, fmt.Errorf("unsupported account registry: %s: %s", path, problem)
		}
		return Registry{}, fmt.Errorf("unsupported account registry: %s: %s; backup: %s", path, problem, backup)
	}
	return r, nil
}

// PeekUsageDir returns the registry's stored usage_dir without validating the
// registry and without touching the file: no backup, no lock, no write. Any
// failure (missing file, bad JSON, schema problem, unresolvable path) yields
// "", so it picks the same directory the menu does.
func PeekUsageDir(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var r Registry
	if json.Unmarshal(raw, &r) != nil || SchemaProblem(r) != "" || r.UsageDir == "" {
		return ""
	}
	dir, err := ResolveHome(r.UsageDir)
	if err != nil {
		return ""
	}
	return dir
}

func LoadOrEmpty(path string) (Registry, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return Empty(), nil
	}
	return Load(path)
}

func backupInvalid(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	stamp := time.Now().UTC().Format("20060102T150405.000000Z")
	stamp = strings.ReplaceAll(stamp, ".", "")
	backup := fmt.Sprintf("%s.corrupt-%s", path, stamp)
	if err := os.WriteFile(backup, raw, 0o644); err != nil {
		return "", err
	}
	return backup, nil
}

func Save(path string, r Registry) error {
	if problem := SchemaProblem(r); problem != "" {
		return fmt.Errorf("refusing to write invalid account registry: %s", problem)
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(data, '\n'), 0o600)
}

// WithLock runs fn while holding an exclusive flock on path+".lock",
// creating the registry's parent directory first.
func WithLock(path string, fn func() error) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	lockPath := path + ".lock"
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lockFile.Close()
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
	return fn()
}

func Register(r *Registry, provider string, accountHome string, alias string, createHome bool) (Account, error) {
	resolvedHome, err := ResolveHome(accountHome)
	if err != nil {
		return Account{}, err
	}
	for _, item := range r.Accounts {
		itemHome, err := ResolveHome(item.Home)
		if err == nil && itemHome == resolvedHome {
			return Account{}, fmt.Errorf("account home is already registered: %s", resolvedHome)
		}
	}
	number := r.NextNumber[provider]
	account := Account{ID: fmt.Sprintf("%s-%d", provider, number), Provider: provider, Number: number, Home: resolvedHome, Alias: alias}
	if createHome {
		if err := os.MkdirAll(resolvedHome, 0o755); err != nil {
			return Account{}, err
		}
	}
	r.Accounts = append(r.Accounts, account)
	r.NextNumber[provider] = number + 1
	return account, nil
}

func AddAccount(path string, provider string, accountHome string, alias string) (Account, error) {
	var account Account
	err := WithLock(path, func() error {
		r, err := LoadOrEmpty(path)
		if err != nil {
			return err
		}
		account, err = Register(&r, provider, accountHome, alias, true)
		if err != nil {
			return err
		}
		return Save(path, r)
	})
	return account, err
}

func RemoveAccount(path string, accountID string) error {
	return WithLock(path, func() error {
		r, err := Load(path)
		if err != nil {
			return err
		}
		remaining := r.Accounts[:0]
		found := false
		for _, item := range r.Accounts {
			if item.ID == accountID {
				found = true
				continue
			}
			remaining = append(remaining, item)
		}
		if !found {
			return fmt.Errorf("account is not registered: %s", accountID)
		}
		r.Accounts = remaining
		return Save(path, r)
	})
}

func FindAccount(r Registry, accountID string) (Account, error) {
	for _, account := range r.Accounts {
		if account.ID == accountID {
			return account, nil
		}
	}
	return Account{}, fmt.Errorf("account is not registered: %s", accountID)
}

func RenameAccount(path string, accountID string, alias string) error {
	return WithLock(path, func() error {
		r, err := Load(path)
		if err != nil {
			return err
		}
		found := false
		for i := range r.Accounts {
			if r.Accounts[i].ID == accountID {
				r.Accounts[i].Alias = alias
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("account is not registered: %s", accountID)
		}
		return Save(path, r)
	})
}

// SetUsageDir persists usageDir as the registry's usage-view directory,
// expanding a leading ~ the same way an account home is resolved, and
// returns the value actually stored. An empty usageDir clears the setting.
func SetUsageDir(path string, usageDir string) (string, error) {
	var resolved string
	err := WithLock(path, func() error {
		r, err := LoadOrEmpty(path)
		if err != nil {
			return err
		}
		if usageDir == "" {
			r.UsageDir = ""
			return Save(path, r)
		}
		resolved, err = ResolveHome(usageDir)
		if err != nil {
			return err
		}
		r.UsageDir = resolved
		return Save(path, r)
	})
	return resolved, err
}

type DiscoveredAccount struct {
	Provider string
	Home     string
}

// Discover finds common Claude/Codex account home directories under $HOME
// that are not already registered.
func Discover(path string) ([]DiscoveredAccount, error) {
	userHome, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	userHome, err = filepath.Abs(userHome)
	if err != nil {
		return nil, err
	}
	registered := map[string]bool{}
	if _, statErr := os.Stat(path); statErr == nil {
		r, err := Load(path)
		if err != nil {
			return nil, err
		}
		for _, account := range r.Accounts {
			home, err := ResolveHome(account.Home)
			if err == nil {
				registered[home] = true
			}
		}
	}

	var candidates []DiscoveredAccount
	for _, entry := range []struct{ provider, name string }{{"claude", ".claude"}, {"codex", ".codex"}} {
		defaultHome := filepath.Join(userHome, entry.name)
		if info, err := os.Stat(defaultHome); err == nil && info.IsDir() {
			candidates = append(candidates, DiscoveredAccount{entry.provider, defaultHome})
		}
		pattern := regexp.MustCompile(`^` + regexp.QuoteMeta(entry.name) + `-[0-9]+$`)
		matches, _ := filepath.Glob(filepath.Join(userHome, entry.name+"-*"))
		sort.Strings(matches)
		for _, match := range matches {
			info, err := os.Stat(match)
			if err != nil || !info.IsDir() {
				continue
			}
			if pattern.MatchString(filepath.Base(match)) {
				candidates = append(candidates, DiscoveredAccount{entry.provider, match})
			}
		}
	}
	legacyRoot := filepath.Join(userHome, ".codex-homes")
	if info, err := os.Stat(legacyRoot); err == nil && info.IsDir() {
		entries, _ := os.ReadDir(legacyRoot)
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		sort.Strings(names)
		for _, name := range names {
			full := filepath.Join(legacyRoot, name)
			if info, err := os.Stat(full); err == nil && info.IsDir() {
				candidates = append(candidates, DiscoveredAccount{"codex", full})
			}
		}
	}

	result := make([]DiscoveredAccount, 0, len(candidates))
	for _, c := range candidates {
		resolved, err := filepath.Abs(c.Home)
		if err != nil || registered[resolved] {
			continue
		}
		result = append(result, c)
	}
	return result, nil
}

// SuggestAccountHome returns the next unused default home directory
// (e.g. ~/.codex, ~/.codex-2, ...) for provider.
func SuggestAccountHome(path string, provider string) (string, error) {
	r, err := LoadOrEmpty(path)
	if err != nil {
		return "", err
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	userHome, err = filepath.Abs(userHome)
	if err != nil {
		return "", err
	}
	registeredHomes := map[string]bool{}
	for _, account := range r.Accounts {
		home, err := ResolveHome(account.Home)
		if err == nil {
			registeredHomes[home] = true
		}
	}
	for suffix := 1; ; suffix++ {
		name := "." + provider
		if suffix != 1 {
			name = fmt.Sprintf(".%s-%d", provider, suffix)
		}
		candidate := filepath.Join(userHome, name)
		if _, err := os.Lstat(candidate); os.IsNotExist(err) {
			resolved, err := filepath.Abs(candidate)
			if err == nil && !registeredHomes[resolved] {
				return candidate, nil
			}
		}
	}
}

// Rows returns (account id, display label) pairs sorted by provider then
// number, appending the account number and alias to the label when a
// provider has more than one registered account.
func Rows(r Registry) []struct{ ID, Label string } {
	counts := map[string]int{}
	for _, account := range r.Accounts {
		counts[account.Provider]++
	}
	sorted := make([]Account, len(r.Accounts))
	copy(sorted, r.Accounts)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Provider != sorted[j].Provider {
			return sorted[i].Provider < sorted[j].Provider
		}
		return sorted[i].Number < sorted[j].Number
	})
	rows := make([]struct{ ID, Label string }, 0, len(sorted))
	for _, account := range sorted {
		name := ProviderNames[account.Provider]
		if counts[account.Provider] > 1 {
			name += fmt.Sprintf(" · %d", account.Number)
			if account.Alias != "" {
				name += " · " + account.Alias
			}
		}
		rows = append(rows, struct{ ID, Label string }{account.ID, name})
	}
	return rows
}
