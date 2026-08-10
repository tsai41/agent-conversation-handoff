// Package menu drives the interactive fzf-based flows: the top-level
// account/action picker, account settings, and the handoff wizard.
package menu

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tsai41/agent-conversation-handoff/internal/handoff"
	"github.com/tsai41/agent-conversation-handoff/internal/provider"
	"github.com/tsai41/agent-conversation-handoff/internal/registry"
	"github.com/tsai41/agent-conversation-handoff/internal/session"
)

type kv struct{ Key, Label string }

// pickKey runs fzf over candidates ("key\tlabel" rows, label-only visible)
// and returns the chosen key. When numbered is true, candidates are
// prefixed "1. ", "2. ", ... and fzf is given digit-key shortcuts
// (pos(N)+accept) so a single keypress selects and accepts, up to 9 items.
func pickKey(candidates []kv, prompt string, numbered bool) (string, error) {
	if _, err := exec.LookPath("fzf"); err != nil {
		return "", fmt.Errorf("fzf is required")
	}
	if numbered {
		numberedCandidates := make([]kv, len(candidates))
		for i, c := range candidates {
			numberedCandidates[i] = kv{c.Key, fmt.Sprintf("%d. %s", i+1, c.Label)}
		}
		candidates = numberedCandidates
	}
	var rows strings.Builder
	for _, c := range candidates {
		rows.WriteString(c.Key)
		rows.WriteByte('\t')
		rows.WriteString(c.Label)
		rows.WriteByte('\n')
	}
	args := []string{"--height=~15", "--border=none", "--with-nth=2..", "--delimiter=\t", "--prompt=" + prompt}
	if numbered {
		shortcuts := len(candidates)
		if shortcuts > 9 {
			shortcuts = 9
		}
		var binds []string
		for n := 1; n <= shortcuts; n++ {
			binds = append(binds, fmt.Sprintf("%d:pos(%d)+accept", n, n))
		}
		args = append(args, "--bind="+strings.Join(binds, ","))
	}
	cmd := exec.Command("fzf", args...)
	cmd.Stdin = strings.NewReader(rows.String())
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("selection cancelled")
	}
	selected := strings.SplitN(strings.TrimSpace(string(out)), "\t", 2)[0]
	if selected == "" {
		return "", fmt.Errorf("nothing selected")
	}
	return selected, nil
}

func pickSession(candidates []session.Candidate, prompt string) (string, error) {
	if _, err := exec.LookPath("fzf"); err != nil {
		return "", fmt.Errorf("fzf is required")
	}
	var rows strings.Builder
	for _, c := range candidates {
		rows.WriteString(c.Path)
		rows.WriteByte('\t')
		rows.WriteString(c.Description)
		rows.WriteByte('\n')
	}
	cmd := exec.Command("fzf", "--height=~15", "--border=none", "--with-nth=2..", "--delimiter=\t", "--prompt="+prompt)
	cmd.Stdin = strings.NewReader(rows.String())
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("conversation selection cancelled")
	}
	selected := strings.SplitN(strings.TrimSpace(string(out)), "\t", 2)[0]
	if selected == "" {
		return "", fmt.Errorf("no conversation selected")
	}
	return selected, nil
}

// stdinReader is shared: a fresh bufio.Reader per prompt would keep
// whatever it read past the newline, losing later lines when input is piped
// rather than typed.
var stdinReader = bufio.NewReader(os.Stdin)

func readLine(prompt string) string {
	fmt.Print(prompt)
	line, _ := stdinReader.ReadString('\n')
	return strings.TrimSpace(line)
}

func readOptionalAlias(prompt string) string {
	if prompt == "" {
		prompt = "alias（選填）: "
	}
	return readLine(prompt)
}

func providerCommand(providerName string) string {
	if providerName == "codex" {
		return "codex"
	}
	return "claude"
}

func cliInstalled(account registry.Account) bool {
	_, err := exec.LookPath(providerCommand(account.Provider))
	return err == nil
}

func sessionsForAccount(account registry.Account, project string) ([]session.Candidate, error) {
	if account.Provider == "claude" {
		return session.ClaudeCandidates(account.Home, project)
	}
	return session.CodexCandidates(account.Home, project)
}

