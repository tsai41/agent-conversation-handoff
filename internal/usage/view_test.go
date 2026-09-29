package usage

import (
	"bytes"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tsai41/agent-conversation-handoff/internal/registry"
)

func TestDisplayWidth(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int
	}{
		{"empty", "", 0},
		{"ascii", "Claude 1", 8},
		{"cjk", "帳號", 4},
		{"fullwidth punctuation", "（）「」、。", 12},
		{"fullwidth latin", "ＡＢ", 4},
		{"hangul", "한글", 4},
		{"mixed", "5 小時", 6},
		{"en dash and middle dot count one", "–·", 2},
		{"emoji pictograph", "😀", 2},
		{"emoji supplemental", "🤖", 2},
		{"misc symbols emoji", "☀✅", 4},
		{"combining mark is zero width", "e\u0301", 1},
		{"combining mark after cjk", "字\u0301", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := displayWidth(tt.in); got != tt.want {
				t.Fatalf("displayWidth(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

var viewNow = time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)

func at(d time.Duration) *time.Time {
	t := viewNow.Add(d)
	return &t
}

func claudeRegistry() registry.Registry {
	return registry.Registry{Accounts: []registry.Account{{ID: "claude-1", Provider: "claude", Number: 1, Home: "/h"}}}
}

func TestBuildRowsCellText(t *testing.T) {
	tests := []struct {
		name          string
		snap          *Snapshot
		wantFive      string
		wantSeven     string
		wantFreshness string
	}{
		{
			name:          "no snapshot",
			snap:          nil,
			wantFive:      "–",
			wantSeven:     "–",
			wantFreshness: "沒有資料",
		},
		{
			name: "reset in minutes",
			snap: &Snapshot{CheckedAt: viewNow.Add(-30 * time.Second),
				FiveHour: &Window{UsedPercentage: pct(45), ResetsAt: at(20*time.Minute + 10*time.Second)}},
			wantFive:      "45%  21 分鐘後重置",
			wantSeven:     "–",
			wantFreshness: "剛剛",
		},
		{
			name: "under a minute rounds up to one",
			snap: &Snapshot{CheckedAt: viewNow.Add(-5 * time.Minute),
				FiveHour: &Window{UsedPercentage: pct(45), ResetsAt: at(10 * time.Second)}},
			wantFive:      "45%  1 分鐘後重置",
			wantSeven:     "–",
			wantFreshness: "5 分鐘前",
		},
		{
			name: "just under an hour reads as one hour",
			snap: &Snapshot{CheckedAt: viewNow.Add(-2 * time.Hour),
				FiveHour: &Window{UsedPercentage: pct(45), ResetsAt: at(59*time.Minute + 30*time.Second)}},
			wantFive:      "45%  1 小時後重置",
			wantSeven:     "–",
			wantFreshness: "2 小時前",
		},
		{
			name: "reset in hours",
			snap: &Snapshot{CheckedAt: viewNow.Add(-3 * 24 * time.Hour),
				FiveHour: &Window{UsedPercentage: pct(45), ResetsAt: at(2 * time.Hour)}},
			wantFive:      "45%  2 小時後重置",
			wantSeven:     "–",
			wantFreshness: "3 天前",
		},
		{
			name: "reset in days",
			snap: &Snapshot{CheckedAt: viewNow,
				SevenDay: &Window{UsedPercentage: pct(98), ResetsAt: at(36 * time.Hour)}},
			wantFive:      "–",
			wantSeven:     "98%  2 天後重置",
			wantFreshness: "剛剛",
		},
		{
			name: "just under a day reads as one day",
			snap: &Snapshot{CheckedAt: viewNow,
				SevenDay: &Window{UsedPercentage: pct(98), ResetsAt: at(23*time.Hour + 50*time.Minute)}},
			wantFive:      "–",
			wantSeven:     "98%  1 天後重置",
			wantFreshness: "剛剛",
		},
		{
			name: "reset passed",
			snap: &Snapshot{CheckedAt: viewNow.Add(-time.Hour),
				FiveHour: &Window{UsedPercentage: pct(55), ResetsAt: at(-30 * time.Minute)}},
			wantFive:      "已重置",
			wantSeven:     "–",
			wantFreshness: "1 小時前",
		},
		{
			name: "reset exactly now",
			snap: &Snapshot{CheckedAt: viewNow.Add(-time.Hour),
				FiveHour: &Window{UsedPercentage: pct(55), ResetsAt: at(0)}},
			wantFive:      "已重置",
			wantSeven:     "–",
			wantFreshness: "1 小時前",
		},
		{
			name: "reset inferred from window length",
			snap: &Snapshot{CheckedAt: viewNow.Add(-6 * time.Hour),
				FiveHour: &Window{UsedPercentage: pct(55)},
				SevenDay: &Window{UsedPercentage: pct(80)}},
			wantFive:      "已重置",
			wantSeven:     "80%",
			wantFreshness: "6 小時前",
		},
		{
			name:          "empty window object is no data",
			snap:          &Snapshot{CheckedAt: viewNow, FiveHour: &Window{}},
			wantFive:      "–",
			wantSeven:     "–",
			wantFreshness: "剛剛",
		},
		{
			name:          "out of range percentage is no data",
			snap:          &Snapshot{CheckedAt: viewNow, FiveHour: &Window{UsedPercentage: pct(100.5)}, SevenDay: &Window{UsedPercentage: pct(math.NaN())}},
			wantFive:      "–",
			wantSeven:     "–",
			wantFreshness: "剛剛",
		},
		{
			name: "checked_at in the future",
			snap: &Snapshot{CheckedAt: viewNow.Add(time.Hour),
				FiveHour: &Window{UsedPercentage: pct(40)}},
			wantFive:      "40%",
			wantSeven:     "–",
			wantFreshness: "時間異常",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matches := map[string]Snapshot{}
			if tt.snap != nil {
				matches["claude-1"] = *tt.snap
			}
			rows := BuildRows(claudeRegistry(), matches, viewNow)
			if len(rows) != 1 {
				t.Fatalf("expected one row, got %d", len(rows))
			}
			got := rows[0]
			if got.FiveHour != tt.wantFive || got.SevenDay != tt.wantSeven || got.Freshness != tt.wantFreshness {
				t.Fatalf("cells = %q / %q / %q, want %q / %q / %q",
					got.FiveHour, got.SevenDay, got.Freshness, tt.wantFive, tt.wantSeven, tt.wantFreshness)
			}
		})
	}
}

func TestBuildRowsShowsCodexAsUnsupportedEvenWithAMatchingSnapshot(t *testing.T) {
	r := registry.Registry{Accounts: []registry.Account{{ID: "codex-1", Provider: "codex", Number: 1, Home: "/c"}}}
	matches := map[string]Snapshot{"codex-1": {CheckedAt: viewNow, FiveHour: &Window{UsedPercentage: pct(40)}}}
	for name, m := range map[string]map[string]Snapshot{"no snapshot": nil, "with snapshot": matches} {
		t.Run(name, func(t *testing.T) {
			rows := BuildRows(r, m, viewNow)
			if rows[0].FiveHour != "不支援" || rows[0].SevenDay != "不支援" || rows[0].Freshness != "–" {
				t.Fatalf("unexpected codex row: %+v", rows[0])
			}
		})
	}
}

func TestBuildRowsSplitsArchivedFromTheLabelAndKeepsItLast(t *testing.T) {
	r := registry.Registry{Accounts: []registry.Account{
		{ID: "claude-1", Provider: "claude", Number: 1, Home: "/1", Archived: true},
		{ID: "claude-2", Provider: "claude", Number: 2, Home: "/2", Alias: "main"},
	}}
	rows := BuildRows(r, nil, viewNow)
	if len(rows) != 2 {
		t.Fatalf("expected two rows, got %d", len(rows))
	}
	if rows[0].Label != "Claude · 2 · main" || rows[0].Archived {
		t.Fatalf("unexpected first row: %+v", rows[0])
	}
	if rows[1].Label != "Claude · 1" || !rows[1].Archived {
		t.Fatalf("expected the archived row last, without the suffix: %+v", rows[1])
	}
}

func sampleRows() []Row {
	return []Row{
		{Label: "Claude · 2 · main", FiveHour: "已重置", SevenDay: "98%  1 天後重置", Freshness: "2 小時前"},
		{Label: "Codex", FiveHour: "不支援", SevenDay: "不支援", Freshness: "–"},
		{Label: "Claude · 1", Archived: true, FiveHour: "–", SevenDay: "–", Freshness: "沒有資料"},
	}
}

func TestFprintTableAlignsEveryColumnByDisplayWidth(t *testing.T) {
	var buf bytes.Buffer
	Fprint(&buf, sampleRows(), 200)
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected header plus three rows, got %d lines:\n%s", len(lines), buf.String())
	}
	cells := [][]string{
		{"帳號", "狀態", "5 小時", "7 天", "更新"},
		{"Claude · 2 · main", "", "已重置", "98%  1 天後重置", "2 小時前"},
		{"Codex", "", "不支援", "不支援", "–"},
		{"Claude · 1", "已封存", "–", "–", "沒有資料"},
	}
	widths := make([]int, 5)
	for _, row := range cells {
		for i, cell := range row {
			widths[i] = max(widths[i], displayWidth(cell))
		}
	}
	starts := make([]int, 5)
	for i := 1; i < 5; i++ {
		starts[i] = starts[i-1] + widths[i-1] + columnGap
	}
	for li, line := range lines {
		off := 0
		for ci := 1; ci < 5; ci++ {
			want := cells[li][ci]
			if want == "" {
				continue
			}
			idx := strings.Index(line[off:], want)
			if idx < 0 {
				t.Fatalf("line %d %q lacks %q", li, line, want)
			}
			off += idx
			if got := displayWidth(line[:off]); got != starts[ci] {
				t.Fatalf("line %d: column %d starts at display column %d, want %d\n%s", li, ci, got, starts[ci], buf.String())
			}
			off += len(want)
		}
		if strings.HasSuffix(line, " ") {
			t.Fatalf("line %d has trailing spaces: %q", li, line)
		}
	}
	if strings.Contains(buf.String(), "（已封存）") {
		t.Fatalf("table mode should use the status column, not the suffix:\n%s", buf.String())
	}
}

func tableWidth(t *testing.T, rows []Row) int {
	t.Helper()
	var buf bytes.Buffer
	Fprint(&buf, rows, math.MaxInt32)
	w := 0
	for _, line := range strings.Split(buf.String(), "\n") {
		w = max(w, displayWidth(line))
	}
	return w
}

func TestFprintOmitsTheStatusColumnWhenNoRowIsArchived(t *testing.T) {
	rows := sampleRows()[:2]
	var buf bytes.Buffer
	Fprint(&buf, rows, 200)
	want := "帳號               5 小時  7 天             更新\n" +
		"Claude · 2 · main  已重置  98%  1 天後重置  2 小時前\n" +
		"Codex              不支援  不支援           –\n"
	if buf.String() != want {
		t.Fatalf("table mismatch\ngot:\n%s\nwant:\n%s", buf.String(), want)
	}
	if strings.Contains(buf.String(), "狀態") {
		t.Fatalf("status column should be absent:\n%s", buf.String())
	}
}

func TestFprintKeepsTheNarrowTableWhenTheOmittedStatusColumnMakesItFit(t *testing.T) {
	rows := sampleRows()[:2]
	without := tableWidth(t, rows)
	withArchived := append(slices.Clone(rows), Row{Label: "Claude · 1", Archived: true, FiveHour: "–", SevenDay: "–", Freshness: "–"})
	if tableWidth(t, withArchived) <= without {
		t.Fatalf("archived row should widen the table")
	}
	var buf bytes.Buffer
	Fprint(&buf, rows, without)
	if strings.Contains(buf.String(), "\n  5 小時  ") {
		t.Fatalf("expected the table at width %d:\n%s", without, buf.String())
	}
}

func TestFprintSwitchesToStackedBlocksWhenTheTableDoesNotFit(t *testing.T) {
	rows := sampleRows()
	full := tableWidth(t, rows)
	tests := []struct {
		name        string
		width       int
		wantStacked bool
	}{
		{"wider than needed", full + 20, false},
		{"exact boundary fits", full, false},
		{"one column short", full - 1, true},
		{"narrow", 50, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			Fprint(&buf, rows, tt.width)
			stacked := strings.Contains(buf.String(), "\n  5 小時  ")
			if stacked != tt.wantStacked {
				t.Fatalf("stacked = %v, want %v at width %d:\n%s", stacked, tt.wantStacked, tt.width, buf.String())
			}
		})
	}
}

func TestFprintStackedLayout(t *testing.T) {
	var buf bytes.Buffer
	Fprint(&buf, sampleRows(), 10)
	want := `Claude · 2 · main
  5 小時  已重置
  7 天    98%  1 天後重置
  更新    2 小時前

Codex
  5 小時  不支援
  7 天    不支援
  更新    –

Claude · 1（已封存）
  5 小時  –
  7 天    –
  更新    沒有資料
`
	if buf.String() != want {
		t.Fatalf("stacked output mismatch\ngot:\n%s\nwant:\n%s", buf.String(), want)
	}
}
