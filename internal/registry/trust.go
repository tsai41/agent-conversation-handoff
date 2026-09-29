package registry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// projectTrustFields is the whitelist SyncProjectTrust merges from a
// source account's per-project settings into a target's: everything Claude
// Code needs to treat a project as already trusted and configured, and
// nothing else. In particular this never touches identity (oauthAccount,
// userID), onboarding state, per-session statistics, or any project key
// outside this list.
var projectTrustFields = []string{
	"hasTrustDialogAccepted",
	"allowedTools",
	"enabledMcpjsonServers",
	"disabledMcpjsonServers",
	"mcpContextUris",
	"hasClaudeMdExternalIncludesApproved",
}

// TrustSync reports what SyncProjectTrust did merging one source account's
// project trust into one target account.
type TrustSync struct {
	Target Account
	// Backup is set only when a backup was made: where the target's
	// pre-write .claude.json was copied. It is reported even when the write
	// that followed failed, so the copy is never left unaccounted for.
	Backup string
	// ProjectsAdded counts projects absent at the target that were created
	// with only the whitelist fields present at the source.
	ProjectsAdded int
	// FieldsFilled counts whitelist fields added to projects the target
	// already had -- one per field filled in, not per project touched.
	FieldsFilled int
	// Skipped is non-empty when nothing was written and why; ProjectsAdded
	// and FieldsFilled are then both 0.
	Skipped string
	// Err is set by SyncProjectTrustToAll when SyncProjectTrust returned an
	// error for this target, so one failing target does not hide what
	// happened to the others.
	Err error
}

// ClaudeConfigFile is where Claude Code keeps account's .claude.json: under
// CLAUDE_CONFIG_DIR when that is set, directly under the user's home when
// it is not. The account whose home is the default config directory is
// launched with the variable unset, so its file sits beside that directory
// rather than inside it; every other account's is inside its home.
func ClaudeConfigFile(account Account) (string, error) {
	userHome, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	home, err := filepath.Abs(account.Home)
	if err != nil {
		return "", err
	}
	defaultHome := filepath.Join(userHome, ".claude")
	if sameFile(home, defaultHome) {
		return filepath.Join(userHome, ".claude.json"), nil
	}
	return filepath.Join(home, ".claude.json"), nil
}

