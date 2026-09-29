package usage

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tsai41/agent-conversation-handoff/internal/registry"
)

func TestSnapshotNameHashesConfigDirString(t *testing.T) {
	// Fixed vector: sha256 of the bare config_dir string, no trailing newline.
	got := SnapshotName("/Users/someone/.claude")
	want := "b38b2c3be42afda0015e8bbbedc71e3e37d8fecbf94421282d88845b38a653f7.json"
	if got != want {
		t.Fatalf("SnapshotName = %s, want %s", got, want)
	}
}

func TestRecord(t *testing.T) {
	now := time.Date(2026, 9, 29, 6, 28, 30, 0, time.UTC)
	const configDir = "/home/u/.claude"

	tests := []struct {
		name      string
		stdin     string
		wantWrite bool
		want      string
	}{
		{
			name:      "both windows",
			stdin:     `{"model":{"id":"x"},"rate_limits":{"five_hour":{"used_percentage":23.5,"resets_at":1738425600},"seven_day":{"used_percentage":41,"resets_at":1738857600}}}`,
			wantWrite: true,
			want:      `{"version":1,"config_dir":"/home/u/.claude","checked_at":"2026-09-29T06:28:30Z","five_hour":{"used_percentage":23.5,"resets_at":"2025-02-01T16:00:00Z"},"seven_day":{"used_percentage":41,"resets_at":"2025-02-06T16:00:00Z"}}`,
		},
		{
			name:      "seven_day missing",
			stdin:     `{"rate_limits":{"five_hour":{"used_percentage":10,"resets_at":1738425600}}}`,
			wantWrite: true,
			want:      `{"version":1,"config_dir":"/home/u/.claude","checked_at":"2026-09-29T06:28:30Z","five_hour":{"used_percentage":10,"resets_at":"2025-02-01T16:00:00Z"}}`,
		},
		{
			name:      "window without resets_at keeps its percentage",
			stdin:     `{"rate_limits":{"seven_day":{"used_percentage":7}}}`,
			wantWrite: true,
			want:      `{"version":1,"config_dir":"/home/u/.claude","checked_at":"2026-09-29T06:28:30Z","seven_day":{"used_percentage":7}}`,
		},
		{name: "window without percentage is dropped", stdin: `{"rate_limits":{"five_hour":{"resets_at":1738425600}}}`},
		{name: "out-of-range percentage is dropped", stdin: `{"rate_limits":{"five_hour":{"used_percentage":140},"seven_day":{"used_percentage":-1}}}`},
		{name: "no rate_limits", stdin: `{"model":{"id":"x"}}`},
		{name: "empty rate_limits", stdin: `{"rate_limits":{}}`},
		{name: "malformed json", stdin: `{"rate_limits":`},
		{name: "empty stdin", stdin: ``},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "usage")
			if err := Record(strings.NewReader(tt.stdin), dir, configDir, now); err != nil {
				t.Fatalf("Record returned error: %v", err)
			}
			raw, err := os.ReadFile(filepath.Join(dir, SnapshotName(configDir)))
			if !tt.wantWrite {
				if err == nil {
					t.Fatalf("expected no snapshot, got %s", raw)
				}
				if entries, _ := os.ReadDir(dir); len(entries) != 0 {
					t.Fatalf("expected an empty usage dir, got %d entries", len(entries))
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if string(raw) != tt.want {
				t.Fatalf("snapshot = %s\nwant       %s", raw, tt.want)
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 1 {
				t.Fatalf("expected only the snapshot in the dir (no temp leftovers), got %d entries", len(entries))
			}
		})
	}
}

func TestRecordOverwritesPreviousSnapshot(t *testing.T) {
	dir := t.TempDir()
	const configDir = "/home/u/.claude"
	now := time.Date(2026, 9, 29, 6, 0, 0, 0, time.UTC)
	for _, stdin := range []string{
		`{"rate_limits":{"five_hour":{"used_percentage":10}}}`,
		`{"rate_limits":{"five_hour":{"used_percentage":20}}}`,
	} {
		if err := Record(strings.NewReader(stdin), dir, configDir, now); err != nil {
			t.Fatal(err)
		}
	}
	snaps, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 1 || snaps[0].FiveHour == nil || *snaps[0].FiveHour.UsedPercentage != 20 {
		t.Fatalf("expected one snapshot holding the latest value, got %+v", snaps)
	}
}

func TestRecordReturnsErrorWhenUsageDirIsNotWritable(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	err := Record(strings.NewReader(`{"rate_limits":{"five_hour":{"used_percentage":1}}}`), filepath.Join(blocker, "usage"), "/x", time.Now())
	if err == nil {
		t.Fatal("expected a write failure to be reported")
	}
}

// What Record writes must be exactly what the menu's reader shows.
func TestRecordRoundTripsThroughBuildRows(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	now := time.Date(2026, 9, 29, 6, 28, 30, 0, time.UTC)
	stdin := `{"rate_limits":{"five_hour":{"used_percentage":24,"resets_at":` +
		strconv.FormatInt(now.Add(30*time.Minute).Unix(), 10) + `},"seven_day":{"used_percentage":3,"resets_at":` +
		strconv.FormatInt(now.Add(5*24*time.Hour).Unix(), 10) + `}}}`
	if err := Record(strings.NewReader(stdin), dir, home, now); err != nil {
		t.Fatal(err)
	}

	snaps, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	accounts := []registry.Account{{ID: "claude-1", Provider: "claude", Number: 1, Home: home}}
	matches := MatchLatest(accounts, snaps)
	rows := BuildRows([]struct{ ID, Label string }{{"claude-1", "Claude 1"}}, matches, now.Add(time.Minute))
	if len(rows) != 1 {
		t.Fatalf("expected one row, got %d", len(rows))
	}
	if !strings.HasPrefix(rows[0].FiveHour, "24%") || !strings.HasPrefix(rows[0].SevenDay, "3%") {
		t.Fatalf("expected recorded percentages in the row, got %+v", rows[0])
	}
	if !strings.Contains(rows[0].Freshness, "1 分鐘前") {
		t.Fatalf("expected the freshness to reflect checked_at, got %q", rows[0].Freshness)
	}
}

