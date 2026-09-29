package provider

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tsai41/agent-conversation-handoff/internal/registry"
)

func TestSetEnvOverwritesExistingKeyEvenWhenValueIsEmpty(t *testing.T) {
	env := []string{"CLAUDE_CONFIG_DIR=", "PATH=/bin"}
	got := setEnv(env, "CLAUDE_CONFIG_DIR", "/home/x/.claude-2")
	want := []string{"CLAUDE_CONFIG_DIR=/home/x/.claude-2", "PATH=/bin"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestSetEnvAppendsWhenKeyAbsent(t *testing.T) {
	env := []string{"PATH=/bin"}
	got := setEnv(env, "CODEX_HOME", "/home/x/.codex")
	want := []string{"PATH=/bin", "CODEX_HOME=/home/x/.codex"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestRemoveEnvDropsEmptyValueEntryToo(t *testing.T) {
	env := []string{"CLAUDE_CONFIG_DIR=", "PATH=/bin"}
	got := removeEnv(env, "CLAUDE_CONFIG_DIR")
	want := []string{"PATH=/bin"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestSetEnvDoesNotMatchAKeyThatIsAPrefixOfAnother(t *testing.T) {
	env := []string{"CLAUDE_CONFIG_DIR_2=/other"}
	got := setEnv(env, "CLAUDE_CONFIG_DIR", "/home/x/.claude")
	want := []string{"CLAUDE_CONFIG_DIR_2=/other", "CLAUDE_CONFIG_DIR=/home/x/.claude"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestLaunchBannerNamesProviderNumberAliasAndHome(t *testing.T) {
	got := launchBanner(registry.Account{ID: "claude-4", Provider: "claude", Number: 4, Alias: "work", Home: "/tmp/home-4"})
	want := "ach 啟動帳號：Claude #4 work（/tmp/home-4）"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestLaunchBannerOmitsAnEmptyAlias(t *testing.T) {
	got := launchBanner(registry.Account{ID: "codex-1", Provider: "codex", Number: 1, Home: "/tmp/codex"})
	if strings.Contains(got, "  ") || !strings.Contains(got, "#1（") {
		t.Fatalf("expected no alias gap, got %q", got)
	}
}

func TestShellLaunchArgsInvokeTheConfiguredCommandThroughZsh(t *testing.T) {
	got := shellLaunchArgs("claude", "--resume", "session-id")
	if len(got) != 7 || !reflect.DeepEqual(got[:2], []string{"zsh", "-lic"}) ||
		!reflect.DeepEqual(got[3:], []string{"ach-launch", "claude", "--resume", "session-id"}) {
		t.Fatalf("unexpected argv shape: %q", got)
	}
}

func TestLaunchScriptRestoresTheAccountEnvironmentAfterStartup(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh is not installed")
	}
	work := t.TempDir()
	launch := t.TempDir()
	report := `print -r -- "pwd=$PWD"; print -r -- "claude=${CLAUDE_CONFIG_DIR-<unset>}"; print -r -- "codex=${CODEX_HOME-<unset>}"; env | grep '^ACH_LAUNCH' || true`
	tests := []struct {
		name string
		env  []string
		want []string
	}{
		{
			name: "claude custom home",
			env:  []string{"ACH_LAUNCH_PROVIDER=claude", "ACH_LAUNCH_HOME=/accounts/two", "ACH_LAUNCH_DEFAULT_CLAUDE_HOME=0"},
			want: []string{"claude=/accounts/two"},
		},
		{
			name: "claude default home unsets the variable",
			env:  []string{"ACH_LAUNCH_PROVIDER=claude", "ACH_LAUNCH_HOME=/home/x/.claude", "ACH_LAUNCH_DEFAULT_CLAUDE_HOME=1"},
			want: []string{"claude=<unset>"},
		},
		{
			name: "codex home",
			env:  []string{"ACH_LAUNCH_PROVIDER=codex", "ACH_LAUNCH_HOME=/accounts/codex", "ACH_LAUNCH_DEFAULT_CLAUDE_HOME=0"},
			want: []string{"codex=/accounts/codex"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := shellLaunchArgs("zsh", "-f", "-c", report)
			cmd := exec.Command(zsh, append([]string{"-f", "-c"}, args[2:]...)...)
			cmd.Env = append(os.Environ(), tt.env...)
			cmd.Env = append(cmd.Env, "ACH_LAUNCH_CWD="+work, "CLAUDE_CONFIG_DIR=/wrong", "CODEX_HOME=/wrong")
			cmd.Dir = launch
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("launch script failed: %v\n%s", err, out)
			}
			for _, want := range append(tt.want, "pwd="+work) {
				if !strings.Contains(string(out), want+"\n") {
					t.Fatalf("expected %q in output:\n%s", want, out)
				}
			}
			if strings.Contains(string(out), "ACH_LAUNCH") {
				t.Fatalf("ACH_LAUNCH vars leaked:\n%s", out)
			}
		})
	}
}

func TestLaunchScriptSurvivesStartupFilesThatChangeDirectoryAndEnvironment(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh is not installed")
	}
	zdotdir := t.TempDir()
	rc := "export CLAUDE_CONFIG_DIR=/wrong\nexport CODEX_HOME=/wrong\ncd /\n"
	for _, name := range []string{".zprofile", ".zshrc"} {
		if err := os.WriteFile(filepath.Join(zdotdir, name), []byte(rc), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	work := t.TempDir()
	report := `print -r -- "pwd=$PWD"; print -r -- "claude=${CLAUDE_CONFIG_DIR-<unset>}"; print -r -- "codex=${CODEX_HOME-<unset>}"`
	tests := []struct {
		name string
		env  []string
		want []string
	}{
		{
			name: "claude custom home",
			env:  []string{"ACH_LAUNCH_PROVIDER=claude", "ACH_LAUNCH_HOME=/accounts/two", "ACH_LAUNCH_DEFAULT_CLAUDE_HOME=0"},
			want: []string{"claude=/accounts/two"},
		},
		{
			name: "claude default home unsets the variable",
			env:  []string{"ACH_LAUNCH_PROVIDER=claude", "ACH_LAUNCH_HOME=/home/x/.claude", "ACH_LAUNCH_DEFAULT_CLAUDE_HOME=1"},
			want: []string{"claude=<unset>"},
		},
		{
			name: "codex home",
			env:  []string{"ACH_LAUNCH_PROVIDER=codex", "ACH_LAUNCH_HOME=/accounts/codex", "ACH_LAUNCH_DEFAULT_CLAUDE_HOME=0"},
			want: []string{"codex=/accounts/codex"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := shellLaunchArgs("zsh", "-f", "-c", report)
			cmd := exec.Command(zsh, args[1:]...)
			cmd.Env = append(os.Environ(), tt.env...)
			cmd.Env = append(cmd.Env, "ZDOTDIR="+zdotdir, "ACH_LAUNCH_CWD="+work)
			cmd.Dir = t.TempDir()
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("launch script failed: %v\n%s", err, out)
			}
			for _, want := range append(tt.want, "pwd="+work) {
				if !strings.Contains(string(out), want+"\n") {
					t.Fatalf("expected %q in output:\n%s", want, out)
				}
			}
		})
	}
}

func TestLaunchDirFallsBackToTheCurrentDirectory(t *testing.T) {
	existing := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		dir  string
		want string
	}{
		{name: "existing directory", dir: existing, want: existing},
		{name: "empty", dir: "", want: cwd},
		{name: "missing", dir: filepath.Join(existing, "gone"), want: cwd},
		{name: "a file", dir: os.Args[0], want: cwd},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := launchDir(tt.dir)
			if err != nil || got != tt.want {
				t.Fatalf("got %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}
