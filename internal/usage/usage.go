// Package usage reads Claude quota snapshot files written by a separate
// program and renders them as a table for the menu's "查看用量" view. It
// never queries an API, never touches credentials, and never launches a
// session -- it only reads files that already exist on disk.
package usage

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

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
// cannot be parsed, or carries an unsupported version is skipped rather
// than failing the whole load, so one bad file never hides the others.
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
	if snap.Version != supportedVersion {
		return Snapshot{}, false
	}
	return snap, true
}

// MatchLatest pairs each account with the most recently checked snapshot
// whose ConfigDir resolves to that account's Home on the filesystem (see
// registry.SameFile) rather than by spelling. An account with no matching
// snapshot is simply absent from the result.
func MatchLatest(accounts []registry.Account, snapshots []Snapshot) map[string]Snapshot {
	result := map[string]Snapshot{}
	for _, account := range accounts {
		var best Snapshot
		found := false
		for _, snap := range snapshots {
			if !registry.SameFile(snap.ConfigDir, account.Home) {
				continue
			}
			if !found || snap.CheckedAt.After(best.CheckedAt) {
				best = snap
				found = true
			}
		}
		if found {
			result[account.ID] = best
		}
	}
	return result
}

// Row is one rendered line of the usage table.
type Row struct {
	Label     string
	FiveHour  string
	SevenDay  string
	Freshness string
}

// BuildRows renders one Row per account row (in the given order), pairing
// each with its matched snapshot if any. now is the reference time for
// staleness and relative-age wording, passed in so tests are not tied to
// the wall clock.
func BuildRows(accountRows []struct{ ID, Label string }, matches map[string]Snapshot, now time.Time) []Row {
	rows := make([]Row, 0, len(accountRows))
	for _, ar := range accountRows {
		snap, ok := matches[ar.ID]
		if !ok {
			rows = append(rows, Row{Label: ar.Label, FiveHour: "–", SevenDay: "–", Freshness: "沒有資料"})
			continue
		}
		rows = append(rows, Row{
			Label:     ar.Label,
			FiveHour:  windowText(snap.FiveHour, snap.CheckedAt, fiveHourWindow, "5 小時", now),
			SevenDay:  windowText(snap.SevenDay, snap.CheckedAt, sevenDayWindow, "7 天", now),
			Freshness: freshnessText(snap.CheckedAt, now),
		})
	}
	return rows
}

// windowText renders one window's percentage, or explains why the number
// shown is not the current usage. A missing window, or one with no
// percentage in its snapshot, is never shown as 0%. A reset at or before
// now is called out instead of the stale percentage, since real usage is
// then almost certainly lower; when the snapshot carries no resets_at at
// all, the window's own nominal length still lets a old-enough checkedAt
// say the same thing -- a 5-hour window's snapshot older than 5 hours has
// certainly reset, and likewise for the 7-day window at 7 days.
func windowText(w *Window, checkedAt time.Time, length time.Duration, label string, now time.Time) string {
	if w == nil || w.UsedPercentage == nil {
		return "–"
	}
	if w.ResetsAt != nil {
		if !w.ResetsAt.After(now) {
			return fmt.Sprintf("已重置於 %s（原用量已過期）", w.ResetsAt.Local().Format("01/02 15:04"))
		}
		return fmt.Sprintf("%.0f%%（%s 重置）", *w.UsedPercentage, w.ResetsAt.Local().Format("01/02 15:04"))
	}
	if now.Sub(checkedAt) >= length {
		return fmt.Sprintf("已重置（資料逾 %s，原用量已過期）", label)
	}
	return fmt.Sprintf("%.0f%%", *w.UsedPercentage)
}

// freshnessText reports both the absolute local time a snapshot was taken
// and its relative age, so a stale row is never mistaken for a current one.
func freshnessText(checkedAt, now time.Time) string {
	return fmt.Sprintf("%s（%s）", checkedAt.Local().Format("01/02 15:04"), ageText(checkedAt, now))
}

// ageText treats a checkedAt after now as clock skew on the writing
// machine, not freshness, and calls it out rather than clamping the age to
// zero and reading as "剛剛" -- the lie this view exists to prevent.
func ageText(t, now time.Time) string {
	d := now.Sub(t)
	if d < 0 {
		return "時間異常（早於現在，來源主機時鐘可能不同步）"
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

// Fprint writes rows as a plain aligned table.
func Fprint(w io.Writer, rows []Row) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "帳號\t5 小時\t7 天\t資料時間")
	for _, row := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", row.Label, row.FiveHour, row.SevenDay, row.Freshness)
	}
	tw.Flush()
}