func TestRecordNormalisesConfigDirBeforeHashingAndWriting(t *testing.T) {
	home := t.TempDir()
	stdin := `{"rate_limits":{"five_hour":{"used_percentage":10}}}`
	tests := []struct {
		name      string
		configDir string
	}{
		{"clean", "/home/u/.claude"},
		{"trailing slash", "/home/u/.claude/"},
		{"dot-dot", "/home/u/x/../.claude"},
		{"double slash", "/home//u/.claude"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := Record(strings.NewReader(stdin), dir, tt.configDir, time.Now()); err != nil {
				t.Fatal(err)
			}
			snaps, err := LoadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(snaps) != 1 || snaps[0].ConfigDir != "/home/u/.claude" {
				t.Fatalf("expected one snapshot with the cleaned config_dir, got %+v", snaps)
			}
			if _, err := os.Stat(filepath.Join(dir, SnapshotName("/home/u/.claude"))); err != nil {
				t.Fatalf("expected the file named by the cleaned config_dir: %v", err)
			}
		})
	}

	t.Run("tilde expands to the home directory", func(t *testing.T) {
		t.Setenv("HOME", home)
		dir := t.TempDir()
		if err := Record(strings.NewReader(stdin), dir, "~/.claude", time.Now()); err != nil {
			t.Fatal(err)
		}
		snaps, err := LoadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(snaps) != 1 || snaps[0].ConfigDir != filepath.Join(home, ".claude") {
			t.Fatalf("expected ~ expanded, got %+v", snaps)
		}
	})
}

func TestRecordRenameFailureReturnsErrorAndLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	const configDir = "/home/u/.claude"
	if err := os.Mkdir(filepath.Join(dir, SnapshotName(configDir)), 0o755); err != nil {
		t.Fatal(err)
	}
	err := Record(strings.NewReader(`{"rate_limits":{"five_hour":{"used_percentage":1}}}`), dir, configDir, time.Now())
	if err == nil {
		t.Fatal("expected the failed rename to be reported")
	}
	leftovers, _ := filepath.Glob(filepath.Join(dir, ".snapshot-*.tmp"))
	if len(leftovers) != 0 {
		t.Fatalf("expected no temp file left behind, got %v", leftovers)
	}
}

func TestResolveRecordDir(t *testing.T) {
	writeRegistry := func(t *testing.T, body string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "accounts.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	stored := t.TempDir()
	tests := []struct {
		name         string
		registry     func(*testing.T) string
		flagExplicit bool
		want         string
	}{
		{"explicit flag wins", func(t *testing.T) string {
			return writeRegistry(t, `{"version":1,"next_number":{"claude":1,"codex":1},"accounts":[],"usage_dir":"`+stored+`"}`)
		}, true, "flag-dir"},
		{"stored setting beats the default", func(t *testing.T) string {
			return writeRegistry(t, `{"version":1,"next_number":{"claude":1,"codex":1},"accounts":[],"usage_dir":"`+stored+`"}`)
		}, false, stored},
		{"missing registry falls back", func(t *testing.T) string { return filepath.Join(t.TempDir(), "none.json") }, false, "flag-dir"},
		{"corrupt registry falls back", func(t *testing.T) string { return writeRegistry(t, `{not json`) }, false, "flag-dir"},
		{"no usage_dir falls back", func(t *testing.T) string {
			return writeRegistry(t, `{"version":1,"next_number":{"claude":1,"codex":1},"accounts":[],"x":0}`)
		}, false, "flag-dir"},
		{"schema-invalid registry falls back like the menu", func(t *testing.T) string { return writeRegistry(t, `{"version":1,"usage_dir":"`+stored+`"}`) }, false, "flag-dir"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveRecordDir(tt.registry(t), "flag-dir", tt.flagExplicit); got != tt.want {
				t.Fatalf("ResolveRecordDir = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRecordKeepsThePreviousSnapshotWhenNeitherWindowIsUsable(t *testing.T) {
	dir := t.TempDir()
	const configDir = "/home/u/.claude"
	now := time.Date(2026, 9, 29, 6, 0, 0, 0, time.UTC)
	for _, stdin := range []string{
		`{"rate_limits":{"five_hour":{"used_percentage":10}}}`,
		`{"rate_limits":{"five_hour":{"used_percentage":250},"seven_day":{"used_percentage":-3}}}`,
	} {
		if err := Record(strings.NewReader(stdin), dir, configDir, now); err != nil {
			t.Fatal(err)
		}
	}
	snaps, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 1 || snaps[0].FiveHour == nil || *snaps[0].FiveHour.UsedPercentage != 10 {
		t.Fatalf("expected the earlier valid snapshot to survive, got %+v", snaps)
	}
}

func TestRecordWritesNothingWhenConfigDirCannotBeResolved(t *testing.T) {
	t.Setenv("HOME", "")
	dir := filepath.Join(t.TempDir(), "usage")
	err := Record(strings.NewReader(`{"rate_limits":{"five_hour":{"used_percentage":10}}}`), dir, "~/.claude", time.Now())
	if err != nil {
		t.Fatalf("expected unusable input to be a silent no-op, got %v", err)
	}
	if _, statErr := os.Stat(dir); statErr == nil {
		t.Fatal("expected nothing written")
	}
}
