// Package session scans Claude and Codex conversation histories for
// sessions that belong to a given project, for the handoff picker.
package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/tsai41/agent-conversation-handoff/internal/jsonlutil"
)

type Candidate struct {
	Path        string
	Description string
}

// Match is a session found by id lookup rather than by project scan. It
// carries the recorded working directory so callers can warn when the
// conversation belongs to a different project than the one being handed to.
type Match struct {
	Candidate
	SessionID string
	CWD       string
	StartTime time.Time
}

const maxCandidates = 5

// minIDFragment keeps a one- or two-character entry from matching most of
// the history and burying the user in a picker.
const minIDFragment = 4

func describe(startTime time.Time, sessionID, preview string) string {
	return fmt.Sprintf("%s  %s  %s", startTime.Format("2006-01-02 15:04"), ShortID(sessionID), truncate(preview, 70))
}

// SortNewestFirst orders matches by conversation start time, newest first.
// Callers that merge matches from several accounts need it to re-sort the
// combined list.
func SortNewestFirst(matches []Match) {
	sort.Slice(matches, func(i, j int) bool { return matches[i].StartTime.After(matches[j].StartTime) })
}

func normalizeFragment(fragment string) (string, error) {
	fragment = strings.ToLower(strings.TrimSpace(fragment))
	if len(fragment) < minIDFragment {
		return "", fmt.Errorf("conversation id needs at least %d characters", minIDFragment)
	}
	return fragment, nil
}

// ShortID truncates long ids (e.g. UUIDs) to their first 8 and last 4
// characters so the surrounding preview text stays visible in a picker.
func ShortID(id string) string {
	if len(id) <= 12 {
		return id
	}
	runes := []rune(id)
	if len(runes) <= 12 {
		return id
	}
	return string(runes[:8]) + "…" + string(runes[len(runes)-4:])
}

func sessionText(entry map[string]any) string {
	var content any
	if message, ok := entry["message"].(map[string]any); ok {
		content = message["content"]
	} else {
		content = entry["content"]
	}
	text := jsonlutil.TextContent(content)
	return strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
}

// ClaudeCandidates lists Claude sessions under sourceHome/projects/<project>
// (already scoped to the project by directory), newest conversation first.
// "Newest" is by the session's first recorded message timestamp, not file
// mtime -- a resumed session's mtime jumps to whenever it was last touched,
// which would otherwise reorder the list away from actual conversation age
// every time an old session gets picked back up.
func ClaudeCandidates(sourceHome, project string) ([]Candidate, error) {
	absProject, err := filepath.Abs(project)
	if err != nil {
		return nil, err
	}
	projectID := strings.NewReplacer("/", "-", "_", "-").Replace(absProject)
	sessionDir := filepath.Join(sourceHome, "projects", projectID)
	info, err := os.Stat(sessionDir)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("no Claude sessions for this project: %s", sessionDir)
	}

	matches, _ := filepath.Glob(filepath.Join(sessionDir, "*.jsonl"))
	type fileEntry struct {
		path      string
		startTime time.Time
	}
	files := make([]fileEntry, 0, len(matches))
	for _, match := range matches {
		files = append(files, fileEntry{match, claudeSessionStartTime(match)})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].startTime.After(files[j].startTime) })

	var candidates []Candidate
	for _, file := range files {
		sessionID, preview, _, err := readClaudeSession(file.path)
		if err != nil {
			continue
		}
		candidates = append(candidates, Candidate{file.path, describe(file.startTime.Local(), sessionID, preview)})
		if len(candidates) == maxCandidates {
			break
		}
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no readable Claude sessions for this project: %s", sessionDir)
	}
	return candidates, nil
}

// claudeSessionStartTime returns the first "timestamp" field found in the
// session's JSONL entries (when the conversation actually started), falling
// back to the file's mtime if the file is unreadable or has none.
func claudeSessionStartTime(path string) time.Time {
	file, err := os.Open(path)
	if err != nil {
		return fallbackModTime(path)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var entry map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		raw, ok := entry["timestamp"].(string)
		if !ok {
			continue
		}
		if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			return parsed
		}
	}
	return fallbackModTime(path)
}

