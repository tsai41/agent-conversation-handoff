package handoff

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateSnapshotsSourceWithoutModifyingIt(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.jsonl")
	original := `{"type":"user","message":{"content":"hello"}}` + "\n"
	if err := os.WriteFile(source, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "project")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}

	destination, err := Create(source, target, "claude")
	if err != nil {
		t.Fatal(err)
	}

	sourceAfter, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if string(sourceAfter) != original {
		t.Fatalf("source was modified: %q", sourceAfter)
	}

	snapshot, err := os.ReadFile(filepath.Join(destination, "source.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if string(snapshot) != original {
		t.Fatalf("snapshot does not match source: %q", snapshot)
	}

	transcript, err := os.ReadFile(filepath.Join(destination, "transcript.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(transcript), "hello") {
		t.Fatalf("transcript missing message text: %q", transcript)
	}

	manifestRaw, err := os.ReadFile(filepath.Join(destination, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		t.Fatal(err)
	}
	sourceInfo := manifest["source"].(map[string]any)
	if sourceInfo["type"] != "claude" {
		t.Fatalf("unexpected source type in manifest: %+v", sourceInfo)
	}

	gitignore, err := os.ReadFile(filepath.Join(target, ".agent-handoffs", ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if string(gitignore) != "*\n" {
		t.Fatalf("expected .agent-handoffs to be entirely gitignored, got %q", gitignore)
	}
}

func TestCreateRendersACodexResponseItemTranscript(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.jsonl")
	lines := []string{
		`{"type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"Continue the migration"}]}}`,
		`{"type":"response_item","payload":{"role":"assistant","content":[{"type":"output_text","text":"Migration inspected."}]}}`,
	}
	if err := os.WriteFile(source, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "project")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}

	destination, err := Create(source, target, "codex")
	if err != nil {
		t.Fatal(err)
	}
	transcript, err := os.ReadFile(filepath.Join(destination, "transcript.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(transcript)
	if !strings.Contains(text, "Continue the migration") || !strings.Contains(text, "Migration inspected.") {
		t.Fatalf("transcript missing expected codex content: %q", text)
	}
}
