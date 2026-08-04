package menu

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeScript(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

// runWithFakePath prepends a temp bin dir (containing the given fake
// scripts) to PATH and HOME for the duration of the test.
func runWithFakePath(t *testing.T, home string) string {
	t.Helper()
	fakeBin := filepath.Join(home, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return fakeBin
}

func TestRunOffersNumberedShortcutsAndRestoresRealStdin(t *testing.T) {
	home := t.TempDir()
	fakeBin := runWithFakePath(t, home)

	claudeHome := filepath.Join(home, ".claude")
	codexHome := filepath.Join(home, ".codex")
	os.MkdirAll(claudeHome, 0o755)
	os.MkdirAll(codexHome, 0o755)

	registryPath := filepath.Join(home, "accounts.json")
	registryJSON := fmt.Sprintf(`{
		"version": 1,
		"next_number": {"claude": 2, "codex": 2},
		"accounts": [
			{"id": "claude-1", "provider": "claude", "number": 1, "home": %q, "alias": ""},
			{"id": "codex-1", "provider": "codex", "number": 1, "home": %q, "alias": ""}
		]
	}`, claudeHome, codexHome)
	os.WriteFile(registryPath, []byte(registryJSON), 0o644)

	rowsCapture := filepath.Join(home, "rows")
	argvCapture := filepath.Join(home, "argv")
	writeScript(t, filepath.Join(fakeBin, "fzf"), fmt.Sprintf(
		"#!/usr/bin/env bash\ncat > %q\nprintf '%%s\\n' \"$@\" > %q\nprintf '%%s\\n' 'claude-1'\n",
		rowsCapture, argvCapture,
	))
	stdinCapture := filepath.Join(home, "stdin-seen")
	writeScript(t, filepath.Join(fakeBin, "claude"), fmt.Sprintf("#!/usr/bin/env bash\nhead -n1 > %q\n", stdinCapture))
	writeScript(t, filepath.Join(fakeBin, "codex"), "#!/usr/bin/env bash\nexit 0\n")

	// Run in a child process: menu.Run execs into "claude" and never
	// returns on success, so it must be driven out-of-process here.
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperRunMenu")
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_MENU_PROCESS=1", "ACH_TEST_REGISTRY="+registryPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdin = strings.NewReader("real-stdin-marker\n")
	if err := cmd.Run(); err != nil {
		t.Fatalf("menu run failed: %v\nstderr: %s", err, stderr.String())
	}

	argv, err := os.ReadFile(argvCapture)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(argv), "--bind=1:pos(1)+accept,2:pos(2)+accept,3:pos(3)+accept,4:pos(4)+accept") {
		t.Fatalf("expected numbered bind flag, got argv: %s", argv)
	}

	rows, err := os.ReadFile(rowsCapture)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"1. Claude", "2. Codex", "3. 接手既有對話", "4. 帳號設定"} {
		if !strings.Contains(string(rows), want) {
			t.Fatalf("expected rows to contain %q, got: %s", want, rows)
		}
	}

	seen, err := os.ReadFile(stdinCapture)
	if err != nil {
		t.Fatal(err)
	}
	if string(seen) != "real-stdin-marker\n" {
		t.Fatalf("expected claude to see real stdin, got %q", seen)
	}
}

