package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tsai41/agent-conversation-handoff/internal/registry"
	"github.com/tsai41/agent-conversation-handoff/internal/usage"
)

func TestBareInvocationDefaultsToMenu(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ach")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = "."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}

	home := t.TempDir()
	fakeBin := filepath.Join(home, "bin")
	os.MkdirAll(fakeBin, 0o755)
	os.WriteFile(filepath.Join(fakeBin, "fzf"), []byte("#!/usr/bin/env bash\nexit 130\n"), 0o755)
	os.MkdirAll(filepath.Join(home, ".claude"), 0o755)

	cmd := exec.Command(binary)
	cmd.Env = append(os.Environ(), "HOME="+home, "PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()

	// A usage error would exit before reaching fzf. A cancelled bootstrap is
	// a normal exit, so the proof is the cancellation line, not the code.
	if err != nil {
		t.Fatalf("expected a clean exit after fzf cancelled the bootstrap prompt: %v\n%s", err, out)
	}
	got := string(out)
	if strings.Contains(got, "usage: ach") {
		t.Fatalf("bare invocation hit the old usage error instead of defaulting to menu: %s", got)
	}
	if !strings.Contains(got, "已取消初次設定") {
		t.Fatalf("expected the bootstrap cancellation line, got: %s", got)
	}
}

// `usage record` sits inside a status line pipeline: it must print nothing,
// exit 0 on input it cannot use, and leave a snapshot the menu's reader
// shows for the account whose config dir it ran under.
func TestUsageRecordWritesSnapshotTheReaderShows(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ach")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}
	usageDir := filepath.Join(t.TempDir(), "usage")
	accountHome := t.TempDir()
	reset := strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)

	tests := []struct {
		name      string
		stdin     string
		wantFiles int
	}{
		{"malformed json", "not json", 0},
		{"no rate_limits", `{"model":{}}`, 0},
		{"both windows", `{"rate_limits":{"five_hour":{"used_percentage":42,"resets_at":` + reset + `},"seven_day":{"used_percentage":9,"resets_at":` + reset + `}}}`, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(usageDir, strings.ReplaceAll(tt.name, " ", "-"))
			cmd := exec.Command(binary, "usage", "record", "--usage-dir", dir)
			cmd.Env = append(os.Environ(), "CLAUDE_CONFIG_DIR="+accountHome)
			cmd.Stdin = strings.NewReader(tt.stdin)
			var stdout bytes.Buffer
			cmd.Stdout = &stdout
			if err := cmd.Run(); err != nil {
				t.Fatalf("expected exit 0, got %v", err)
			}
			if stdout.Len() != 0 {
				t.Fatalf("expected no stdout, got %q", stdout.String())
			}
			entries, _ := os.ReadDir(dir)
			if len(entries) != tt.wantFiles {
				t.Fatalf("expected %d snapshot files, got %d", tt.wantFiles, len(entries))
			}
			if tt.wantFiles == 0 {
				return
			}
			snaps, err := usage.LoadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			matches := usage.MatchLatest([]registry.Account{{ID: "claude-1", Home: accountHome}}, snaps)
			rows := usage.BuildRows([]struct{ ID, Label string }{{"claude-1", "Claude 1"}}, matches, time.Now())
			if !strings.HasPrefix(rows[0].FiveHour, "42%") || !strings.HasPrefix(rows[0].SevenDay, "9%") {
				t.Fatalf("expected the recorded percentages, got %+v", rows[0])
			}
		})
	}
}

// With no CLAUDE_CONFIG_DIR and no resolvable home there is no account to
// record for; the status line must still see a clean exit 0.
func TestUsageRecordWithoutAnyHomeExitsQuietly(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ach")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}
	dir := filepath.Join(t.TempDir(), "usage")
	reset := strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)

	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "CLAUDE_CONFIG_DIR=") && !strings.HasPrefix(kv, "HOME=") && !strings.HasPrefix(kv, "ACH_CONFIG_DIR=") {
			env = append(env, kv)
		}
	}
	cmd := exec.Command(binary, "usage", "record", "--usage-dir", dir)
	cmd.Dir = t.TempDir()
	cmd.Env = append(env, "HOME=")
	cmd.Stdin = strings.NewReader(`{"rate_limits":{"five_hour":{"used_percentage":42,"resets_at":` + reset + `}}}`)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("expected exit 0, got %v\n%s", err, out.String())
	}
	if out.Len() != 0 {
		t.Fatalf("expected no output, got %q", out.String())
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("expected no snapshot files, got %d", len(entries))
	}
}

