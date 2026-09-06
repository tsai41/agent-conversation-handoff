// Package menu drives the interactive fzf-based flows: the top-level
// function picker, account settings, and the handoff wizard.
package menu

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tsai41/agent-conversation-handoff/internal/handoff"
	"github.com/tsai41/agent-conversation-handoff/internal/provider"
	"github.com/tsai41/agent-conversation-handoff/internal/registry"
	"github.com/tsai41/agent-conversation-handoff/internal/session"
	"github.com/tsai41/agent-conversation-handoff/internal/usage"
)

type kv struct{ Key, Label string }

// errCancelled marks a picker the user backed out of (ESC / CTRL-C) rather
// than one that failed. It propagates uncaught to Run, which restarts at
// the function level; Run's own root picker instead returns nil, so
// backing out of the root menu exits the program cleanly instead of
// erroring out.
var errCancelled = errors.New("selection cancelled")

// Breadcrumb segments for the levels below the root. Each level is named
// once here so the path bar cannot drift from the menu row leading to it.
const (
	rootCrumb       = "主選單"
	crumbChat       = "使用帳號對話"
	crumbHandoff    = "接手對話"
	crumbSource     = "選擇來源 Agent"
	crumbSession    = "選擇來源對話"
	crumbTarget     = "選擇目標 Agent"
	crumbAccounts   = "帳號設定"
	crumbSetup      = "初次設定"
	crumbUsage      = "查看用量"
	crumbStatusline = "狀態列設定"
)

// breadcrumb renders the path bar shown above every picker, so which level
// a keypress is answering is never ambiguous.
func breadcrumb(segments ...string) string {
	return strings.Join(append([]string{rootCrumb}, segments...), " > ")
}

func sourceAgentCrumb(label string) string {
	return "來源 Agent：" + label
}

// showCrumbs prints the path bar above a plain-text prompt; fzf's --header
// does the same job for the pickers.
func showCrumbs(crumbs string) {
	if crumbs != "" {
		fmt.Println(crumbs)
	}
}

// labelOf is how a chosen row becomes the next breadcrumb segment: the
// label the user just read is the name of the level they are entering.
func labelOf(candidates []kv, key string) string {
	for _, c := range candidates {
		if c.Key == key {
			return c.Label
		}
	}
	return key
}

// pickKey runs fzf over candidates ("key\tlabel" rows, label-only visible)
// and returns the chosen key. When numbered is true, candidates are
// prefixed "1. ", "2. ", ... and fzf is given digit-key shortcuts
// (pos(N)+accept) so a single keypress selects and accepts, up to 9 items.
func pickKey(candidates []kv, crumbs, prompt string, numbered bool) (string, error) {
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
	if crumbs != "" {
		args = append(args, "--header="+crumbs)
	}
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
		return "", errCancelled
	}
	selected := strings.SplitN(strings.TrimSpace(string(out)), "\t", 2)[0]
	if selected == "" {
		return "", errCancelled
	}
	return selected, nil
}

// pickSession runs fzf over conversation rows and returns the chosen path
// alongside whatever was typed into the search box. fzf gets --print-query
// so a conversation id typed there is usable even when it matches no listed
// row: the picker lists only the current project's newest conversations, so
// an id the user already knows is regularly absent from it. Such an id
// comes back as the query, with an empty path.
func pickSession(candidates []session.Candidate, crumbs, prompt string) (string, string, error) {
	if _, err := exec.LookPath("fzf"); err != nil {
		return "", "", fmt.Errorf("fzf is required")
	}
	var rows strings.Builder
	for _, c := range candidates {
		rows.WriteString(c.Path)
		rows.WriteByte('\t')
		rows.WriteString(c.Description)
		rows.WriteByte('\n')
	}
	args := []string{"--height=~15", "--border=none", "--with-nth=2..", "--delimiter=\t", "--print-query", "--prompt=" + prompt}
	if crumbs != "" {
		args = append(args, "--header="+crumbs)
	}
	cmd := exec.Command("fzf", args...)
	cmd.Stdin = strings.NewReader(rows.String())
	out, err := cmd.Output()
	if err != nil {
		// Exit code 1 is "no match", which is the whole point of
		// --print-query here: the query is still on stdout and is the id
		// the user typed. Anything else (130 for ESC, 2 for a real
		// failure) is a cancellation.
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			return "", "", errCancelled
		}
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	query := strings.TrimSpace(lines[0])
	selected := ""
	if len(lines) > 1 {
		selected = strings.SplitN(strings.TrimSpace(lines[1]), "\t", 2)[0]
	}
	if selected == "" && query == "" {
		return "", "", errCancelled
	}
	return selected, query, nil
}