func fallbackModTime(path string) time.Time {
	if info, err := os.Stat(path); err == nil {
		return info.ModTime()
	}
	return time.Time{}
}

func readClaudeSession(path string) (sessionID string, preview string, cwd string, err error) {
	file, err := os.Open(path)
	if err != nil {
		return "", "", "", err
	}
	defer file.Close()
	sessionID = "unknown UUID"
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	lines := 0
	for scanner.Scan() {
		lines++
		var entry map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		if id, ok := entry["sessionId"].(string); ok && id != "" {
			sessionID = id
		} else if id, ok := entry["session_id"].(string); ok && id != "" {
			sessionID = id
		}
		if recorded, ok := entry["cwd"].(string); ok && recorded != "" && cwd == "" {
			cwd = recorded
		}
		if entry["type"] == "user" && preview == "" {
			preview = sessionText(entry)
		}
		// cwd is optional: a session that never records one must not cost a
		// full scan of a large file just to find that out.
		if sessionID != "unknown UUID" && preview != "" && (cwd != "" || lines >= cwdScanLimit) {
			break
		}
	}
	return sessionID, preview, cwd, scanner.Err()
}

const cwdScanLimit = 200

// FindClaudeByID returns every Claude session under sourceHome whose id
// contains fragment, newest first, regardless of which project it belongs
// to. Claude names each session file after its id, so the whole history is
// matched by filename and only the hits are opened.
func FindClaudeByID(sourceHome, fragment string) ([]Match, error) {
	fragment, err := normalizeFragment(fragment)
	if err != nil {
		return nil, err
	}
	matches, err := filepath.Glob(filepath.Join(escapeGlobMeta(sourceHome), "projects", "*", "*.jsonl"))
	if err != nil {
		return nil, err
	}

	var found []Match
	for _, path := range matches {
		base := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		if !strings.Contains(strings.ToLower(base), fragment) {
			continue
		}
		sessionID, preview, cwd, err := readClaudeSession(path)
		if err != nil {
			continue
		}
		startTime := claudeSessionStartTime(path).Local()
		found = append(found, Match{
			Candidate: Candidate{path, describe(startTime, sessionID, preview)},
			SessionID: sessionID,
			CWD:       cwd,
			StartTime: startTime,
		})
	}
	SortNewestFirst(found)
	return found, nil
}