// A bad registry must not be touched by the writer: registry.Load would move
// it aside into a .corrupt- backup, and a status line runs this on every
// refresh.
func TestUsageRecordLeavesACorruptRegistryAlone(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ach")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}
	home := t.TempDir()
	registryDir := t.TempDir()
	registryPath := filepath.Join(registryDir, "accounts.json")
	const corrupt = "{not json"
	if err := os.WriteFile(registryPath, []byte(corrupt), 0o600); err != nil {
		t.Fatal(err)
	}
	stdin := `{"rate_limits":{"five_hour":{"used_percentage":42}}}`
	defaultDir := filepath.Join(home, ".config", "agent-conversation-handoff", "usage")
	flagDir := filepath.Join(t.TempDir(), "flag-usage")

	tests := []struct {
		name    string
		args    []string
		wantDir string
	}{
		{"default dir", []string{"usage", "record", "--registry", registryPath}, defaultDir},
		{"flag dir", []string{"usage", "record", "--registry", registryPath, "--usage-dir", flagDir}, flagDir},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(binary, tt.args...)
			cmd.Env = append(os.Environ(), "HOME="+home, "CLAUDE_CONFIG_DIR="+filepath.Join(home, ".claude"))
			cmd.Stdin = strings.NewReader(stdin)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("expected exit 0, got %v\n%s", err, out)
			}
			entries, err := os.ReadDir(tt.wantDir)
			if err != nil || len(entries) != 1 {
				t.Fatalf("expected one snapshot in %s, got %v (err %v)", tt.wantDir, entries, err)
			}
			backups, _ := filepath.Glob(registryPath + ".corrupt-*")
			if len(backups) != 0 {
				t.Fatalf("expected no .corrupt- backup, got %v", backups)
			}
			raw, _ := os.ReadFile(registryPath)
			if string(raw) != corrupt {
				t.Fatalf("registry was modified: %q", raw)
			}
		})
	}
}

func TestAccountsArchiveAndUnarchiveSubcommands(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ach")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}
	dir := t.TempDir()
	registryPath := filepath.Join(dir, "accounts.json")
	accountHome := filepath.Join(dir, ".claude")
	if _, err := registry.AddAccount(registryPath, "claude", accountHome, ""); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name         string
		args         []string
		wantArchived bool
		wantFail     bool
	}{
		{"archive", []string{"accounts", "archive", "--registry", registryPath, "--id", "claude-1"}, true, false},
		{"unarchive", []string{"accounts", "unarchive", "--registry", registryPath, "--id", "claude-1"}, false, false},
		{"missing id", []string{"accounts", "archive", "--registry", registryPath}, false, true},
		{"unknown id", []string{"accounts", "archive", "--registry", registryPath, "--id", "claude-9"}, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := exec.Command(binary, tt.args...).CombinedOutput()
			if tt.wantFail != (err != nil) {
				t.Fatalf("wantFail=%v, got err=%v\n%s", tt.wantFail, err, out)
			}
			r, loadErr := registry.Load(registryPath)
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			if r.Accounts[0].Archived != tt.wantArchived {
				t.Fatalf("expected archived=%v, got %+v", tt.wantArchived, r.Accounts[0])
			}
			if _, statErr := os.Stat(accountHome); statErr != nil {
				t.Fatalf("the account directory must be untouched: %v", statErr)
			}
		})
	}
}

func TestUninstallSubcommand(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ach")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}
	home := t.TempDir()
	config := filepath.Join(home, "config")
	env := append(os.Environ(), "HOME="+home, "ACH_CONFIG_DIR="+config)
	seed := func(t *testing.T) {
		t.Helper()
		if err := os.RemoveAll(config); err != nil {
			t.Fatal(err)
		}
		if err := registry.Save(filepath.Join(config, "accounts.json"), registry.Empty()); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(config, "auth-cache.json"), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	listing := func(t *testing.T) []string {
		t.Helper()
		entries, err := os.ReadDir(config)
		if err != nil {
			return nil
		}
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		return names
	}

	tests := []struct {
		name      string
		args      []string
		lockDir   bool
		wantFail  bool
		wantNames []string
	}{
		{"dry-run changes nothing", []string{"uninstall"}, false, false, []string{"accounts.json", "auth-cache.json"}},
		{"extra positional arg is rejected", []string{"uninstall", "yes"}, false, true, []string{"accounts.json", "auth-cache.json"}},
		{"--yes removes the config dir", []string{"uninstall", "--yes"}, false, false, nil},
		{"a failed item exits 1", []string{"uninstall", "--yes"}, true, true, []string{"accounts.json", "auth-cache.json"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seed(t)
			if tt.lockDir {
				if err := os.Chmod(config, 0o500); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.Chmod(config, 0o755) })
			}
			cmd := exec.Command(binary, tt.args...)
			cmd.Env = env
			out, err := cmd.CombinedOutput()
			if tt.lockDir {
				os.Chmod(config, 0o755)
			}

			var exitErr *exec.ExitError
			if tt.wantFail {
				if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
					t.Fatalf("want exit 1, got %v\n%s", err, out)
				}
			} else if err != nil {
				t.Fatalf("want exit 0, got %v\n%s", err, out)
			}
			if got := listing(t); strings.Join(got, ",") != strings.Join(tt.wantNames, ",") {
				t.Fatalf("config dir = %v, want %v\n%s", got, tt.wantNames, out)
			}
			if _, err := os.Stat(binary); err != nil {
				t.Fatalf("the temp-dir binary was deleted: %v", err)
			}
		})
	}
}

func TestUninstallRefusalExitsWithoutCount(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ach")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}
	home := t.TempDir()
	cmd := exec.Command(binary, "uninstall", "--yes", "--registry", filepath.Join(home, "accounts.json"))
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.CombinedOutput()

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("want exit 1, got %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "拒絕執行") {
		t.Errorf("refusal not explained:\n%s", out)
	}
	if strings.Contains(string(out), "Error:") {
		t.Errorf("refusal printed a failure count:\n%s", out)
	}
}
