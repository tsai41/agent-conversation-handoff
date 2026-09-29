package usage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/tsai41/agent-conversation-handoff/internal/registry"
)

// statusLineInput is the subset of Claude Code's statusLine stdin JSON that
// Record reads. resets_at is Unix epoch seconds there, unlike the RFC3339
// string a snapshot carries.
type statusLineInput struct {
	RateLimits *struct {
		FiveHour *statusLineWindow `json:"five_hour"`
		SevenDay *statusLineWindow `json:"seven_day"`
	} `json:"rate_limits"`
}

type statusLineWindow struct {
	UsedPercentage *float64 `json:"used_percentage"`
	ResetsAt       *float64 `json:"resets_at"`
}

type snapshotFile struct {
	Version   int         `json:"version"`
	ConfigDir string      `json:"config_dir"`
	CheckedAt string      `json:"checked_at"`
	FiveHour  *windowFile `json:"five_hour,omitempty"`
	SevenDay  *windowFile `json:"seven_day,omitempty"`
}

type windowFile struct {
	UsedPercentage float64 `json:"used_percentage"`
	ResetsAt       string  `json:"resets_at,omitempty"`
}

// SnapshotName is the file name a snapshot for configDir is stored under:
// the hex SHA-256 of the config_dir string exactly as written into the
// file, plus ".json". Any writer that follows this scheme overwrites the
// same file for the same account instead of leaving a second one behind.
func SnapshotName(configDir string) string {
	sum := sha256.Sum256([]byte(configDir))
	return hex.EncodeToString(sum[:]) + ".json"
}

// ResolveRecordDir applies flag > registry usage_dir > built-in default for
// the writer. Unlike the menu's resolution it never loads the registry
// through registry.Load, which backs up an invalid file: a status line runs
// this on every refresh and must not have side effects on the registry.
// flagValue already holds the default when flagExplicit is false.
func ResolveRecordDir(registryPath, flagValue string, flagExplicit bool) string {
	if flagExplicit {
		return flagValue
	}
	if dir := registry.PeekUsageDir(registryPath); dir != "" {
		return dir
	}
	return flagValue
}

// Record reads one Claude Code statusLine JSON document from in and writes a
// version 1 snapshot for configDir into usageDir, atomically. configDir is
// normalised the way registry account homes are (~ expanded, absolute, cleaned)
// before it is hashed and written, so spellings of one directory share one file.
// Input that yields nothing to record (malformed JSON, no rate_limits, no window
// with a usable used_percentage, or a configDir that cannot be resolved) writes
// nothing and returns nil, so a status line piping into it is never broken by a
// payload it does not like. Only a failure to write returns an error.
func Record(in io.Reader, usageDir, configDir string, now time.Time) error {
	configDir, err := registry.ResolveHome(configDir)
	if err != nil {
		return nil
	}
	raw, err := io.ReadAll(in)
	if err != nil {
		return nil
	}
	var input statusLineInput
	if err := json.Unmarshal(raw, &input); err != nil || input.RateLimits == nil {
		return nil
	}
	snap := snapshotFile{
		Version:   supportedVersion,
		ConfigDir: configDir,
		CheckedAt: now.UTC().Format(time.RFC3339),
		FiveHour:  toWindowFile(input.RateLimits.FiveHour),
		SevenDay:  toWindowFile(input.RateLimits.SevenDay),
	}
	if snap.FiveHour == nil && snap.SevenDay == nil {
		return nil
	}
	data, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(usageDir, SnapshotName(configDir)), data)
}

func toWindowFile(w *statusLineWindow) *windowFile {
	if w == nil || w.UsedPercentage == nil {
		return nil
	}
	if p := *w.UsedPercentage; math.IsNaN(p) || p < 0 || p > 100 {
		return nil
	}
	out := &windowFile{UsedPercentage: *w.UsedPercentage}
	if w.ResetsAt != nil && !math.IsNaN(*w.ResetsAt) && !math.IsInf(*w.ResetsAt, 0) {
		out.ResetsAt = time.Unix(int64(*w.ResetsAt), 0).UTC().Format(time.RFC3339)
	}
	return out
}

// writeAtomic writes via a temp file in the same directory, named so that
// LoadDir (which only reads *.json) never sees a half-written snapshot.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".snapshot-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
