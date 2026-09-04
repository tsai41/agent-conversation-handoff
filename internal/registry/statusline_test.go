package registry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// realisticSettingsDoc is a hand-formatted settings.json standing in for a
// real, hand-maintained document: many top-level keys, a hooks table whose
// own entries carry "command" fields that must not be mistaken for
// statusLine's, and a sizeable permissions list. command is embedded
// verbatim (unescaped) as statusLine.command, so tests can locate and
// replace it in the raw text to compute what a correct edit should produce.
func realisticSettingsDoc(command string) string {
	return `{
  "model": "opusplan",
  "theme": "dark",
  "includeCoAuthoredBy": false,
  "cleanupPeriodDays": 30,
  "env": {
    "ANTHROPIC_LOG": "info"
  },
  "statusLine": {
    "type": "command",
    "command": "` + command + `",
    "padding": 0
  },
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "~/.claude/hooks/bash-guard.py"
          }
        ]
      }
    ],
    "SubagentStop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "~/.claude/hooks/subagent-model-guard.mjs"
          }
        ]
      }
    ]
  },
  "permissions": {
    "allow": [
      "Bash(git status:*)",
      "Bash(git diff:*)",
      "Bash(git log:*)",
      "Bash(go build:*)",
      "Bash(go vet:*)",
      "Bash(go test:*)",
      "Bash(gofmt:*)",
      "Read(~/.claude/**)",
      "Edit(~/go/src/**)",
      "Write(~/go/src/**)"
    ],
    "deny": [
      "Bash(rm -rf /:*)",
      "Read(~/.ssh/**)"
    ]
  },
  "extra": {
    "nested": {
      "deeply": [1, 2, 3]
    }
  },
  "spacing": "irregular",
  "tabs":	"a value after a literal tab"
}
`
}

// backupNamePattern matches freeBackupPath's ".bak-<timestamp>" naming
// scheme so a test can assert a real backup was made, not merely that some
// file exists.
var backupNamePattern = regexp.MustCompile(`^settings\.json\.bak-\d{8}-\d{6}(-\d+)?$`)

func jsonStringLiteral(t *testing.T, s string) string {
	t.Helper()
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// claudeAccountWithSettings writes doc as the sole registered claude
// account's settings.json and returns a Registry containing it as the
// primary account.
func claudeAccountWithSettings(t *testing.T, doc string) (Registry, string) {
	t.Helper()
	home := t.TempDir()
	acc := account("claude-1", "claude", home, 1)
	settingsPath := EntryPath(acc, SettingsEntry{Name: "settings.json", Kind: EntryFile})
	if err := os.WriteFile(settingsPath, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	return Registry{Accounts: []Account{acc}}, settingsPath
}

func TestEnableStatuslineUsageDirLeavesEveryOtherByteUnchanged(t *testing.T) {
	const oldCommand = "~/.claude/statusline-go"
	doc := realisticSettingsDoc(oldCommand)
	r, settingsPath := claudeAccountWithSettings(t, doc)
	usageDir := filepath.Join(t.TempDir(), "usage")

	oldLiteral := jsonStringLiteral(t, oldCommand)
	if count := strings.Count(doc, oldLiteral); count != 1 {
		t.Fatalf("test fixture is ambiguous: %q appears %d times", oldLiteral, count)
	}
	newCommand := oldCommand + " --usage-dir " + usageDir
	newLiteral := jsonStringLiteral(t, newCommand)
	want := strings.Replace(doc, oldLiteral, newLiteral, 1)

	change, err := EnableStatuslineUsageDir(r, usageDir, false)
	if err != nil {
		t.Fatal(err)
	}
	if change.Outcome != StatuslineWrote {
		t.Fatalf("expected StatuslineWrote, got %v", change.Outcome)
	}
	got, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("edit touched more than statusLine.command:\n got:  %q\n want: %q", got, want)
	}
	if !change.Info.Matches || change.Info.UsageDir != usageDir {
		t.Fatalf("expected the resulting state to report a match, got %+v", change.Info)
	}

	// A backup of the pre-write document must exist, named the way
	// freeBackupPath names it, and hold the original bytes.
	if change.Backup == "" {
		t.Fatal("expected a backup path to be reported")
	}
	if !backupNamePattern.MatchString(filepath.Base(change.Backup)) {
		t.Fatalf("backup name %q does not match the .bak-<timestamp> scheme", filepath.Base(change.Backup))
	}
	backupRaw, err := os.ReadFile(change.Backup)
	if err != nil {
		t.Fatalf("backup is unreadable: %v", err)
	}
	if string(backupRaw) != doc {
		t.Fatalf("backup does not hold the pre-write document: %q", backupRaw)
	}
}

func TestEnableStatuslineUsageDirIsIdempotent(t *testing.T) {
	doc := realisticSettingsDoc("~/.claude/statusline-go")
	r, settingsPath := claudeAccountWithSettings(t, doc)
	usageDir := filepath.Join(t.TempDir(), "usage")

	first, err := EnableStatuslineUsageDir(r, usageDir, false)
	if err != nil {
		t.Fatal(err)
	}
	if first.Outcome != StatuslineWrote {
		t.Fatalf("expected the first call to write, got %v", first.Outcome)
	}
	afterFirst, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}

	second, err := EnableStatuslineUsageDir(r, usageDir, false)
	if err != nil {
		t.Fatal(err)
	}
	if second.Outcome != StatuslineAlreadyEnabled {
		t.Fatalf("expected the second call to be a no-op, got %v", second.Outcome)
	}
	afterSecond, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterFirst) != string(afterSecond) {
		t.Fatalf("a second enable changed the document:\nafter 1st: %q\nafter 2nd: %q", afterFirst, afterSecond)
	}
	if strings.Count(second.Info.Command, statuslineUsageDirFlag) != 1 {
		t.Fatalf("expected exactly one --usage-dir in %q", second.Info.Command)
	}
}