// SyncProjectTrust merges a whitelist of per-project trust and permission
// fields from source's .claude.json into target's, filling in only what
// target lacks -- never overwriting a value target already has, even a
// zero value like false, [], {} or null.
//
// Claude Code keeps this state in .claude.json (see ClaudeConfigFile) under
// the top-level "projects" object, in the same file as account identity
// (oauthAccount, userID), onboarding state, and per-session statistics --
// all of which this function must never read into the merge or write back.
// The file cannot be symlinked between accounts the way ShareSettings links
// settings.json, because identity lives beside the state actually being
// shared here.
//
// The document is decoded, merged, and re-encoded whole: Claude Code
// rewrites .claude.json wholesale on its own runs, so only its content has
// to survive a merge, not its byte layout -- key order may change.
//
// The returned error is non-nil for a problem with the pair of accounts,
// for a document this function cannot safely read, or for a failed lock,
// backup or write step: not both accounts are provider "claude"; source
// and target are the same account or the same physical home; source's
// .claude.json is missing, unreadable, or not valid JSON; target's
// .claude.json is unreadable or not valid JSON; or the lock, backup or
// write itself failed. Every other reason nothing was written -- target
// has not run Claude Code yet, a session currently holds target's lock, a
// document is not the shape this function merges, or there is nothing to
// change -- is reported through TrustSync.Skipped instead, with a nil
// error.
func SyncProjectTrust(source, target Account) (TrustSync, error) {
	if source.Provider != "claude" || target.Provider != "claude" {
		return TrustSync{}, fmt.Errorf("project trust sync is claude-only: %s is %s and %s is %s", source.ID, source.Provider, target.ID, target.Provider)
	}
	if source.ID == target.ID || sameFile(source.Home, target.Home) {
		return TrustSync{}, fmt.Errorf("%s and %s are the same account home", source.ID, target.ID)
	}
	sourcePath, err := ClaudeConfigFile(source)
	if err != nil {
		return TrustSync{}, fmt.Errorf("%s: %w", source.ID, err)
	}
	targetPath, err := ClaudeConfigFile(target)
	if err != nil {
		return TrustSync{}, fmt.Errorf("%s: %w", target.ID, err)
	}

	sourceDoc, err := decodeClaudeJSON(sourcePath)
	if err != nil {
		return TrustSync{}, fmt.Errorf("%s: %w", source.ID, err)
	}

	if _, err := os.Stat(targetPath); os.IsNotExist(err) {
		return TrustSync{Target: target, Skipped: "that account has not run Claude Code yet, so there is nothing to merge into"}, nil
	}
	lockPath := targetPath + ".lock"
	held, err := takeClaudeLock(lockPath)
	if err != nil {
		return TrustSync{}, fmt.Errorf("%s: %w", target.ID, err)
	}
	if !held {
		return TrustSync{Target: target, Skipped: fmt.Sprintf("a Claude Code session is writing it (lock %s)", lockPath)}, nil
	}
	defer os.Remove(lockPath)

	targetInfo, err := os.Stat(targetPath)
	if err != nil {
		return TrustSync{}, fmt.Errorf("%s: %w", target.ID, err)
	}
	targetRaw, err := os.ReadFile(targetPath)
	if err != nil {
		return TrustSync{}, fmt.Errorf("%s: %w", target.ID, err)
	}
	targetDoc, err := decodeClaudeJSONBytes(targetRaw)
	if err != nil {
		return TrustSync{}, fmt.Errorf("%s: %w", target.ID, err)
	}

	added, filled, refusal := mergeProjectTrust(sourceDoc, targetDoc)
	if refusal != "" {
		return TrustSync{Target: target, Skipped: refusal}, nil
	}
	if added == 0 && filled == 0 {
		return TrustSync{Target: target, Skipped: "already in sync, or the source account has no project trust to share"}, nil
	}
	updated, err := encodeClaudeJSON(targetDoc)
	if err != nil {
		return TrustSync{}, fmt.Errorf("%s: %w", target.ID, err)
	}

	backup, err := freeBackupPath(targetPath, EntryFile)
	if err != nil {
		return TrustSync{}, fmt.Errorf("%s: %w", target.ID, err)
	}
	if err := os.WriteFile(backup, targetRaw, targetInfo.Mode().Perm()); err != nil {
		os.Remove(backup)
		return TrustSync{}, fmt.Errorf("%s: %w", target.ID, err)
	}
	if err := writeFileAtomic(targetPath, updated, targetInfo.Mode().Perm()); err != nil {
		return TrustSync{Target: target, Backup: backup}, fmt.Errorf("%s: %w", target.ID, err)
	}

	return TrustSync{Target: target, Backup: backup, ProjectsAdded: added, FieldsFilled: filled}, nil
}

// SyncProjectTrustToAll runs SyncProjectTrust from source into every other
// registered claude account, in Number order, archived accounts last. A target that fails is
// recorded on its own TrustSync.Err and does not stop the others, so the
// returned slice always covers every target; the error is non-nil only
// when source is not a claude account.
func SyncProjectTrustToAll(r Registry, source Account) ([]TrustSync, error) {
	if source.Provider != "claude" {
		return nil, fmt.Errorf("project trust sync is claude-only: %s is %s", source.ID, source.Provider)
	}
	var targets []Account
	for _, candidate := range r.Accounts {
		if candidate.Provider != "claude" || candidate.ID == source.ID {
			continue
		}
		targets = append(targets, candidate)
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].Archived != targets[j].Archived {
			return !targets[i].Archived
		}
		return targets[i].Number < targets[j].Number
	})

	results := make([]TrustSync, 0, len(targets))
	for _, target := range targets {
		result, err := SyncProjectTrust(source, target)
		if err != nil {
			result.Target = target
			result.Err = err
		}
		results = append(results, result)
	}
	return results, nil
}

// claudeLockStale is how old a lock directory must be before it is taken
// over: Claude Code's own writer refreshes its lock's mtime every 5 seconds
// and treats one untouched for 60 seconds as abandoned, so the same rule
// here cannot steal a lock that writer still holds.
const claudeLockStale = 60 * time.Second

