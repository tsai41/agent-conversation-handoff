package menu

import (
	"os"
	"strconv"
)

const defaultTerminalWidth = 80

// terminalWidth is a variable so tests can pin the width.
var terminalWidth = detectTerminalWidth

// detectTerminalWidth asks the terminal on stdout, then falls back to
// $COLUMNS, then to 80.
func detectTerminalWidth() int {
	if cols := ioctlWidth(); cols > 0 {
		return cols
	}
	if cols, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && cols > 0 {
		return cols
	}
	return defaultTerminalWidth
}
