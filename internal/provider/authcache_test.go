package provider

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/tsai41/agent-conversation-handoff/internal/registry"
)

func TestCachedAuthStatusSkipsTheRealCheckWithinTTL(t *testing.T) {
	dir := t.TempDir()
	registryPath := filepath.Join(dir, "accounts.json")
	cache := map[string]authCacheEntry{
		"claude-1": {Authenticated: true, CheckedAt: time.Now()},
	}
	saveAuthCache(authCachePath(registryPath), cache)

	calls := 0
	authenticated, err := CachedAuthStatus(registryPath, registry.Account{ID: "claude-1", Provider: "claude", Home: dir}, func() { calls++ })
	if err != nil {
		t.Fatal(err)
	}
	if !authenticated {
		t.Fatal("expected cached authenticated=true")
	}
	if calls != 0 {
		t.Fatalf("expected onChecking not to be called on a cache hit, called %d times", calls)
	}
}

func TestCachedAuthStatusRechecksAfterTTLExpires(t *testing.T) {
	// Empty PATH keeps AuthStatus from reaching a real claude/codex CLI.
	t.Setenv("PATH", "")

	dir := t.TempDir()
	registryPath := filepath.Join(dir, "accounts.json")
	cache := map[string]authCacheEntry{
		"claude-1": {Authenticated: true, CheckedAt: time.Now().Add(-AuthCacheTTL - time.Second)},
	}
	saveAuthCache(authCachePath(registryPath), cache)

	calls := 0
	_, err := CachedAuthStatus(registryPath, registry.Account{ID: "claude-1", Provider: "claude", Home: dir}, func() { calls++ })
	if err == nil {
		t.Fatal("expected AuthStatus to fail with no CLI on PATH")
	}
	if calls != 1 {
		t.Fatalf("expected onChecking to be called once for a stale entry, called %d times", calls)
	}
}

func TestCachedAuthStatusPersistsAFreshCheck(t *testing.T) {
	dir := t.TempDir()
	registryPath := filepath.Join(dir, "accounts.json")
	cachePath := authCachePath(registryPath)

	entry := authCacheEntry{Authenticated: false, CheckedAt: time.Now()}
	saveAuthCache(cachePath, map[string]authCacheEntry{"codex-1": entry})

	loaded := loadAuthCache(cachePath)
	got, ok := loaded["codex-1"]
	if !ok || got.Authenticated != false {
		t.Fatalf("expected persisted entry to round-trip, got %+v (ok=%v)", got, ok)
	}
}
