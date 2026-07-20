package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestBareInvocationDefaultsToMenu locks in that `ccs`/`ach` with no
// subcommand behaves like the old bash launcher, which always ran
// `ach menu --registry <default>` -- not a usage error.
func TestBareInvocationDefaultsToMenu(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ach")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = "."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}

	home := t.TempDir()
	fakeBin := filepath.Join(home, "bin")
	os.MkdirAll(fakeBin, 0o755)
	os.WriteFile(filepath.Join(fakeBin, "fzf"), []byte("#!/usr/bin/env bash\nexit 130\n"), 0o755)
	os.MkdirAll(filepath.Join(home, ".claude"), 0o755)

	cmd := exec.Command(binary)
	cmd.Env = append(os.Environ(), "HOME="+home, "PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()

	// A bare-invocation usage error would print "usage: ach ..." and
	// exit before ever touching fzf/the registry. Reaching the menu's
	// bootstrap flow (which calls our fake, cancelling fzf) proves the
	// default routed through menu.Run instead.
	if err == nil {
		t.Fatal("expected a non-zero exit (fzf cancelled the bootstrap prompt)")
	}
	if got := string(out); strings.Contains(got, "usage: ach") {
		t.Fatalf("bare invocation hit the old usage error instead of defaulting to menu: %s", got)
	}
}
