// Package usage reads Claude quota snapshot files written by a separate
// program and renders them as a table for the menu's "查看用量" view. It
// never queries an API, never touches credentials, and never launches a
// session -- it only reads files that already exist on disk.
package usage

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/tsai41/agent-conversation-handoff/internal/registry"
)

// supportedVersion is the only snapshot schema version this code
// understands. A file carrying any other version is treated the same as an
// unparseable one: the account shows as having no data. For whoever writes
// these files: an additive optional field keeps version 1 (readers here
// already treat unknown fields and absent windows as no data); anything
// that changes the meaning of an existing field is a deliberate break and
// must bump this number.
const supportedVersion = 1

// fiveHourWindow and sevenDayWindow are the nominal lengths of the two
// quota windows, used to infer that a window has reset even when its
// snapshot carries no resets_at.
const (
	fiveHourWindow = 5 * time.Hour
	sevenDayWindow = 7 * 24 * time.Hour
)

// Window is one quota window (5-hour or 7-day) read from a snapshot file.
// UsedPercentage is nil when the field was absent from the JSON, which
// BuildRows renders as no data rather than as 0%. ResetsAt is nil both when
// the field was absent and when it could not be parsed as RFC3339 -- a bad
// resets_at costs only itself, not the percentage beside it. See
// UnmarshalJSON.
type Window struct {
	UsedPercentage *float64
	ResetsAt       *time.Time

	// checkedAt is the checked_at of the snapshot this window was chosen
	// from, set by MatchLatest. Zero means the caller built the Window by
	// hand and the enclosing Snapshot's CheckedAt applies.
	checkedAt time.Time
}

// hasData reports whether w carries a usable percentage: present, finite,
// and within 0 to 100. Anything else counts as no data for that window.
func (w *Window) hasData() bool {
	if w == nil || w.UsedPercentage == nil {
		return false
	}
	p := *w.UsedPercentage
	return !math.IsNaN(p) && p >= 0 && p <= 100
}

