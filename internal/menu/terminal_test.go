package menu

import "testing"

func TestDetectTerminalWidthFallsBackToColumnsThenDefault(t *testing.T) {
	if ioctlWidth() > 0 {
		t.Skip("stdout is a terminal")
	}
	tests := []struct {
		name    string
		columns string
		want    int
	}{
		{"columns set", "123", 123},
		{"columns unset", "", 80},
		{"columns not a number", "wide", 80},
		{"columns zero", "0", 80},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("COLUMNS", tt.columns)
			if got := detectTerminalWidth(); got != tt.want {
				t.Fatalf("detectTerminalWidth() = %d, want %d", got, tt.want)
			}
		})
	}
}