// RegistryHandoff verifies source != target and target is authenticated,
// snapshots the source session into project, and (if launch) execs into
// the target account pointed at the new artifact.
func RegistryHandoff(registryPath, sourceID, targetID, sessionPath, project string, launch bool) error {
	r, err := registry.Load(registryPath)
	if err != nil {
		return err
	}
	source, err := registry.FindAccount(r, sourceID)
	if err != nil {
		return err
	}
	target, err := registry.FindAccount(r, targetID)
	if err != nil {
		return err
	}
	if sourceID == targetID {
		return fmt.Errorf("source and target accounts must be different")
	}
	authenticated, err := provider.CachedAuthStatus(registryPath, target, func() {
		fmt.Println("驗證目標帳號登入狀態...")
	})
	if err != nil || !authenticated {
		return fmt.Errorf("target account is not authenticated: %s", targetID)
	}
	artifact, err := handoff.Create(sessionPath, project, source.Provider)
	if err != nil {
		return err
	}
	fmt.Println(artifact)
	if launch {
		prompt := fmt.Sprintf(
			"Read %s first. Use %s for complete history, then continue the user's work in this project.",
			filepath.Join(artifact, "transcript.md"), filepath.Join(artifact, "source.jsonl"),
		)
		return provider.Launch(target, prompt)
	}
	return nil
}

// manualIDRow is the sentinel path returned by pickSession when the user
// chooses to type an id instead of picking a listed conversation. The
// picker only ever lists real conversations for the current project, and
// only the five newest of those, so a conversation the user knows the id of
// is regularly absent from it -- the row exists so that is not a dead end.
const manualIDRow = "action:manual-id"

func interactiveRegistryHandoff(registryPath, project string) error {
	r, err := registry.Load(registryPath)
	if err != nil {
		return err
	}
	labels := map[string]string{}
	for _, row := range registry.Rows(r) {
		labels[row.ID] = row.Label
	}

	sessionsByAccount := map[string][]session.Candidate{}
	for _, account := range r.Accounts {
		sessions, err := sessionsForAccount(account, project)
		if err != nil {
			continue
		}
		sessionsByAccount[account.ID] = sessions
	}

	sourceIDs := make([]string, 0, len(r.Accounts))
	for _, account := range r.Accounts {
		sourceIDs = append(sourceIDs, account.ID)
	}
	sort.Strings(sourceIDs)

	var directions []kv
	for _, sourceID := range sourceIDs {
		for _, account := range r.Accounts {
			targetID := account.ID
			if targetID == sourceID || !cliInstalled(account) {
				continue
			}
			directions = append(directions, kv{
				sourceID + "|" + targetID,
				fmt.Sprintf("「%s」→「%s」", labels[sourceID], labels[targetID]),
			})
		}
	}
	if len(directions) == 0 {
		return fmt.Errorf("no different target account is available")
	}
	chosen, err := pickKey(directions, "接力方向: ", false)
	if err != nil {
		return err
	}
	parts := strings.SplitN(chosen, "|", 2)
	sourceID, targetID := parts[0], parts[1]

	sourceSessions := sessionsByAccount[sourceID]
	candidates := make([]session.Candidate, 0, len(sourceSessions)+1)
	candidates = append(candidates, sourceSessions...)
	candidates = append(candidates, session.Candidate{Path: manualIDRow, Description: "✎ 手動輸入對話 ID"})
	sessionPath, err := pickSession(candidates, fmt.Sprintf("選擇「%s」要接力的對話: ", labels[sourceID]))
	if err != nil {
		return err
	}
	if sessionPath == manualIDRow {
		return manualIDHandoff(registryPath, r, labels, targetID, project)
	}
	return RegistryHandoff(registryPath, sourceID, targetID, sessionPath, project, true)
}

