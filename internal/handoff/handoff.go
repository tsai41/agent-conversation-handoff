// Package handoff creates immutable, read-only conversation handoff
// artifacts: a snapshot of the source JSONL, a rendered transcript, and a
// manifest, published atomically under <project>/.agent-handoffs/.
package handoff

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tsai41/agent-conversation-handoff/internal/jsonlutil"
)

const ParserVersion = "1"

var ProviderNames = map[string]string{"claude": "Claude", "codex": "Codex"}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func renderTranscript(raw []byte, sourceType string) string {
	var sections []string
	sections = append(sections, fmt.Sprintf("# %s Conversation Transcript", ProviderNames[sourceType]), "",
		"The complete source record is `source.jsonl` in this directory.", "")

	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		var entry map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		var kind string
		var content any
		if sourceType == "codex" {
			if entry["type"] != "response_item" {
				continue
			}
			payload, ok := entry["payload"].(map[string]any)
			if !ok {
				continue
			}
			kind, _ = payload["role"].(string)
			content = payload["content"]
		} else {
			kind, _ = entry["type"].(string)
			if message, ok := entry["message"].(map[string]any); ok {
				content = message["content"]
			} else {
				content = entry["content"]
			}
		}
		if kind != "user" && kind != "assistant" {
			continue
		}
		text := jsonlutil.TextContent(content)
		if strings.TrimSpace(text) == "" {
			continue
		}
		title := "Assistant"
		if kind == "user" {
			title = "User"
		}
		sections = append(sections, fmt.Sprintf("## %s (source line %d)", title, lineNumber), "", text, "")
	}
	return strings.Join(sections, "\n")
}

// Create snapshots sourcePath's raw content into a new
// targetPath/.agent-handoffs/<timestamp>-<hash>/ artifact directory
// (source.jsonl, transcript.md, manifest.json) and returns its path.
// The source file is only ever read, never modified.
func Create(sourcePath, targetPath string, sourceType string) (string, error) {
	source, err := filepath.Abs(sourcePath)
	if err != nil {
		return "", err
	}
	target, err := filepath.Abs(targetPath)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(source)
	if err != nil || info.IsDir() {
		return "", fmt.Errorf("source is not a file: %s", source)
	}

	raw, err := os.ReadFile(source)
	if err != nil {
		return "", err
	}
	sourceHash := sha256Hex(raw)

	artifactRoot := filepath.Join(target, ".agent-handoffs")
	if err := os.MkdirAll(artifactRoot, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(artifactRoot, 0o700); err != nil {
		return "", err
	}
	ignoreFile := filepath.Join(artifactRoot, ".gitignore")
	if _, err := os.Stat(ignoreFile); os.IsNotExist(err) {
		if err := os.WriteFile(ignoreFile, []byte("*\n"), 0o600); err != nil {
			return "", err
		}
	}
	os.Chmod(ignoreFile, 0o600)

	stamp := time.Now().UTC().Format("20060102T150405Z")
	artifactName := fmt.Sprintf("%s-%s", stamp, sourceHash[:12])
	destination := filepath.Join(artifactRoot, artifactName)
	if _, err := os.Stat(destination); err == nil {
		return "", fmt.Errorf("artifact already exists: %s", destination)
	}

	temp, err := os.MkdirTemp(artifactRoot, ".creating-")
	if err != nil {
		return "", err
	}
	cleanupTemp := true
	defer func() {
		if cleanupTemp {
			os.RemoveAll(temp)
		}
	}()

	snapshotPath := filepath.Join(temp, "source.jsonl")
	if err := os.WriteFile(snapshotPath, raw, 0o600); err != nil {
		return "", err
	}
	transcriptPath := filepath.Join(temp, "transcript.md")
	transcript := renderTranscript(raw, sourceType)
	if err := os.WriteFile(transcriptPath, []byte(transcript), 0o600); err != nil {
		return "", err
	}

	manifest := map[string]any{
		"artifact_version": 1,
		"parser_version":   ParserVersion,
		"created_at":       time.Now().UTC().Format(time.RFC3339Nano),
		"source": map[string]any{
			"path":   source,
			"type":   sourceType,
			"bytes":  len(raw),
			"sha256": sourceHash,
		},
		"files": map[string]any{
			"source.jsonl":  sha256Hex(raw),
			"transcript.md": sha256Hex([]byte(transcript)),
		},
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", err
	}
	manifestBytes = append(manifestBytes, '\n')
	if err := os.WriteFile(filepath.Join(temp, "manifest.json"), manifestBytes, 0o600); err != nil {
		return "", err
	}

	recheck, err := os.ReadFile(source)
	if err != nil {
		return "", err
	}
	if sha256Hex(recheck) != sourceHash {
		return "", fmt.Errorf("source changed while creating handoff; no artifact was published")
	}

	if err := os.Rename(temp, destination); err != nil {
		return "", err
	}
	cleanupTemp = false
	return destination, nil
}