// pickSessionRow is pickSession for the pickers where only a listed row
// means anything -- disambiguating an id that already matched several
// conversations, where whatever was typed is not another id to go look up.
func pickSessionRow(candidates []session.Candidate, crumbs, prompt string) (string, error) {
	path, _, err := pickSession(candidates, crumbs, prompt)
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", fmt.Errorf("no conversation selected")
	}
	return path, nil
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

func readOptionalAlias(crumbs, prompt string) string {
	showCrumbs(crumbs)
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

func accountLabels(r registry.Registry) map[string]string {
	labels := map[string]string{}
	for _, row := range registry.Rows(r) {
		labels[row.ID] = row.Label
	}
	return labels
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

// chooseSourceAccount is the second level of the handoff flow. Every
// registered account is offered whether or not its CLI is installed:
// handing a conversation off only reads the source account's files, and an
// id typed at the next level can turn out to belong to any account anyway.
func chooseSourceAccount(r registry.Registry, labels map[string]string) (string, error) {
	ids := make([]string, 0, len(r.Accounts))
	for _, account := range r.Accounts {
		ids = append(ids, account.ID)
	}
	sort.Strings(ids)
	candidates := make([]kv, 0, len(ids))
	for _, id := range ids {
		candidates = append(candidates, kv{id, labels[id]})
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("no account is registered")
	}
	return pickKey(candidates, breadcrumb(crumbHandoff, crumbSource), "來源帳號: ", true)
}

// interactiveRegistryHandoff is a three-step wizard (source agent ->
// conversation -> target agent). Backing out of a step returns to the step
// before it rather than the whole flow, except at the first step: ESC
// there is the flow's own cancellation, propagated to the caller.
func interactiveRegistryHandoff(registryPath, project string) error {
	r, err := registry.Load(registryPath)
	if err != nil {
		return err
	}
	labels := accountLabels(r)

	const (
		stepSource = iota
		stepSession
		stepTarget
	)
	step := stepSource
	var sourceID string
	var sessions []session.Candidate
	var sourceCrumb, sessionPath, typed string

	for {
		switch step {
		case stepSource:
			sourceID, err = chooseSourceAccount(r, labels)
			if err != nil {
				return err
			}
			source, err := registry.FindAccount(r, sourceID)
			if err != nil {
				return err
			}
			// A source account whose conversations cannot be listed is not a
			// dead end: a typed id still reaches every account.
			sessions, err = sessionsForAccount(source, project)
			if err != nil {
				sessions = nil
			}
			sourceCrumb = sourceAgentCrumb(labels[sourceID])
			step = stepSession

		case stepSession:
			crumbs := breadcrumb(crumbHandoff, sourceCrumb, crumbSession)
			sessionPath, typed, err = pickSession(sessions, crumbs, "搜尋或輸入對話 ID: ")
			if err != nil {
				if errors.Is(err, errCancelled) {
					step = stepSource
					continue
				}
				return err
			}
			if sessionPath == "" {
				if err := manualIDHandoff(registryPath, r, labels, typed, project); err != nil {
					if errors.Is(err, errCancelled) {
						step = stepSession
						continue
					}
					return err
				}
				return nil
			}
			step = stepTarget

		case stepTarget:
			targetID, err := chooseTargetExcluding(r, labels, sourceID, breadcrumb(crumbHandoff, sourceCrumb, crumbTarget))
			if err != nil {
				if errors.Is(err, errCancelled) {
					step = stepSession
					continue
				}
				return err
			}
			return RegistryHandoff(registryPath, sourceID, targetID, sessionPath, project, true)
		}
	}
}

// QuickHandoff finds a Claude conversation by id fragment and hands it to a
// Codex account. When exactly one Codex account is available, no picker is
// opened; multiple Codex accounts are resolved with one target picker.
func QuickHandoff(registryPath, fragment, project string) error {
	r, err := registry.Load(registryPath)
	if err != nil {
		return err
	}
	labels := accountLabels(r)

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
		path, pickErr := pickSessionRow(candidates, breadcrumb(crumbHandoff), "多筆符合，選擇要接力的對話: ")
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
	return pickKey(candidates, breadcrumb(crumbHandoff), "選擇接手的 Codex 帳號: ", true)
}

// manualIDHandoff resolves a conversation id typed by the user against every
// registered account, so the source account comes from wherever the id was
// actually found rather than from the source account picked beforehand.
func manualIDHandoff(registryPath string, r registry.Registry, labels map[string]string, fragment, project string) error {
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
		path, err := pickSessionRow(candidates, breadcrumb(crumbHandoff), "多筆符合，選擇要接力的對話: ")
		if err != nil {
			return err
		}
		chosen = byPath[path]
	}

	sourceID := chosen.accountID
	targetID, err := chooseTargetExcluding(r, labels, sourceID, breadcrumb(crumbHandoff, sourceAgentCrumb(labels[sourceID]), crumbTarget))
	if err != nil {
		return err
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

func chooseTargetExcluding(r registry.Registry, labels map[string]string, sourceID, crumbs string) (string, error) {
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
	return pickKey(candidates, crumbs, "接手帳號: ", true)
}

func bootstrapRegistry(registryPath string) error {
	candidates, err := registry.Discover(registryPath)
	if err != nil {
		return err
	}
	crumbs := breadcrumb(crumbSetup)
	r := registry.Empty()
	for _, candidate := range candidates {
		providerName := registry.ProviderNames[candidate.Provider]
		choice, err := pickKey([]kv{
			{"import", fmt.Sprintf("匯入 %s: %s", providerName, candidate.Home)},
			{"skip", "略過這個目錄"},
		}, crumbs, "偵測到既有帳號目錄: ", true)
		if err != nil {
			return err
		}
		if choice == "import" {
			alias := readOptionalAlias(crumbs, fmt.Sprintf("%s alias（選填）: ", providerName))
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

func chooseRegisteredAccount(r registry.Registry, crumbs, prompt string) (string, error) {
	labels := accountLabels(r)
	candidates := make([]kv, 0, len(r.Accounts))
	for _, account := range r.Accounts {
		candidates = append(candidates, kv{account.ID, labels[account.ID]})
	}
	return pickKey(candidates, crumbs, prompt, true)
}

// offerSharedSettings asks whether a brand-new account should read the same
// settings as the first account of its provider. Declining leaves an empty
// home, which is a real choice, so this asks rather than decides. Each
// shared entry is reported on its own; one entry failing to link does not
// hide the others, and the login matters more than any of them.
func offerSharedSettings(r registry.Registry, account registry.Account, crumbs string) {
	source, found := registry.PrimaryAccount(r, account.Provider)
	if !found || source.ID == account.ID {
		return
	}
	labels := accountLabels(r)
	choice, err := pickKey([]kv{
		{"share", fmt.Sprintf("共用「%s」的設定", labels[source.ID])},
		{"own", "這個帳號自己一份設定"},
	}, crumbs, "新帳號的設定: ", true)
	if err != nil || choice != "share" {
		return
	}
	shares, err := registry.ShareSettings(source, account, false)
	if err != nil {
		fmt.Printf("提醒: 未能共用「%s」的設定: %s\n", labels[source.ID], err)
		return
	}
	var linked, already, failed int
	for _, share := range shares {
		if share.SourceMissing {
			continue
		}
		// Named before the error is handled: an entry moved aside by a
		// share that then failed is exactly the one nobody must lose.
		if share.Backup != "" {
			fmt.Printf("%s 原本的版本已備份到 %s\n", share.Name, share.Backup)
		}
		if share.Err != nil {
			fmt.Printf("提醒: 未能共用 %s: %s\n", share.Name, share.Err)
			failed++
			continue
		}
		if share.Linked {
			linked++
		} else {
			already++
		}
	}
	switch {
	case linked > 0 && failed == 0:
		fmt.Printf("設定已共用自「%s」。\n", labels[source.ID])
	case linked > 0 && failed > 0:
		fmt.Printf("設定已共用自「%s」的 %d/%d 個項目，其餘請見上方提醒。\n", labels[source.ID], linked+already, linked+already+failed)
	}
}

// shareAllAccountSettings relinks every account's shared entries onto the
// first account of its provider. Unlike account creation this replaces an
// entry the account already has, so the original is moved aside and named
// rather than assumed unwanted. Entries link independently: one refused or
// failed entry is reported without stopping the rest.
func shareAllAccountSettings(r registry.Registry) {
	labels := accountLabels(r)
	candidates := 0
	reported := false
	// examined is true once some entry actually had a source to compare
	// against, distinguishing "every entry already shared" from "the source
	// had nothing to share in the first place".
	examined := false
	for _, account := range r.Accounts {
		source, found := registry.PrimaryAccount(r, account.Provider)
		if !found || source.ID == account.ID {
			continue
		}
		candidates++
		shares, err := registry.ShareSettings(source, account, true)
		if err != nil {
			fmt.Printf("%s: 未共用: %s\n", labels[account.ID], err)
			reported = true
			continue
		}
		for _, share := range shares {
			if share.SourceMissing {
				continue
			}
			examined = true
			// Named before the error is handled: an entry moved aside by a
			// share that then failed is exactly the one nobody must lose.
			if share.Backup != "" {
				fmt.Printf("%s: %s 原本的版本已備份到 %s\n", labels[account.ID], share.Name, share.Backup)
				reported = true
			}
			if share.Err != nil {
				fmt.Printf("%s: %s 未共用: %s\n", labels[account.ID], share.Name, share.Err)
				reported = true
				continue
			}
			if share.Linked {
				fmt.Printf("%s: %s 已改為共用「%s」的版本\n", labels[account.ID], share.Name, labels[source.ID])
				reported = true
			}
		}
	}
	switch {
	case candidates == 0:
		fmt.Println("沒有可以共用設定的對象。")
	case reported:
		return
	case examined:
		fmt.Println("所有帳號都已經在共用設定了。")
	default:
		fmt.Println("來源帳號沒有可共用的設定，未進行任何共用。")
	}
}

func manageAccounts(registryPath, usageDirFlag string, usageDirFlagExplicit bool) error {
	crumbs := breadcrumb(crumbAccounts)
	for {
		actions := []kv{
			{"add", "新增帳號"},
			{"rename", "修改 alias"},
			{"login", "登入／重新登入"},
			{"share", "共用設定到所有帳號"},
			{"remove", "從 ccs 移除帳號"},
			{"import", "匯入既有帳號目錄"},
			{"usage-dir", "設定用量資料目錄"},
			{"statusline", crumbStatusline},
			{"trust", "同步專案信任到其他帳號"},
			{"back", "返回主選單"},
		}
		action, err := pickKey(actions, crumbs, "選擇動作: ", true)
		if err != nil {
			if errors.Is(err, errCancelled) {
				return nil
			}
			return err
		}
		actionCrumbs := breadcrumb(crumbAccounts, labelOf(actions, action))
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
			chosenProvider, err := pickKey(providers, actionCrumbs, "選擇 provider: ", true)
			if err != nil {
				if errors.Is(err, errCancelled) {
					continue
				}
				return err
			}
			accountHome, err := registry.SuggestAccountHome(registryPath, chosenProvider)
			if err != nil {
				return err
			}
			confirmation, err := pickKey([]kv{
				{"confirm", fmt.Sprintf("建立帳號目錄: %s", accountHome)},
				{"cancel", "取消"},
			}, actionCrumbs, "確認新增帳號: ", true)
			if err != nil {
				if errors.Is(err, errCancelled) {
					continue
				}
				return err
			}
			if confirmation == "confirm" {
				alias := readOptionalAlias(actionCrumbs, "")
				account, err := registry.AddAccount(registryPath, chosenProvider, accountHome, alias)
				if err != nil {
					return err
				}
				// The registry has to be reloaded: the account being
				// offered a share was only just added to it.
				if updated, loadErr := registry.Load(registryPath); loadErr == nil {
					offerSharedSettings(updated, account, actionCrumbs)
				}
				// The interactive CLI, not the login subcommand: a brand
				// new account home has no onboarding state, so the CLI
				// asks to log in on its first run whatever the login
				// subcommand already stored. Logging in there is the same
				// login, minus the second one.
				return provider.LaunchSession(account)
			}
		case "share":
			r, err := registry.Load(registryPath)
			if err != nil {
				return err
			}
			confirmation, err := pickKey([]kv{
				{"confirm", "每個帳號都改讀第一個同 provider 帳號的設定檔"},
				{"cancel", "取消"},
			}, actionCrumbs, "確認共用設定: ", true)
			if err != nil {
				if errors.Is(err, errCancelled) {
					continue
				}
				return err
			}
			if confirmation == "confirm" {
				shareAllAccountSettings(r)
			}
		case "rename":
			r, err := registry.Load(registryPath)
			if err != nil {
				return err
			}
			accountID, err := chooseRegisteredAccount(r, actionCrumbs, "選擇要修改 alias 的帳號: ")
			if err != nil {
				if errors.Is(err, errCancelled) {
					continue
				}
				return err
			}
			alias := readOptionalAlias(actionCrumbs, "新的 alias（留空即清除）: ")
			if err := registry.RenameAccount(registryPath, accountID, alias); err != nil {
				return err
			}
		case "login":
			r, err := registry.Load(registryPath)
			if err != nil {
				return err
			}
			accountID, err := chooseRegisteredAccount(r, actionCrumbs, "選擇要登入的帳號: ")
			if err != nil {
				if errors.Is(err, errCancelled) {
					continue
				}
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
			selected, err := pickKey(options, actionCrumbs, "選擇要匯入的帳號目錄: ", true)
			if err != nil {
				if errors.Is(err, errCancelled) {
					continue
				}
				return err
			}
			parts := strings.SplitN(selected, "|", 2)
			alias := readOptionalAlias(actionCrumbs, "")
			if _, err := registry.AddAccount(registryPath, parts[0], parts[1], alias); err != nil {
				return err
			}
		case "remove":
			r, err := registry.Load(registryPath)
			if err != nil {
				return err
			}
			accountID, err := chooseRegisteredAccount(r, actionCrumbs, "選擇要取消登記的帳號: ")
			if err != nil {
				if errors.Is(err, errCancelled) {
					continue
				}
				return err
			}
			confirmation, err := pickKey([]kv{
				{"confirm", "只從 ccs 移除，保留所有帳號資料"},
				{"cancel", "取消"},
			}, actionCrumbs, "確認取消登記: ", true)
			if err != nil {
				if errors.Is(err, errCancelled) {
					continue
				}
				return err
			}
			if confirmation == "confirm" {
				if err := registry.RemoveAccount(registryPath, accountID); err != nil {
					return err
				}
			}
		case "usage-dir":
			input := readOptionalAlias(actionCrumbs, "新的用量資料目錄（留空即清除，改用預設路徑）: ")
			resolved, err := registry.SetUsageDir(registryPath, input)
			if err != nil {
				return err
			}
			if resolved == "" {
				fmt.Println("用量資料目錄已清除，將使用預設路徑。")
			} else {
				fmt.Printf("用量資料目錄已設定為：%s\n", resolved)
			}
		case "statusline":
			usageDir := resolveUsageDir(registryPath, usageDirFlag, usageDirFlagExplicit)
			if err := manageStatusline(registryPath, usageDir, actionCrumbs); err != nil {
				if errors.Is(err, errCancelled) {
					continue
				}
				return err
			}
		case "trust":
			if err := manageTrustSync(registryPath, actionCrumbs); err != nil {
				if errors.Is(err, errCancelled) {
					continue
				}
				return err
			}
		}
	}
}

// manageStatusline shows what the primary claude account's shared
// statusLine.command currently does about usage-snapshot writing, and lets
// the user toggle it on or off. Every account of that provider reads this
// same document (see ShareSettings), so the state and any change shown here
// apply to all of them, not just one.
func manageStatusline(registryPath, usageDir, crumbs string) error {
	showCrumbs(crumbs)
	r, err := registry.Load(registryPath)
	if err != nil {
		return err
	}
	info, err := registry.ReadStatuslineInfo(r, usageDir)
	if err != nil {
		return err
	}
	fmt.Println("此設定是 Claude 該 provider 所有帳號共用的一份 settings.json（symlink），不是單一帳號的設定。")
	fmt.Printf("設定檔：%s\n", info.SettingsPath)

	if info.Refusal != "" {
		fmt.Printf("無法判讀 statusLine 設定（%s），不會做任何修改。請手動貼上以下設定：\n%s\n", info.Refusal, info.Snippet)
		return nil
	}

	fmt.Printf("執行檔：%s\n", info.Executable)
	fmt.Printf("ccs 目前讀取的用量目錄：%s\n", usageDir)
	switch {
	case !info.HasUsageDir:
		fmt.Println("用量目錄參數：未設定（status line 目前不會寫入用量 snapshot）")
	case info.Matches:
		fmt.Printf("用量目錄參數：%s（與 ccs 一致，snapshot 寫入已啟用）\n", info.UsageDir)
	default:
		fmt.Printf("用量目錄參數：%s（與 ccs 目前讀取的目錄不一致）\n", info.UsageDir)
	}

	var actions []kv
	switch {
	case !info.HasUsageDir:
		actions = []kv{{"enable", "啟用（加上 --usage-dir）"}, {"cancel", "取消"}}
	case info.Matches:
		actions = []kv{{"disable", "停用（移除 --usage-dir）"}, {"cancel", "取消"}}
	default:
		actions = []kv{
			{"replace", "取代為 ccs 目前讀取的目錄"},
			{"disable", "停用（移除既有設定）"},
			{"cancel", "取消"},
		}
	}
	choice, err := pickKey(actions, crumbs, "選擇動作: ", true)
	if err != nil {
		return err
	}

	var change registry.StatuslineChange
	switch choice {
	case "cancel":
		return nil
	case "enable":
		change, err = registry.EnableStatuslineUsageDir(r, usageDir, false)
	case "replace":
		change, err = registry.EnableStatuslineUsageDir(r, usageDir, true)
	case "disable":
		change, err = registry.DisableStatuslineUsageDir(r, usageDir)
	}
	if err != nil {
		return err
	}
	reportStatuslineChange(change)
	return nil
}

func reportStatuslineChange(change registry.StatuslineChange) {
	switch change.Outcome {
	case registry.StatuslineWrote:
		fmt.Printf("已更新 statusLine.command：%s\n", change.Info.Command)
		fmt.Printf("原設定已備份到：%s\n", change.Backup)
	case registry.StatuslineAlreadyEnabled:
		fmt.Println("已經是啟用狀態，未做任何修改。")
	case registry.StatuslineAlreadyDisabled:
		fmt.Println("已經是停用狀態，未做任何修改。")
	case registry.StatuslineMismatch:
		fmt.Println("未做任何修改：請改選「取代」以覆蓋既有的 --usage-dir。")
	case registry.StatuslineRefused:
		fmt.Printf("未做任何修改（%s）。\n", change.Info.Refusal)
	}
}

func manageTrustSync(registryPath, crumbs string) error {
	r, err := registry.Load(registryPath)
	if err != nil {
		return err
	}
	labels := accountLabels(r)
	sourceID, err := chooseRegisteredAccount(r, crumbs, "選擇來源帳號: ")
	if err != nil {
		return err
	}
	source, err := registry.FindAccount(r, sourceID)
	if err != nil {
		return err
	}
	if source.Provider != "claude" {
		fmt.Println("只有 Claude 帳號有這份專案信任狀態，其他 provider 沒有可同步的內容。")
		return nil
	}

	fmt.Println("只補目標帳號缺的專案信任與權限欄位，不覆蓋既有值。")
	fmt.Println("目標帳號若有 Claude Code session 在跑，請先關閉，否則它結束時會把這次合併蓋回去。")
	confirmation, err := pickKey([]kv{
		{"confirm", fmt.Sprintf("將「%s」的專案信任同步到其他帳號", labels[source.ID])},
		{"cancel", "取消"},
	}, crumbs, "確認同步專案信任: ", true)
	if err != nil {
		return err
	}
	if confirmation != "confirm" {
		return nil
	}

	results, err := registry.SyncProjectTrustToAll(r, source)
	if err != nil {
		return err
	}
	if len(results) == 0 {
		fmt.Println("沒有其他 Claude 帳號可以同步。")
		return nil
	}
	for _, result := range results {
		reportTrustSync(labels[result.Target.ID], result)
	}
	return nil
}

func reportTrustSync(label string, result registry.TrustSync) {
	if result.Err != nil {
		if result.Backup != "" {
			fmt.Printf("%s: 同步失敗（%v），原設定已備份到 %s\n", label, result.Err, result.Backup)
		} else {
			fmt.Printf("%s: 同步失敗（%v）\n", label, result.Err)
		}
		return
	}
	if result.Skipped != "" {
		fmt.Printf("%s: 未同步（%s）\n", label, result.Skipped)
		return
	}
	fmt.Printf("%s: 已補上 %d 個專案、%d 個欄位，原設定已備份到 %s\n", label, result.ProjectsAdded, result.FieldsFilled, result.Backup)
}

// launchAccount is the second level of the chat flow. provider.LaunchSession
// execs, so this only returns when the account could not be launched or the
// user backed out.
func launchAccount(registryPath string) error {
	r, err := registry.Load(registryPath)
	if err != nil {
		return err
	}
	labels := accountLabels(r)
	var candidates []kv
	for _, account := range r.Accounts {
		if cliInstalled(account) {
			candidates = append(candidates, kv{account.ID, labels[account.ID]})
		}
	}
	if len(candidates) == 0 {
		return fmt.Errorf("no registered account has its provider CLI installed")
	}
	selected, err := pickKey(candidates, breadcrumb(crumbChat), "選擇帳號: ", true)
	if err != nil {
		return err
	}
	account, err := registry.FindAccount(r, selected)
	if err != nil {
		return err
	}
	return provider.LaunchSession(account)
}

// showUsage prints every registered account's Claude quota from whatever
// snapshot files a separate program has already written to usageDir. It
// never queries an API, never touches credentials, and never launches a
// session -- it only reads files that are already there. A usageDir that
// cannot be read (no permission, or the path is not a directory) is
// reported to the user rather than failing the view: every account still
// gets a row, just with no data, and the menu comes back either way.
func showUsage(registryPath, usageDir string) error {
	showCrumbs(breadcrumb(crumbUsage))
	r, err := registry.Load(registryPath)
	if err != nil {
		return err
	}
	snapshots, err := usage.LoadDir(usageDir)
	if err != nil {
		fmt.Printf("無法讀取用量資料目錄 %s：%v（以下顯示為沒有資料）\n", usageDir, err)
		snapshots = nil
	}
	matches := usage.MatchLatest(r.Accounts, snapshots)
	rows := usage.BuildRows(registry.Rows(r), matches, time.Now())
	usage.Fprint(os.Stdout, rows)
	fmt.Printf("資料來源目錄：%s\n", usageDir)
	if len(matches) == 0 {
		fmt.Println("目前沒有任何帳號的用量資料：這份資料由 status line 程式寫入該目錄，需要在該程式啟用寫入才會出現。")
	}
	return nil
}

// resolveUsageDir applies flag > stored setting > built-in default
// precedence for the usage view's snapshot directory. flagValue already
// carries the built-in default whenever flagExplicit is false, so falling
// back to it covers both "no stored setting" and "registry unreadable".
func resolveUsageDir(registryPath, flagValue string, flagExplicit bool) string {
	if flagExplicit {
		return flagValue
	}
	if r, err := registry.LoadOrEmpty(registryPath); err == nil && r.UsageDir != "" {
		return r.UsageDir
	}
	return flagValue
}

// Run drives the top-level menu loop until an account is launched (which
// execs and never returns) or the handoff flow returns. Backing out of a
// second-level picker lands back here rather than quitting, so a wrong turn
// costs one ESC instead of a restart.
//
// usageDirFlag and usageDirFlagExplicit are --usage-dir as parsed by the
// caller; the usage view re-resolves the effective directory on every visit
// (see resolveUsageDir) so a directory set from 帳號設定 during this same
// run takes effect immediately, without a restart.
func Run(registryPath, usageDirFlag string, usageDirFlagExplicit bool) error {
	if _, err := os.Stat(registryPath); os.IsNotExist(err) {
		if err := bootstrapRegistry(registryPath); err != nil {
			// A cancelled bootstrap wrote no registry, so the menu below
			// would fail on its first action; ending the run is the honest
			// outcome, and the next start re-enters bootstrap.
			if errors.Is(err, errCancelled) {
				fmt.Println("已取消初次設定。")
				return nil
			}
			return err
		}
	}
	for {
		action, err := pickKey([]kv{
			{"chat", crumbChat},
			{"handoff", crumbHandoff},
			{"accounts", crumbAccounts},
			{"usage", crumbUsage},
			{"quit", "離開"},
		}, breadcrumb(), "選擇功能: ", true)
		if err != nil {
			if errors.Is(err, errCancelled) {
				return nil
			}
			return err
		}
		switch action {
		case "quit":
			return nil
		case "chat":
			if err := launchAccount(registryPath); err != nil {
				if errors.Is(err, errCancelled) {
					continue
				}
				return err
			}
			return nil
		case "handoff":
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			if err := interactiveRegistryHandoff(registryPath, cwd); err != nil {
				if errors.Is(err, errCancelled) {
					continue
				}
				return err
			}
			return nil
		case "accounts":
			if err := manageAccounts(registryPath, usageDirFlag, usageDirFlagExplicit); err != nil {
				if errors.Is(err, errCancelled) {
					continue
				}
				return err
			}
		case "usage":
			usageDir := resolveUsageDir(registryPath, usageDirFlag, usageDirFlagExplicit)
			if err := showUsage(registryPath, usageDir); err != nil {
				return err
			}
		}
	}
}
