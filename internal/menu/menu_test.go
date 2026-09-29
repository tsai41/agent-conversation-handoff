package menu

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsai41/agent-conversation-handoff/internal/registry"
	"github.com/tsai41/agent-conversation-handoff/internal/session"
)

// captureStdout runs f with os.Stdout redirected to a pipe and returns
// everything it wrote. offerSharedSettings and shareAllAccountSettings print
// directly to os.Stdout rather than returning their report, so this is the
// only way to observe what they told the user.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	real := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	f()
	w.Close()
	os.Stdout = real
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

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

func cancel() reply { return reply{"", 130} }

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
	return runMenuWithUsageDir(t, registryPath, "", dir, stdin)
}

// runMenuWithUsageDir is runMenu with an explicit usage snapshot directory
// (as if --usage-dir were passed on the command line), for tests that drive
// the "查看用量" view.
func runMenuWithUsageDir(t *testing.T, registryPath, usageDir, dir, stdin string) (string, string, error) {
	t.Helper()
	return runMenuWithUsageDirFlag(t, registryPath, usageDir, true, dir, stdin)
}

// runMenuWithUsageDirFlag is runMenuWithUsageDir with control over whether
// --usage-dir counts as explicitly passed, for tests exercising the
// flag-vs-stored-setting precedence through the full menu loop.
func runMenuWithUsageDirFlag(t *testing.T, registryPath, usageDir string, usageDirExplicit bool, dir, stdin string) (string, string, error) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperRunMenu")
	cmd.Dir = dir
	env := append(os.Environ(), "GO_WANT_HELPER_MENU_PROCESS=1", "ACH_TEST_REGISTRY="+registryPath, "ACH_TEST_USAGE_DIR="+usageDir)
	if !usageDirExplicit {
		env = append(env, "ACH_TEST_USAGE_DIR_EXPLICIT=0")
	}
	cmd.Env = env
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
	usageDirExplicit := os.Getenv("ACH_TEST_USAGE_DIR_EXPLICIT") != "0"
	if err := Run(os.Getenv("ACH_TEST_REGISTRY"), os.Getenv("ACH_TEST_USAGE_DIR"), usageDirExplicit); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// runQuickHandoff drives QuickHandoff in a child process: it execs into the