// takeClaudeLock takes the directory lock Claude Code's own writer uses for
// its config document, so a concurrent session waits rather than racing
// the write. held is false when a live session holds the lock; an
// abandoned one is removed and retaken.
func takeClaudeLock(lockPath string) (held bool, err error) {
	for attempt := 0; attempt < 2; attempt++ {
		err := os.Mkdir(lockPath, 0o755)
		if err == nil {
			return true, nil
		}
		if !os.IsExist(err) {
			return false, err
		}
		info, statErr := os.Stat(lockPath)
		if statErr != nil || time.Since(info.ModTime()) < claudeLockStale {
			return false, nil
		}
		if err := os.RemoveAll(lockPath); err != nil {
			return false, err
		}
	}
	return false, nil
}

// mergeProjectTrust fills sourceDoc's whitelisted per-project fields into
// targetDoc in place, and reports how many projects were created and how
// many fields were filled into projects that already existed at the
// target. A non-empty refusal means a document is not the shape this
// merges -- "projects" present but not an object, or a project value that
// is not an object -- and targetDoc was left untouched: partial merges into
// a document of unexpected shape are not attempted. A whitelist field
// whose source value is null is treated as absent.
func mergeProjectTrust(sourceDoc, targetDoc map[string]any) (added, filled int, refusal string) {
	sourceProjects, refusal := projectsObject(sourceDoc, "source")
	if refusal != "" || len(sourceProjects) == 0 {
		return 0, 0, refusal
	}
	targetProjects, refusal := projectsObject(targetDoc, "target")
	if refusal != "" {
		return 0, 0, refusal
	}
	if targetProjects == nil {
		targetProjects = map[string]any{}
	}

	paths := make([]string, 0, len(sourceProjects))
	for path := range sourceProjects {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	type pending struct {
		path   string
		fields map[string]any
		target map[string]any // nil when the project is absent at the target
	}
	var plan []pending
	for _, path := range paths {
		sourceProject, ok := sourceProjects[path].(map[string]any)
		if !ok {
			return 0, 0, fmt.Sprintf("the source's project %s is not an object", path)
		}
		fields := map[string]any{}
		for _, name := range projectTrustFields {
			if value, present := sourceProject[name]; present && value != nil {
				fields[name] = value
			}
		}
		if len(fields) == 0 {
			continue
		}
		entry := pending{path: path, fields: fields}
		if rawTarget, exists := targetProjects[path]; exists {
			targetProject, ok := rawTarget.(map[string]any)
			if !ok {
				return 0, 0, fmt.Sprintf("the target's project %s is not an object", path)
			}
			entry.target = targetProject
		}
		plan = append(plan, entry)
	}

	for _, entry := range plan {
		if entry.target == nil {
			targetProjects[entry.path] = maps.Clone(entry.fields)
			added++
			continue
		}
		for name, value := range entry.fields {
			if _, present := entry.target[name]; !present {
				entry.target[name] = value
				filled++
			}
		}
	}
	if added > 0 || filled > 0 {
		targetDoc["projects"] = targetProjects
	}
	return added, filled, ""
}

// projectsObject returns doc's "projects" object, nil when it is absent or
// null, and a refusal when it is present as anything else.
func projectsObject(doc map[string]any, side string) (map[string]any, string) {
	raw, present := doc["projects"]
	if !present || raw == nil {
		return nil, ""
	}
	projects, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Sprintf("the %s's projects is not an object", side)
	}
	return projects, ""
}

func decodeClaudeJSON(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no .claude.json at %s", path)
		}
		return nil, err
	}
	return decodeClaudeJSONBytes(raw)
}

// decodeClaudeJSONBytes decodes with UseNumber so integer and fractional
// literals round-trip exactly: .claude.json holds 13-digit millisecond
// timestamps and fractional costs that a plain float64 decode would lose
// precision on or reformat.
func decodeClaudeJSONBytes(raw []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("not valid JSON: %w", err)
	}
	if doc == nil {
		return nil, fmt.Errorf("not valid JSON: document is null")
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("not valid JSON: data after the document")
	}
	return doc, nil
}

func encodeClaudeJSON(doc map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
