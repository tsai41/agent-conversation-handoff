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
// unparseable one: the account shows as having no data.
const supportedVersion = 1

// Window is one quota window (5-hour or 7-day) read from a snapshot file.
type Window struct {
	UsedPercentage float64    `json:"used_percentage"`
	ResetsAt       *time.Time `json:"resets_at,omitempty"`
}

// Snapshot is one account's usage snapshot, matched to a registered account
// by ConfigDir. Either window may be nil.
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
			FiveHour:  windowText(snap.FiveHour, now),
			SevenDay:  windowText(snap.SevenDay, now),
			Freshness: freshnessText(snap.CheckedAt, now),
		})
	}
	return rows
}

// windowText renders one window's percentage, or explains why the number
// shown is not the current usage: a missing window is never shown as 0%,
// and a window whose reset time has already passed shows the reset instead
// of the stale percentage, since real usage is then almost certainly lower.
func windowText(w *Window, now time.Time) string {
	if w == nil {
		return "–"
	}
	if w.ResetsAt != nil && w.ResetsAt.Before(now) {
		return fmt.Sprintf("已重置於 %s（原用量已過期）", w.ResetsAt.Local().Format("01/02 15:04"))
	}
	text := fmt.Sprintf("%.0f%%", w.UsedPercentage)
	if w.ResetsAt != nil {
		text += fmt.Sprintf("（%s 重置）", w.ResetsAt.Local().Format("01/02 15:04"))
	}
	return text
}

// freshnessText reports both the absolute local time a snapshot was taken
// and its relative age, so a stale row is never mistaken for a current one.
func freshnessText(checkedAt, now time.Time) string {
	return fmt.Sprintf("%s（%s）", checkedAt.Local().Format("01/02 15:04"), ageText(checkedAt, now))
}

func ageText(t, now time.Time) string {
	d := now.Sub(t)
	if d < 0 {
		d = 0
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
