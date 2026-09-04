package registry

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAddCreatesHomeAndAssignsStableID(t *testing.T) {
	dir := t.TempDir()
	registryPath := filepath.Join(dir, "accounts.json")
	home := filepath.Join(dir, ".claude")

	account, err := AddAccount(registryPath, "claude", home, "kile@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if account.ID != "claude-1" {
		t.Fatalf("expected claude-1, got %s", account.ID)
	}

	r, err := Load(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Accounts) != 1 || r.Accounts[0].Alias != "kile@example.com" {
		t.Fatalf("unexpected accounts: %+v", r.Accounts)
	}
	if r.NextNumber["claude"] != 2 {
		t.Fatalf("expected next_number.claude=2, got %d", r.NextNumber["claude"])
	}
}

func TestAddRejectsDuplicateHome(t *testing.T) {
	dir := t.TempDir()
	registryPath := filepath.Join(dir, "accounts.json")
	home := filepath.Join(dir, ".claude")

	if _, err := AddAccount(registryPath, "claude", home, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := AddAccount(registryPath, "claude", home, ""); err == nil {
		t.Fatal("expected duplicate home to be rejected")
	}
}

func TestRemoveKeepsAccountNumbering(t *testing.T) {
	dir := t.TempDir()
	registryPath := filepath.Join(dir, "accounts.json")

	first, err := AddAccount(registryPath, "claude", filepath.Join(dir, ".claude"), "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := AddAccount(registryPath, "claude", filepath.Join(dir, ".claude-2"), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := RemoveAccount(registryPath, first.ID); err != nil {
		t.Fatal(err)
	}

	r, err := Load(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Accounts) != 1 || r.Accounts[0].ID != second.ID {
		t.Fatalf("expected only %s to remain, got %+v", second.ID, r.Accounts)
	}
	if r.NextNumber["claude"] != 3 {
		t.Fatalf("expected next_number.claude to stay 3 (no reuse), got %d", r.NextNumber["claude"])
	}
}

func TestRenameChangesOnlyAlias(t *testing.T) {
	dir := t.TempDir()
	registryPath := filepath.Join(dir, "accounts.json")
	account, err := AddAccount(registryPath, "codex", filepath.Join(dir, ".codex"), "old")
	if err != nil {
		t.Fatal(err)
	}
	if err := RenameAccount(registryPath, account.ID, "new"); err != nil {
		t.Fatal(err)
	}
	r, err := Load(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if r.Accounts[0].Alias != "new" {
		t.Fatalf("expected alias 'new', got %q", r.Accounts[0].Alias)
	}
}

func TestSchemaProblemRejectsNextNumberNotAheadOfHighest(t *testing.T) {
	r := Registry{
		Version:    1,
		NextNumber: map[string]int{"claude": 1, "codex": 1},
		Accounts:   []Account{{ID: "claude-1", Provider: "claude", Number: 1, Home: "/tmp/x", Alias: ""}},
	}
	if problem := SchemaProblem(r); problem == "" {
		t.Fatal("expected schema problem when next_number.claude does not exceed the highest registered number")
	}
}

func TestRowsOmitsSuffixForASingleAccountPerProvider(t *testing.T) {
	r := Registry{
		Version:    1,
		NextNumber: map[string]int{"claude": 2, "codex": 1},
		Accounts:   []Account{{ID: "claude-1", Provider: "claude", Number: 1, Home: "/tmp/x", Alias: "me@example.com"}},
	}
	rows := Rows(r)
	if len(rows) != 1 || rows[0].Label != "Claude" {
		t.Fatalf("expected label 'Claude' with no suffix for a lone account, got %+v", rows)
	}
}

func TestRowsAddsNumberAndAliasWhenProviderHasMultipleAccounts(t *testing.T) {
	r := Registry{
		Version:    1,
		NextNumber: map[string]int{"claude": 3, "codex": 1},
		Accounts: []Account{
			{ID: "claude-1", Provider: "claude", Number: 1, Home: "/tmp/a", Alias: ""},
			{ID: "claude-2", Provider: "claude", Number: 2, Home: "/tmp/b", Alias: "me@example.com"},
		},
	}
	rows := Rows(r)
	if rows[1].Label != "Claude · 2 · me@example.com" {
		t.Fatalf("unexpected label: %q", rows[1].Label)
	}
}

func TestWithLockSerializesConcurrentUpdates(t *testing.T) {
	dir := t.TempDir()
	registryPath := filepath.Join(dir, "accounts.json")
	if _, err := AddAccount(registryPath, "claude", filepath.Join(dir, ".claude"), ""); err != nil {
		t.Fatal(err)
	}

	const n = 20
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			_, err := AddAccount(registryPath, "codex", filepath.Join(dir, ".codex", string(rune('a'+i))), "")
			errs <- err
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}

	r, err := Load(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Accounts) != n+1 {
		t.Fatalf("expected %d accounts after concurrent adds, got %d", n+1, len(r.Accounts))
	}
	if r.NextNumber["codex"] != n+1 {
		t.Fatalf("expected next_number.codex=%d, got %d", n+1, r.NextNumber["codex"])
	}
}

// A registry document written before usage_dir existed must still load, with
// the field read back as empty -- "use the caller's built-in default".
func TestRegistryWithoutUsageDirFieldStillLoadsWithEmptyDefault(t *testing.T) {
	dir := t.TempDir()
	registryPath := filepath.Join(dir, "accounts.json")
	content := `{
		"version": 1,
		"next_number": {"claude": 1, "codex": 1},
		"accounts": []
	}`
	if err := os.WriteFile(registryPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	r, err := Load(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if r.UsageDir != "" {
		t.Fatalf("expected UsageDir to default to empty, got %q", r.UsageDir)
	}
}

func TestSetUsageDirClearsOnEmptyInput(t *testing.T) {
	dir := t.TempDir()
	registryPath := filepath.Join(dir, "accounts.json")
	usageDir := filepath.Join(dir, "usage")

	if _, err := SetUsageDir(registryPath, usageDir); err != nil {
		t.Fatal(err)
	}
	r, err := Load(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if r.UsageDir != usageDir {
		t.Fatalf("expected UsageDir to be set to %q, got %q", usageDir, r.UsageDir)
	}

	resolved, err := SetUsageDir(registryPath, "")
	if err != nil {
		t.Fatal(err)
	}
	if resolved != "" {
		t.Fatalf("expected an empty input to report a cleared setting, got %q", resolved)
	}
	r, err = Load(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if r.UsageDir != "" {
		t.Fatalf("expected UsageDir to be cleared, got %q", r.UsageDir)
	}
}

func TestUsageDirSurvivesSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	registryPath := filepath.Join(dir, "accounts.json")
	usageDir := filepath.Join(dir, "usage")

	r := Empty()
	r.UsageDir = usageDir
	if err := Save(registryPath, r); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.UsageDir != usageDir {
		t.Fatalf("expected UsageDir %q to survive a save/load round trip, got %q", usageDir, loaded.UsageDir)
	}
}
