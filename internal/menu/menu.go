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

	"github.com/tsai41/agent-conversation-handoff/internal/handoff"
	"github.com/tsai41/agent-conversation-handoff/internal/provider"
	"github.com/tsai41/agent-conversation-handoff/internal/registry"
	"github.com/tsai41/agent-conversation-handoff/internal/session"
)

type kv struct{ Key, Label string }

// errCancelled marks a picker the user backed out of (ESC / CTRL-C) rather
// than one that failed. It propagates uncaught to Run, which restarts at
// the function level; only Run's own picker treats it as quitting.
var errCancelled = errors.New("selection cancelled")

// Breadcrumb segments for the levels below the root. Each level is named
// once here so the path bar cannot drift from the menu row leading to it.
const (
	rootCrumb     = "主選單"
	crumbChat     = "使用帳號對話"
	crumbHandoff  = "接手對話"
	crumbAccounts = "帳號設定"
	crumbSetup    = "初次設定"
)

// breadcrumb renders the path bar shown above every picker, so which level
// a keypress is answering is never ambiguous.
func breadcrumb(segments ...string) string {
	return strings.Join(append([]string{rootCrumb}, segments...), " > ")
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

// manualIDRow is the sentinel path returned by pickSession when the user
// picks the row that teaches the shortcut instead of using it. Typing an id
// straight into the search box reaches the same place without the extra
// keypress; the row is what makes that discoverable.
const manualIDRow = "action:manual-id"

const manualIDLabel = "✎ 輸入對話 ID（也可直接在上面的搜尋框輸入）"

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
	return pickKey(candidates, breadcrumb(crumbHandoff), "來源帳號: ", true)
}

func interactiveRegistryHandoff(registryPath, project string) error {
	r, err := registry.Load(registryPath)
	if err != nil {
		return err
	}
	labels := accountLabels(r)

	sourceID, err := chooseSourceAccount(r, labels)
	if err != nil {
		return err
	}
	source, err := registry.FindAccount(r, sourceID)
	if err != nil {
		return err
	}

	// A source account whose conversations cannot be listed is not a dead
	// end: the id row below still reaches every account.
	sessions, err := sessionsForAccount(source, project)
	if err != nil {
		sessions = nil
	}
	candidates := make([]session.Candidate, 0, len(sessions)+1)
	candidates = append(candidates, sessions...)
	candidates = append(candidates, session.Candidate{Path: manualIDRow, Description: manualIDLabel})

	crumbs := breadcrumb(crumbHandoff, labels[sourceID])
	sessionPath, typed, err := pickSession(candidates, crumbs, "選擇對話，或直接輸入 ID: ")
	if err != nil {
		return err
	}
	if sessionPath == manualIDRow {
		showCrumbs(crumbs)
		typed = readLine("對話 ID（可只輸入前綴）: ")
		sessionPath = ""
	}
	if sessionPath == "" {
		return manualIDHandoff(registryPath, r, labels, typed, project)
	}

	targetID, err := chooseTargetExcluding(r, labels, sourceID, crumbs)
	if err != nil {
		return err
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
	targetID, err := chooseTargetExcluding(r, labels, sourceID, breadcrumb(crumbHandoff, labels[sourceID]))
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
// settings document as the first account of its provider. Declining leaves
// an empty home, which is a real choice, so this asks rather than decides.
// A failure to link is reported and stepped over; the login matters more.
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
	share, err := registry.ShareSettings(source, account, false)
	if err != nil {
		fmt.Printf("提醒: 未能共用「%s」的設定: %s\n", labels[source.ID], err)
		return
	}
	if share.Linked {
		fmt.Printf("設定已共用自「%s」。\n", labels[source.ID])
	}
}

// shareAllAccountSettings relinks every account onto the first account of
// its provider. Unlike account creation this replaces a document the
// account already has, so the original is moved aside and named rather than
// assumed unwanted.
func shareAllAccountSettings(r registry.Registry) {
	labels := accountLabels(r)
	candidates := 0
	reported := false
	for _, account := range r.Accounts {
		source, found := registry.PrimaryAccount(r, account.Provider)
		if !found || source.ID == account.ID {
			continue
		}
		candidates++
		share, err := registry.ShareSettings(source, account, true)
		// Named before the error is handled: a document moved aside by a
		// share that then failed is exactly the one nobody must lose.
		if share.Backup != "" {
			fmt.Printf("%s: 原設定已備份到 %s\n", labels[account.ID], share.Backup)
			reported = true
		}
		if err != nil {
			fmt.Printf("%s: 未共用: %s\n", labels[account.ID], err)
			reported = true
			continue
		}
		if share.Linked {
			fmt.Printf("%s: 已改為共用「%s」的設定\n", labels[account.ID], labels[source.ID])
			reported = true
		}
	}
	switch {
	case candidates == 0:
		fmt.Println("每個 provider 都只有一個帳號，沒有可以共用的對象。")
	case !reported:
		fmt.Println("所有帳號都已經在共用設定了。")
	}
}

func manageAccounts(registryPath string) error {
	crumbs := breadcrumb(crumbAccounts)
	for {
		actions := []kv{
			{"add", "新增帳號"},
			{"rename", "修改 alias"},
			{"login", "登入／重新登入"},
			{"share", "共用設定到所有帳號"},
			{"remove", "從 ccs 移除帳號"},
			{"import", "匯入既有帳號目錄"},
			{"back", "返回主選單"},
		}
		action, err := pickKey(actions, crumbs, "選擇動作: ", true)
		if err != nil {
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
				return provider.Login(account)
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
				return err
			}
			confirmation, err := pickKey([]kv{
				{"confirm", "只從 ccs 移除，保留所有帳號資料"},
				{"cancel", "取消"},
			}, actionCrumbs, "確認取消登記: ", true)
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

// Run drives the top-level menu loop until an account is launched (which
// execs and never returns) or the handoff flow returns. Backing out of a
// second-level picker lands back here rather than quitting, so a wrong turn
// costs one ESC instead of a restart.
func Run(registryPath string) error {
	if _, err := os.Stat(registryPath); os.IsNotExist(err) {
		if err := bootstrapRegistry(registryPath); err != nil {
			return err
		}
	}
	for {
		action, err := pickKey([]kv{
			{"chat", crumbChat},
			{"handoff", crumbHandoff},
			{"accounts", crumbAccounts},
		}, breadcrumb(), "選擇功能: ", true)
		if err != nil {
			return err
		}
		switch action {
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
			if err := manageAccounts(registryPath); err != nil {
				if errors.Is(err, errCancelled) {
					continue
				}
				return err
			}
		}
	}
}
