package registry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%s is not valid JSON: %v", path, err)
	}
	return out
}

func TestCopySettingsKeySeedsAnAccountHomeThatHasNoSettingsYet(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	target := filepath.Join(dir, "target")
	os.MkdirAll(source, 0o755)
	os.MkdirAll(target, 0o755)
	os.WriteFile(filepath.Join(source, "settings.json"), []byte(
		`{"theme":"dark","statusLine":{"type":"command","command":"~/.claude/statusline-go","padding":0}}`), 0o600)

	copied, err := CopySettingsKey(source, target, "statusLine")
	if err != nil {
		t.Fatal(err)
	}
	if !copied {
		t.Fatal("expected the key to be copied into an empty account home")
	}
	got := readJSON(t, filepath.Join(target, "settings.json"))
	statusLine, ok := got["statusLine"].(map[string]any)
	if !ok {
		t.Fatalf("expected a statusLine object, got %#v", got["statusLine"])
	}
	if statusLine["command"] != "~/.claude/statusline-go" {
		t.Fatalf("expected the source command to come across, got %#v", statusLine)
	}
	if _, leaked := got["theme"]; leaked {
		t.Fatalf("only the named key should be copied, got %#v", got)
	}
}

func TestCopySettingsKeyKeepsTheTargetsOwnSettings(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	target := filepath.Join(dir, "target")
	os.MkdirAll(source, 0o755)
	os.MkdirAll(target, 0o755)
	os.WriteFile(filepath.Join(source, "settings.json"), []byte(`{"statusLine":{"type":"command"}}`), 0o600)
	os.WriteFile(filepath.Join(target, "settings.json"), []byte(`{"theme":"dark","tui":"fullscreen"}`), 0o600)

	if _, err := CopySettingsKey(source, target, "statusLine"); err != nil {
		t.Fatal(err)
	}
	got := readJSON(t, filepath.Join(target, "settings.json"))
	if got["theme"] != "dark" || got["tui"] != "fullscreen" {
		t.Fatalf("expected the target's own settings to survive, got %#v", got)
	}
	if _, ok := got["statusLine"]; !ok {
		t.Fatalf("expected the seeded key to be present, got %#v", got)
	}
}

// Seeding fills a gap; it does not push one account's preference onto an
// account that already made a choice.
func TestCopySettingsKeyNeverOverwritesAKeyTheTargetAlreadySets(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	target := filepath.Join(dir, "target")
	os.MkdirAll(source, 0o755)
	os.MkdirAll(target, 0o755)
	os.WriteFile(filepath.Join(source, "settings.json"), []byte(`{"statusLine":{"command":"from-source"}}`), 0o600)
	os.WriteFile(filepath.Join(target, "settings.json"), []byte(`{"statusLine":{"command":"already-mine"}}`), 0o600)

	copied, err := CopySettingsKey(source, target, "statusLine")
	if err != nil {
		t.Fatal(err)
	}
	if copied {
		t.Fatal("expected no copy when the target already sets the key")
	}
	got := readJSON(t, filepath.Join(target, "settings.json"))
	statusLine := got["statusLine"].(map[string]any)
	if statusLine["command"] != "already-mine" {
		t.Fatalf("the target's own value was overwritten: %#v", statusLine)
	}
}

func TestCopySettingsKeyIsANoOpWhenTheSourceHasNothingToGive(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	target := filepath.Join(dir, "target")
	os.MkdirAll(source, 0o755)
	os.MkdirAll(target, 0o755)
	os.WriteFile(filepath.Join(source, "settings.json"), []byte(`{"theme":"dark"}`), 0o600)

	copied, err := CopySettingsKey(source, target, "statusLine")
	if err != nil {
		t.Fatal(err)
	}
	if copied {
		t.Fatal("expected nothing to be copied")
	}
	if _, err := os.Stat(filepath.Join(target, "settings.json")); !os.IsNotExist(err) {
		t.Fatal("expected no settings file to be created for the target")
	}
}

// A settings file that cannot be parsed must not be replaced by one built
// from a partial reading of it.
func TestCopySettingsKeyRefusesToRewriteUnparseableTargetSettings(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	target := filepath.Join(dir, "target")
	os.MkdirAll(source, 0o755)
	os.MkdirAll(target, 0o755)
	os.WriteFile(filepath.Join(source, "settings.json"), []byte(`{"statusLine":{"command":"x"}}`), 0o600)
	targetPath := filepath.Join(target, "settings.json")
	os.WriteFile(targetPath, []byte("{not json"), 0o600)

	if _, err := CopySettingsKey(source, target, "statusLine"); err == nil {
		t.Fatal("expected an error rather than a silent rewrite")
	}
	raw, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "{not json" {
		t.Fatalf("the unparseable file was modified: %q", raw)
	}
}

func TestPrimaryAccountIsTheLowestNumberedOfItsProvider(t *testing.T) {
	r := Registry{Accounts: []Account{
		{ID: "codex-1", Provider: "codex", Number: 1},
		{ID: "claude-3", Provider: "claude", Number: 3},
		{ID: "claude-1", Provider: "claude", Number: 1},
		{ID: "claude-2", Provider: "claude", Number: 2},
	}}
	account, found := PrimaryAccount(r, "claude")
	if !found || account.ID != "claude-1" {
		t.Fatalf("expected claude-1, got %q (found=%v)", account.ID, found)
	}
	if _, found := PrimaryAccount(r, "gemini"); found {
		t.Fatal("expected no account for an unregistered provider")
	}
}

// A JSON `null` document unmarshals into a nil map without erroring, so it
// slips past the "not a JSON object" check and then panics on the first
// write. It is no more an object than a truncated file is.
func TestCopySettingsKeyRejectsANullSettingsDocument(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	target := filepath.Join(dir, "target")
	os.MkdirAll(source, 0o755)
	os.MkdirAll(target, 0o755)
	os.WriteFile(filepath.Join(source, "settings.json"), []byte(`{"statusLine":{"command":"x"}}`), 0o600)
	targetPath := filepath.Join(target, "settings.json")
	os.WriteFile(targetPath, []byte("null"), 0o600)

	copied, err := CopySettingsKey(source, target, "statusLine")
	if err == nil {
		t.Fatal("expected an error rather than a panic or a silent rewrite")
	}
	if copied {
		t.Fatal("expected nothing to be copied")
	}
	raw, readErr := os.ReadFile(targetPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(raw) != "null" {
		t.Fatalf("the null document was modified: %q", raw)
	}
}

// The source side reads through the same helper, so a null document there
// must not take the process down either.
func TestCopySettingsKeyRejectsANullSourceDocument(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	target := filepath.Join(dir, "target")
	os.MkdirAll(source, 0o755)
	os.MkdirAll(target, 0o755)
	os.WriteFile(filepath.Join(source, "settings.json"), []byte("null"), 0o600)

	if _, err := CopySettingsKey(source, target, "statusLine"); err == nil {
		t.Fatal("expected an error for a null source document")
	}
}