// QuickHandoff finds a Claude conversation by id fragment and hands it to a
// Codex account. When exactly one Codex account is available, no picker is
// opened; multiple Codex accounts are resolved with one target picker.
func QuickHandoff(registryPath, fragment, project string) error {
	r, err := registry.Load(registryPath)
	if err != nil {
		return err
	}
	labels := map[string]string{}
	for _, row := range registry.Rows(r) {
		labels[row.ID] = row.Label
	}

	targetID, err := chooseCodexTarget(r, labels)
	if err != nil {
		return err
	}

	type hit struct {
		accountID string
		match     session.Match
	}
	var hits []hit
	for _, account := range r.Accounts {
		if account.Provider != "claude" {
			continue
		}
		matches, findErr := session.FindClaudeByID(account.Home, fragment)
		if findErr != nil {
			continue
		}
		for _, match := range matches {
			hits = append(hits, hit{account.ID, match})
		}
	}
	if len(hits) == 0 {
		return fmt.Errorf("no Claude conversation matches id: %s", fragment)
	}

	chosen := hits[0]
	if len(hits) > 1 {
		byPath := map[string]hit{}
		candidates := make([]session.Candidate, len(hits))
		for i, h := range hits {
			candidates[i] = session.Candidate{
				Path:        h.match.Path,
				Description: fmt.Sprintf("%s  %s  %s", labels[h.accountID], h.match.Description, h.match.CWD),
			}
			byPath[h.match.Path] = h
		}
		path, pickErr := pickSession(candidates, "多筆符合，選擇要接力的對話: ")
		if pickErr != nil {
			return pickErr
		}
		chosen = byPath[path]
	}

	return finishManualHandoff(registryPath, chosen.accountID, targetID, labels, chosen.match, project)
}

func chooseCodexTarget(r registry.Registry, labels map[string]string) (string, error) {
	var candidates []kv
	for _, account := range r.Accounts {
		if account.Provider == "codex" && cliInstalled(account) {
			candidates = append(candidates, kv{account.ID, labels[account.ID]})
		}
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("no different target account is available")
	}
	if len(candidates) == 1 {
		return candidates[0].Key, nil
	}
	return pickKey(candidates, "選擇接手的 Codex 帳號: ", false)
}

// manualIDHandoff resolves a conversation id typed by the user against every
// registered account, so the source account comes from wherever the id was
// actually found rather than from the direction picked beforehand.
func manualIDHandoff(registryPath string, r registry.Registry, labels map[string]string, targetID, project string) error {
	fragment := readLine("對話 ID（可只輸入前綴）: ")
	if fragment == "" {
		return fmt.Errorf("no conversation id entered")
	}

	type hit struct {
		accountID string
		match     session.Match
	}
	var hits []hit
	var lookupErrors []string
	for _, account := range r.Accounts {
		var matches []session.Match
		var err error
		if account.Provider == "claude" {
			matches, err = session.FindClaudeByID(account.Home, fragment)
		} else {
			matches, err = session.FindCodexByID(account.Home, fragment)
		}
		if err != nil {
			// Keep searching the other accounts, but remember why this one
			// produced nothing: "not found" and "could not look" must not
			// reach the user as the same message.
			lookupErrors = append(lookupErrors, fmt.Sprintf("%s: %s", labels[account.ID], err))
			continue
		}
		for _, match := range matches {
			hits = append(hits, hit{account.ID, match})
		}
	}
	if len(hits) == 0 {
		if len(lookupErrors) > 0 {
			return fmt.Errorf("no conversation matches id %s; some accounts could not be searched (%s)", fragment, strings.Join(lookupErrors, "; "))
		}
		return fmt.Errorf("no conversation matches id: %s", fragment)
	}

	chosen := hits[0]
	if len(hits) > 1 {
		sort.SliceStable(hits, func(i, j int) bool { return hits[i].match.StartTime.After(hits[j].match.StartTime) })
		byPath := map[string]hit{}
		candidates := make([]session.Candidate, len(hits))
		for i, h := range hits {
			candidates[i] = session.Candidate{Path: h.match.Path, Description: fmt.Sprintf("%s  %s  %s", labels[h.accountID], h.match.Description, h.match.CWD)}
			byPath[h.match.Path] = h
		}
		path, err := pickSession(candidates, "多筆符合，選擇要接力的對話: ")
		if err != nil {
			return err
		}
		chosen = byPath[path]
	}

	sourceID := chosen.accountID
	if sourceID == targetID {
		replacement, err := chooseTargetExcluding(r, labels, sourceID)
		if err != nil {
			return err
		}
		targetID = replacement
	}
	return finishManualHandoff(registryPath, sourceID, targetID, labels, chosen.match, project)
}