func TestDisableStatuslineUsageDirRemovesExactlyTheArgument(t *testing.T) {
	const original = "~/.claude/statusline-go --padding-note irrelevant"
	doc := realisticSettingsDoc(original)
	r, settingsPath := claudeAccountWithSettings(t, doc)
	usageDir := filepath.Join(t.TempDir(), "usage")

	if _, err := EnableStatuslineUsageDir(r, usageDir, false); err != nil {
		t.Fatal(err)
	}

	change, err := DisableStatuslineUsageDir(r, usageDir)
	if err != nil {
		t.Fatal(err)
	}
	if change.Outcome != StatuslineWrote {
		t.Fatalf("expected StatuslineWrote, got %v", change.Outcome)
	}
	if change.Info.Command != original {
		t.Fatalf("expected disabling to restore %q, got %q", original, change.Info.Command)
	}
	if change.Info.HasUsageDir {
		t.Fatalf("expected no --usage-dir after disabling, got %+v", change.Info)
	}
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), statuslineUsageDirFlag) {
		t.Fatalf("the flag is still present in the document: %s", raw)
	}
}

func TestDisableStatuslineUsageDirWhenAbsentIsANoOp(t *testing.T) {
	doc := realisticSettingsDoc("~/.claude/statusline-go")
	r, settingsPath := claudeAccountWithSettings(t, doc)

	change, err := DisableStatuslineUsageDir(r, filepath.Join(t.TempDir(), "usage"))
	if err != nil {
		t.Fatal(err)
	}
	if change.Outcome != StatuslineAlreadyDisabled {
		t.Fatalf("expected StatuslineAlreadyDisabled, got %v", change.Outcome)
	}
	if change.Backup != "" {
		t.Fatalf("expected no backup for a no-op, got %s", change.Backup)
	}
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != doc {
		t.Fatal("a no-op disable modified the document")
	}
}

func TestEnableStatuslineUsageDirRefusesUnsafeShapes(t *testing.T) {
	tests := []struct {
		name string
		doc  string
	}{
		{
			name: "no statusLine field",
			doc:  `{"model": "opusplan", "theme": "dark"}`,
		},
		{
			name: "command is not a plain string",
			doc: `{
  "statusLine": {
    "type": "command",
    "command": ["not", "a", "string"],
    "padding": 0
  }
}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, settingsPath := claudeAccountWithSettings(t, tt.doc)
			usageDir := filepath.Join(t.TempDir(), "usage")

			change, err := EnableStatuslineUsageDir(r, usageDir, false)
			if err != nil {
				t.Fatal(err)
			}
			if change.Outcome != StatuslineRefused {
				t.Fatalf("expected StatuslineRefused, got %v", change.Outcome)
			}
			if change.Info.Refusal == "" {
				t.Fatal("expected a Refusal reason")
			}
			if !strings.Contains(change.Info.Snippet, statuslinePlaceholder) {
				t.Fatalf("expected the snippet to carry the placeholder, got %q", change.Info.Snippet)
			}
			if !strings.Contains(change.Info.Snippet, usageDir) {
				t.Fatalf("expected the snippet to carry the real usage dir, got %q", change.Info.Snippet)
			}
			raw, err := os.ReadFile(settingsPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(raw) != tt.doc {
				t.Fatal("a refused enable modified the document")
			}
			assertNoBackupFiles(t, filepath.Dir(settingsPath))
		})
	}
}

func TestEnableStatuslineUsageDirReportsAMismatchedExistingDirectory(t *testing.T) {
	oldDir := filepath.Join(t.TempDir(), "old-usage")
	doc := realisticSettingsDoc("~/.claude/statusline-go --usage-dir " + oldDir)
	r, settingsPath := claudeAccountWithSettings(t, doc)
	newDir := filepath.Join(t.TempDir(), "new-usage")

	change, err := EnableStatuslineUsageDir(r, newDir, false)
	if err != nil {
		t.Fatal(err)
	}
	if change.Outcome != StatuslineMismatch {
		t.Fatalf("expected StatuslineMismatch, got %v", change.Outcome)
	}
	if change.Info.UsageDir != oldDir || change.Info.Matches {
		t.Fatalf("expected the existing (mismatched) directory to be reported, got %+v", change.Info)
	}
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != doc {
		t.Fatal("a reported mismatch modified the document without replace=true")
	}

	// The user can still explicitly replace it.
	replaced, err := EnableStatuslineUsageDir(r, newDir, true)
	if err != nil {
		t.Fatal(err)
	}
	if replaced.Outcome != StatuslineWrote || !replaced.Info.Matches || replaced.Info.UsageDir != newDir {
		t.Fatalf("expected replace=true to switch to the new directory, got %+v", replaced.Info)
	}
}

func assertNoBackupFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".bak-") {
			t.Fatalf("expected no backup file, found %s", e.Name())
		}
	}
}