// (faked) provider CLI and never returns, so it cannot run in the test
// process.
func runQuickHandoff(t *testing.T, registryPath, fragment, project string) (string, string, error) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperQuickHandoff")
	cmd.Dir = project
	cmd.Env = append(os.Environ(),
		"GO_WANT_HELPER_QUICK_HANDOFF_PROCESS=1",
		"ACH_TEST_REGISTRY="+registryPath,
		"ACH_TEST_FRAGMENT="+fragment,
		"ACH_TEST_PROJECT="+project,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// TestHelperQuickHandoff is not a real test; it is exec'd as a subprocess so
// QuickHandoff's exec into the provider CLI doesn't replace the test binary.
func TestHelperQuickHandoff(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_QUICK_HANDOFF_PROCESS") != "1" {
		return
	}
	err := QuickHandoff(os.Getenv("ACH_TEST_REGISTRY"), os.Getenv("ACH_TEST_FRAGMENT"), os.Getenv("ACH_TEST_PROJECT"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// Free-text input (alias, usage-dir path) now goes through fzf rather than
// os.Stdin, so nothing in the menu package reads real stdin ahead of an
// account launch any more; this only confirms that launching an account
// still hands the CLI process the real stdin untouched.
func TestRunPicksAFunctionBeforeAnAccountThenPassesRealStdinToTheLaunchedCLI(t *testing.T) {
	home := t.TempDir()
	runWithFakePath(t, home)

	claudeHome := filepath.Join(home, ".claude")
	codexHome := filepath.Join(home, ".codex")
	os.MkdirAll(claudeHome, 0o755)
	os.MkdirAll(codexHome, 0o755)

	registryPath := filepath.Join(home, "accounts.json")
	writeRegistry(t, registryPath, claudeHome, codexHome)

	calls := fakeFzf(t, home, key("chat"), key("claude-1"), key("new"))
	stdinCapture := filepath.Join(home, "stdin-seen")
	writeScript(t, filepath.Join(home, "bin", "claude"), fmt.Sprintf("#!/usr/bin/env bash\nhead -n1 > %q\n", stdinCapture))
	writeScript(t, filepath.Join(home, "bin", "codex"), "#!/usr/bin/env bash\nexit 0\n")

	stdout, stderr, err := runMenu(t, registryPath, home, "real-stdin-marker\n")
	if err != nil {
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
	if strings.Contains(functions, "離開") {
		t.Fatalf("expected ESC to be the only root-menu exit, got: %s", functions)
	}
	argv := callFile(t, calls, "argv-0")
	if !strings.Contains(argv, "--bind=1:pos(1)+accept,2:pos(2)+accept,3:pos(3)+accept") {
		t.Fatalf("expected numbered bind flag, got argv: %s", argv)
	}
	if strings.Contains(stdout, "主選單") {
		t.Fatalf("expected path bars not to remain in stdout, got: %s", stdout)
	}
	if !strings.Contains(argv, "--header=主選單") {
		t.Fatalf("expected the root path bar in the picker, got argv: %s", argv)
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
		t.Fatalf("expected the second-level path bar in the picker, got argv: %s", accountArgv)
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

func TestRunResumesTheSelectedAccountSession(t *testing.T) {
	home := t.TempDir()
	runWithFakePath(t, home)

	claudeHome := filepath.Join(home, ".claude")
	codexHome := filepath.Join(home, ".codex")
	project := filepath.Join(home, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	project, err = os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	projectID := session.ClaudeProjectID(project)
	sessionDir := filepath.Join(claudeHome, "projects", projectID)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionPath := filepath.Join(sessionDir, "resume.jsonl")
	const sessionID = "aaaaaaaa-1111-4222-8333-44444444abcd"
	if err := os.WriteFile(sessionPath, []byte(fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":"2026-09-29T09:00:00Z","message":{"content":"continue this"}}`+"\n", sessionID)), 0o644); err != nil {
		t.Fatal(err)
	}

	registryPath := filepath.Join(home, "accounts.json")
	writeRegistry(t, registryPath, claudeHome, codexHome)
	calls := fakeFzf(t, home, key("chat"), key("claude-1"), key("resume"), row(sessionPath))
	argvCapture := filepath.Join(home, "claude-argv")
	writeScript(t, filepath.Join(home, "bin", "claude"), fmt.Sprintf("#!/usr/bin/env bash\nprintf '%%s\\n' \"$@\" > %q\n", argvCapture))
	writeScript(t, filepath.Join(home, "bin", "codex"), "#!/usr/bin/env bash\nexit 0\n")

	_, stderr, err := runMenu(t, registryPath, project, "")
	if err != nil {
		t.Fatalf("menu run failed: %v\nstderr: %s", err, stderr)
	}
	if got, err := os.ReadFile(argvCapture); err != nil || string(got) != "--resume\n"+sessionID+"\n" {
		t.Fatalf("expected claude --resume %s, got %q, err %v", sessionID, got, err)
	}
	if got := callFile(t, calls, "rows-2"); !strings.Contains(got, "繼續既有對話") {
		t.Fatalf("expected resume option, got %s", got)
	}
}

func evalSymlinks(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func TestRunResumeByTypedIDLaunchesInTheSessionsRecordedDirectory(t *testing.T) {
	tests := []struct {
		name         string
		recordedGone bool
	}{
		{name: "recorded directory exists"},
		{name: "recorded directory is gone falls back to the current directory", recordedGone: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			runWithFakePath(t, home)

			claudeHome := filepath.Join(home, ".claude")
			codexHome := filepath.Join(home, ".codex")
			here := filepath.Join(home, "here")
			elsewhere := filepath.Join(home, "elsewhere")
			for _, dir := range []string{here, elsewhere} {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			recorded := elsewhere
			if tt.recordedGone {
				recorded = filepath.Join(home, "deleted")
			}
			sessionDir := filepath.Join(claudeHome, "projects", "-other-project")
			if err := os.MkdirAll(sessionDir, 0o755); err != nil {
				t.Fatal(err)
			}
			const sessionID = "aaaaaaaa-1111-4222-8333-44444444abcd"
			line := fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":%q,"timestamp":"2026-09-29T09:00:00Z","message":{"content":"continue this"}}`+"\n", sessionID, recorded)
			if err := os.WriteFile(filepath.Join(sessionDir, sessionID+".jsonl"), []byte(line), 0o644); err != nil {
				t.Fatal(err)
			}

			registryPath := filepath.Join(home, "accounts.json")
			writeRegistry(t, registryPath, claudeHome, codexHome)
			fakeFzf(t, home, key("chat"), key("claude-1"), key("resume"), typed("aaaaaaaa"))
			pwdCapture := filepath.Join(home, "claude-pwd")
			writeScript(t, filepath.Join(home, "bin", "claude"), fmt.Sprintf("#!/usr/bin/env bash\npwd -P > %q\n", pwdCapture))
			writeScript(t, filepath.Join(home, "bin", "codex"), "#!/usr/bin/env bash\nexit 0\n")

			if _, stderr, err := runMenu(t, registryPath, here, ""); err != nil {
				t.Fatalf("menu run failed: %v\nstderr: %s", err, stderr)
			}

			want := elsewhere
			if tt.recordedGone {
				want = here
			}
			want = evalSymlinks(t, want)
			got, err := os.ReadFile(pwdCapture)
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(string(got)) != want {
				t.Fatalf("expected claude to start in %s, got %q", want, got)
			}
		})
	}
}

func TestRunResumeByTypedIDWithSeveralMatchesLaunchesInThePickedSessionsDirectory(t *testing.T) {
	home := t.TempDir()
	runWithFakePath(t, home)

	claudeHome := filepath.Join(home, ".claude")
	codexHome := filepath.Join(home, ".codex")
	here := filepath.Join(home, "here")
	firstDir := filepath.Join(home, "first")
	secondDir := filepath.Join(home, "second")
	for _, dir := range []string{here, firstDir, secondDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sessions := []struct {
		id  string
		cwd string
	}{
		{"aaaaaaaa-0000-4222-8333-44444444abcd", firstDir},
		{"aaaaaaaa-2222-4222-8333-44444444abcd", secondDir},
	}
	var paths []string
	for _, s := range sessions {
		dir := filepath.Join(claudeHome, "projects", session.ClaudeProjectID(s.cwd))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		line := fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":%q,"timestamp":"2026-09-29T09:00:00Z","message":{"content":"continue this"}}`+"\n", s.id, s.cwd)
		path := filepath.Join(dir, s.id+".jsonl")
		if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}

	registryPath := filepath.Join(home, "accounts.json")
	writeRegistry(t, registryPath, claudeHome, codexHome)
	fakeFzf(t, home, key("chat"), key("claude-1"), key("resume"), typed("aaaaaaaa"), row(paths[1]))
	argvCapture := filepath.Join(home, "claude-argv")
	pwdCapture := filepath.Join(home, "claude-pwd")
	writeScript(t, filepath.Join(home, "bin", "claude"), fmt.Sprintf("#!/usr/bin/env bash\npwd -P > %q\nprintf '%%s\\n' \"$@\" > %q\n", pwdCapture, argvCapture))
	writeScript(t, filepath.Join(home, "bin", "codex"), "#!/usr/bin/env bash\nexit 0\n")

	if _, stderr, err := runMenu(t, registryPath, here, ""); err != nil {
		t.Fatalf("menu run failed: %v\nstderr: %s", err, stderr)
	}

	got, err := os.ReadFile(pwdCapture)
	if err != nil {
		t.Fatal(err)
	}
	if want := evalSymlinks(t, secondDir); strings.TrimSpace(string(got)) != want {
		t.Fatalf("expected claude to start in %s, got %q", want, got)
	}
	if got, err := os.ReadFile(argvCapture); err != nil || string(got) != "--resume\n"+sessions[1].id+"\n" {
		t.Fatalf("expected claude --resume %s, got %q, err %v", sessions[1].id, got, err)
	}
}

// ESC during first-run setup is a normal exit, not an error: nothing has
// been written yet, so backing out must not surface as "Error: selection
// cancelled" with a non-zero exit.
func TestRunExitsCleanlyWhenBootstrapSetupIsCancelled(t *testing.T) {
	home := t.TempDir()
	runWithFakePath(t, home)
	os.MkdirAll(filepath.Join(home, ".claude"), 0o755)

	registryPath := filepath.Join(home, "accounts.json")
	calls := fakeFzf(t, home, cancel(), key("chat"))

	stdout, stderr, err := runMenu(t, registryPath, home, "")
	if err != nil {
		t.Fatalf("expected ESC during first-run setup to exit cleanly, got: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}
	if strings.Contains(stderr, "Error:") {
		t.Fatalf("expected no Error: on stderr, got: %s", stderr)
	}
	if !strings.Contains(stdout, "已取消初次設定") {
		t.Fatalf("expected a message confirming the cancelled setup, got: %s", stdout)
	}
	// With no registry written, the root menu would fail on its first
	// action, so the run must end here instead of offering it.
	if _, err := os.Stat(filepath.Join(calls, "rows-1")); err == nil {
		t.Fatal("expected no root menu after a cancelled bootstrap")
	}
	if _, err := os.Stat(registryPath); err == nil {
		t.Fatal("expected no registry file after a cancelled bootstrap")
	}
}

// Selecting 查看用量 must print the table and land back at the root
// picker rather than exiting, since the view only reads files and launches
// nothing.
func TestUsageViewPrintsAndReturnsToTheMenu(t *testing.T) {
	home := t.TempDir()
	runWithFakePath(t, home)

	claudeHome := filepath.Join(home, ".claude")
	codexHome := filepath.Join(home, ".codex")
	os.MkdirAll(claudeHome, 0o755)
	os.MkdirAll(codexHome, 0o755)

	registryPath := filepath.Join(home, "accounts.json")
	writeRegistry(t, registryPath, claudeHome, codexHome)

	usageDir := filepath.Join(home, "usage")
	os.MkdirAll(usageDir, 0o755)
	resolvedClaudeHome, err := filepath.EvalSymlinks(claudeHome)
	if err != nil {
		t.Fatal(err)
	}
	checkedAt := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	os.WriteFile(filepath.Join(usageDir, "snapshot.json"), fmt.Appendf(nil, `{
		"version": 1,
		"config_dir": %q,
		"checked_at": %q,
		"five_hour": {"used_percentage": 55.0}
	}`, resolvedClaudeHome, checkedAt), 0o644)

	calls := fakeFzf(t, home, key("usage"), key("chat"), key("claude-1"), key("new"))
	writeScript(t, filepath.Join(home, "bin", "claude"), "#!/usr/bin/env bash\nexit 0\n")
	writeScript(t, filepath.Join(home, "bin", "codex"), "#!/usr/bin/env bash\nexit 0\n")

	stdout, stderr, err := runMenuWithUsageDir(t, registryPath, usageDir, home, "\n")
	if err != nil {
		t.Fatalf("menu run failed: %v\nstderr: %s", err, stderr)
	}

	if !strings.Contains(stdout, "Claude") || !strings.Contains(stdout, "55%") {
		t.Fatalf("expected the usage table to list Claude's matched snapshot, got: %s", stdout)
	}
	if !strings.Contains(stdout, "Codex") {
		t.Fatalf("expected an unmatched account to still be listed, got: %s", stdout)
	}
	if strings.Contains(stdout, "0%") {
		t.Fatalf("expected no window to render as 0%%, got: %s", stdout)
	}
	if !strings.Contains(stdout, usageDir) {
		t.Fatalf("expected the view to state which directory it read, got: %s", stdout)
	}

	// The loop must have come back to the root picker for a second pick
	// ("chat") rather than exiting after printing the table.
	if _, err := os.Stat(filepath.Join(calls, "rows-1")); err != nil {
		t.Fatalf("expected a second root-level pick after the usage view, got: %v", err)
	}
}

// TestUsageViewWithNoSnapshotDirectoryShowsNoDataForEveryAccount covers the
// ordinary state before the writing program has ever run: the view must
// still print a table and hand the menu back, not exit non-zero just
// because usage data hasn't been configured yet.
func TestUsageViewWithNoSnapshotDirectoryShowsNoDataForEveryAccount(t *testing.T) {
	home := t.TempDir()
	runWithFakePath(t, home)

	claudeHome := filepath.Join(home, ".claude")
	codexHome := filepath.Join(home, ".codex")
	os.MkdirAll(claudeHome, 0o755)
	os.MkdirAll(codexHome, 0o755)

	registryPath := filepath.Join(home, "accounts.json")
	writeRegistry(t, registryPath, claudeHome, codexHome)

	usageDir := filepath.Join(home, "usage-not-created-yet")

	calls := fakeFzf(t, home, key("usage"), key("chat"), key("claude-1"), key("new"))
	writeScript(t, filepath.Join(home, "bin", "claude"), "#!/usr/bin/env bash\nexit 0\n")
	writeScript(t, filepath.Join(home, "bin", "codex"), "#!/usr/bin/env bash\nexit 0\n")

	stdout, stderr, err := runMenuWithUsageDir(t, registryPath, usageDir, home, "\n")
	if err != nil {
		t.Fatalf("expected the menu to exit 0 with no usage directory at all, got: %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stdout, "Claude") || !strings.Contains(stdout, "Codex") {
		t.Fatalf("expected both accounts to be listed, got: %s", stdout)
	}
	if strings.Count(stdout, "沒有資料") < 2 {
		t.Fatalf("expected every row to be marked as having no data, got: %s", stdout)
	}
	if strings.Contains(stdout, "0%") {
		t.Fatalf("expected no window to render as 0%%, got: %s", stdout)
	}
	if !strings.Contains(stdout, "status line") {
		t.Fatalf("expected a hint that usage data comes from the status line program, got: %s", stdout)
	}
	if !strings.Contains(stdout, crumbAccounts+" > "+crumbStatusline) {
		t.Fatalf("expected the hint to name the menu path to enable it, got: %s", stdout)
	}
	if strings.Count(stdout, usageDir) != 1 {
		t.Fatalf("expected the directory to be named exactly once (the reading-from line, not repeated by the no-data hint too), got: %s", stdout)
	}
	if _, err := os.Stat(filepath.Join(calls, "rows-1")); err != nil {
		t.Fatalf("expected a second root-level pick after the usage view, got: %v", err)
	}
}

// TestUsageViewWithAnEmptySnapshotDirectoryShowsNoDataForEveryAccount covers
// a writing program that has been enabled but has not written anything for
// these accounts yet -- an empty directory must read the same as no
// directory at all, not as an error.
func TestUsageViewWithAnEmptySnapshotDirectoryShowsNoDataForEveryAccount(t *testing.T) {
	home := t.TempDir()
	runWithFakePath(t, home)

	claudeHome := filepath.Join(home, ".claude")
	codexHome := filepath.Join(home, ".codex")
	os.MkdirAll(claudeHome, 0o755)
	os.MkdirAll(codexHome, 0o755)

	registryPath := filepath.Join(home, "accounts.json")
	writeRegistry(t, registryPath, claudeHome, codexHome)

	usageDir := filepath.Join(home, "usage")
	os.MkdirAll(usageDir, 0o755)

	calls := fakeFzf(t, home, key("usage"), key("chat"), key("claude-1"), key("new"))
	writeScript(t, filepath.Join(home, "bin", "claude"), "#!/usr/bin/env bash\nexit 0\n")
	writeScript(t, filepath.Join(home, "bin", "codex"), "#!/usr/bin/env bash\nexit 0\n")

	stdout, stderr, err := runMenuWithUsageDir(t, registryPath, usageDir, home, "\n")
	if err != nil {
		t.Fatalf("expected the menu to exit 0 with an empty usage directory, got: %v\nstderr: %s", err, stderr)
	}
	if strings.Count(stdout, "沒有資料") < 2 {
		t.Fatalf("expected every row to be marked as having no data, got: %s", stdout)
	}
	if !strings.Contains(stdout, "status line") {
		t.Fatalf("expected a hint that usage data comes from the status line program, got: %s", stdout)
	}
	if _, err := os.Stat(filepath.Join(calls, "rows-1")); err != nil {
		t.Fatalf("expected a second root-level pick after the usage view, got: %v", err)
	}
}

// TestUsageViewSurvivesAnUnreadableSnapshotDirectory covers a usageDir that
// os.ReadDir refuses for a reason other than not existing (here, the path
// is a plain file). The old behavior propagated that error all the way out
// of Run and exited the CLI with status 1; the view must instead report the
// problem and still hand the menu back.
func TestUsageViewSurvivesAnUnreadableSnapshotDirectory(t *testing.T) {
	home := t.TempDir()
	runWithFakePath(t, home)

	claudeHome := filepath.Join(home, ".claude")
	codexHome := filepath.Join(home, ".codex")
	os.MkdirAll(claudeHome, 0o755)
	os.MkdirAll(codexHome, 0o755)

	registryPath := filepath.Join(home, "accounts.json")
	writeRegistry(t, registryPath, claudeHome, codexHome)

	// A plain file where a directory is expected makes os.ReadDir fail with
	// something other than IsNotExist.
	usageDir := filepath.Join(home, "usage-is-a-file")
	os.WriteFile(usageDir, []byte("not a directory"), 0o644)

	calls := fakeFzf(t, home, key("usage"), key("chat"), key("claude-1"), key("new"))
	writeScript(t, filepath.Join(home, "bin", "claude"), "#!/usr/bin/env bash\nexit 0\n")
	writeScript(t, filepath.Join(home, "bin", "codex"), "#!/usr/bin/env bash\nexit 0\n")

	stdout, stderr, err := runMenuWithUsageDir(t, registryPath, usageDir, home, "\n")
	if err != nil {
		t.Fatalf("expected the menu to survive an unreadable usage directory, got: %v\nstderr: %s", err, stderr)
	}
	if strings.Count(stdout, "沒有資料") < 2 {
		t.Fatalf("expected every row to be marked as having no data, got: %s", stdout)
	}
	if !strings.Contains(stdout, usageDir) {
		t.Fatalf("expected the unreadable directory's path to be named, got: %s", stdout)
	}
	if _, err := os.Stat(filepath.Join(calls, "rows-1")); err != nil {
		t.Fatalf("expected a second root-level pick after the usage view, got: %v", err)
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

	longUUID := "bbbbbbbb-1111-7222-8333-444444444444"
	sessionPath := filepath.Join(codexSessions, "rollout-2026-08-04T14-55-56-"+longUUID+".jsonl")
	os.WriteFile(sessionPath, []byte(fmt.Sprintf(
		`{"type":"session_meta","payload":{"id":%q,"cwd":%q}}`+"\n"+
			`{"type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"carry this on"}]}}`+"\n",
		longUUID, resolvedElsewhere)), 0o644)

	registryPath := filepath.Join(home, "accounts.json")
	writeRegistry(t, registryPath, claudeHome, codexHome)

	// The source picked is claude-1, but the typed id belongs to codex-1,
	// so the source flips and the target picker offers claude-1 instead.
	calls := fakeFzf(t, home, key("handoff"), key("claude-1"), typed("bbbbbbbb"), key("claude-1"))

	launched := filepath.Join(home, "launched")
	writeScript(t, filepath.Join(home, "bin", "claude"), fmt.Sprintf(
		"#!/usr/bin/env bash\nif [ \"$1 $2\" = 'auth status' ]; then exit 0; fi\nprintf '%%s|%%s' \"$CLAUDE_CONFIG_DIR\" \"$*\" > %q\n",
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
	if !strings.Contains(sessionArgv, "無法掃描：no Claude sessions for this project") || !strings.Contains(sessionArgv, "仍可輸入 ID 搜尋") {
		t.Fatalf("expected the scan failure in the picker header, got argv: %s", sessionArgv)
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
	if !strings.Contains(string(launchedContent), "|-- ") {
		t.Fatalf("expected the prompt after \"--\" so wrapper options cannot consume it, got %q", launchedContent)
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
	longUUID := "aaaaaaaa-1111-4222-8333-44444444abcd"
	// The subprocess resolves its cwd via os.Getwd(), which returns the
	// symlink-resolved path (e.g. /private/var/... on macOS), so the
	// session directory must be keyed off the same resolved path.
	resolvedProject, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	sessionDir := filepath.Join(claudeHome, "projects", session.ClaudeProjectID(resolvedProject))
	os.MkdirAll(sessionDir, 0o755)
	os.MkdirAll(codexHome, 0o755)
	sessionPath := filepath.Join(sessionDir, "source.jsonl")
	os.WriteFile(sessionPath, []byte(fmt.Sprintf(`{"type":"user","sessionId":%q,"message":{"content":"continue this"}}`+"\n", longUUID)), 0o644)

	registryPath := filepath.Join(home, "accounts.json")
	writeRegistry(t, registryPath, claudeHome, codexHome)

	calls := fakeFzf(t, home, key("handoff"), key("claude-1"), row(sessionPath), key("codex-1"))

	launched := filepath.Join(home, "launched")
	writeScript(t, filepath.Join(home, "bin", "codex"), fmt.Sprintf(
		"#!/usr/bin/env bash\nif [ \"$1 $2\" = 'login status' ]; then exit 0; fi\nprintf '%%s|%%s' \"$CODEX_HOME\" \"$*\" > %q\n",
		launched,
	))
	writeScript(t, filepath.Join(home, "bin", "claude"), "#!/usr/bin/env bash\nexit 0\n")

	stdout, stderr, err := runMenu(t, registryPath, project, "")
	if err != nil {
		t.Fatalf("menu run failed: %v\nstderr: %s", err, stderr)
	}

	if sources := callFile(t, calls, "rows-1"); !strings.Contains(sources, "1. Claude") {
		t.Fatalf("expected a numbered source account level, got: %s", sources)
	}
	if sourceArgv := callFile(t, calls, "argv-1"); !strings.Contains(sourceArgv, "--header=主選單 > 接手對話 > 選擇來源 Agent") {
		t.Fatalf("expected the source picker path bar in the picker, got argv: %s", sourceArgv)
	}
	rows := callFile(t, calls, "rows-2")
	if !strings.Contains(rows, "aaaaaaaa…abcd") {
		t.Fatalf("expected truncated session id in rows, got: %s", rows)
	}
	if strings.Contains(rows, longUUID) {
		t.Fatalf("full uuid leaked into session rows: %s", rows)
	}
	if sessionArgv := callFile(t, calls, "argv-2"); !strings.Contains(sessionArgv, "--header=主選單 > 接手對話 > 來源 Agent：Claude > 選擇來源對話（找到 1 筆，顯示最新 1 筆；其他對話可輸入 ID 搜尋）") {
		t.Fatalf("expected the conversation count in the picker header, got argv: %s", sessionArgv)
	}
	if targetArgv := callFile(t, calls, "argv-3"); !strings.Contains(targetArgv, "--header=主選單 > 接手對話 > 來源 Agent：Claude > 選擇目標 Agent") {
		t.Fatalf("expected the target picker path bar in the picker, got argv: %s", targetArgv)
	}
	if strings.Contains(stdout, "主選單") {
		t.Fatalf("expected path bars not to remain in stdout, got: %s", stdout)
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

// ESC on the conversation picker must return to the source-account picker,
// not restart the wizard at the root menu.
func TestHandoffEscAtSessionPickerReturnsToSourcePicker(t *testing.T) {
	home := t.TempDir()
	runWithFakePath(t, home)

	claudeHome := filepath.Join(home, ".claude")
	codexHome := filepath.Join(home, ".codex")
	os.MkdirAll(claudeHome, 0o755)
	os.MkdirAll(codexHome, 0o755)

	registryPath := filepath.Join(home, "accounts.json")
	writeRegistry(t, registryPath, claudeHome, codexHome)

	calls := fakeFzf(t, home, key("handoff"), key("claude-1"), cancel())
	writeScript(t, filepath.Join(home, "bin", "claude"), "#!/usr/bin/env bash\nexit 0\n")
	writeScript(t, filepath.Join(home, "bin", "codex"), "#!/usr/bin/env bash\nexit 0\n")

	if _, stderr, err := runMenu(t, registryPath, home, ""); err != nil {
		t.Fatalf("menu run failed: %v\nstderr: %s", err, stderr)
	}

	sourceAgain := callFile(t, calls, "rows-3")
	if !strings.Contains(sourceAgain, "1. Claude") || !strings.Contains(sourceAgain, "2. Codex") {
		t.Fatalf("expected ESC at the session picker to return to the source picker, got: %s", sourceAgain)
	}
}

// ESC on the target-account picker must return to the conversation picker,
// not all the way back to the source picker.
func TestHandoffEscAtTargetPickerReturnsToSessionPicker(t *testing.T) {
	home := t.TempDir()
	runWithFakePath(t, home)

	project := filepath.Join(home, "project")
	os.MkdirAll(project, 0o755)
	claudeHome := filepath.Join(home, ".claude")
	codexHome := filepath.Join(home, ".codex")
	longUUID := "aaaaaaaa-1111-4222-8333-44444444abcd"
	resolvedProject, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	sessionDir := filepath.Join(claudeHome, "projects", session.ClaudeProjectID(resolvedProject))
	os.MkdirAll(sessionDir, 0o755)
	os.MkdirAll(codexHome, 0o755)
	sessionPath := filepath.Join(sessionDir, "source.jsonl")
	os.WriteFile(sessionPath, []byte(fmt.Sprintf(`{"type":"user","sessionId":%q,"message":{"content":"continue this"}}`+"\n", longUUID)), 0o644)

	registryPath := filepath.Join(home, "accounts.json")
	writeRegistry(t, registryPath, claudeHome, codexHome)

	calls := fakeFzf(t, home, key("handoff"), key("claude-1"), row(sessionPath), cancel())
	writeScript(t, filepath.Join(home, "bin", "claude"), "#!/usr/bin/env bash\nexit 0\n")
	writeScript(t, filepath.Join(home, "bin", "codex"), "#!/usr/bin/env bash\nexit 0\n")

	if _, stderr, err := runMenu(t, registryPath, project, ""); err != nil {
		t.Fatalf("menu run failed: %v\nstderr: %s", err, stderr)
	}

	sessionAgain := callFile(t, calls, "rows-4")
	if !strings.Contains(sessionAgain, "aaaaaaaa…abcd") {
		t.Fatalf("expected ESC at the target picker to return to the session picker, got: %s", sessionAgain)
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
	projectID := session.ClaudeProjectID(resolvedProject)
	claudeProjectDir := filepath.Join(claudeHome, "projects", projectID)
	if err := os.MkdirAll(claudeProjectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(codexHome, 0o755); err != nil {
		t.Fatal(err)
	}

	id := "bbbbbbbb-1111-7222-8333-444444444444"
	sessionPath := filepath.Join(claudeProjectDir, id+".jsonl")
	content := fmt.Sprintf(`{"type":"user","sessionId":%q,"message":{"content":"continue this"}}`+"\n", id)
	if err := os.WriteFile(sessionPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	registryPath := filepath.Join(home, "accounts.json")
	writeRegistry(t, registryPath, claudeHome, codexHome)

	launched := filepath.Join(home, "launched")
	writeScript(t, filepath.Join(home, "bin", "codex"), fmt.Sprintf(
		"#!/usr/bin/env bash\nif [ \"$1 $2\" = 'login status' ]; then exit 0; fi\nprintf '%%s|%%s' \"$CODEX_HOME\" \"$*\" > %q\n", launched,
	))
	writeScript(t, filepath.Join(home, "bin", "claude"), "#!/usr/bin/env bash\nexit 0\n")

	if _, stderr, err := runQuickHandoff(t, registryPath, "bbbbbbbb", project); err != nil {
		t.Fatalf("quick handoff failed: %v\nstderr: %s", err, stderr)
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
		key("accounts"), key("add"), key("claude"), key("confirm"), typed(""), key("own"))
	argvCapture := filepath.Join(home, "claude-argv")
	writeScript(t, filepath.Join(home, "bin", "claude"),
		fmt.Sprintf("#!/usr/bin/env bash\nprintf '%%s\\n' \"$0\" \"$@\" > %q\n", argvCapture))
	writeScript(t, filepath.Join(home, "bin", "codex"), "#!/usr/bin/env bash\nexit 0\n")

	if _, stderr, err := runMenu(t, registryPath, home, ""); err != nil {
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

// A bare source account has nothing to share, which is not the same thing
// as every account already sharing settings -- the summary must say so
// distinctly rather than claiming sharing that never happened.
func TestShareAllAccountSettingsReportsABareSourceDistinctly(t *testing.T) {
	home := t.TempDir()
	claude1 := filepath.Join(home, "claude-1")
	claude2 := filepath.Join(home, "claude-2")
	if err := os.MkdirAll(claude1, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(claude2, 0o755); err != nil {
		t.Fatal(err)
	}
	r := registry.Registry{Accounts: []registry.Account{
		{ID: "claude-1", Provider: "claude", Number: 1, Home: claude1},
		{ID: "claude-2", Provider: "claude", Number: 2, Home: claude2},
	}}

	out := captureStdout(t, func() { shareAllAccountSettings(r) })

	if strings.Contains(out, "所有帳號都已經在共用設定了") {
		t.Fatalf("expected a bare source not to be reported as already shared, got: %s", out)
	}
	if !strings.Contains(out, "沒有可共用的設定") {
		t.Fatalf("expected a message naming the bare source, got: %s", out)
	}
	if _, err := os.Lstat(filepath.Join(claude2, "settings.json")); !os.IsNotExist(err) {
		t.Fatal("expected nothing to have been linked from a bare source")
	}
}

// offerSharedSettings reported linked+failed as the denominator, excluding
// entries that were already shared -- one linked, three already shared and
// one failed printed "1/2" instead of the true "4/5".
func TestOfferSharedSettingsCountsAlreadySharedEntriesInTheTotal(t *testing.T) {
	home := t.TempDir()
	runWithFakePath(t, home)
	fakeFzf(t, home, key("share"))

	sourceHome := filepath.Join(home, "claude-1")
	targetHome := filepath.Join(home, "claude-2")
	if err := os.MkdirAll(sourceHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(targetHome, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"settings.json", "settings.local.json"} {
		if err := os.WriteFile(filepath.Join(sourceHome, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"skills", "commands", "agents", "plugins"} {
		if err := os.MkdirAll(filepath.Join(sourceHome, name, "x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// settings.local.json, skills, commands and plugins already point at
	// the source, as a previous share would have left them.
	for _, name := range []string{"settings.local.json", "skills", "commands", "plugins"} {
		if err := os.Symlink(filepath.Join(sourceHome, name), filepath.Join(targetHome, name)); err != nil {
			t.Fatal(err)
		}
	}
	// agents is the account's own directory, which offerSharedSettings (no
	// replace) must refuse. settings.json is left missing so it links fresh.
	if err := os.MkdirAll(filepath.Join(targetHome, "agents", "mine"), 0o755); err != nil {
		t.Fatal(err)
	}

	r := registry.Registry{Accounts: []registry.Account{
		{ID: "claude-1", Provider: "claude", Number: 1, Home: sourceHome},
		{ID: "claude-2", Provider: "claude", Number: 2, Home: targetHome},
	}}
	target := registry.Account{ID: "claude-2", Provider: "claude", Number: 2, Home: targetHome}

	out := captureStdout(t, func() { offerSharedSettings(r, target, "") })

	if !strings.Contains(out, "5/6 個項目") {
		t.Fatalf("expected the summary to count all six entries, got: %s", out)
	}
	if strings.Contains(out, "1/2 個項目") {
		t.Fatalf("expected the old under-reporting fraction not to appear, got: %s", out)
	}
}

func TestResolveUsageDirPrefersExplicitFlagOverStored(t *testing.T) {
	dir := t.TempDir()
	registryPath := filepath.Join(dir, "accounts.json")
	if _, err := registry.SetUsageDir(registryPath, filepath.Join(dir, "stored-usage")); err != nil {
		t.Fatal(err)
	}

	flagValue := filepath.Join(dir, "flag-usage")
	if got := resolveUsageDir(registryPath, flagValue, true); got != flagValue {
		t.Fatalf("expected an explicit flag to win over the stored setting, got %q", got)
	}
}

func TestResolveUsageDirUsesStoredValueWhenNoFlagPassed(t *testing.T) {
	dir := t.TempDir()
	registryPath := filepath.Join(dir, "accounts.json")
	stored := filepath.Join(dir, "stored-usage")
	if _, err := registry.SetUsageDir(registryPath, stored); err != nil {
		t.Fatal(err)
	}

	builtinDefault := filepath.Join(dir, "builtin-default")
	if got := resolveUsageDir(registryPath, builtinDefault, false); got != stored {
		t.Fatalf("expected the stored setting to be used when the flag was not explicit, got %q", got)
	}
}

func TestResolveUsageDirFallsBackToBuiltinDefaultWhenNothingStored(t *testing.T) {
	dir := t.TempDir()
	registryPath := filepath.Join(dir, "accounts.json")
	builtinDefault := filepath.Join(dir, "builtin-default")

	if got := resolveUsageDir(registryPath, builtinDefault, false); got != builtinDefault {
		t.Fatalf("expected the built-in default when nothing is stored, got %q", got)
	}
}

// Setting the usage directory from 帳號設定 must persist to the registry and
// tell the user what it is now set to, without breaking the numbered
// shortcuts around it (the picker still offers "back" and reaches "chat").
func TestManageAccountsSetsUsageDirectoryAndReportsIt(t *testing.T) {
	home := t.TempDir()
	runWithFakePath(t, home)

	claudeHome := filepath.Join(home, ".claude")
	codexHome := filepath.Join(home, ".codex")
	os.MkdirAll(claudeHome, 0o755)
	os.MkdirAll(codexHome, 0o755)

	registryPath := filepath.Join(home, "accounts.json")
	writeRegistry(t, registryPath, claudeHome, codexHome)

	customUsageDir := filepath.Join(home, "custom-usage")

	calls := fakeFzf(t, home, key("accounts"), key("usage-dir"), typed(customUsageDir), cancel(), key("chat"), key("claude-1"), key("new"))
	writeScript(t, filepath.Join(home, "bin", "claude"), "#!/usr/bin/env bash\nexit 0\n")
	writeScript(t, filepath.Join(home, "bin", "codex"), "#!/usr/bin/env bash\nexit 0\n")

	stdout, stderr, err := runMenu(t, registryPath, home, "")
	if err != nil {
		t.Fatalf("menu run failed: %v\nstderr: %s", err, stderr)
	}

	actions := callFile(t, calls, "rows-1")
	if !strings.Contains(actions, "設定用量資料目錄") {
		t.Fatalf("expected 帳號設定 to offer setting the usage directory, got: %s", actions)
	}
	if !strings.Contains(stdout, customUsageDir) {
		t.Fatalf("expected the new directory to be reported back, got: %s", stdout)
	}
	if strings.Contains(stdout, "主選單 > 帳號設定 > 設定用量資料目錄") {
		t.Fatalf("expected the input path bar not to remain in stdout, got: %s", stdout)
	}
	inputArgv := callFile(t, calls, "argv-2")
	if !strings.Contains(inputArgv, "--header=主選單 > 帳號設定 > 設定用量資料目錄") {
		t.Fatalf("expected the input path bar in the picker, got argv: %s", inputArgv)
	}

	updated, err := registry.Load(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if updated.UsageDir != customUsageDir {
		t.Fatalf("expected the registry to store %q, got %q", customUsageDir, updated.UsageDir)
	}
}

// An empty input at the usage-directory prompt clears a previously stored
// setting rather than storing an empty path.
func TestManageAccountsClearsUsageDirectoryOnEmptyInput(t *testing.T) {
	home := t.TempDir()
	runWithFakePath(t, home)

	claudeHome := filepath.Join(home, ".claude")
	codexHome := filepath.Join(home, ".codex")
	os.MkdirAll(claudeHome, 0o755)
	os.MkdirAll(codexHome, 0o755)

	registryPath := filepath.Join(home, "accounts.json")
	writeRegistry(t, registryPath, claudeHome, codexHome)
	if _, err := registry.SetUsageDir(registryPath, filepath.Join(home, "already-stored")); err != nil {
		t.Fatal(err)
	}

	fakeFzf(t, home, key("accounts"), key("usage-dir"), typed(""), cancel(), key("chat"), key("claude-1"), key("new"))
	writeScript(t, filepath.Join(home, "bin", "claude"), "#!/usr/bin/env bash\nexit 0\n")
	writeScript(t, filepath.Join(home, "bin", "codex"), "#!/usr/bin/env bash\nexit 0\n")

	stdout, stderr, err := runMenu(t, registryPath, home, "")
	if err != nil {
		t.Fatalf("menu run failed: %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stdout, "已清除") {
		t.Fatalf("expected a message confirming the setting was cleared, got: %s", stdout)
	}

	updated, err := registry.Load(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if updated.UsageDir != "" {
		t.Fatalf("expected an empty input to clear the stored directory, got %q", updated.UsageDir)
	}
}

// writeClaudeSettings writes a minimal, realistic settings.json (a
// statusLine.command among other keys) as the claude account's shared
// settings document.
func writeClaudeSettings(t *testing.T, claudeHome, command string) {
	t.Helper()
	doc := fmt.Sprintf(`{
  "model": "opusplan",
  "statusLine": {
    "type": "command",
    "command": %q,
    "padding": 0
  },
  "hooks": {}
}
`, command)
	if err := os.WriteFile(filepath.Join(claudeHome, "settings.json"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestManageAccountsOffersStatuslineAction(t *testing.T) {
	home := t.TempDir()
	runWithFakePath(t, home)

	claudeHome := filepath.Join(home, ".claude")
	codexHome := filepath.Join(home, ".codex")
	os.MkdirAll(claudeHome, 0o755)
	os.MkdirAll(codexHome, 0o755)
	writeClaudeSettings(t, claudeHome, "~/.claude/statusline-go")

	registryPath := filepath.Join(home, "accounts.json")
	writeRegistry(t, registryPath, claudeHome, codexHome)

	calls := fakeFzf(t, home, key("accounts"), cancel(), key("chat"), key("claude-1"), key("new"))
	writeScript(t, filepath.Join(home, "bin", "claude"), "#!/usr/bin/env bash\nexit 0\n")
	writeScript(t, filepath.Join(home, "bin", "codex"), "#!/usr/bin/env bash\nexit 0\n")

	if _, stderr, err := runMenu(t, registryPath, home, ""); err != nil {
		t.Fatalf("menu run failed: %v\nstderr: %s", err, stderr)
	}
	actions := callFile(t, calls, "rows-1")
	if !strings.Contains(actions, "狀態列設定") {
		t.Fatalf("expected 帳號設定 to offer 狀態列設定, got: %s", actions)
	}
}

// Enabling from the menu must write --usage-dir into the shared
// settings.json and tell the user where the pre-write backup went.
func TestManageAccountsStatuslineEnablesUsageDir(t *testing.T) {
	home := t.TempDir()
	runWithFakePath(t, home)

	claudeHome := filepath.Join(home, ".claude")
	codexHome := filepath.Join(home, ".codex")
	os.MkdirAll(claudeHome, 0o755)
	os.MkdirAll(codexHome, 0o755)
	writeClaudeSettings(t, claudeHome, "~/.claude/statusline-go")

	registryPath := filepath.Join(home, "accounts.json")
	writeRegistry(t, registryPath, claudeHome, codexHome)
	usageDir := filepath.Join(home, "usage")

	fakeFzf(t, home, key("accounts"), key("statusline"), key("enable"), cancel(), key("chat"), key("claude-1"), key("new"))
	writeScript(t, filepath.Join(home, "bin", "claude"), "#!/usr/bin/env bash\nexit 0\n")
	writeScript(t, filepath.Join(home, "bin", "codex"), "#!/usr/bin/env bash\nexit 0\n")

	stdout, stderr, err := runMenuWithUsageDir(t, registryPath, usageDir, home, "")
	if err != nil {
		t.Fatalf("menu run failed: %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stdout, "已更新") || !strings.Contains(stdout, "已備份") {
		t.Fatalf("expected a confirmation naming the backup, got: %s", stdout)
	}
	raw, err := os.ReadFile(filepath.Join(claudeHome, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "--usage-dir "+usageDir) {
		t.Fatalf("expected settings.json to carry --usage-dir %s, got: %s", usageDir, raw)
	}
}

// A settings.json with no statusLine must be reported, with guidance, and
// never written to.
func TestManageAccountsStatuslineRefusesWhenStatusLineMissing(t *testing.T) {
	home := t.TempDir()
	runWithFakePath(t, home)

	claudeHome := filepath.Join(home, ".claude")
	codexHome := filepath.Join(home, ".codex")
	os.MkdirAll(claudeHome, 0o755)
	os.MkdirAll(codexHome, 0o755)
	if err := os.WriteFile(filepath.Join(claudeHome, "settings.json"), []byte(`{"model":"opusplan"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	registryPath := filepath.Join(home, "accounts.json")
	writeRegistry(t, registryPath, claudeHome, codexHome)

	fakeFzf(t, home, key("accounts"), key("statusline"), cancel(), key("chat"), key("claude-1"), key("new"))
	writeScript(t, filepath.Join(home, "bin", "claude"), "#!/usr/bin/env bash\nexit 0\n")
	writeScript(t, filepath.Join(home, "bin", "codex"), "#!/usr/bin/env bash\nexit 0\n")

	stdout, stderr, err := runMenu(t, registryPath, home, "")
	if err != nil {
		t.Fatalf("menu run failed: %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stdout, "無法判讀") || !strings.Contains(stdout, "<path-to-your-status-line-executable>") {
		t.Fatalf("expected a refusal message with a pasteable snippet, got: %s", stdout)
	}
	raw, err := os.ReadFile(filepath.Join(claudeHome, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"model":"opusplan"}` {
		t.Fatal("a refused statusline action modified the document")
	}
}

// writeRegistryTwoClaudeAccounts is writeRegistry with a second claude
// account, for tests that need trust to have a real other-account target.
func writeRegistryTwoClaudeAccounts(t *testing.T, path, claude1Home, claude2Home, codexHome string) {
	t.Helper()
	content := fmt.Sprintf(`{
		"version": 1,
		"next_number": {"claude": 3, "codex": 2},
		"accounts": [
			{"id": "claude-1", "provider": "claude", "number": 1, "home": %q, "alias": ""},
			{"id": "claude-2", "provider": "claude", "number": 2, "home": %q, "alias": ""},
			{"id": "codex-1", "provider": "codex", "number": 1, "home": %q, "alias": ""}
		]
	}`, claude1Home, claude2Home, codexHome)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestManageAccountsOffersTrustAction(t *testing.T) {
	home := t.TempDir()
	runWithFakePath(t, home)

	claudeHome := filepath.Join(home, ".claude")
	codexHome := filepath.Join(home, ".codex")
	os.MkdirAll(claudeHome, 0o755)
	os.MkdirAll(codexHome, 0o755)

	registryPath := filepath.Join(home, "accounts.json")
	writeRegistry(t, registryPath, claudeHome, codexHome)

	calls := fakeFzf(t, home, key("accounts"), cancel(), key("chat"), key("claude-1"), key("new"))
	writeScript(t, filepath.Join(home, "bin", "claude"), "#!/usr/bin/env bash\nexit 0\n")
	writeScript(t, filepath.Join(home, "bin", "codex"), "#!/usr/bin/env bash\nexit 0\n")

	if _, stderr, err := runMenu(t, registryPath, home, ""); err != nil {
		t.Fatalf("menu run failed: %v\nstderr: %s", err, stderr)
	}
	actions := callFile(t, calls, "rows-1")
	if !strings.Contains(actions, "同步專案信任到其他帳號") {
		t.Fatalf("expected 帳號設定 to offer 同步專案信任到其他帳號, got: %s", actions)
	}
}

// A run that fills a field a target project is missing must write it into
// that target's .claude.json, back the original up first, and report both.
func TestManageAccountsTrustFillsAndReportsField(t *testing.T) {
	home := t.TempDir()
	runWithFakePath(t, home)

	claude1Home := filepath.Join(home, ".claude")
	claude2Home := filepath.Join(home, ".claude-2")
	codexHome := filepath.Join(home, ".codex")
	os.MkdirAll(claude1Home, 0o755)
	os.MkdirAll(claude2Home, 0o755)
	os.MkdirAll(codexHome, 0o755)

	// The primary's home is $HOME/.claude, which Claude Code runs with
	// CLAUDE_CONFIG_DIR unset -- its .claude.json is $HOME/.claude.json.
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"projects": {"/repo/a": {"hasTrustDialogAccepted": true}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claude2Home, ".claude.json"), []byte(`{"projects": {"/repo/a": {"someKey": "keep"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	registryPath := filepath.Join(home, "accounts.json")
	writeRegistryTwoClaudeAccounts(t, registryPath, claude1Home, claude2Home, codexHome)

	fakeFzf(t, home, key("accounts"), key("trust"), key("claude-1"), key("confirm"), cancel(), key("chat"), key("claude-1"), key("new"))
	writeScript(t, filepath.Join(home, "bin", "claude"), "#!/usr/bin/env bash\nexit 0\n")
	writeScript(t, filepath.Join(home, "bin", "codex"), "#!/usr/bin/env bash\nexit 0\n")

	stdout, stderr, err := runMenu(t, registryPath, home, "")
	if err != nil {
		t.Fatalf("menu run failed: %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stdout, "已補上") || !strings.Contains(stdout, "原設定已備份到") {
		t.Fatalf("expected a report naming what was filled and the backup, got: %s", stdout)
	}
	raw, err := os.ReadFile(filepath.Join(claude2Home, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "hasTrustDialogAccepted") {
		t.Fatalf("expected the target's .claude.json to gain the missing field, got: %s", raw)
	}
	if !strings.Contains(string(raw), "keep") {
		t.Fatalf("expected the target's existing project key to survive, got: %s", raw)
	}
}

// A codex source must be refused before any file is touched, with no
// confirmation step offered.
func TestManageAccountsTrustRefusesCodexSourceWithoutTouchingFiles(t *testing.T) {
	home := t.TempDir()
	runWithFakePath(t, home)

	claudeHome := filepath.Join(home, ".claude")
	codexHome := filepath.Join(home, ".codex")
	os.MkdirAll(claudeHome, 0o755)
	os.MkdirAll(codexHome, 0o755)
	claudeJSON := `{"projects": {"/repo/a": {"hasTrustDialogAccepted": true}}}`
	if err := os.WriteFile(filepath.Join(claudeHome, ".claude.json"), []byte(claudeJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	registryPath := filepath.Join(home, "accounts.json")
	writeRegistry(t, registryPath, claudeHome, codexHome)

	fakeFzf(t, home, key("accounts"), key("trust"), key("codex-1"), cancel(), key("chat"), key("claude-1"), key("new"))
	writeScript(t, filepath.Join(home, "bin", "claude"), "#!/usr/bin/env bash\nexit 0\n")
	writeScript(t, filepath.Join(home, "bin", "codex"), "#!/usr/bin/env bash\nexit 0\n")

	stdout, stderr, err := runMenu(t, registryPath, home, "")
	if err != nil {
		t.Fatalf("menu run failed: %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stdout, "只有 Claude 帳號") {
		t.Fatalf("expected a refusal explaining only claude accounts have this state, got: %s", stdout)
	}
	raw, err := os.ReadFile(filepath.Join(claudeHome, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != claudeJSON {
		t.Fatal("a refused trust sync modified the claude account's .claude.json")
	}
	entries, err := os.ReadDir(claudeHome)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".bak-") {
			t.Fatalf("expected no backup file, found %s", e.Name())
		}
	}
}

// Choosing cancel at the confirmation step must leave every target's
// .claude.json byte-identical and create no backup.
func TestManageAccountsTrustCancelWritesNothing(t *testing.T) {
	home := t.TempDir()
	runWithFakePath(t, home)

	claude1Home := filepath.Join(home, ".claude")
	claude2Home := filepath.Join(home, ".claude-2")
	codexHome := filepath.Join(home, ".codex")
	os.MkdirAll(claude1Home, 0o755)
	os.MkdirAll(claude2Home, 0o755)
	os.MkdirAll(codexHome, 0o755)

	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"projects": {"/repo/a": {"hasTrustDialogAccepted": true}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	targetDoc := `{"projects": {"/repo/a": {"someKey": "keep"}}}`
	if err := os.WriteFile(filepath.Join(claude2Home, ".claude.json"), []byte(targetDoc), 0o644); err != nil {
		t.Fatal(err)
	}

	registryPath := filepath.Join(home, "accounts.json")
	writeRegistryTwoClaudeAccounts(t, registryPath, claude1Home, claude2Home, codexHome)

	fakeFzf(t, home, key("accounts"), key("trust"), key("claude-1"), key("cancel"), cancel(), key("chat"), key("claude-1"), key("new"))
	writeScript(t, filepath.Join(home, "bin", "claude"), "#!/usr/bin/env bash\nexit 0\n")
	writeScript(t, filepath.Join(home, "bin", "codex"), "#!/usr/bin/env bash\nexit 0\n")

	stdout, stderr, err := runMenu(t, registryPath, home, "")
	if err != nil {
		t.Fatalf("menu run failed: %v\nstderr: %s", err, stderr)
	}
	if strings.Contains(stdout, "已補上") {
		t.Fatalf("expected no sync report after cancel, got: %s", stdout)
	}
	raw, err := os.ReadFile(filepath.Join(claude2Home, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != targetDoc {
		t.Fatalf("cancel modified the target: %s", raw)
	}
	entries, err := os.ReadDir(claude2Home)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".bak-") {
			t.Fatalf("cancel left a backup: %s", e.Name())
		}
	}
}
