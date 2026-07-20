// Package jsonlutil holds small helpers shared by the session and handoff
// packages for pulling human-readable text out of Claude/Codex JSONL entries.
package jsonlutil

import "strings"

// TextContent extracts plain text from a Claude/Codex message "content"
// field, which is either a string or a list of {"text": "..."} /
// {"content": "..."} blocks.
func TextContent(content any) string {
	if text, ok := content.(string); ok {
		return text
	}
	items, ok := content.([]any)
	if !ok {
		return ""
	}
	var parts []string
	for _, item := range items {
		block, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if text, ok := block["text"].(string); ok {
			parts = append(parts, text)
			continue
		}
		if text, ok := block["content"].(string); ok {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}
