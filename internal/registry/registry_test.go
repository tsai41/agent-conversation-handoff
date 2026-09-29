package registry

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

func idsOf(accounts []Account) []string {
	ids := make([]string, len(accounts))
	for i, account := range accounts {
		ids[i] = account.ID
	}
	return ids
}

func TestDisplayOrderMovesArchivedToTheEndKeepingRegistryOrder(t *testing.T) {
	tests := []struct {
		name     string
		archived map[string]bool
		want     []string
	}{
		{"none archived", nil, []string{"claude-1", "codex-1", "claude-2"}},
		{"mixed", map[string]bool{"claude-1": true}, []string{"codex-1", "claude-2", "claude-1"}},
		{"mixed keeps relative order", map[string]bool{"claude-1": true, "codex-1": true}, []string{"claude-2", "claude-1", "codex-1"}},
		{"all archived", map[string]bool{"claude-1": true, "codex-1": true, "claude-2": true}, []string{"claude-1", "codex-1", "claude-2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Registry{Accounts: []Account{
				{ID: "claude-1", Provider: "claude", Number: 1},
				{ID: "codex-1", Provider: "codex", Number: 1},
				{ID: "claude-2", Provider: "claude", Number: 2},
			}}
			for i := range r.Accounts {
				r.Accounts[i].Archived = tt.archived[r.Accounts[i].ID]
			}
			got := idsOf(DisplayOrder(r))
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
			if r.Accounts[0].ID != "claude-1" || r.Accounts[1].ID != "codex-1" {
				t.Fatalf("DisplayOrder must not reorder the registry itself: %v", idsOf(r.Accounts))
			}
		})
	}
}

func TestRowsPutsArchivedLastWithSuffix(t *testing.T) {
	r := Registry{
		Version:    1,
		NextNumber: map[string]int{"claude": 3, "codex": 2},
		Accounts: []Account{
			{ID: "claude-1", Provider: "claude", Number: 1, Home: "/tmp/a", Archived: true},
			{ID: "claude-2", Provider: "claude", Number: 2, Home: "/tmp/b"},
			{ID: "codex-1", Provider: "codex", Number: 1, Home: "/tmp/c"},
		},
	}
	rows := Rows(r)
	wantIDs := []string{"claude-2", "codex-1", "claude-1"}
	for i, want := range wantIDs {
		if rows[i].ID != want {
			t.Fatalf("expected row %d to be %s, got %+v", i, want, rows)
		}
	}
	if rows[2].Label != "Claude · 1（已封存）" {
		t.Fatalf("unexpected archived label: %q", rows[2].Label)
	}
	if rows[1].Label != "Codex" {
		t.Fatalf("unarchived label must carry no suffix: %q", rows[1].Label)
	}
}

func TestSetArchivedPersistsAndTogglesBack(t *testing.T) {
	dir := t.TempDir()
	registryPath := filepath.Join(dir, "accounts.json")
	if _, err := AddAccount(registryPath, "claude", filepath.Join(dir, ".claude"), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := AddAccount(registryPath, "claude", filepath.Join(dir, ".claude-2"), ""); err != nil {
		t.Fatal(err)
	}

	for _, archived := range []bool{true, false} {
		if err := SetArchived(registryPath, "claude-1", archived); err != nil {
			t.Fatal(err)
		}
		r, err := Load(registryPath)
		if err != nil {
			t.Fatal(err)
		}
		if r.Accounts[0].Archived != archived || r.Accounts[1].Archived {
			t.Fatalf("after SetArchived(%v): %+v", archived, r.Accounts)
		}
		if r.Accounts[0].ID != "claude-1" || r.NextNumber["claude"] != 3 {
			t.Fatalf("order and numbering must not change: %+v", r)
		}
	}
	raw, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "archived") {
		t.Fatalf("a false flag must be omitted from the file: %s", raw)
	}
}

func TestToggleArchivedReturnsTheNewStateAndPersistsIt(t *testing.T) {
	registryPath := filepath.Join(t.TempDir(), "accounts.json")
	if err := Save(registryPath, Registry{Version: 1, NextNumber: map[string]int{"claude": 3, "codex": 1}, Accounts: []Account{
		{ID: "claude-1", Provider: "claude", Number: 1, Home: "/tmp/a"},
		{ID: "claude-2", Provider: "claude", Number: 2, Home: "/tmp/b"},
	}}); err != nil {
		t.Fatal(err)
	}

	for _, want := range []bool{true, false} {
		got, err := ToggleArchived(registryPath, "claude-1")
		if err != nil {
			t.Fatal(err)
		}
		r, err := Load(registryPath)
		if err != nil {
			t.Fatal(err)
		}
		if got != want || r.Accounts[0].Archived != want || r.Accounts[1].Archived {
			t.Fatalf("expected the toggle to return and persist %v only for claude-1, got %v: %+v", want, got, r.Accounts)
		}
	}

	if _, err := ToggleArchived(registryPath, "claude-9"); err == nil {
		t.Fatal("expected an error for an unregistered account")
	}
}

func TestSetArchivedRejectsAnUnknownID(t *testing.T) {
	dir := t.TempDir()
	registryPath := filepath.Join(dir, "accounts.json")
	if _, err := AddAccount(registryPath, "claude", filepath.Join(dir, ".claude"), ""); err != nil {
		t.Fatal(err)
	}
	if err := SetArchived(registryPath, "claude-9", true); err == nil {
		t.Fatal("expected an unknown id to be rejected")
	}
}

func TestSetArchivedLeavesTheAccountHomeAlone(t *testing.T) {
	dir := t.TempDir()
	registryPath := filepath.Join(dir, "accounts.json")
	home := filepath.Join(dir, ".claude")
	if _, err := AddAccount(registryPath, "claude", home, ""); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(home, "history.jsonl")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetArchived(registryPath, "claude-1", true); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(marker); err != nil || string(raw) != "keep" {
		t.Fatalf("expected the home to be untouched, got %q, %v", raw, err)
	}
}

func TestRegistryWithoutArchivedFieldLoadsAsNotArchived(t *testing.T) {
	dir := t.TempDir()
	registryPath := filepath.Join(dir, "accounts.json")
	content := `{
		"version": 1,
		"next_number": {"claude": 2, "codex": 1},
		"accounts": [{"id": "claude-1", "provider": "claude", "number": 1, "home": "/tmp/legacy", "alias": ""}]
	}`
	if err := os.WriteFile(registryPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := Load(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if r.Accounts[0].Archived {
		t.Fatal("a missing archived field must mean not archived")
	}
}