func finishManualHandoff(registryPath, sourceID, targetID string, labels map[string]string, match session.Match, project string) error {
	// The direction picked before the id was typed may not be the direction
	// being used: the source account is wherever the id turned up.
	fmt.Printf("接力方向：「%s」→「%s」\n", labels[sourceID], labels[targetID])
	if match.CWD != "" {
		if resolved := resolvePath(project); match.CWD != resolved {
			fmt.Printf("注意：這個對話原本在 %s 進行，交接資料會建立在 %s。\n", match.CWD, resolved)
		}
	}
	return RegistryHandoff(registryPath, sourceID, targetID, match.Path, project, true)
}

// resolvePath follows symlinks so a project reached through one isn't
// reported as a different project than the cwd the provider recorded.
func resolvePath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	if absolute, err := filepath.Abs(path); err == nil {
		return absolute
	}
	return path
}

func chooseTargetExcluding(r registry.Registry, labels map[string]string, sourceID string) (string, error) {
	var candidates []kv
	for _, account := range r.Accounts {
		if account.ID == sourceID || !cliInstalled(account) {
			continue
		}
		candidates = append(candidates, kv{account.ID, labels[account.ID]})
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("no different target account is available")
	}
	return pickKey(candidates, "這個對話屬於原本選定的目標帳號，改選接手帳號: ", false)
}

func bootstrapRegistry(registryPath string) error {
	candidates, err := registry.Discover(registryPath)
	if err != nil {
		return err
	}
	r := registry.Empty()
	for _, candidate := range candidates {
		providerName := registry.ProviderNames[candidate.Provider]
		choice, err := pickKey([]kv{
			{"import", fmt.Sprintf("匯入 %s: %s", providerName, candidate.Home)},
			{"skip", "略過這個目錄"},
		}, "偵測到既有帳號目錄: ", false)
		if err != nil {
			return err
		}
		if choice == "import" {
			alias := readOptionalAlias(fmt.Sprintf("%s alias（選填）: ", providerName))
			if _, err := registry.Register(&r, candidate.Provider, candidate.Home, alias, false); err != nil {
				return err
			}
		}
	}
	if _, statErr := os.Stat(registryPath); os.IsNotExist(statErr) {
		return registry.WithLock(registryPath, func() error {
			if _, statErr := os.Stat(registryPath); os.IsNotExist(statErr) {
				return registry.Save(registryPath, r)
			}
			return nil
		})
	}
	return nil
}

func chooseRegisteredAccount(r registry.Registry, prompt string) (string, error) {
	labels := map[string]string{}
	for _, row := range registry.Rows(r) {
		labels[row.ID] = row.Label
	}
	candidates := make([]kv, 0, len(r.Accounts))
	for _, account := range r.Accounts {
		candidates = append(candidates, kv{account.ID, labels[account.ID]})
	}
	return pickKey(candidates, prompt, false)
}

