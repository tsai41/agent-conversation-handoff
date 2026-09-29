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

// isDefaultClaudeHome reports whether home is the user's default ~/.claude,
// which Claude reads without CLAUDE_CONFIG_DIR.
func isDefaultClaudeHome(home string) (bool, error) {
	userHome, err := os.UserHomeDir()
	if err != nil {
		return false, err
	}
	defaultHome, err := filepath.Abs(filepath.Join(userHome, ".claude"))
	if err != nil {
		return false, err
	}
	return home == defaultHome, nil
}

func environment(account registry.Account) ([]string, error) {
	env := os.Environ()
	home, err := filepath.Abs(account.Home)
	if err != nil {
		return nil, err
	}
	if account.Provider == "claude" {
		isDefault, err := isDefaultClaudeHome(home)
		if err != nil {
			return nil, err
		}
		if isDefault {
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

// shellLaunchArgs invokes the provider command through the user's login zsh.
// This preserves project-specific shell functions such as wrappers that add
// repository context before calling the real provider CLI. The selected
// account environment is restored after shell startup files have run, since
// they may change it.
func shellLaunchArgs(providerCommand string, args ...string) []string {
	const launchScript = `builtin cd -- "$ACH_LAUNCH_CWD" || builtin exit
if [[ "$ACH_LAUNCH_PROVIDER" == "claude" ]]; then
  if [[ "$ACH_LAUNCH_DEFAULT_CLAUDE_HOME" == "1" ]]; then
    builtin unset CLAUDE_CONFIG_DIR
  else
    builtin export CLAUDE_CONFIG_DIR="$ACH_LAUNCH_HOME"
  fi
else
  builtin export CODEX_HOME="$ACH_LAUNCH_HOME"
fi
builtin unset ACH_LAUNCH_CWD ACH_LAUNCH_PROVIDER ACH_LAUNCH_HOME ACH_LAUNCH_DEFAULT_CLAUDE_HOME
"$@"`
	return append([]string{"zsh", "-lic", launchScript, "ccs-launch", providerCommand}, args...)
}

// launchDir returns dir when it is an existing directory, else the current
// directory.
func launchDir(dir string) (string, error) {
	if dir != "" {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return filepath.Abs(dir)
		}
		fmt.Fprintf(os.Stderr, "recorded directory %s is gone; resuming from current directory\n", dir)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("current directory unavailable: %w", err)
	}
	return cwd, nil
}

func launchThroughShell(account registry.Account, dir string, args ...string) error {
	env, err := environment(account)
	if err != nil {
		return err
	}
	cwd, err := launchDir(dir)
	if err != nil {
		return err
	}
	home, err := filepath.Abs(account.Home)
	if err != nil {
		return err
	}
	defaultClaudeHome := "0"
	if account.Provider == "claude" {
		isDefault, err := isDefaultClaudeHome(home)
		if err != nil {
			return err
		}
		if isDefault {
			defaultClaudeHome = "1"
		}
	}
	env = setEnv(env, "ACH_LAUNCH_CWD", cwd)
	env = setEnv(env, "ACH_LAUNCH_PROVIDER", account.Provider)
	env = setEnv(env, "ACH_LAUNCH_HOME", home)
	env = setEnv(env, "ACH_LAUNCH_DEFAULT_CLAUDE_HOME", defaultClaudeHome)
	providerCommand := command(account)
	if _, err := exec.LookPath(providerCommand); err != nil {
		return err
	}
	shell, err := exec.LookPath("zsh")
	if err != nil {
		return fmt.Errorf("zsh is required: %w", err)
	}
	fmt.Println(launchBanner(account))
	return syscall.Exec(shell, shellLaunchArgs(providerCommand, args...), env)
}

// Launch replaces the current process with the provider CLI, passing prompt
// as its single argument (used to point a freshly-handed-off session at its
// transcript).
func Launch(account registry.Account, prompt string) error {
	return launchThroughShell(account, "", prompt)
}

// LaunchSession replaces the current process with the provider CLI, no
// arguments (used to just open the account's normal interactive session).
func LaunchSession(account registry.Account) error {
	return launchThroughShell(account, "")
}

// ResumeSession replaces the current process with the provider CLI resuming
// the named session for account. Claude refuses to resume a conversation
// outside the directory it was recorded in, so dir is where to launch; an empty
// or missing dir falls back to the current directory.
func ResumeSession(account registry.Account, sessionID, dir string) error {
	if sessionID == "" {
		return fmt.Errorf("session id is required")
	}
	args := []string{"--resume", sessionID}
	if account.Provider == "codex" {
		args = []string{"resume", sessionID}
	}
	return launchThroughShell(account, dir, args...)
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
