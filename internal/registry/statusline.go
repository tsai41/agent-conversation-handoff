package registry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// statuslineUsageDirFlag is the flag this package owns inside
// statusLine.command: its presence is what makes the status line program
// write quota snapshots at all.
const statuslineUsageDirFlag = "--usage-dir"

// statuslinePlaceholder marks the one thing this package can never fill in
// on its own: it has no way to know where a user's status line executable
// lives when statusLine is missing or unreadable.
const statuslinePlaceholder = "<path-to-your-status-line-executable>"

// StatuslineInfo is what the primary Claude account's shared settings
// document says about statusLine.command, resolved against the usage
// directory the usage view is currently reading. Command, Executable,
// HasUsageDir, UsageDir, and Matches are meaningful only when Refusal is
// empty: a non-empty Refusal means the document's shape was not one this
// package will parse or rewrite, and Snippet then holds what the user
// should paste in by hand instead.
type StatuslineInfo struct {
	Account      Account
	SettingsPath string
	Refusal      string
	Snippet      string
	Command      string
	Executable   string
	HasUsageDir  bool
	UsageDir     string
	Matches      bool
}

// StatuslineOutcome reports what an Enable/DisableStatuslineUsageDir call
// actually did.
type StatuslineOutcome int

const (
	StatuslineWrote           StatuslineOutcome = iota // backed up and rewritten
	StatuslineAlreadyEnabled                           // --usage-dir already present and correct
	StatuslineAlreadyDisabled                          // --usage-dir already absent
	StatuslineMismatch                                 // --usage-dir present but pointed elsewhere, replace not set
	StatuslineRefused                                  // document shape unsafe to rewrite; see Info.Refusal
)

// StatuslineChange is the result of an Enable/DisableStatuslineUsageDir
// call: the resulting state, what happened, and -- only when Outcome is
// StatuslineWrote -- where the pre-write backup was left.
type StatuslineChange struct {
	Info    StatuslineInfo
	Outcome StatuslineOutcome
	Backup  string
}

// ReadStatuslineInfo inspects the primary claude account's shared settings
// document (see PrimaryAccount, EntryPath) and reports what its
// statusLine.command currently does, without writing anything. usageDir is
// the directory the usage view is currently reading, compared against any
// --usage-dir already in the command. The returned error is non-nil only
// when there is no claude account to inspect at all, or the document
// exists but could not be read (e.g. permissions) -- every other
// unreadable shape is reported through StatuslineInfo.Refusal instead, so
// the state can still be shown.
func ReadStatuslineInfo(r Registry, usageDir string) (StatuslineInfo, error) {
	primary, found := PrimaryAccount(r, "claude")
	if !found {
		return StatuslineInfo{}, fmt.Errorf("no claude account is registered")
	}
	settingsPath := EntryPath(primary, SettingsEntry{Name: "settings.json", Kind: EntryFile})

	raw, err := os.ReadFile(settingsPath)
	if os.IsNotExist(err) {
		return StatuslineInfo{
			Account:      primary,
			SettingsPath: settingsPath,
			Refusal:      fmt.Sprintf("no settings document exists yet at %s", settingsPath),
			Snippet:      statuslineSnippet(usageDir),
		}, nil
	}
	if err != nil {
		return StatuslineInfo{}, err
	}
	return statuslineInfoFromDoc(primary, settingsPath, raw, usageDir), nil
}

// EnableStatuslineUsageDir adds "--usage-dir usageDir" to the primary
// claude account's statusLine.command so the status line program starts
// writing quota snapshots there, backing the document up first. Calling it
// again with the same usageDir is a no-op (StatuslineAlreadyEnabled). When
// a --usage-dir argument is already present pointing elsewhere, it refuses
// to overwrite it (StatuslineMismatch) unless replace is true.
func EnableStatuslineUsageDir(r Registry, usageDir string, replace bool) (StatuslineChange, error) {
	info, err := ReadStatuslineInfo(r, usageDir)
	if err != nil {
		return StatuslineChange{}, err
	}
	if info.Refusal != "" {
		return StatuslineChange{Info: info, Outcome: StatuslineRefused}, nil
	}
	if info.HasUsageDir && info.Matches {
		return StatuslineChange{Info: info, Outcome: StatuslineAlreadyEnabled}, nil
	}
	if info.HasUsageDir && !replace {
		return StatuslineChange{Info: info, Outcome: StatuslineMismatch}, nil
	}
	newCommand := setUsageDirArg(info.Command, usageDir)
	updatedRaw, backup, err := writeStatuslineCommand(info.SettingsPath, newCommand)
	if err != nil {
		return StatuslineChange{}, err
	}
	return StatuslineChange{
		Info:    statuslineInfoFromDoc(info.Account, info.SettingsPath, updatedRaw, usageDir),
		Outcome: StatuslineWrote,
		Backup:  backup,
	}, nil
}