func manageAccounts(registryPath string) error {
	for {
		action, err := pickKey([]kv{
			{"add", "新增帳號"},
			{"rename", "修改 alias"},
			{"login", "登入／重新登入"},
			{"remove", "從 ccs 移除帳號"},
			{"import", "匯入既有帳號目錄"},
			{"back", "返回主選單"},
		}, "帳號設定: ", false)
		if err != nil {
			return err
		}
		switch action {
		case "back":
			return nil
		case "add":
			var providers []kv
			for _, entry := range []struct{ id, cmd string }{{"claude", "claude"}, {"codex", "codex"}} {
				if _, err := exec.LookPath(entry.cmd); err == nil {
					providers = append(providers, kv{entry.id, registry.ProviderNames[entry.id]})
				}
			}
			if len(providers) == 0 {
				return fmt.Errorf("no provider CLI is installed")
			}
			chosenProvider, err := pickKey(providers, "選擇 provider: ", false)
			if err != nil {
				return err
			}
			accountHome, err := registry.SuggestAccountHome(registryPath, chosenProvider)
			if err != nil {
				return err
			}
			confirmation, err := pickKey([]kv{
				{"confirm", fmt.Sprintf("建立帳號目錄: %s", accountHome)},
				{"cancel", "取消"},
			}, "確認新增帳號: ", false)
			if err != nil {
				return err
			}
			if confirmation == "confirm" {
				alias := readOptionalAlias("")
				account, err := registry.AddAccount(registryPath, chosenProvider, accountHome, alias)
				if err != nil {
					return err
				}
				return provider.Login(account)
			}
		case "rename":
			r, err := registry.Load(registryPath)
			if err != nil {
				return err
			}
			accountID, err := chooseRegisteredAccount(r, "選擇要修改 alias 的帳號: ")
			if err != nil {
				return err
			}
			alias := readOptionalAlias("新的 alias（留空即清除）: ")
			if err := registry.RenameAccount(registryPath, accountID, alias); err != nil {
				return err
			}
		case "login":
			r, err := registry.Load(registryPath)
			if err != nil {
				return err
			}
			accountID, err := chooseRegisteredAccount(r, "選擇要登入的帳號: ")
			if err != nil {
				return err
			}
			account, err := registry.FindAccount(r, accountID)
			if err != nil {
				return err
			}
			return provider.Login(account)
		case "import":
			candidates, err := registry.Discover(registryPath)
			if err != nil {
				return err
			}
			if len(candidates) == 0 {
				fmt.Println("沒有可匯入的既有帳號目錄。")
				continue
			}
			options := make([]kv, len(candidates))
			for i, c := range candidates {
				options[i] = kv{c.Provider + "|" + c.Home, fmt.Sprintf("%s: %s", registry.ProviderNames[c.Provider], c.Home)}
			}
			selected, err := pickKey(options, "選擇要匯入的帳號目錄: ", false)
			if err != nil {
				return err
			}
			parts := strings.SplitN(selected, "|", 2)
			alias := readOptionalAlias("")
			if _, err := registry.AddAccount(registryPath, parts[0], parts[1], alias); err != nil {
				return err
			}
		case "remove":
			r, err := registry.Load(registryPath)
			if err != nil {
				return err
			}
			accountID, err := chooseRegisteredAccount(r, "選擇要取消登記的帳號: ")
			if err != nil {
				return err
			}
			confirmation, err := pickKey([]kv{
				{"confirm", "只從 ccs 移除，保留所有帳號資料"},
				{"cancel", "取消"},
			}, "確認取消登記: ", false)
			if err != nil {
				return err
			}
			if confirmation == "confirm" {
				if err := registry.RemoveAccount(registryPath, accountID); err != nil {
					return err
				}
			}
		}
	}
}

// Run drives the top-level menu loop until an account is launched (which
// execs and never returns) or the handoff/accounts flow returns.
func Run(registryPath string) error {
	if _, err := os.Stat(registryPath); os.IsNotExist(err) {
		if err := bootstrapRegistry(registryPath); err != nil {
			return err
		}
	}
	for {
		r, err := registry.Load(registryPath)
		if err != nil {
			return err
		}
		labels := map[string]string{}
		for _, row := range registry.Rows(r) {
			labels[row.ID] = row.Label
		}
		var candidates []kv
		for _, account := range r.Accounts {
			if cliInstalled(account) {
				candidates = append(candidates, kv{account.ID, labels[account.ID]})
			}
		}
		candidates = append(candidates, kv{"action:handoff", "接手既有對話"}, kv{"action:accounts", "帳號設定"})
		selected, err := pickKey(candidates, "選擇帳號或功能: ", true)
		if err != nil {
			return err
		}
		switch selected {
		case "action:handoff":
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			return interactiveRegistryHandoff(registryPath, cwd)
		case "action:accounts":
			if err := manageAccounts(registryPath); err != nil {
				return err
			}
			continue
		default:
			account, err := registry.FindAccount(r, selected)
			if err != nil {
				return err
			}
			return provider.LaunchSession(account)
		}
	}
}
