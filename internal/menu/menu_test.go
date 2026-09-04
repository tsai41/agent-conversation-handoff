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

	"github.com/tsai41/agent-conversation-handoff/internal/registry"
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

// reply is one canned fzf invocation: what it prints and what it exits
// with. Exit code 1 with only a query line is how fzf reports "nothing in
// the list matched what was typed", which the session picker treats as a
// conversation id rather than as a failure.
type reply struct {
	stdout   string
	exitCode int
}

func row(path string) reply { return reply{"\n" + path + "\n", 0} }

func typed(query string) reply { return reply{query + "\n", 1} }

func key(k string) reply { return reply{k + "\n", 0} }

// fakeFzf installs a stub fzf that replays one canned answer per call and
// records the rows and argv of every call, so a whole menu path can be
// driven and then inspected level by level.
func fakeFzf(t *testing.T, home string, replies ...reply) string {
	t.Helper()
	calls := filepath.Join(home, "fzf-calls")
	if err := os.MkdirAll(calls, 0o755); err != nil {
		t.Fatal(err)
	}
	var cases strings.Builder
	for i, r := range replies {
		cases.WriteString(fmt.Sprintf("  %d) printf '%%s' '%s'; exit %d ;;\n",
			i, strings.ReplaceAll(r.stdout, "'", `'\''`), r.exitCode))
	}
	writeScript(t, filepath.Join(home, "bin", "fzf"), fmt.Sprintf(`#!/usr/bin/env bash
state=%q
n=$(cat "$state" 2>/dev/null || echo 0)
echo $((n+1)) > "$state"
cat > %q/rows-$n
printf '%%s\n' "$@" > %q/argv-$n
case $n in
%s  *) exit 130 ;;
esac
`, filepath.Join(home, "fzf-state"), calls, calls, cases.String()))
	return calls
}

func callFile(t *testing.T, calls, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(calls, name))
	if err != nil {
		t.Fatalf("expected fzf call %s: %v", name, err)
	}
	return string(raw)
}

