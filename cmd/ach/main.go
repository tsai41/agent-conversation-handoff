// Command ach manages provider accounts and immutable conversation
// handoffs between Claude Code and Codex accounts.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tsai41/agent-conversation-handoff/internal/handoff"
	"github.com/tsai41/agent-conversation-handoff/internal/menu"
	"github.com/tsai41/agent-conversation-handoff/internal/provider"
	"github.com/tsai41/agent-conversation-handoff/internal/registry"
	"github.com/tsai41/agent-conversation-handoff/internal/usage"
)

func defaultRegistryPath() string {
	if dir := os.Getenv("ACH_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "accounts.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".config", "agent-conversation-handoff", "accounts.json")
}

func defaultUsageDir() string {
	if dir := os.Getenv("ACH_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "usage")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".config", "agent-conversation-handoff", "usage")
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "Error: %s\n", err)
	os.Exit(1)
}

func main() {
	// A bare `ach` invocation (no subcommand) opens the menu, same
	// as the old bash launcher used to always run `ach menu --registry ...`.
	command := "menu"
	args := []string{}
	if len(os.Args) >= 2 {
		command = os.Args[1]
		args = os.Args[2:]
	}

	switch command {
	case "h", "quick-handoff":
		fs := flag.NewFlagSet("handoff", flag.ExitOnError)
		registryPath := fs.String("registry", defaultRegistryPath(), "")
		fs.Parse(args)
		if len(fs.Args()) != 1 {
			fail(fmt.Errorf("usage: ach h [--registry path] <conversation-id>"))
		}
		cwd, err := os.Getwd()
		if err != nil {
			fail(err)
		}
		if err := menu.QuickHandoff(*registryPath, fs.Args()[0], cwd); err != nil {
			fail(err)
		}

	case "create":
		fs := flag.NewFlagSet("create", flag.ExitOnError)
		source := fs.String("source", "", "")
		target := fs.String("target", "", "")
		sourceType := fs.String("source-type", "claude", "")
		fs.Parse(args)
		if *source == "" || *target == "" {
			fail(fmt.Errorf("--source and --target are required"))
		}
		if *sourceType != "claude" && *sourceType != "codex" {
			fail(fmt.Errorf("--source-type must be claude or codex"))
		}
		destination, err := handoff.Create(*source, *target, *sourceType)
		if err != nil {
			fail(err)
		}
		fmt.Println(destination)

	case "menu":
		fs := flag.NewFlagSet("menu", flag.ExitOnError)
		registryPath := fs.String("registry", defaultRegistryPath(), "")
		usageDir := fs.String("usage-dir", defaultUsageDir(), "")
		fs.Parse(args)
		// fs.Visit only calls back for flags actually set on the command
		// line, which is how "the user passed --usage-dir" is told apart
		// from "the user passed nothing" even though usageDir's *string
		// already holds the default value in both cases.
		usageDirSet := false
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "usage-dir" {
				usageDirSet = true
			}
		})
		if err := menu.Run(*registryPath, *usageDir, usageDirSet); err != nil {
			fail(err)
		}

	case "handoff":
		fs := flag.NewFlagSet("handoff", flag.ExitOnError)
		registryPath := fs.String("registry", defaultRegistryPath(), "")
		sourceID := fs.String("source-id", "", "")
		targetID := fs.String("target-id", "", "")
		sessionPath := fs.String("session", "", "")
		noLaunch := fs.Bool("no-launch", false, "")
		fs.Parse(args)
		if *sourceID == "" || *targetID == "" || *sessionPath == "" {
			fail(fmt.Errorf("--source-id, --target-id, and --session are required"))
		}
		cwd, err := os.Getwd()
		if err != nil {
			fail(err)
		}
		if err := menu.RegistryHandoff(*registryPath, *sourceID, *targetID, *sessionPath, cwd, !*noLaunch); err != nil {
			fail(err)
		}

	case "usage":
		if len(args) < 1 || args[0] != "record" {
			fail(fmt.Errorf("usage: ach usage record [--usage-dir dir] < statusline.json"))
		}
		fs := flag.NewFlagSet("usage record", flag.ExitOnError)
		registryPath := fs.String("registry", defaultRegistryPath(), "")
		usageDir := fs.String("usage-dir", defaultUsageDir(), "")
		fs.Parse(args[1:])
		usageDirSet := false
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "usage-dir" {
				usageDirSet = true
			}
		})
		configDir := os.Getenv("CLAUDE_CONFIG_DIR")
		if configDir == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return
			}
			configDir = filepath.Join(home, ".claude")
		}
		dir := usage.ResolveRecordDir(*registryPath, *usageDir, usageDirSet)
		// Unusable input exits 0 inside Record so a status line is never
		// broken by it; only a failed write reaches here.
		if err := usage.Record(os.Stdin, dir, configDir, time.Now()); err != nil {
			fail(err)
		}

	case "accounts":
		if len(args) < 1 {
			fail(fmt.Errorf("accounts requires a subcommand"))
		}
		accountsCommand := args[0]
		rest := args[1:]
		fs := flag.NewFlagSet("accounts "+accountsCommand, flag.ExitOnError)
		registryPath := fs.String("registry", "", "")
		id := fs.String("id", "", "")
		providerName := fs.String("provider", "", "")
		home := fs.String("home", "", "")
		alias := fs.String("alias", "", "")
		fs.Parse(rest)
		if *registryPath == "" {
			fail(fmt.Errorf("--registry is required"))
		}

		switch accountsCommand {
		case "list":
			r, err := registry.Load(*registryPath)
			if err != nil {
				fail(err)
			}
			for _, row := range registry.Rows(r) {
				fmt.Printf("%s\t%s\n", row.ID, row.Label)
			}
		case "add":
			if *providerName == "" || *home == "" {
				fail(fmt.Errorf("--provider and --home are required"))
			}
			account, err := registry.AddAccount(*registryPath, *providerName, *home, *alias)
			if err != nil {
				fail(err)
			}
			fmt.Println(account.ID)
		case "remove":
			if *id == "" {
				fail(fmt.Errorf("--id is required"))
			}
			if err := registry.RemoveAccount(*registryPath, *id); err != nil {
				fail(err)
			}
		case "rename":
			if *id == "" {
				fail(fmt.Errorf("--id is required"))
			}
			if err := registry.RenameAccount(*registryPath, *id, *alias); err != nil {
				fail(err)
			}
		case "auth-status":
			if *id == "" {
				fail(fmt.Errorf("--id is required"))
			}
			r, err := registry.Load(*registryPath)
			if err != nil {
				fail(err)
			}
			account, err := registry.FindAccount(r, *id)
			if err != nil {
				fail(err)
			}
			authenticated, err := provider.AuthStatus(account)
			if err != nil {
				fail(err)
			}
			if authenticated {
				fmt.Println("authenticated")
				os.Exit(0)
			}
			fmt.Println("not-authenticated")
			os.Exit(1)
		case "discover":
			candidates, err := registry.Discover(*registryPath)
			if err != nil {
				fail(err)
			}
			for _, c := range candidates {
				fmt.Printf("%s\t%s\n", c.Provider, c.Home)
			}
		case "suggest-home":
			if *providerName == "" {
				fail(fmt.Errorf("--provider is required"))
			}
			suggestion, err := registry.SuggestAccountHome(*registryPath, *providerName)
			if err != nil {
				fail(err)
			}
			fmt.Println(suggestion)
		default:
			fail(fmt.Errorf("unknown accounts subcommand: %s", accountsCommand))
		}

	default:
		fail(fmt.Errorf("unknown command: %s", command))
	}
}
