package registry

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

// trustBackupNamePattern matches freeBackupPath's ".bak-<timestamp>" naming
// scheme applied to .claude.json, the way backupNamePattern in
// statusline_test.go matches it for settings.json.
var trustBackupNamePattern = regexp.MustCompile(`^\.claude\.json\.bak-\d{8}-\d{6}(-\d+)?$`)

// claudeAccountHome creates a fresh account home (as account creation would
// leave it) and returns a claude Account rooted there.
func claudeAccountHome(t *testing.T, id string, number int) Account {
	t.Helper()
	return account(id, "claude", t.TempDir(), number)
}

func writeClaudeJSON(t *testing.T, acc Account, doc string) {
	t.Helper()
	path, err := ClaudeConfigFile(acc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
}

// projectsOf decodes doc's "projects" object for comparison, failing the
// test if doc is not valid JSON or has no such object.
func projectsOf(t *testing.T, doc string) map[string]any {
	t.Helper()
	decoded, err := decodeClaudeJSONBytes([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	projects, ok := decoded["projects"].(map[string]any)
	if !ok {
		t.Fatalf("fixture has no projects object: %s", doc)
	}
	return projects
}

func TestSyncProjectTrustMergeBehavior(t *testing.T) {
	tests := []struct {
		name         string
		sourceDoc    string
		targetDoc    string
		wantProjects string
		wantAdded    int
		wantFilled   int
	}{
		{
			name:      "fills absent whitelist fields into an existing project",
			sourceDoc: `{"projects": {"/repo/a": {"hasTrustDialogAccepted": true, "allowedTools": ["Bash(git:*)"], "notWhitelisted": "source-value"}}}`,
			targetDoc: `{"projects": {"/repo/a": {"notWhitelisted": "target-value"}}}`,
			wantProjects: `{"/repo/a": {
				"hasTrustDialogAccepted": true,
				"allowedTools": ["Bash(git:*)"],
				"notWhitelisted": "target-value"
			}}`,
			wantAdded:  0,
			wantFilled: 2,
		},
		{
			name:      "adds a project absent at the target with only whitelist fields",
			sourceDoc: `{"projects": {"/repo/b": {"hasTrustDialogAccepted": false, "mcpContextUris": ["file:///x"], "notWhitelisted": "source-only"}}}`,
			targetDoc: `{"projects": {}}`,
			wantProjects: `{"/repo/b": {
				"hasTrustDialogAccepted": false,
				"mcpContextUris": ["file:///x"]
			}}`,
			wantAdded:  1,
			wantFilled: 0,
		},
		{
			name:      "never overwrites an existing false",
			sourceDoc: `{"projects": {"/repo/c": {"hasTrustDialogAccepted": true, "allowedTools": ["Bash(git:*)"]}}}`,
			targetDoc: `{"projects": {"/repo/c": {"hasTrustDialogAccepted": false}}}`,
			wantProjects: `{"/repo/c": {
				"hasTrustDialogAccepted": false,
				"allowedTools": ["Bash(git:*)"]
			}}`,
			wantAdded:  0,
			wantFilled: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := claudeAccountHome(t, "claude-1", 1)
			target := claudeAccountHome(t, "claude-2", 2)
			writeClaudeJSON(t, source, tt.sourceDoc)
			writeClaudeJSON(t, target, tt.targetDoc)

			result, err := SyncProjectTrust(source, target)
			if err != nil {
				t.Fatal(err)
			}
			if result.Skipped != "" {
				t.Fatalf("expected a write, got Skipped: %s", result.Skipped)
			}
			if result.ProjectsAdded != tt.wantAdded || result.FieldsFilled != tt.wantFilled {
				t.Fatalf("got ProjectsAdded=%d FieldsFilled=%d, want %d/%d", result.ProjectsAdded, result.FieldsFilled, tt.wantAdded, tt.wantFilled)
			}

			written, err := decodeClaudeJSON(configFileOf(t, target))
			if err != nil {
				t.Fatal(err)
			}
			got, _ := written["projects"].(map[string]any)
			want := projectsOf(t, `{"projects": `+tt.wantProjects+`}`)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("projects after merge = %#v, want %#v", got, want)
			}
		})
	}
}

// TestSyncProjectTrustPreservesEverythingOutsideTheWhitelist proves the merge
// touches only whitelist fields on projects the source also has: other
// top-level keys, a project only the target has, and non-whitelist keys on a
// project both sides have must all survive value-for-value, and the two
// numbers most likely to lose precision through a naive round-trip
// (a 13-digit millisecond timestamp and a fractional cost) must still be
// present as the exact literal that was written. It also proves the
// pre-write backup this same call makes holds the original bytes under the
// naming scheme freeBackupPath uses.
func TestSyncProjectTrustPreservesEverythingOutsideTheWhitelist(t *testing.T) {
	source := claudeAccountHome(t, "claude-1", 1)
	target := claudeAccountHome(t, "claude-2", 2)
	writeClaudeJSON(t, source, `{
		"oauthAccount": {"accountUuid": "source-uuid"},
		"userID": "source-user-id",
		"numStartups": 999,
		"projects": {
			"/repo/a": {
				"hasTrustDialogAccepted": true,
				"allowedTools": ["Bash(git:*)"],
				"someOtherKey": "source-value"
			}
		}
	}`)
	targetDoc := `{
		"oauthAccount": {"accountUuid": "target-uuid"},
		"userID": "target-user-id",
		"numStartups": 7,
		"lastCost": 0.1234567,
		"someTimestamp": 1757000000000,
		"hasCompletedOnboarding": true,
		"projects": {
			"/repo/a": {
				"someOtherKey": "target-value",
				"lastStartupTime": 1757000000000
			},
			"/repo/untouched": {
				"hasTrustDialogAccepted": false,
				"customField": "keep-me"
			}
		}
	}`
	writeClaudeJSON(t, target, targetDoc)

	result, err := SyncProjectTrust(source, target)
	if err != nil {
		t.Fatal(err)
	}
	if result.Skipped != "" {
		t.Fatalf("expected a write, got Skipped: %s", result.Skipped)
	}
	if result.ProjectsAdded != 0 || result.FieldsFilled != 2 {
		t.Fatalf("got ProjectsAdded=%d FieldsFilled=%d, want 0/2", result.ProjectsAdded, result.FieldsFilled)
	}

	original, err := decodeClaudeJSONBytes([]byte(targetDoc))
	if err != nil {
		t.Fatal(err)
	}
	rawWritten, err := os.ReadFile(configFileOf(t, target))
	if err != nil {
		t.Fatal(err)
	}
	written, err := decodeClaudeJSONBytes(rawWritten)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"oauthAccount", "userID", "numStartups", "lastCost", "someTimestamp", "hasCompletedOnboarding"} {
		if !reflect.DeepEqual(written[key], original[key]) {
			t.Fatalf("top-level key %q changed: got %#v, want %#v", key, written[key], original[key])
		}
	}
	writtenProjects := written["projects"].(map[string]any)
	if !reflect.DeepEqual(writtenProjects["/repo/untouched"], original["projects"].(map[string]any)["/repo/untouched"]) {
		t.Fatalf("project the source never mentioned was changed: got %#v", writtenProjects["/repo/untouched"])
	}
	repoA := writtenProjects["/repo/a"].(map[string]any)
	if repoA["someOtherKey"] != "target-value" {
		t.Fatalf("non-whitelist key on a shared project was touched: %#v", repoA)
	}
	if fmtNumber(t, repoA["lastStartupTime"]) != "1757000000000" {
		t.Fatalf("non-whitelist numeric key on a shared project was touched: %#v", repoA["lastStartupTime"])
	}

	if !strings.Contains(string(rawWritten), "1757000000000") {
		t.Fatalf("expected the 13-digit timestamp literal verbatim in the written file, got: %s", rawWritten)
	}
	if !strings.Contains(string(rawWritten), "0.1234567") {
		t.Fatalf("expected the fractional-cost literal verbatim in the written file, got: %s", rawWritten)
	}

	if result.Backup == "" {
		t.Fatal("expected a backup path")
	}
	if !trustBackupNamePattern.MatchString(filepath.Base(result.Backup)) {
		t.Fatalf("backup name %q does not match the .bak-<timestamp> scheme", filepath.Base(result.Backup))
	}
	backupRaw, err := os.ReadFile(result.Backup)
	if err != nil {
		t.Fatalf("backup is unreadable: %v", err)
	}
	if string(backupRaw) != targetDoc {
		t.Fatalf("backup does not hold the pre-write document:\n got:  %q\n want: %q", backupRaw, targetDoc)
	}
}

