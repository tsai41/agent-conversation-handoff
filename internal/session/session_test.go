package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestShortIDTruncatesLongIDsOnly(t *testing.T) {
	if got := ShortID("short"); got != "short" {
		t.Fatalf("expected short id unchanged, got %q", got)
	}
	long := "edcda8ee-19af-45ac-ad5d-206136874fdd"
	want := "edcda8ee…4fdd"
	if got := ShortID(long); got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestClaudeCandidatesScopesToProjectDirectory(t *testing.T) {
	dir := t.TempDir()
	project := filepath.Join(dir, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	claudeHome := filepath.Join(dir, ".claude")
	projectID := strings.NewReplacer("/", "-", "_", "-").Replace(mustAbs(t, project))
	sessionDir := filepath.Join(claudeHome, "projects", projectID)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"edcda8ee-19af-45ac-ad5d-206136874fdd","message":{"content":"continue this"}}` + "\n"
	if err := os.WriteFile(filepath.Join(sessionDir, "source.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	candidates, err := ClaudeCandidates(claudeHome, project)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	if !strings.Contains(candidates[0].Description, "edcda8ee…4fdd") {
		t.Fatalf("expected truncated id in description, got %q", candidates[0].Description)
	}
	if strings.Contains(candidates[0].Description, "edcda8ee-19af-45ac-ad5d-206136874fdd") {
		t.Fatalf("full uuid leaked into description: %q", candidates[0].Description)
	}
	if !strings.Contains(candidates[0].Description, "continue this") {
		t.Fatalf("expected preview text, got %q", candidates[0].Description)
	}
}

func TestClaudeCandidatesUsesClaudeProjectPathEncoding(t *testing.T) {
	dir := t.TempDir()
	project := filepath.Join(dir, "project_with_underscore")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	claudeHome := filepath.Join(dir, ".claude")
	projectID := strings.NewReplacer("/", "-", "_", "-").Replace(mustAbs(t, project))
	sessionDir := filepath.Join(claudeHome, "projects", projectID)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"session-id","message":{"content":"continue this"}}` + "\n"
	if err := os.WriteFile(filepath.Join(sessionDir, "source.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	candidates, err := ClaudeCandidates(claudeHome, project)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
}

func TestClaudeCandidatesLimitsToFiveNewest(t *testing.T) {
	dir := t.TempDir()
	project := filepath.Join(dir, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	claudeHome := filepath.Join(dir, ".claude")
	projectID := strings.NewReplacer("/", "-", "_", "-").Replace(mustAbs(t, project))
	sessionDir := filepath.Join(claudeHome, "projects", projectID)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}

	for day := 1; day <= 6; day++ {
		content := fmt.Sprintf(`{"type":"user","sessionId":"session-%d","timestamp":"2026-07-%02dT09:00:00Z","message":{"content":"conversation %d"}}`+"\n", day, day, day)
		path := filepath.Join(sessionDir, fmt.Sprintf("session-%d.jsonl", day))
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	candidates, err := ClaudeCandidates(claudeHome, project)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 5 {
		t.Fatalf("expected 5 candidates, got %d: %+v", len(candidates), candidates)
	}
	if !strings.Contains(candidates[0].Path, "session-6.jsonl") {
		t.Fatalf("expected newest session first, got %s", candidates[0].Path)
	}
	if strings.Contains(candidates[4].Path, "session-1.jsonl") {
		t.Fatalf("expected oldest session to be excluded, got %s", candidates[4].Path)
	}
}

func TestCodexCandidatesFiltersByRecordedCwd(t *testing.T) {
	dir := t.TempDir()
	codexHome := filepath.Join(dir, ".codex")
	sessionsDir := filepath.Join(codexHome, "sessions", "2026", "07", "20")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(dir, "myproj")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}

	writeSession(t, filepath.Join(sessionsDir, "match.jsonl"), mustAbs(t, project), "match-id", "continue this")
	writeSession(t, filepath.Join(sessionsDir, "mismatch.jsonl"), mustAbs(t, other), "other-id", "unrelated")

	candidates, err := CodexCandidates(codexHome, project)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected 1 matching candidate, got %d: %+v", len(candidates), candidates)
	}
	if !strings.Contains(candidates[0].Path, "match.jsonl") {
		t.Fatalf("expected the matching session, got %s", candidates[0].Path)
	}
}

func TestCodexCandidatesLimitsToFiveNewest(t *testing.T) {
	dir := t.TempDir()
	codexHome := filepath.Join(dir, ".codex")
	sessionsDir := filepath.Join(codexHome, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(dir, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}

	for day := 1; day <= 6; day++ {
		path := filepath.Join(sessionsDir, fmt.Sprintf("rollout-2026-07-%02dT09-00-00-session-%d.jsonl", day, day))
		writeSession(t, path, mustAbs(t, project), fmt.Sprintf("session-%d", day), fmt.Sprintf("conversation %d", day))
	}

	candidates, err := CodexCandidates(codexHome, project)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 5 {
		t.Fatalf("expected 5 candidates, got %d: %+v", len(candidates), candidates)
	}
	if !strings.Contains(candidates[0].Path, "2026-07-06") {
		t.Fatalf("expected newest session first, got %s", candidates[0].Path)
	}
	if strings.Contains(candidates[4].Path, "2026-07-01") {
		t.Fatalf("expected oldest session to be excluded, got %s", candidates[4].Path)
	}
}

func TestCodexCandidatesStopsReadingMismatchedFilesEarly(t *testing.T) {
	dir := t.TempDir()
	codexHome := filepath.Join(dir, ".codex")
	sessionsDir := filepath.Join(codexHome, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(dir, "myproj")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}

	// A large mismatched-project file: correctness must not depend on
	// reading past session_meta once cwd is known not to match.
	path := filepath.Join(sessionsDir, "big.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(f, `{"type":"session_meta","payload":{"id":"big-id","cwd":%q}}`+"\n", mustAbs(t, other))
	for i := 0; i < 5000; i++ {
		fmt.Fprintf(f, `{"type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"filler"}]}}`+"\n")
	}
	f.Close()

	writeSession(t, filepath.Join(sessionsDir, "match.jsonl"), mustAbs(t, project), "match-id", "continue this")

	candidates, err := CodexCandidates(codexHome, project)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || !strings.Contains(candidates[0].Path, "match.jsonl") {
		t.Fatalf("expected only the matching session, got %+v", candidates)
	}
}

func TestCodexCandidatesOrderByFilenameStartTimeNotMtime(t *testing.T) {
	dir := t.TempDir()
	codexHome := filepath.Join(dir, ".codex")
	sessionsDir := filepath.Join(codexHome, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(dir, "myproj")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}

	// "older" started earlier but was resumed much more recently than
	// "newer" -- its mtime is now the later of the two, the opposite of
	// their real conversation order. The picker must still list the
	// session that started later ("newer") first.
	olderPath := filepath.Join(sessionsDir, "rollout-2026-01-01T09-00-00-aaaaaaaa-0000-0000-0000-000000000000.jsonl")
	newerPath := filepath.Join(sessionsDir, "rollout-2026-01-02T09-00-00-bbbbbbbb-0000-0000-0000-000000000000.jsonl")
	writeSession(t, olderPath, mustAbs(t, project), "older-id", "started first, resumed later")
	writeSession(t, newerPath, mustAbs(t, project), "newer-id", "started second, never resumed")

	oldMtime := time.Date(2026, 1, 1, 9, 5, 0, 0, time.Local)
	resumedMtime := time.Date(2026, 3, 1, 9, 5, 0, 0, time.Local)
	if err := os.Chtimes(olderPath, resumedMtime, resumedMtime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newerPath, oldMtime, oldMtime); err != nil {
		t.Fatal(err)
	}

	candidates, err := CodexCandidates(codexHome, project)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d: %+v", len(candidates), candidates)
	}
	if !strings.Contains(candidates[0].Path, "2026-01-02") {
		t.Fatalf("expected the later-started session first despite its older mtime, got order: %+v", candidates)
	}
}

func TestCodexSessionStartTimeParsesTheRolloutFilename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout-2026-05-22T11-40-06-019e4dc4-d76d-7f61-85f8-481823cd9abf.jsonl")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	// Set an mtime far from the filename's encoded time; the parsed
	// filename time must win.
	unrelatedMtime := time.Date(2020, 1, 1, 0, 0, 0, 0, time.Local)
	if err := os.Chtimes(path, unrelatedMtime, unrelatedMtime); err != nil {
		t.Fatal(err)
	}

	got := codexSessionStartTime(path)
	want := time.Date(2026, 5, 22, 11, 40, 6, 0, time.Local)
	if !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestClaudeSessionStartTimeUsesFirstMessageTimestampNotMtime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	content := `{"type":"user","sessionId":"x","timestamp":"2026-07-15T09:33:40.412Z","message":{"content":"hi"}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	unrelatedMtime := time.Date(2020, 1, 1, 0, 0, 0, 0, time.Local)
	if err := os.Chtimes(path, unrelatedMtime, unrelatedMtime); err != nil {
		t.Fatal(err)
	}

	got := claudeSessionStartTime(path)
	want, _ := time.Parse(time.RFC3339Nano, "2026-07-15T09:33:40.412Z")
	if !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func writeSession(t *testing.T, path, cwd, id, preview string) {
	t.Helper()
	content := fmt.Sprintf(
		`{"type":"session_meta","payload":{"id":%q,"cwd":%q}}`+"\n"+
			`{"type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":%q}]}}`+"\n",
		id, cwd, preview,
	)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustAbs(t *testing.T, path string) string {
	t.Helper()
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}