// DisableStatuslineUsageDir removes any --usage-dir argument from the
// primary claude account's statusLine.command, backing the document up
// first. Calling it when no such argument is present is a no-op
// (StatuslineAlreadyDisabled).
func DisableStatuslineUsageDir(r Registry, usageDir string) (StatuslineChange, error) {
	info, err := ReadStatuslineInfo(r, usageDir)
	if err != nil {
		return StatuslineChange{}, err
	}
	if info.Refusal != "" {
		return StatuslineChange{Info: info, Outcome: StatuslineRefused}, nil
	}
	if !info.HasUsageDir {
		return StatuslineChange{Info: info, Outcome: StatuslineAlreadyDisabled}, nil
	}
	newCommand, _ := removeUsageDirArg(info.Command)
	updatedRaw, backup, err := writeStatuslineCommand(info.SettingsPath, newCommand)
	if err != nil {
		return StatuslineChange{}, err
	}
	return StatuslineChange{
		Info:    statuslineInfoFromDoc(info.Account, info.SettingsPath, updatedRaw, usageDir),
		Outcome: StatuslineWrote,
		Backup:  backup,
	}, nil
}

func statuslineInfoFromDoc(account Account, settingsPath string, raw []byte, usageDir string) StatuslineInfo {
	_, _, current, ok, reason := statuslineCommandSpan(raw)
	if !ok {
		return StatuslineInfo{
			Account:      account,
			SettingsPath: settingsPath,
			Refusal:      reason,
			Snippet:      statuslineSnippet(usageDir),
		}
	}
	cmd := parseCommandLine(current)
	return StatuslineInfo{
		Account:      account,
		SettingsPath: settingsPath,
		Command:      current,
		Executable:   cmd.Executable,
		HasUsageDir:  cmd.HasUsageDir,
		UsageDir:     cmd.UsageDir,
		Matches:      cmd.HasUsageDir && sameUsageDir(cmd.UsageDir, usageDir),
	}
}

func statuslineSnippet(usageDir string) string {
	return fmt.Sprintf(`  "statusLine": {
    "type": "command",
    "command": "%s --usage-dir %s",
    "padding": 0
  }`, statuslinePlaceholder, usageDir)
}

func sameUsageDir(existing, usageDir string) bool {
	resolved, err := resolveHome(existing)
	if err != nil {
		return false
	}
	return SameFile(resolved, usageDir)
}

// writeStatuslineCommand backs settingsPath up (via freeBackupPath and the
// original bytes), then replaces only its statusLine.command JSON string
// with newCommand and writes the result atomically (via writeFileAtomic).
// It returns the document's new bytes so the caller can report the
// resulting state without a second read.
func writeStatuslineCommand(settingsPath, newCommand string) (updated []byte, backup string, err error) {
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		return nil, "", err
	}
	info, err := os.Stat(settingsPath)
	if err != nil {
		return nil, "", err
	}
	start, end, _, ok, reason := statuslineCommandSpan(raw)
	if !ok {
		return nil, "", fmt.Errorf("%s", reason)
	}

	backup, err = freeBackupPath(settingsPath, EntryFile)
	if err != nil {
		return nil, "", err
	}
	if err := os.WriteFile(backup, raw, info.Mode().Perm()); err != nil {
		os.Remove(backup)
		return nil, "", err
	}

	literal, err := json.Marshal(newCommand)
	if err != nil {
		return nil, backup, err
	}
	updated = make([]byte, 0, len(raw)-(end-start)+len(literal))
	updated = append(updated, raw[:start]...)
	updated = append(updated, literal...)
	updated = append(updated, raw[end:]...)

	if err := writeFileAtomic(settingsPath, updated, info.Mode().Perm()); err != nil {
		return nil, backup, err
	}
	return updated, backup, nil
}

// statuslineCommandSpan locates statusLine.command's JSON string literal
// (quotes included) within a settings document's raw bytes, without
// decoding -- and therefore without reformatting -- anything else in the
// document. ok is false when statusLine is missing, statusLine is not an
// object, it has no command, or command is not a plain JSON string; reason
// then explains which. start and end are the literal's byte offsets within
// raw, valid only when ok is true.
func statuslineCommandSpan(raw []byte) (start, end int, current string, ok bool, reason string) {
	if !json.Valid(raw) {
		return 0, 0, "", false, "settings document is not valid JSON"
	}
	slStart, _, statusLineRaw, err := jsonRawValueSpan(raw, "statusLine")
	if err != nil {
		return 0, 0, "", false, "settings document has no statusLine"
	}
	cmdStart, cmdEnd, commandRaw, err := jsonRawValueSpan(statusLineRaw, "command")
	if err != nil {
		return 0, 0, "", false, "statusLine has no command, or statusLine is not an object"
	}
	if err := json.Unmarshal(commandRaw, &current); err != nil {
		return 0, 0, "", false, "statusLine.command is not a plain string"
	}
	return slStart + cmdStart, slStart + cmdEnd, current, true, ""
}