// A project with no conversations at all used to be a dead end: the handoff
// flow bailed out before showing anything. It must now still reach the
// manual-id row, and an id typed there must resolve against every registered
// account -- including a conversation recorded in a different project.
func TestInteractiveHandoffAcceptsATypedIDWhenTheProjectHasNoConversations(t *testing.T) {
	home := t.TempDir()
	fakeBin := runWithFakePath(t, home)

	project := filepath.Join(home, "project")
	os.MkdirAll(project, 0o755)
	elsewhere := filepath.Join(home, "elsewhere")
	os.MkdirAll(elsewhere, 0o755)
	resolvedElsewhere, err := filepath.EvalSymlinks(elsewhere)
	if err != nil {
		t.Fatal(err)
	}
	claudeHome := filepath.Join(home, ".claude")
	codexHome := filepath.Join(home, ".codex")
	codexSessions := filepath.Join(codexHome, "sessions", "2026", "08", "04")
	os.MkdirAll(claudeHome, 0o755)
	os.MkdirAll(codexSessions, 0o755)

	longUUID := "019fcb8e-b8cf-76b1-bc81-e444a74c4d60"
	sessionPath := filepath.Join(codexSessions, "rollout-2026-08-04T14-55-56-"+longUUID+".jsonl")
	os.WriteFile(sessionPath, []byte(fmt.Sprintf(
		`{"type":"session_meta","payload":{"id":%q,"cwd":%q}}`+"\n"+
			`{"type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"carry this on"}]}}`+"\n",
		longUUID, resolvedElsewhere)), 0o644)

	registryPath := filepath.Join(home, "accounts.json")
	registryJSON := fmt.Sprintf(`{
		"version": 1,
		"next_number": {"claude": 2, "codex": 2},
		"accounts": [
			{"id": "claude-1", "provider": "claude", "number": 1, "home": %q, "alias": ""},
			{"id": "codex-1", "provider": "codex", "number": 1, "home": %q, "alias": ""}
		]
	}`, claudeHome, codexHome)
	os.WriteFile(registryPath, []byte(registryJSON), 0o644)

	// The direction picked here is claude-1 -> codex-1, but the typed id
	// belongs to codex-1, so the source flips to codex-1 and the target has
	// to be re-picked (step 3).
	state := filepath.Join(home, "state")
	sessionRows := filepath.Join(home, "session-rows")
	writeScript(t, filepath.Join(fakeBin, "fzf"), fmt.Sprintf(`#!/usr/bin/env bash
n=$(cat %q 2>/dev/null || echo 0)
if [ "$n" -eq 2 ]; then cat > %q; fi
values=("action:handoff" "claude-1|codex-1" "action:manual-id" "claude-1")
printf '%%s\n' "${values[$n]}"
echo $((n+1)) > %q
`, state, sessionRows, state))

	launched := filepath.Join(home, "launched")
	writeScript(t, filepath.Join(fakeBin, "claude"), fmt.Sprintf(
		"#!/usr/bin/env bash\nif [ \"$1 $2\" = 'auth status' ]; then exit 0; fi\nprintf '%%s|%%s' \"$CLAUDE_CONFIG_DIR\" \"$1\" > %q\n",
		launched,
	))
	writeScript(t, filepath.Join(fakeBin, "codex"), "#!/usr/bin/env bash\nexit 0\n")

	cmd := exec.Command(os.Args[0], "-test.run=TestHelperRunMenu")
	cmd.Dir = project
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_MENU_PROCESS=1", "ACH_TEST_REGISTRY="+registryPath)
	cmd.Stdin = strings.NewReader("019fcb8e\n")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("menu run failed: %v\nstderr: %s", err, stderr.String())
	}

	rows, err := os.ReadFile(sessionRows)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rows), "手動輸入對話 ID") {
		t.Fatalf("expected the manual-id row to be offered, got: %s", rows)
	}
	if !strings.Contains(stdout.String(), resolvedElsewhere) {
		t.Fatalf("expected a warning naming the conversation's original project, got: %s", stdout.String())
	}

	launchedContent, err := os.ReadFile(launched)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(launchedContent), "transcript.md") {
		t.Fatalf("expected the claude account to be launched at the new artifact, got %q", launchedContent)
	}

	var sourcePath string
	filepath.Walk(filepath.Join(project, ".agent-handoffs"), func(path string, info os.FileInfo, err error) error {
		if err == nil && filepath.Base(path) == "manifest.json" {
			raw, _ := os.ReadFile(path)
			var manifest map[string]any
			json.Unmarshal(raw, &manifest)
			source, _ := manifest["source"].(map[string]any)
			sourcePath, _ = source["path"].(string)
		}
		return nil
	})
	if sourcePath == "" {
		t.Fatal("expected a handoff artifact manifest.json to be created")
	}
	if filepath.Base(sourcePath) != filepath.Base(sessionPath) {
		t.Fatalf("expected the artifact to snapshot the typed conversation, got %q", sourcePath)
	}
}