// fmtNumber renders a decoded json.Number (or any other decoded scalar) as
// its String()-equivalent text so a test can compare it against a literal
// without caring whether UseNumber produced a json.Number or something else.
func fmtNumber(t *testing.T, v any) string {
	t.Helper()
	type stringer interface{ String() string }
	if s, ok := v.(stringer); ok {
		return s.String()
	}
	t.Fatalf("value is not a number: %#v", v)
	return ""
}

func TestSyncProjectTrustSkippedWhenTargetFileMissing(t *testing.T) {
	source := claudeAccountHome(t, "claude-1", 1)
	target := claudeAccountHome(t, "claude-2", 2)
	writeClaudeJSON(t, source, `{"projects": {"/repo/a": {"hasTrustDialogAccepted": true}}}`)

	result, err := SyncProjectTrust(source, target)
	if err != nil {
		t.Fatal(err)
	}
	if result.Skipped == "" {
		t.Fatal("expected Skipped to be set")
	}
	if result.ProjectsAdded != 0 || result.FieldsFilled != 0 || result.Backup != "" {
		t.Fatalf("expected a no-op result, got %+v", result)
	}
	if _, err := os.Stat(configFileOf(t, target)); !os.IsNotExist(err) {
		t.Fatalf("expected no .claude.json to have been created, stat err = %v", err)
	}
}

