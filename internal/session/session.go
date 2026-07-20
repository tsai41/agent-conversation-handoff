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
	projectID := strings.ReplaceAll(absProject, "/", "-")
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
		sessionID, preview, err := readClaudeSession(file.path)
		if err != nil {
			continue
		}
		timestamp := file.startTime.Format("2006-01-02 15:04")
		candidates = append(candidates, Candidate{file.path, fmt.Sprintf("%s  %s  %s", timestamp, ShortID(sessionID), truncate(preview, 70))})
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

func readClaudeSession(path string) (sessionID string, preview string, err error) {
	file, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer file.Close()
	sessionID = "unknown UUID"
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var entry map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		if id, ok := entry["sessionId"].(string); ok && id != "" {
			sessionID = id
		} else if id, ok := entry["session_id"].(string); ok && id != "" {
			sessionID = id
		}
		if entry["type"] == "user" && preview == "" {
			preview = sessionText(entry)
		}
		if sessionID != "unknown UUID" && preview != "" {
			break
		}
	}
	return sessionID, preview, scanner.Err()
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
		sessionID, preview, matched, readErr := readCodexSession(path, resolvedProject)
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

	candidates := make([]Candidate, len(matches))
	for i, m := range matches {
		timestamp := m.startTime.Format("2006-01-02 15:04")
		candidates[i] = Candidate{m.path, fmt.Sprintf("%s  %s  %s", timestamp, ShortID(m.sessionID), truncate(m.preview, 70))}
	}
	return candidates, nil
}

func readCodexSession(path string, resolvedProject string) (sessionID string, preview string, matched bool, err error) {
	file, err := os.Open(path)
	if err != nil {
		return "", "", false, err
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
			cwd, _ := payload["cwd"].(string)
			haveCwd = true
			resolvedCwd, resolveErr := filepath.Abs(cwd)
			if cwd == "" || resolveErr != nil || resolvedCwd != resolvedProject {
				return sessionID, preview, false, nil
			}
		case "response_item":
			if payload == nil {
				continue
			}
			if role, _ := payload["role"].(string); role == "user" && preview == "" {
				preview = strings.TrimSpace(strings.ReplaceAll(jsonlutil.TextContent(payload["content"]), "\n", " "))
				if haveCwd {
					return sessionID, preview, true, scanner.Err()
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", "", false, err
	}
	if !haveCwd {
		return sessionID, preview, false, nil
	}
	return sessionID, preview, true, nil
}

func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}