// TestHelperRunMenu is not a real test; it's exec'd as a subprocess by
// TestRunOffersNumberedShortcutsAndRestoresRealStdin so menu.Run's exec
// into the (faked) provider CLI doesn't replace the test binary itself.
func TestHelperRunMenu(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_MENU_PROCESS") != "1" {
		return
	}
	if err := Run(os.Getenv("ACH_TEST_REGISTRY")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func TestInteractiveHandoffPicksDirectionThenSessionAndLaunchesTarget(t *testing.T) {
	home := t.TempDir()
	fakeBin := runWithFakePath(t, home)

	project := filepath.Join(home, "project")
	os.MkdirAll(project, 0o755)
	claudeHome := filepath.Join(home, ".claude")
	codexHome := filepath.Join(home, ".codex")
	longUUID := "edcda8ee-19af-45ac-ad5d-206136874fdd"
	// The subprocess resolves its cwd via os.Getwd(), which returns the
	// symlink-resolved path (e.g. /private/var/... on macOS), so the
	// session directory must be keyed off the same resolved path.
	resolvedProject, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	sessionDir := filepath.Join(claudeHome, "projects", strings.NewReplacer("/", "-", "_", "-").Replace(resolvedProject))
	os.MkdirAll(sessionDir, 0o755)
	os.MkdirAll(codexHome, 0o755)
	sessionPath := filepath.Join(sessionDir, "source.jsonl")
	os.WriteFile(sessionPath, []byte(fmt.Sprintf(`{"type":"user","sessionId":%q,"message":{"content":"continue this"}}`+"\n", longUUID)), 0o644)

	registryPath := filepath.Join(home, "accounts.json")
	registryJSON := fmt.Sprintf(`{
		"version": 1,
		"next_number": {"claude": 2, "codex": 2},
		"accounts": [
			{"id": "claude-1", "provider": "claude", "number": 1, "home": %q, "alias": ""},
			{"id": "codex-1", "provider": "codex", "number": 1, "home": %q, "alias": ""}
		]
	}`, claudeHome, codexHome)
	os.WriteFile(registryPath, []byte(registryJSON), 0o644)

	state := filepath.Join(home, "state")
	sessionRows := filepath.Join(home, "session-rows")
	writeScript(t, filepath.Join(fakeBin, "fzf"), fmt.Sprintf(`#!/usr/bin/env bash
n=$(cat %q 2>/dev/null || echo 0)
if [ "$n" -eq 2 ]; then cat > %q; fi
values=("action:handoff" "claude-1|codex-1" %q)
printf '%%s\n' "${values[$n]}"
echo $((n+1)) > %q
`, state, sessionRows, sessionPath, state))

	launched := filepath.Join(home, "launched")
	writeScript(t, filepath.Join(fakeBin, "codex"), fmt.Sprintf(
		"#!/usr/bin/env bash\nif [ \"$1 $2\" = 'login status' ]; then exit 0; fi\nprintf '%%s|%%s' \"$CODEX_HOME\" \"$1\" > %q\n",
		launched,
	))
	writeScript(t, filepath.Join(fakeBin, "claude"), "#!/usr/bin/env bash\nexit 0\n")

	cmd := exec.Command(os.Args[0], "-test.run=TestHelperRunMenu")
	cmd.Dir = project
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_MENU_PROCESS=1", "ACH_TEST_REGISTRY="+registryPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("menu run failed: %v\nstderr: %s", err, stderr.String())
	}

	rows, err := os.ReadFile(sessionRows)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rows), "edcda8ee…4fdd") {
		t.Fatalf("expected truncated session id in rows, got: %s", rows)
	}
	if strings.Contains(string(rows), longUUID) {
		t.Fatalf("full uuid leaked into session rows: %s", rows)
	}

	launchedContent, err := os.ReadFile(launched)
	if err != nil {
		t.Fatal(err)
	}
	resolvedCodexHome, _ := filepath.Abs(codexHome)
	if !strings.HasPrefix(string(launchedContent), resolvedCodexHome+"|") {
		t.Fatalf("expected launch into codex account, got %q", launchedContent)
	}
	if !strings.Contains(string(launchedContent), "transcript.md") {
		t.Fatalf("expected launch prompt to reference transcript.md, got %q", launchedContent)
	}

	manifestFound := false
	filepath.Walk(filepath.Join(project, ".agent-handoffs"), func(path string, info os.FileInfo, err error) error {
		if err == nil && filepath.Base(path) == "manifest.json" {
			manifestFound = true
			raw, _ := os.ReadFile(path)
			var manifest map[string]any
			json.Unmarshal(raw, &manifest)
		}
		return nil
	})
	if !manifestFound {
		t.Fatal("expected a handoff artifact manifest.json to be created")
	}
}