func TestSyncProjectTrustSkippedWhenLockFileExists(t *testing.T) {
	source := claudeAccountHome(t, "claude-1", 1)
	target := claudeAccountHome(t, "claude-2", 2)
	writeClaudeJSON(t, source, `{"projects": {"/repo/a": {"hasTrustDialogAccepted": true}}}`)
	targetDoc := `{"projects": {}}`
	writeClaudeJSON(t, target, targetDoc)
	if err := os.WriteFile(configFileOf(t, target)+".lock", nil, 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := SyncProjectTrust(source, target)
	if err != nil {
		t.Fatal(err)
	}
	if result.Skipped == "" {
		t.Fatal("expected Skipped to be set")
	}
	if result.Backup != "" {
		t.Fatalf("expected no backup, got %s", result.Backup)
	}
	raw, err := os.ReadFile(configFileOf(t, target))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != targetDoc {
		t.Fatal("a locked target was modified")
	}
}

func TestSyncProjectTrustSkippedWithNoBackupWhenAlreadyInSync(t *testing.T) {
	source := claudeAccountHome(t, "claude-1", 1)
	target := claudeAccountHome(t, "claude-2", 2)
	doc := `{"projects": {"/repo/a": {"hasTrustDialogAccepted": true}}}`
	writeClaudeJSON(t, source, doc)
	writeClaudeJSON(t, target, doc)

	result, err := SyncProjectTrust(source, target)
	if err != nil {
		t.Fatal(err)
	}
	if result.Skipped == "" {
		t.Fatal("expected Skipped to be set")
	}
	if result.Backup != "" {
		t.Fatalf("expected no backup, got %s", result.Backup)
	}
	entries, err := os.ReadDir(target.Home)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".bak-") {
			t.Fatalf("expected no backup file, found %s", e.Name())
		}
	}
}

func TestSyncProjectTrustRefusesCodexAccounts(t *testing.T) {
	tests := []struct {
		name           string
		sourceProvider string
		targetProvider string
	}{
		{"source is codex", "codex", "claude"},
		{"target is codex", "claude", "codex"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := account("src", tt.sourceProvider, t.TempDir(), 1)
			target := account("tgt", tt.targetProvider, t.TempDir(), 2)

			result, err := SyncProjectTrust(source, target)
			if err == nil {
				t.Fatal("expected an error refusing a non-claude account")
			}
			if (result != TrustSync{}) {
				t.Fatalf("expected a zero-value result on error, got %+v", result)
			}
		})
	}
}

func TestSyncProjectTrustRefusesSameHome(t *testing.T) {
	home := t.TempDir()
	source := account("claude-1", "claude", home, 1)
	target := account("claude-2", "claude", home, 2)
	writeClaudeJSON(t, source, `{"projects": {"/repo/a": {"hasTrustDialogAccepted": true}}}`)

	_, err := SyncProjectTrust(source, target)
	if err == nil {
		t.Fatal("expected an error refusing the same account home")
	}
}

func TestSyncProjectTrustToAllOrdersAndExcludesNonTargets(t *testing.T) {
	source := claudeAccountHome(t, "claude-1", 1)
	writeClaudeJSON(t, source, `{"projects": {"/repo/a": {"hasTrustDialogAccepted": true}}}`)
	second := claudeAccountHome(t, "claude-2", 2)
	third := claudeAccountHome(t, "claude-3", 3)
	codex := account("codex-1", "codex", t.TempDir(), 1)

	r := Registry{Accounts: []Account{third, codex, source, second}}
	results, err := SyncProjectTrustToAll(r, source)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 targets (excluding the source and the codex account), got %d: %+v", len(results), results)
	}
	if results[0].Target.ID != "claude-2" || results[1].Target.ID != "claude-3" {
		t.Fatalf("expected claude-2 then claude-3 in Number order, got %s then %s", results[0].Target.ID, results[1].Target.ID)
	}
}

