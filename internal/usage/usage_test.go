package usage

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsai41/agent-conversation-handoff/internal/registry"
)

func writeSnapshotFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadDirSkipsUnknownVersionAndCorruptFilesWithoutHidingOthers(t *testing.T) {
	dir := t.TempDir()
	writeSnapshotFile(t, dir, "good.json", `{
		"version": 1,
		"config_dir": "/home/.claude",
		"checked_at": "2026-09-04T07:12:33Z",
		"five_hour": {"used_percentage": 55.0}
	}`)
	writeSnapshotFile(t, dir, "future-version.json", `{
		"version": 2,
		"config_dir": "/home/.claude-2",
		"checked_at": "2026-09-04T07:12:33Z",
		"five_hour": {"used_percentage": 10.0}
	}`)
	writeSnapshotFile(t, dir, "corrupt.json", `{not json`)

	snapshots, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("expected only the one valid snapshot to survive, got %d: %+v", len(snapshots), snapshots)
	}
	if snapshots[0].ConfigDir != "/home/.claude" {
		t.Fatalf("expected the valid snapshot's config_dir, got %q", snapshots[0].ConfigDir)
	}
}

func TestLoadDirOnAMissingDirectoryIsNotAnError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")

	snapshots, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("expected no error for a missing snapshot directory, got %v", err)
	}
	if snapshots != nil {
		t.Fatalf("expected no snapshots, got %+v", snapshots)
	}
}

func TestMatchLatestFollowsASymlinkedHomeToTheSnapshotsConfigDir(t *testing.T) {
	home := t.TempDir()
	realHome := filepath.Join(home, "real-claude")
	if err := os.MkdirAll(realHome, 0o755); err != nil {
		t.Fatal(err)
	}
	linkedHome := filepath.Join(home, "linked-claude")
	if err := os.Symlink(realHome, linkedHome); err != nil {
		t.Fatal(err)
	}

	accounts := []registry.Account{{ID: "claude-1", Provider: "claude", Number: 1, Home: linkedHome}}
	snapshots := []Snapshot{{Version: 1, ConfigDir: realHome, CheckedAt: time.Now()}}

	matches := MatchLatest(accounts, snapshots)
	if _, ok := matches["claude-1"]; !ok {
		t.Fatalf("expected the snapshot to match through the symlink, got %+v", matches)
	}
}

func TestBuildRowsCallsOutAResetsAtAlreadyInThePast(t *testing.T) {
	now := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	past := now.Add(-30 * time.Minute)
	accountRows := []struct{ ID, Label string }{{"claude-1", "Claude"}}
	matches := map[string]Snapshot{
		"claude-1": {
			CheckedAt: now.Add(-1 * time.Hour),
			FiveHour:  &Window{UsedPercentage: 55.0, ResetsAt: &past},
		},
	}

	rows := BuildRows(accountRows, matches, now)
	if len(rows) != 1 {
		t.Fatalf("expected one row, got %d", len(rows))
	}
	if strings.Contains(rows[0].FiveHour, "55%") {
		t.Fatalf("expected the stale percentage not to be shown as current, got %q", rows[0].FiveHour)
	}
	if !strings.Contains(rows[0].FiveHour, "已重置") {
		t.Fatalf("expected the window to be called out as reset, got %q", rows[0].FiveHour)
	}
}

func TestBuildRowsNeverRendersAMissingWindowAsZeroPercent(t *testing.T) {
	now := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	accountRows := []struct{ ID, Label string }{{"claude-1", "Claude"}}
	matches := map[string]Snapshot{
		"claude-1": {CheckedAt: now, FiveHour: &Window{UsedPercentage: 40.0}},
	}

	rows := BuildRows(accountRows, matches, now)
	if strings.Contains(rows[0].SevenDay, "0%") {
		t.Fatalf("expected a missing window not to render as 0%%, got %q", rows[0].SevenDay)
	}
}

func TestBuildRowsMarksAnAccountWithNoSnapshotAtAll(t *testing.T) {
	now := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	accountRows := []struct{ ID, Label string }{
		{"claude-1", "Claude"},
		{"claude-2", "Claude · 2"},
	}
	matches := map[string]Snapshot{
		"claude-1": {CheckedAt: now, FiveHour: &Window{UsedPercentage: 40.0}},
	}

	rows := BuildRows(accountRows, matches, now)
	if len(rows) != 2 {
		t.Fatalf("expected both accounts to be listed, got %d", len(rows))
	}
	if rows[1].Freshness != "沒有資料" {
		t.Fatalf("expected the unmatched account to be marked as having no data, got %q", rows[1].Freshness)
	}
	if strings.Contains(rows[1].FiveHour, "0%") || strings.Contains(rows[1].SevenDay, "0%") {
		t.Fatalf("expected no-data windows not to render as 0%%, got %+v", rows[1])
	}
	// The matched account must still be present and correct: one missing
	// snapshot must not hide the other account's row.
	if !strings.Contains(rows[0].FiveHour, "40%") {
		t.Fatalf("expected the matched account's row to be unaffected, got %q", rows[0].FiveHour)
	}
}

func TestFprintRendersAllRows(t *testing.T) {
	rows := []Row{
		{Label: "Claude", FiveHour: "55%", SevenDay: "92%", Freshness: "3 分鐘前"},
		{Label: "Codex", FiveHour: "–", SevenDay: "–", Freshness: "沒有資料"},
	}
	var buf bytes.Buffer
	Fprint(&buf, rows)
	out := buf.String()
	for _, want := range []string{"Claude", "Codex", "55%", "92%", "3 分鐘前", "沒有資料"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected output to contain %q, got: %s", want, out)
		}
	}
}
