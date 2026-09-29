package provider

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/tsai41/agent-conversation-handoff/internal/registry"
)

// AuthCacheTTL is how long a cached auth-status result is trusted before
// CachedAuthStatus shells out to the provider CLI again.
const AuthCacheTTL = 5 * time.Minute

type authCacheEntry struct {
	Authenticated bool      `json:"authenticated"`
	CheckedAt     time.Time `json:"checked_at"`
}

func authCachePath(registryPath string) string {
	return filepath.Join(filepath.Dir(registryPath), "auth-cache.json")
}

func loadAuthCache(path string) map[string]authCacheEntry {
	cache := map[string]authCacheEntry{}
	raw, err := os.ReadFile(path)
	if err != nil {
		return cache
	}
	// A corrupt cache just means every lookup misses; it is not worth failing over.
	json.Unmarshal(raw, &cache)
	return cache
}

func saveAuthCache(path string, cache map[string]authCacheEntry) {
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(path, data, 0o600)
}

// CachedAuthStatus is AuthStatus, but skips the provider CLI call (and its
// possible network round-trip) when a check for this account already
// succeeded or failed within the last AuthCacheTTL. When a cache hit isn't
// available, onChecking (if non-nil) is called right before the real,
// possibly-slow check starts, so callers can print a "still working" status
// line at the right moment instead of going silent.
func CachedAuthStatus(registryPath string, account registry.Account, onChecking func()) (authenticated bool, err error) {
	cachePath := authCachePath(registryPath)
	cache := loadAuthCache(cachePath)
	if entry, ok := cache[account.ID]; ok && time.Since(entry.CheckedAt) < AuthCacheTTL {
		return entry.Authenticated, nil
	}
	if onChecking != nil {
		onChecking()
	}
	authenticated, err = AuthStatus(account)
	if err != nil {
		return false, err
	}
	cache[account.ID] = authCacheEntry{Authenticated: authenticated, CheckedAt: time.Now()}
	saveAuthCache(cachePath, cache)
	return authenticated, nil
}