func TestSyncProjectTrustToAllRefusesNonClaudeSource(t *testing.T) {
	source := account("codex-1", "codex", t.TempDir(), 1)
	target := claudeAccountHome(t, "claude-1", 1)
	r := Registry{Accounts: []Account{source, target}}

	_, err := SyncProjectTrustToAll(r, source)
	if err == nil {
		t.Fatal("expected an error refusing a non-claude source")
	}
}

func configFileOf(t *testing.T, acc Account) string {
	t.Helper()
	path, err := ClaudeConfigFile(acc)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestClaudeConfigFileSitsBesideTheDefaultHomeAndInsideOthers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	defaultHome := filepath.Join(home, ".claude")
	if err := os.MkdirAll(defaultHome, 0o755); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(home, ".claude-2")

	got, err := ClaudeConfigFile(account("claude-1", "claude", defaultHome, 1))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".claude.json"); got != want {
		t.Fatalf("default home: got %q, want %q", got, want)
	}
	got, err = ClaudeConfigFile(account("claude-2", "claude", other, 2))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(other, ".claude.json"); got != want {
		t.Fatalf("other home: got %q, want %q", got, want)
	}
}

func TestSyncProjectTrustRefusesDocumentsOfUnexpectedShape(t *testing.T) {
	const sourceDoc = `{"projects": {"/repo/a": {"hasTrustDialogAccepted": true}}}`
	cases := []struct {
		name      string
		targetDoc string
		wantErr   bool
	}{
		{name: "target is null", targetDoc: `null`, wantErr: true},
		{name: "projects is a string", targetDoc: `{"projects": "nope"}`},
		{name: "project value is an array", targetDoc: `{"projects": {"/repo/a": []}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := claudeAccountHome(t, "claude-1", 1)
			target := claudeAccountHome(t, "claude-2", 2)
			writeClaudeJSON(t, source, sourceDoc)
			writeClaudeJSON(t, target, tc.targetDoc)

			result, err := SyncProjectTrust(source, target)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
			} else if err != nil || result.Skipped == "" {
				t.Fatalf("expected a refusal via Skipped, got %+v, %v", result, err)
			}
			raw, err := os.ReadFile(configFileOf(t, target))
			if err != nil {
				t.Fatal(err)
			}
			if string(raw) != tc.targetDoc {
				t.Fatalf("a refused document was modified: %s", raw)
			}
			assertNoBackupFiles(t, target.Home)
		})
	}
}

func TestSyncProjectTrustCreatesProjectsWhenTargetHasNull(t *testing.T) {
	source := claudeAccountHome(t, "claude-1", 1)
	target := claudeAccountHome(t, "claude-2", 2)
	writeClaudeJSON(t, source, `{"projects": {"/repo/a": {"hasTrustDialogAccepted": true}}}`)
	writeClaudeJSON(t, target, `{"projects": null, "numStartups": 3}`)

	result, err := SyncProjectTrust(source, target)
	if err != nil {
		t.Fatal(err)
	}
	if result.ProjectsAdded != 1 || result.Skipped != "" {
		t.Fatalf("expected one project added, got %+v", result)
	}
	raw, err := os.ReadFile(configFileOf(t, target))
	if err != nil {
		t.Fatal(err)
	}
	projects := projectsOf(t, string(raw))
	if _, ok := projects["/repo/a"].(map[string]any); !ok {
		t.Fatalf("expected /repo/a to be created, got %s", raw)
	}
	if !strings.Contains(string(raw), `"numStartups": 3`) {
		t.Fatalf("expected the top-level key to survive, got %s", raw)
	}
}

func TestSyncProjectTrustTreatsANullSourceValueAsAbsent(t *testing.T) {
	source := claudeAccountHome(t, "claude-1", 1)
	target := claudeAccountHome(t, "claude-2", 2)
	writeClaudeJSON(t, source, `{"projects": {"/repo/a": {"allowedTools": null, "hasTrustDialogAccepted": true}}}`)
	writeClaudeJSON(t, target, `{"projects": {"/repo/a": {}}}`)

	result, err := SyncProjectTrust(source, target)
	if err != nil {
		t.Fatal(err)
	}
	if result.FieldsFilled != 1 {
		t.Fatalf("expected only the non-null field to be filled, got %+v", result)
	}
	raw, err := os.ReadFile(configFileOf(t, target))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "allowedTools") {
		t.Fatalf("a null source value was copied: %s", raw)
	}
}

func TestSyncProjectTrustReleasesTheLockAfterWritingAndAfterRefusing(t *testing.T) {
	source := claudeAccountHome(t, "claude-1", 1)
	writeClaudeJSON(t, source, `{"projects": {"/repo/a": {"hasTrustDialogAccepted": true}}}`)
	for _, tc := range []struct{ name, targetDoc string }{
		{name: "written", targetDoc: `{"projects": {}}`},
		{name: "refused", targetDoc: `{"projects": "nope"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := claudeAccountHome(t, "claude-2", 2)
			writeClaudeJSON(t, target, tc.targetDoc)
			if _, err := SyncProjectTrust(source, target); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(configFileOf(t, target) + ".lock"); !os.IsNotExist(err) {
				t.Fatalf("lock left behind: %v", err)
			}
		})
	}
}

