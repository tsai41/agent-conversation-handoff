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

func TestClaudeCandidateListReportsTotalBeforeApplyingDisplayLimit(t *testing.T) {
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
		if err := os.WriteFile(filepath.Join(sessionDir, fmt.Sprintf("session-%d.jsonl", day)), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	list, err := ClaudeCandidateList(claudeHome, project)
	if err != nil {
		t.Fatal(err)
	}
	if list.Total != 6 || len(list.Candidates) != 5 {
		t.Fatalf("got total=%d displayed=%d, want total=6 displayed=5", list.Total, len(list.Candidates))
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

func TestFindCodexByIDMatchesFullIDAndPrefixAcrossProjects(t *testing.T) {
	dir := t.TempDir()
	codexHome := filepath.Join(dir, ".codex")
	sessionsDir := filepath.Join(codexHome, "sessions", "2026", "08", "04")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(dir, "some-other-project")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	wanted := "019fcb8e-b8cf-76b1-bc81-e444a74c4d60"
	other := "019aaaaa-0000-0000-0000-000000000000"
	wantedPath := filepath.Join(sessionsDir, "rollout-2026-08-04T14-55-56-"+wanted+".jsonl")
	writeSession(t, wantedPath, mustAbs(t, elsewhere), wanted, "hand this over")
	writeSession(t, filepath.Join(sessionsDir, "rollout-2026-08-04T09-00-00-"+other+".jsonl"), mustAbs(t, elsewhere), other, "unrelated")

	for _, fragment := range []string{wanted, "019fcb8e", "019FCB8E"} {
		matches, err := FindCodexByID(codexHome, fragment)
		if err != nil {
			t.Fatalf("fragment %q: %v", fragment, err)
		}
		if len(matches) != 1 {
			t.Fatalf("fragment %q: expected 1 match, got %d: %+v", fragment, len(matches), matches)
		}
		if matches[0].Path != wantedPath {
			t.Fatalf("fragment %q: expected %s, got %s", fragment, wantedPath, matches[0].Path)
		}
		if matches[0].SessionID != wanted {
			t.Fatalf("fragment %q: expected session id %s, got %s", fragment, wanted, matches[0].SessionID)
		}
		// The session belongs to a different project than any caller's cwd;
		// it must still be found, and report where it actually ran.
		if matches[0].CWD != mustAbs(t, elsewhere) {
			t.Fatalf("fragment %q: expected cwd %s, got %s", fragment, mustAbs(t, elsewhere), matches[0].CWD)
		}
	}
}

func TestFindCodexByIDIgnoresTheTimestampPartOfTheFilename(t *testing.T) {
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
	writeSession(t, filepath.Join(sessionsDir, "rollout-2026-07-20T09-00-00-abcdef12-0000-0000-0000-000000000000.jsonl"),
		mustAbs(t, project), "abcdef12-0000-0000-0000-000000000000", "conversation")

	matches, err := FindCodexByID(codexHome, "2026-07")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected the timestamp not to be matched as an id, got %+v", matches)
	}
}

func TestFindCodexByIDReturnsNothingForAnUnknownID(t *testing.T) {
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
	writeSession(t, filepath.Join(sessionsDir, "rollout-2026-07-20T09-00-00-abcdef12-0000-0000-0000-000000000000.jsonl"),
		mustAbs(t, project), "abcdef12-0000-0000-0000-000000000000", "conversation")

	matches, err := FindCodexByID(codexHome, "ffffffff")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected no matches, got %+v", matches)
	}
}

func TestFindClaudeByIDScansEveryProjectDirectory(t *testing.T) {
	dir := t.TempDir()
	claudeHome := filepath.Join(dir, ".claude")
	wanted := "5e9bb808-3048-4569-bf1b-338f1101f7b0"
	elsewhere := "/Users/someone/go/src/other"

	for _, project := range []struct{ encoded, id, cwd string }{
		{"-Users-someone-go-src-other", wanted, elsewhere},
		{"-Users-someone-tmp", "11111111-0000-0000-0000-000000000000", "/Users/someone/tmp"},
	} {
		sessionDir := filepath.Join(claudeHome, "projects", project.encoded)
		if err := os.MkdirAll(sessionDir, 0o755); err != nil {
			t.Fatal(err)
		}
		line := fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":%q,"timestamp":"2026-08-04T09:00:00Z","message":{"content":"pick this up"}}`+"\n",
			project.id, project.cwd)
		if err := os.WriteFile(filepath.Join(sessionDir, project.id+".jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	matches, err := FindClaudeByID(claudeHome, "5e9bb808")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d: %+v", len(matches), matches)
	}
	if matches[0].SessionID != wanted {
		t.Fatalf("expected session id %s, got %s", wanted, matches[0].SessionID)
	}
	if matches[0].CWD != elsewhere {
		t.Fatalf("expected cwd %s, got %s", elsewhere, matches[0].CWD)
	}
	if !strings.Contains(matches[0].Description, "5e9bb808…f7b0") {
		t.Fatalf("expected truncated id in description, got %q", matches[0].Description)
	}
	if strings.Contains(matches[0].Description, wanted) {
		t.Fatalf("full uuid leaked into description: %q", matches[0].Description)
	}
}

func TestFindByIDRejectsFragmentsTooShortToBeSelective(t *testing.T) {
	dir := t.TempDir()
	for _, fragment := range []string{"", "  ", "01"} {
		if _, err := FindClaudeByID(dir, fragment); err == nil {
			t.Fatalf("expected an error for Claude id fragment %q", fragment)
		}
		if _, err := FindCodexByID(dir, fragment); err == nil {
			t.Fatalf("expected an error for Codex id fragment %q", fragment)
		}
	}
}

func TestFindCodexByIDSkipsFilesWithoutSessionMeta(t *testing.T) {
	dir := t.TempDir()
	codexHome := filepath.Join(dir, ".codex")
	sessionsDir := filepath.Join(codexHome, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// No session_meta: neither a real id nor a cwd, so it must not be
	// offered as something to hand off.
	path := filepath.Join(sessionsDir, "rollout-2026-08-04T09-00-00-abcdef12-0000-0000-0000-000000000000.jsonl")
	line := `{"type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"orphan"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	matches, err := FindCodexByID(codexHome, "abcdef12")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected the metadata-less session to be skipped, got %+v", matches)
	}
}

func TestFindByIDReportsStartTimeForCrossAccountOrdering(t *testing.T) {
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
	path := filepath.Join(sessionsDir, "rollout-2026-05-22T11-40-06-abcdef12-0000-0000-0000-000000000000.jsonl")
	writeSession(t, path, mustAbs(t, project), "abcdef12-0000-0000-0000-000000000000", "conversation")

	matches, err := FindCodexByID(codexHome, "abcdef12")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	want := time.Date(2026, 5, 22, 11, 40, 6, 0, time.Local)
	if !matches[0].StartTime.Equal(want) {
		t.Fatalf("got %v, want %v", matches[0].StartTime, want)
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