func writeRegistry(t *testing.T, path, claudeHome, codexHome string) {
	t.Helper()
	content := fmt.Sprintf(`{
		"version": 1,
		"next_number": {"claude": 2, "codex": 2},
		"accounts": [
			{"id": "claude-1", "provider": "claude", "number": 1, "home": %q, "alias": ""},
			{"id": "codex-1", "provider": "codex", "number": 1, "home": %q, "alias": ""}
		]
	}`, claudeHome, codexHome)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// runMenu drives menu.Run in a child process: it execs into the (faked)
// provider CLI and never returns, so it cannot run in the test process.
func runMenu(t *testing.T, registryPath, dir, stdin string) (string, string, error) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperRunMenu")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_MENU_PROCESS=1", "ACH_TEST_REGISTRY="+registryPath)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// TestHelperRunMenu is not a real test; it is exec'd as a subprocess so
// menu.Run's exec into the provider CLI doesn't replace the test binary.
func TestHelperRunMenu(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_MENU_PROCESS") != "1" {
		return
	}
	if err := Run(os.Getenv("ACH_TEST_REGISTRY")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func TestRunPicksAFunctionBeforeAnAccountAndRestoresRealStdin(t *testing.T) {
	home := t.TempDir()
	runWithFakePath(t, home)

	claudeHome := filepath.Join(home, ".claude")
	codexHome := filepath.Join(home, ".codex")
	os.MkdirAll(claudeHome, 0o755)
	os.MkdirAll(codexHome, 0o755)

	registryPath := filepath.Join(home, "accounts.json")
	writeRegistry(t, registryPath, claudeHome, codexHome)

	calls := fakeFzf(t, home, key("chat"), key("claude-1"))
	stdinCapture := filepath.Join(home, "stdin-seen")
	writeScript(t, filepath.Join(home, "bin", "claude"), fmt.Sprintf("#!/usr/bin/env bash\nhead -n1 > %q\n", stdinCapture))
	writeScript(t, filepath.Join(home, "bin", "codex"), "#!/usr/bin/env bash\nexit 0\n")

	if _, stderr, err := runMenu(t, registryPath, home, "real-stdin-marker\n"); err != nil {
		t.Fatalf("menu run failed: %v\nstderr: %s", err, stderr)
	}

	// Level one offers the three functions, not the accounts.
	functions := callFile(t, calls, "rows-0")
	for _, want := range []string{"1. 使用帳號對話", "2. 接手對話", "3. 帳號設定"} {
		if !strings.Contains(functions, want) {
			t.Fatalf("expected the function rows to contain %q, got: %s", want, functions)
		}
	}
	if strings.Contains(functions, "Claude") || strings.Contains(functions, "Codex") {
		t.Fatalf("accounts leaked into the function level: %s", functions)
	}
	argv := callFile(t, calls, "argv-0")
	if !strings.Contains(argv, "--bind=1:pos(1)+accept,2:pos(2)+accept,3:pos(3)+accept") {
		t.Fatalf("expected numbered bind flag, got argv: %s", argv)
	}
	if !strings.Contains(argv, "--header=主選單") {
		t.Fatalf("expected the root path bar, got argv: %s", argv)
	}

	// Level two offers the accounts, under a path bar naming the function.
	accounts := callFile(t, calls, "rows-1")
	for _, want := range []string{"1. Claude", "2. Codex"} {
		if !strings.Contains(accounts, want) {
			t.Fatalf("expected the account rows to contain %q, got: %s", want, accounts)
		}
	}
	accountArgv := callFile(t, calls, "argv-1")
	if !strings.Contains(accountArgv, "--header=主選單 > 使用帳號對話") {
		t.Fatalf("expected the second-level path bar, got argv: %s", accountArgv)
	}
	if !strings.Contains(accountArgv, "--bind=1:pos(1)+accept") {
		t.Fatalf("expected the account level to be numbered too, got argv: %s", accountArgv)
	}

	seen, err := os.ReadFile(stdinCapture)
	if err != nil {
		t.Fatal(err)
	}
	if string(seen) != "real-stdin-marker\n" {
		t.Fatalf("expected claude to see real stdin, got %q", seen)
	}
}

// The whole point of --print-query: an id typed into the search box is
// used even though it matches nothing in the list. The picker only lists
// the current project's conversations, and here the project has none --
// the conversation was recorded elsewhere, under the other account.
func TestHandoffUsesAnIDTypedIntoTheSearchBox(t *testing.T) {
	home := t.TempDir()
	runWithFakePath(t, home)

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
	writeRegistry(t, registryPath, claudeHome, codexHome)

	// The source picked is claude-1, but the typed id belongs to codex-1,
	// so the source flips and the target picker offers claude-1 instead.
	calls := fakeFzf(t, home, key("handoff"), key("claude-1"), typed("019fcb8e"), key("claude-1"))

	launched := filepath.Join(home, "launched")
	writeScript(t, filepath.Join(home, "bin", "claude"), fmt.Sprintf(
		"#!/usr/bin/env bash\nif [ \"$1 $2\" = 'auth status' ]; then exit 0; fi\nprintf '%%s|%%s' \"$CLAUDE_CONFIG_DIR\" \"$1\" > %q\n",
		launched,
	))
	writeScript(t, filepath.Join(home, "bin", "codex"), "#!/usr/bin/env bash\nexit 0\n")

	stdout, stderr, err := runMenu(t, registryPath, project, "")
	if err != nil {
		t.Fatalf("menu run failed: %v\nstderr: %s", err, stderr)
	}

	sessionArgv := callFile(t, calls, "argv-2")
	if !strings.Contains(sessionArgv, "--print-query") {
		t.Fatalf("expected the session picker to return its query, got argv: %s", sessionArgv)
	}
	if !strings.Contains(sessionArgv, "--header=主選單 > 接手對話 > 來源 Agent：Claude > 選擇來源對話") {
		t.Fatalf("expected a path bar naming the source account, got argv: %s", sessionArgv)
	}
	if rows := callFile(t, calls, "rows-2"); strings.Contains(rows, "輸入對話 ID") {
		t.Fatalf("expected ids to be entered directly in the search box, got: %s", rows)
	}
	if !strings.Contains(stdout, resolvedElsewhere) {
		t.Fatalf("expected a warning naming the conversation's original project, got: %s", stdout)
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

func TestHandoffPicksSourceThenConversationThenTarget(t *testing.T) {
	home := t.TempDir()
	runWithFakePath(t, home)

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
	writeRegistry(t, registryPath, claudeHome, codexHome)

	calls := fakeFzf(t, home, key("handoff"), key("claude-1"), row(sessionPath), key("codex-1"))

	launched := filepath.Join(home, "launched")
	writeScript(t, filepath.Join(home, "bin", "codex"), fmt.Sprintf(
		"#!/usr/bin/env bash\nif [ \"$1 $2\" = 'login status' ]; then exit 0; fi\nprintf '%%s|%%s' \"$CODEX_HOME\" \"$1\" > %q\n",
		launched,
	))
	writeScript(t, filepath.Join(home, "bin", "claude"), "#!/usr/bin/env bash\nexit 0\n")

	if _, stderr, err := runMenu(t, registryPath, project, ""); err != nil {
		t.Fatalf("menu run failed: %v\nstderr: %s", err, stderr)
	}

	if sources := callFile(t, calls, "rows-1"); !strings.Contains(sources, "1. Claude") {
		t.Fatalf("expected a numbered source account level, got: %s", sources)
	}
	if sourceArgv := callFile(t, calls, "argv-1"); !strings.Contains(sourceArgv, "--header=主選單 > 接手對話 > 選擇來源 Agent") {
		t.Fatalf("expected the source picker path bar to name its role, got argv: %s", sourceArgv)
	}
	rows := callFile(t, calls, "rows-2")
	if !strings.Contains(rows, "edcda8ee…4fdd") {
		t.Fatalf("expected truncated session id in rows, got: %s", rows)
	}
	if strings.Contains(rows, longUUID) {
		t.Fatalf("full uuid leaked into session rows: %s", rows)
	}
	if sessionArgv := callFile(t, calls, "argv-2"); !strings.Contains(sessionArgv, "--header=主選單 > 接手對話 > 來源 Agent：Claude > 選擇來源對話") {
		t.Fatalf("expected the conversation picker path bar to name its role, got argv: %s", sessionArgv)
	}
	if targetArgv := callFile(t, calls, "argv-3"); !strings.Contains(targetArgv, "--header=主選單 > 接手對話 > 來源 Agent：Claude > 選擇目標 Agent") {
		t.Fatalf("expected the target picker to keep the path bar, got argv: %s", targetArgv)
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
}

func TestQuickHandoffByIDFindsClaudeConversationAndLaunchesCodex(t *testing.T) {
	home := t.TempDir()
	runWithFakePath(t, home)

	project := filepath.Join(home, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	claudeHome := filepath.Join(home, ".claude")
	codexHome := filepath.Join(home, ".codex")
	resolvedProject, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	projectID := strings.NewReplacer("/", "-", "_", "-").Replace(resolvedProject)
	claudeProjectDir := filepath.Join(claudeHome, "projects", projectID)
	if err := os.MkdirAll(claudeProjectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(codexHome, 0o755); err != nil {
		t.Fatal(err)
	}

	id := "019fcb8e-b8cf-76b1-bc81-e444a74c4d60"
	sessionPath := filepath.Join(claudeProjectDir, id+".jsonl")
	content := fmt.Sprintf(`{"type":"user","sessionId":%q,"message":{"content":"continue this"}}`+"\n", id)
	if err := os.WriteFile(sessionPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	registryPath := filepath.Join(home, "accounts.json")
	writeRegistry(t, registryPath, claudeHome, codexHome)

	launched := filepath.Join(home, "launched")
	writeScript(t, filepath.Join(home, "bin", "codex"), fmt.Sprintf(
		"#!/usr/bin/env bash\nif [ \"$1 $2\" = 'login status' ]; then exit 0; fi\nprintf '%%s|%%s' \"$CODEX_HOME\" \"$1\" > %q\n", launched,
	))
	writeScript(t, filepath.Join(home, "bin", "claude"), "#!/usr/bin/env bash\nexit 0\n")

	if err := QuickHandoff(registryPath, "019fcb8e", project); err != nil {
		t.Fatalf("quick handoff failed: %v", err)
	}
	launchedContent, err := os.ReadFile(launched)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(launchedContent), "transcript.md") {
		t.Fatalf("expected codex to launch from the artifact, got %q", launchedContent)
	}
}

// A new account's home has no onboarding state, so `claude auth login`
// leaves the first interactive run asking to log in all over again. Adding
// an account therefore ends in the plain CLI, where that one prompt is the
// only login the user sees.
func TestAddingAnAccountEndsInThePlainCLINotTheLoginSubcommand(t *testing.T) {
	home := t.TempDir()
	runWithFakePath(t, home)

	claudeHome := filepath.Join(home, ".claude")
	codexHome := filepath.Join(home, ".codex")
	os.MkdirAll(claudeHome, 0o755)
	os.MkdirAll(codexHome, 0o755)

	registryPath := filepath.Join(home, "accounts.json")
	writeRegistry(t, registryPath, claudeHome, codexHome)

	fakeFzf(t, home,
		key("accounts"), key("add"), key("claude"), key("confirm"), key("own"))
	argvCapture := filepath.Join(home, "claude-argv")
	writeScript(t, filepath.Join(home, "bin", "claude"),
		fmt.Sprintf("#!/usr/bin/env bash\nprintf '%%s\\n' \"$0\" \"$@\" > %q\n", argvCapture))
	writeScript(t, filepath.Join(home, "bin", "codex"), "#!/usr/bin/env bash\nexit 0\n")

	if _, stderr, err := runMenu(t, registryPath, home, "\n"); err != nil {
		t.Fatalf("menu run failed: %v\nstderr: %s", err, stderr)
	}

	raw, err := os.ReadFile(argvCapture)
	if err != nil {
		t.Fatalf("expected the menu to exec the claude CLI: %v", err)
	}
	argv := strings.Fields(string(raw))
	if len(argv) != 1 {
		t.Fatalf("expected the CLI to be launched with no arguments, got %v", argv)
	}

	r, err := registry.Load(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.FindAccount(r, "claude-2"); err != nil {
		t.Fatalf("expected the new account to be registered: %v", err)
	}
}