// UnmarshalJSON parses resets_at leniently: a value that is not a valid
// RFC3339 timestamp leaves ResetsAt nil instead of failing, so a single bad
// field in one window does not cost the whole snapshot its percentage.
func (w *Window) UnmarshalJSON(data []byte) error {
	var raw struct {
		UsedPercentage *float64 `json:"used_percentage"`
		ResetsAt       *string  `json:"resets_at"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	w.UsedPercentage = raw.UsedPercentage
	w.ResetsAt = nil
	if raw.ResetsAt != nil {
		if t, err := time.Parse(time.RFC3339, *raw.ResetsAt); err == nil {
			w.ResetsAt = &t
		}
	}
	return nil
}

// Snapshot is one account's usage snapshot, matched to a registered account
// by ConfigDir. Either window may be nil. ConfigDir, Version, and CheckedAt
// are this snapshot's own identity and freshness -- unlike a window's
// resets_at, an unparseable CheckedAt fails decoding the whole Snapshot
// (ordinary strict JSON decoding already does this; there is no lenient
// path for it).
type Snapshot struct {
	Version   int       `json:"version"`
	ConfigDir string    `json:"config_dir"`
	CheckedAt time.Time `json:"checked_at"`
	FiveHour  *Window   `json:"five_hour,omitempty"`
	SevenDay  *Window   `json:"seven_day,omitempty"`
}

// LoadDir reads every *.json file in dir as a Snapshot. A missing directory
// yields no snapshots rather than an error -- that is the ordinary state
// before the writing program has ever run. A file that cannot be read,
// cannot be parsed, carries an unsupported version, has an empty or
// non-absolute config_dir, or has a zero checked_at is skipped rather than
// failing the whole load, so one bad file never hides the others.
func LoadDir(dir string) ([]Snapshot, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var snapshots []Snapshot
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		if snap, ok := readSnapshot(filepath.Join(dir, entry.Name())); ok {
			snapshots = append(snapshots, snap)
		}
	}
	return snapshots, nil
}

func readSnapshot(path string) (Snapshot, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Snapshot{}, false
	}
	var snap Snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return Snapshot{}, false
	}
	if snap.Version != supportedVersion || !filepath.IsAbs(snap.ConfigDir) || snap.CheckedAt.IsZero() {
		return Snapshot{}, false
	}
	return snap, true
}

// MatchLatest builds each account's snapshot from every snapshot whose
// ConfigDir resolves to that account's Home on the filesystem (see
// registry.SameFile) rather than by spelling. Each window is chosen on its
// own from all matching snapshots, without consulting the wall clock: the
// value with the latest resets_at wins, since that is the newest quota
// window; on equal resets_at the larger percentage wins, since usage only
// grows within a window (an idle session can keep rewriting an older, lower
// number under a newer checked_at). A candidate with a resets_at outranks one
// without; when neither has one, the latest checked_at wins. The returned
// CheckedAt is the oldest checked_at among the chosen windows, so the
// freshness shown never overstates how recent the numbers are. An account
// with no matching snapshot is simply absent from the result.
func MatchLatest(accounts []registry.Account, snapshots []Snapshot) map[string]Snapshot {
	result := map[string]Snapshot{}
	for _, account := range accounts {
		var matched []Snapshot
		for _, snap := range snapshots {
			if registry.SameFile(snap.ConfigDir, account.Home) {
				matched = append(matched, snap)
			}
		}
		if len(matched) == 0 {
			continue
		}
		newest := matched[0]
		for _, snap := range matched[1:] {
			if snap.CheckedAt.After(newest.CheckedAt) {
				newest = snap
			}
		}
		merged := Snapshot{
			Version:   supportedVersion,
			ConfigDir: newest.ConfigDir,
			FiveHour:  pickWindow(matched, func(s Snapshot) *Window { return s.FiveHour }),
			SevenDay:  pickWindow(matched, func(s Snapshot) *Window { return s.SevenDay }),
		}
		merged.CheckedAt = newest.CheckedAt
		var oldest time.Time
		for _, w := range []*Window{merged.FiveHour, merged.SevenDay} {
			if w != nil && (oldest.IsZero() || w.checkedAt.Before(oldest)) {
				oldest = w.checkedAt
			}
		}
		if !oldest.IsZero() {
			merged.CheckedAt = oldest
		}
		result[account.ID] = merged
	}
	return result
}

// pickWindow returns a copy of the best window among snaps, or nil when no
// snapshot has usable data for it.
func pickWindow(snaps []Snapshot, get func(Snapshot) *Window) *Window {
	var best *Window
	for _, snap := range snaps {
		w := get(snap)
		if !w.hasData() {
			continue
		}
		cand := *w
		cand.checkedAt = snap.CheckedAt
		if best == nil || windowBeats(&cand, best) {
			best = &cand
		}
	}
	return best
}

// windowBeats reports whether a should replace b. A candidate with a
// resets_at normally outranks one without, but not when the one without was
// observed at or after that resets_at: its window had already ended by then,
// so the newer observation is the current one.
func windowBeats(a, b *Window) bool {
	switch {
	case a.ResetsAt != nil && b.ResetsAt == nil:
		if b.checkedAt.Before(*a.ResetsAt) {
			return true
		}
	case a.ResetsAt == nil && b.ResetsAt != nil:
		if a.checkedAt.Before(*b.ResetsAt) {
			return false
		}
	case a.ResetsAt != nil && !a.ResetsAt.Equal(*b.ResetsAt):
		return a.ResetsAt.After(*b.ResetsAt)
	case a.ResetsAt != nil && *a.UsedPercentage != *b.UsedPercentage:
		return *a.UsedPercentage > *b.UsedPercentage
	}
	return a.checkedAt.After(b.checkedAt)
}

// Row is one account's display cells. Label carries no archived suffix:
// Archived is rendered as its own column in the table and as a suffix in the
// stacked layout.
type Row struct {
	Label     string
	Archived  bool
	FiveHour  string
	SevenDay  string
	Freshness string
}

const (
	noData           = "–"
	unsupportedText  = "不支援"
	noSnapshotText   = "沒有資料"
	resetText        = "已重置"
	clockSkewText    = "時間異常"
	archivedText     = "已封存"
	codexProvider    = "codex"
	columnGap        = 2
	stackedLabelCols = 6
)

// BuildRows renders one Row per account in registry.Rows order, pairing each
// with its matched snapshot if any. now is the reference time for staleness
// and relative wording, passed in so tests are not tied to the wall clock.
// Codex accounts never produce snapshots, so their windows read as
// unsupported rather than as missing data.
func BuildRows(r registry.Registry, matches map[string]Snapshot, now time.Time) []Row {
	accounts := make(map[string]registry.Account, len(r.Accounts))
	for _, account := range r.Accounts {
		accounts[account.ID] = account
	}
	accountRows := registry.Rows(r)
	rows := make([]Row, 0, len(accountRows))
	for _, ar := range accountRows {
		account := accounts[ar.ID]
		row := Row{
			Label:    strings.TrimSuffix(ar.Label, registry.ArchivedSuffix),
			Archived: account.Archived,
		}
		snap, ok := matches[ar.ID]
		switch {
		case account.Provider == codexProvider:
			row.FiveHour, row.SevenDay, row.Freshness = unsupportedText, unsupportedText, noData
		case !ok:
			row.FiveHour, row.SevenDay, row.Freshness = noData, noData, noSnapshotText
		default:
			row.FiveHour = windowText(snap.FiveHour, snap.CheckedAt, fiveHourWindow, now)
			row.SevenDay = windowText(snap.SevenDay, snap.CheckedAt, sevenDayWindow, now)
			row.Freshness = ageText(snap.CheckedAt, now)
		}
		rows = append(rows, row)
	}
	return rows
}

// windowText renders one window's percentage with its relative reset time,
// or explains why the number is not the current usage. A missing window, or
// one with no percentage in its snapshot, is never shown as 0%. A reset at or
// before now shows only the reset, never the stale percentage, since real
// usage is then almost certainly lower; when the snapshot carries no
// resets_at at all, the window's nominal length still lets an old-enough
// checkedAt say the same thing.
func windowText(w *Window, checkedAt time.Time, length time.Duration, now time.Time) string {
	if !w.hasData() {
		return noData
	}
	if !w.checkedAt.IsZero() {
		checkedAt = w.checkedAt
	}
	if w.ResetsAt != nil {
		if !w.ResetsAt.After(now) {
			return resetText
		}
		return fmt.Sprintf("%.0f%%  %s", *w.UsedPercentage, untilText(w.ResetsAt.Sub(now)))
	}
	if now.Sub(checkedAt) >= length {
		return resetText
	}
	return fmt.Sprintf("%.0f%%", *w.UsedPercentage)
}

// untilText words a positive duration as a coarse "reset in" phrase, rounding
// to the nearest unit and never below one.
func untilText(d time.Duration) string {
	if minutes := int(math.Ceil(d.Minutes())); minutes < 60 {
		return fmt.Sprintf("%d 分鐘後重置", max(minutes, 1))
	}
	if hours := int(math.Round(d.Hours())); hours < 24 {
		return fmt.Sprintf("%d 小時後重置", max(hours, 1))
	}
	return fmt.Sprintf("%d 天後重置", max(int(math.Round(d.Hours()/24)), 1))
}

// ageText treats a checkedAt after now as clock skew on the writing
// machine, not freshness, and calls it out rather than clamping the age to
// zero and reading as "剛剛" -- the lie this view exists to prevent.
func ageText(t, now time.Time) string {
	d := now.Sub(t)
	if d < 0 {
		return clockSkewText
	}
	switch {
	case d < time.Minute:
		return "剛剛"
	case d < time.Hour:
		return fmt.Sprintf("%d 分鐘前", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小時前", int(d/time.Hour))
	default:
		return fmt.Sprintf("%d 天前", int(d/(24*time.Hour)))
	}
}

// displayWidth is the number of terminal columns s occupies: East Asian
// Wide and Fullwidth runes and emoji take two, combining marks zero,
// everything else one.
func displayWidth(s string) int {
	n := 0
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Mn, r):
		case isWide(r):
			n += 2
		default:
			n++
		}
	}
	return n
}

func isWide(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115F, // Hangul Jamo
		r >= 0x2E80 && r <= 0x303E, // CJK radicals, symbols and punctuation
		r >= 0x3041 && r <= 0x33FF, // kana, CJK compatibility
		r >= 0x3400 && r <= 0x4DBF,
		r >= 0x4E00 && r <= 0x9FFF,
		r >= 0xA000 && r <= 0xA4CF,
		r >= 0xAC00 && r <= 0xD7A3, // Hangul syllables
		r >= 0xF900 && r <= 0xFAFF,
		r >= 0xFE30 && r <= 0xFE6F,
		r >= 0xFF00 && r <= 0xFF60, // fullwidth forms
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x1F300 && r <= 0x1F64F, // emoji
		r >= 0x1F900 && r <= 0x1F9FF,
		r >= 0x2600 && r <= 0x27BF,
		r >= 0x20000 && r <= 0x3FFFD:
		return true
	}
	return false
}

func padRight(s string, width int) string {
	if gap := width - displayWidth(s); gap > 0 {
		return s + strings.Repeat(" ", gap)
	}
	return s
}

// Fprint writes rows as an aligned table, or as stacked per-account blocks
// when the table would be wider than width terminal columns. Cells are padded
// by display width, so CJK text lines up; tabwriter counts bytes and cannot.
// The status column is left out unless some row is archived.
func Fprint(w io.Writer, rows []Row, width int) {
	showStatus := slices.ContainsFunc(rows, func(r Row) bool { return r.Archived })
	toCells := func(fields ...string) []string {
		if !showStatus {
			return slices.Delete(fields, 1, 2)
		}
		return fields
	}
	cells := make([][]string, 0, len(rows)+1)
	cells = append(cells, toCells("帳號", "狀態", "5 小時", "7 天", "更新"))
	for _, row := range rows {
		status := ""
		if row.Archived {
			status = archivedText
		}
		cells = append(cells, toCells(row.Label, status, row.FiveHour, row.SevenDay, row.Freshness))
	}
	colWidths := make([]int, len(cells[0]))
	for _, line := range cells {
		for i, cell := range line {
			colWidths[i] = max(colWidths[i], displayWidth(cell))
		}
	}
	total := columnGap * (len(colWidths) - 1)
	for _, cw := range colWidths {
		total += cw
	}
	if total > width {
		fprintStacked(w, rows)
		return
	}
	gap := strings.Repeat(" ", columnGap)
	for _, line := range cells {
		out := make([]string, len(line))
		for i, cell := range line {
			out[i] = padRight(cell, colWidths[i])
		}
		fmt.Fprintln(w, strings.TrimRight(strings.Join(out, gap), " "))
	}
}

func fprintStacked(w io.Writer, rows []Row) {
	gap := strings.Repeat(" ", columnGap)
	for i, row := range rows {
		if i > 0 {
			fmt.Fprintln(w)
		}
		title := row.Label
		if row.Archived {
			title += registry.ArchivedSuffix
		}
		fmt.Fprintln(w, title)
		for _, line := range [][2]string{{"5 小時", row.FiveHour}, {"7 天", row.SevenDay}, {"更新", row.Freshness}} {
			fmt.Fprintf(w, "  %s%s%s\n", padRight(line[0], stackedLabelCols), gap, line[1])
		}
	}
}