// escapeGlobMeta keeps a literal directory path literal: an account home
// containing "[", "*" or "?" would otherwise be read as pattern syntax and
// silently match nothing.
func escapeGlobMeta(path string) string {
	var escaped strings.Builder
	for _, r := range path {
		if strings.ContainsRune(`*?[\`, r) {
			escaped.WriteByte('\\')
		}
		escaped.WriteRune(r)
	}
	return escaped.String()
}

var codexRolloutTimestamp = regexp.MustCompile(`rollout-(\d{4}-\d{2}-\d{2})T(\d{2})-(\d{2})-(\d{2})-`)

// codexSessionStartTime parses the session's start time out of its
// "rollout-YYYY-MM-DDTHH-MM-SS-<uuid>.jsonl" filename (encoded in local
// time -- confirmed against fresh, never-resumed sessions where filename
// and mtime match), falling back to mtime if the name doesn't match.
func codexSessionStartTime(path string) time.Time {
	match := codexRolloutTimestamp.FindStringSubmatch(filepath.Base(path))
	if match != nil {
		if parsed, err := time.ParseInLocation("2006-01-02T15-04-05", match[1]+"T"+match[2]+"-"+match[3]+"-"+match[4], time.Local); err == nil {
			return parsed
		}
	}
	return fallbackModTime(path)
}

// CodexCandidates lists Codex sessions under codexHome/sessions whose
// recorded cwd matches project, newest conversation first (see
// codexSessionStartTime for why this isn't mtime). Codex sessions aren't
// stored per-project, so this walks the whole history; it stops reading a
// file as soon as its session_meta cwd doesn't match, instead of reading
// the file in full.
func CodexCandidates(codexHome, project string) ([]Candidate, error) {
	sessionsDir := filepath.Join(codexHome, "sessions")
	info, err := os.Stat(sessionsDir)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("no Codex sessions directory: %s", sessionsDir)
	}
	resolvedProject, err := filepath.Abs(project)
	if err != nil {
		return nil, err
	}

	type match struct {
		path      string
		startTime time.Time
		sessionID string
		preview   string
	}
	var matches []match
	err = filepath.WalkDir(sessionsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		sessionID, preview, _, matched, readErr := scanCodexSession(path, resolvedProject)
		if readErr != nil || !matched {
			return nil
		}
		matches = append(matches, match{path, codexSessionStartTime(path), sessionID, preview})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("no readable Codex sessions for this project: %s", sessionsDir)
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].startTime.After(matches[j].startTime) })

	count := min(len(matches), maxCandidates)
	candidates := make([]Candidate, count)
	for i, m := range matches[:count] {
		candidates[i] = Candidate{m.path, describe(m.startTime, m.sessionID, m.preview)}
	}
	return candidates, nil
}

// FindCodexByID returns every Codex session under codexHome whose id
// contains fragment, newest first, regardless of the project it was started
// in. The id is part of the rollout filename, so the history is matched by
// name and only the hits are opened.
func FindCodexByID(codexHome, fragment string) ([]Match, error) {
	fragment, err := normalizeFragment(fragment)
	if err != nil {
		return nil, err
	}
	sessionsDir := filepath.Join(codexHome, "sessions")
	info, err := os.Stat(sessionsDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("not a Codex sessions directory: %s", sessionsDir)
	}

	var found []Match
	err = filepath.WalkDir(sessionsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		if !strings.Contains(strings.ToLower(codexIDPart(path)), fragment) {
			return nil
		}
		// A file with no session_meta has neither a real id nor a cwd, so
		// it can only be handed off blind -- skip it rather than offer it.
		sessionID, preview, cwd, hasMeta, readErr := scanCodexSession(path, "")
		if readErr != nil || !hasMeta {
			return nil
		}
		startTime := codexSessionStartTime(path)
		found = append(found, Match{
			Candidate: Candidate{path, describe(startTime, sessionID, preview)},
			SessionID: sessionID,
			CWD:       cwd,
			StartTime: startTime,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	SortNewestFirst(found)
	return found, nil
}

// codexIDPart strips the "rollout-<timestamp>-" prefix so an id fragment is
// matched against the id alone -- otherwise a fragment like "2026-08" would
// match every session recorded that month.
func codexIDPart(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	if loc := codexRolloutTimestamp.FindStringIndex(base); loc != nil && loc[0] == 0 {
		return base[loc[1]:]
	}
	return base
}

// scanCodexSession reads a rollout file's session id, first user message and
// recorded cwd. When wantCwd is set, a session recorded elsewhere is reported
// as not matched and the rest of the file is left unread; when it is empty,
// every session with a session_meta entry matches.
func scanCodexSession(path string, wantCwd string) (sessionID string, preview string, cwd string, matched bool, err error) {
	file, err := os.Open(path)
	if err != nil {
		return "", "", "", false, err
	}
	defer file.Close()
	sessionID = "unknown UUID"
	haveCwd := false
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		payload, _ := entry["payload"].(map[string]any)
		switch entry["type"] {
		case "session_meta":
			if payload == nil {
				continue
			}
			if id, ok := payload["id"].(string); ok {
				sessionID = id
			}
			recorded, _ := payload["cwd"].(string)
			if recorded != "" && cwd == "" {
				cwd = recorded
			}
			haveCwd = true
			if wantCwd == "" {
				continue
			}
			resolvedCwd, resolveErr := filepath.Abs(recorded)
			if recorded == "" || resolveErr != nil || resolvedCwd != wantCwd {
				return sessionID, preview, cwd, false, nil
			}
		case "response_item":
			if payload == nil {
				continue
			}
			if role, _ := payload["role"].(string); role == "user" && preview == "" {
				preview = strings.TrimSpace(strings.ReplaceAll(jsonlutil.TextContent(payload["content"]), "\n", " "))
				if haveCwd {
					return sessionID, preview, cwd, true, scanner.Err()
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", "", "", false, err
	}
	return sessionID, preview, cwd, haveCwd, nil
}

func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}