func TestSyncProjectTrustToAllWritesEveryTargetAndKeepsGoingPastAFailure(t *testing.T) {
	source := claudeAccountHome(t, "claude-1", 1)
	broken := claudeAccountHome(t, "claude-2", 2)
	good := claudeAccountHome(t, "claude-3", 3)
	other := claudeAccountHome(t, "claude-4", 4)
	writeClaudeJSON(t, source, `{"projects": {"/repo/a": {"hasTrustDialogAccepted": true}}}`)
	writeClaudeJSON(t, broken, `{not json`)
	writeClaudeJSON(t, good, `{"projects": {}}`)
	writeClaudeJSON(t, other, `{"projects": {"/repo/b": {"allowedTools": []}}}`)
	r := Registry{Accounts: []Account{other, source, good, broken}}

	results, err := SyncProjectTrustToAll(r, source)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("expected one result per target, got %d", len(results))
	}
	if results[0].Target.ID != "claude-2" || results[0].Err == nil {
		t.Fatalf("expected the broken target first with its error recorded, got %+v", results[0])
	}
	for _, result := range results[1:] {
		if result.Err != nil || result.ProjectsAdded != 1 || result.Backup == "" {
			t.Fatalf("expected %s to be written after the failure, got %+v", result.Target.ID, result)
		}
	}
}

func TestSyncProjectTrustTakesOverAStaleLockButNotALiveOne(t *testing.T) {
	source := claudeAccountHome(t, "claude-1", 1)
	writeClaudeJSON(t, source, `{"projects": {"/repo/a": {"hasTrustDialogAccepted": true}}}`)
	for _, tc := range []struct {
		name    string
		age     time.Duration
		written bool
	}{
		{name: "live lock is respected", age: 0, written: false},
		{name: "stale lock is taken over", age: 2 * claudeLockStale, written: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := claudeAccountHome(t, "claude-2", 2)
			writeClaudeJSON(t, target, `{"projects": {}}`)
			lockPath := configFileOf(t, target) + ".lock"
			if err := os.Mkdir(lockPath, 0o755); err != nil {
				t.Fatal(err)
			}
			stamp := time.Now().Add(-tc.age)
			if err := os.Chtimes(lockPath, stamp, stamp); err != nil {
				t.Fatal(err)
			}
			result, err := SyncProjectTrust(source, target)
			if err != nil {
				t.Fatal(err)
			}
			if tc.written != (result.ProjectsAdded == 1) {
				t.Fatalf("written=%v, got %+v", tc.written, result)
			}
			if !tc.written && !strings.Contains(result.Skipped, lockPath) {
				t.Fatalf("expected the skip reason to name the lock, got %q", result.Skipped)
			}
			if tc.written {
				if _, err := os.Lstat(lockPath); !os.IsNotExist(err) {
					t.Fatalf("lock left behind after takeover: %v", err)
				}
			}
		})
	}
}

func TestDecodeClaudeJSONRejectsDataAfterTheDocument(t *testing.T) {
	if _, err := decodeClaudeJSONBytes([]byte(`{"projects": {}} garbage`)); err == nil {
		t.Fatal("expected trailing data to be rejected")
	}
	if _, err := decodeClaudeJSONBytes([]byte("{\"projects\": {}}\n")); err != nil {
		t.Fatalf("a trailing newline must still decode: %v", err)
	}
}
