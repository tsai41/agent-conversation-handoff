package provider

import (
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
	got := launchBanner(registry.Account{ID: "claude-4", Provider: "claude", Number: 4, Alias: "orli", Home: "/tmp/home-4"})
	want := "ccs 啟動帳號：Claude #4 orli（/tmp/home-4）"
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
	want := []string{"zsh", "-lic", `"$@"`, "ccs-launch", "claude", "--resume", "session-id"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}
