// Package provider execs into the claude/codex CLIs with the right
// per-account environment (CLAUDE_CONFIG_DIR / CODEX_HOME).
//
// Unlike the old bash+python ach, this binary is the process entrypoint
// itself, so there is no heredoc-diverted stdin to restore before exec:
// os.Stdin is always the real terminal already.
package provider

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/tsai41/agent-conversation-handoff/internal/registry"
)

func environment(account registry.Account) ([]string, error) {
	env := os.Environ()
	home, err := filepath.Abs(account.Home)
	if err != nil {
		return nil, err
	}
	if account.Provider == "claude" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		defaultHome, err := filepath.Abs(filepath.Join(userHome, ".claude"))
		if err != nil {
			return nil, err
		}
		if home == defaultHome {
			env = removeEnv(env, "CLAUDE_CONFIG_DIR")
		} else {
			env = setEnv(env, "CLAUDE_CONFIG_DIR", home)
		}
		return env, nil
	}
	return setEnv(env, "CODEX_HOME", home), nil
}

func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func removeEnv(env []string, key string) []string {
	prefix := key + "="
	result := env[:0]
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		result = append(result, entry)
	}
	return result
}

func command(account registry.Account) string {
	if account.Provider == "claude" {
		return "claude"
	}
	return "codex"
}

// Launch replaces the current process with the provider CLI, passing prompt
// as its single argument (used to point a freshly-handed-off session at its
// transcript).
func Launch(account registry.Account, prompt string) error {
	env, err := environment(account)
	if err != nil {
		return err
	}
	binary, err := exec.LookPath(command(account))
	if err != nil {
		return err
	}
	fmt.Println(launchBanner(account))
	return syscall.Exec(binary, []string{command(account), prompt}, env)
}

// LaunchSession replaces the current process with the provider CLI, no
// arguments (used to just open the account's normal interactive session).
func LaunchSession(account registry.Account) error {
	env, err := environment(account)
	if err != nil {
		return err
	}
	binary, err := exec.LookPath(command(account))
	if err != nil {
		return err
	}
	fmt.Println(launchBanner(account))
	return syscall.Exec(binary, []string{command(account)}, env)
}

// launchBanner is the one line left in the terminal's scrollback above a
// session, so that after the CLI exits it is still visible which account
// the session ran as. The home is included because number and alias can
// both be reassigned, the directory cannot.
func launchBanner(account registry.Account) string {
	label := fmt.Sprintf("%s #%d", registry.ProviderNames[account.Provider], account.Number)
	if account.Alias != "" {
		label += " " + account.Alias
	}
	return fmt.Sprintf("ccs 啟動帳號：%s（%s）", label, account.Home)
}

// Login replaces the current process with the provider CLI's login flow.
func Login(account registry.Account) error {
	env, err := environment(account)
	if err != nil {
		return err
	}
	binary, err := exec.LookPath(command(account))
	if err != nil {
		return err
	}
	var args []string
	if account.Provider == "claude" {
		args = []string{"claude", "auth", "login"}
	} else {
		args = []string{"codex", "login"}
	}
	return syscall.Exec(binary, args, env)
}

// AuthStatus reports whether account is currently authenticated, by
// shelling out to the provider CLI's own status command with a 15s timeout.
func AuthStatus(account registry.Account) (bool, error) {
	if _, err := exec.LookPath(command(account)); err != nil {
		return false, err
	}
	env, err := environment(account)
	if err != nil {
		return false, err
	}
	var args []string
	if account.Provider == "claude" {
		args = []string{"auth", "status"}
	} else {
		args = []string{"login", "status"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, command(account), args...)
	cmd.Env = env
	err = cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return false, nil
	}
	return err == nil, nil
}