// jsonRawValueSpan locates key's value within a JSON object's raw bytes.
// start and end are the value's exact byte offsets within raw; value is a
// copy of the same bytes, exactly as encoding/json read them off the wire
// -- unreformatted -- which is what lets a caller splice a replacement into
// raw without disturbing anything outside [start,end). A key appearing more
// than once resolves to its last occurrence, matching how decoding the
// object into a map would treat duplicate keys.
func jsonRawValueSpan(raw []byte, key string) (start, end int, value json.RawMessage, err error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return 0, 0, nil, err
	}
	if delim, isDelim := tok.(json.Delim); !isDelim || delim != '{' {
		return 0, 0, nil, fmt.Errorf("not a JSON object")
	}
	found := false
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return 0, 0, nil, err
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return 0, 0, nil, err
		}
		if keyTok == key {
			end = int(dec.InputOffset())
			start = end - len(v)
			value = v
			found = true
		}
	}
	if !found {
		return 0, 0, nil, fmt.Errorf("key not found: %s", key)
	}
	return start, end, value, nil
}

// parsedCommand is a statusLine.command string as far as toggling
// --usage-dir needs to reason about it.
type parsedCommand struct {
	Executable  string
	HasUsageDir bool
	UsageDir    string
}

func parseCommandLine(command string) parsedCommand {
	var p parsedCommand
	if fields := strings.Fields(command); len(fields) > 0 {
		p.Executable = fields[0]
	}
	if arg, ok := findUsageDirArg(command); ok {
		p.HasUsageDir = true
		p.UsageDir = arg.value
	}
	return p
}

// usageDirArg is one --usage-dir flag found in a command string, as
// whitespace-delimited token spans so it can be edited by slicing the
// original string rather than by tokenizing and rejoining it (which would
// normalize whitespace it has no business touching).
type usageDirArg struct {
	flagStart, valueStart, valueEnd int
	value                           string
}

// findUsageDirArg finds statuslineUsageDirFlag as a whole token in command
// (bounded by whitespace or the string's ends, so it never matches inside a
// longer flag name) together with the whitespace-delimited token right
// after it. ok is false when no such flag, with a value, is present.
func findUsageDirArg(command string) (usageDirArg, bool) {
	searchFrom := 0
	for {
		rel := strings.Index(command[searchFrom:], statuslineUsageDirFlag)
		if rel < 0 {
			return usageDirArg{}, false
		}
		pos := searchFrom + rel
		flagEnd := pos + len(statuslineUsageDirFlag)
		boundedBefore := pos == 0 || isSpace(command[pos-1])
		boundedAfter := flagEnd == len(command) || isSpace(command[flagEnd])
		if boundedBefore && boundedAfter {
			valueStart := flagEnd
			for valueStart < len(command) && isSpace(command[valueStart]) {
				valueStart++
			}
			valueEnd := valueStart
			for valueEnd < len(command) && !isSpace(command[valueEnd]) {
				valueEnd++
			}
			if valueStart < valueEnd {
				return usageDirArg{pos, valueStart, valueEnd, command[valueStart:valueEnd]}, true
			}
		}
		searchFrom = flagEnd
	}
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

// setUsageDirArg returns command with its --usage-dir value set to dir,
// replacing an existing argument's value in place or appending a new
// "--usage-dir dir" at the end when none is present.
func setUsageDirArg(command, dir string) string {
	if arg, ok := findUsageDirArg(command); ok {
		return command[:arg.valueStart] + dir + command[arg.valueEnd:]
	}
	return strings.TrimRight(command, " \t") + " " + statuslineUsageDirFlag + " " + dir
}

// removeUsageDirArg returns command with its --usage-dir flag and value
// removed, along with one adjacent separator so the removal does not leave
// a double space. ok is false, command unchanged, when no such flag is
// present.
func removeUsageDirArg(command string) (string, bool) {
	arg, ok := findUsageDirArg(command)
	if !ok {
		return command, false
	}
	start, end := arg.flagStart, arg.valueEnd
	switch {
	case start > 0 && isSpace(command[start-1]):
		start--
	case end < len(command) && isSpace(command[end]):
		end++
	}
	return command[:start] + command[end:], true
}
