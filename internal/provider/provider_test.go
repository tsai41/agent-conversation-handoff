package provider

import (
	"reflect"
	"testing"
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
