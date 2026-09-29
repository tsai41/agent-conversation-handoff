package uninstall

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsai41/agent-conversation-handoff/internal/registry"
)

const stamp = "20260101-101010"

type fixture struct {
	root, home, config, regPath, acc1, acc2, elsewhere, bin, fakeTmp string
}

func write(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func link(t *testing.T, target, path string) {
	t.Helper()
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
}

func writeRegistry(t *testing.T, path, usageDir string, homes ...string) {
	t.Helper()
	reg := registry.Empty()
	reg.NextNumber["claude"] = len(homes) + 1
	reg.UsageDir = usageDir
	for i, home := range homes {
		n := i + 1
		reg.Accounts = append(reg.Accounts, registry.Account{ID: "claude-" + string(rune('0'+n)), Provider: "claude", Number: n, Home: home})
	}
	raw, err := json.Marshal(reg)
	if err != nil {
		t.Fatal(err)
	}
	write(t, path, string(raw), 0o600)
}

// newFixture lays out two claude accounts (acc2 shares with acc1), a foreign
// link, tool backups, decoys, a config dir and a fake installed binary, all
// under one temp root with HOME pointed inside it.
func newFixture(t *testing.T, usageDir string) fixture {
	t.Helper()
	root := t.TempDir()
	f := fixture{
		root:      root,
		home:      filepath.Join(root, "home"),
		config:    filepath.Join(root, "config"),
		acc1:      filepath.Join(root, "acc1"),
		acc2:      filepath.Join(root, "acc2"),
		elsewhere: filepath.Join(root, "elsewhere"),
		bin:       filepath.Join(root, "bin", "ach"),
		fakeTmp:   filepath.Join(root, "faketmp"),
	}
	t.Setenv("HOME", f.home)
	mkdir(t, f.home)
	f.regPath = filepath.Join(f.config, "accounts.json")

	writeRegistry(t, f.regPath, usageDir, f.acc1, f.acc2)
	write(t, filepath.Join(f.config, "auth-cache.json"), "{}", 0o600)
	write(t, filepath.Join(f.config, "usage", "snap.json"), "{}", 0o600)
	write(t, filepath.Join(f.config, "accounts.json.corrupt-20260101T000000000000Z"), "{", 0o644)

	write(t, filepath.Join(f.acc1, "settings.json"), "primary settings", 0o640)
	write(t, filepath.Join(f.acc1, "skills", "a.txt"), "skill a", 0o644)
	write(t, filepath.Join(f.acc1, "skills", "sub", "run.sh"), "#!/bin/sh", 0o755)
	link(t, "a.txt", filepath.Join(f.acc1, "skills", "rel"))
	write(t, filepath.Join(f.acc1, ".credentials.json"), "secret", 0o600)
	write(t, filepath.Join(f.elsewhere, "cmd.md"), "foreign", 0o644)

	mkdir(t, f.acc2)
	link(t, filepath.Join(f.acc1, "settings.json"), filepath.Join(f.acc2, "settings.json"))
	link(t, filepath.Join(f.acc1, "skills"), filepath.Join(f.acc2, "skills"))
	link(t, f.elsewhere, filepath.Join(f.acc2, "commands"))

	write(t, filepath.Join(f.acc2, "settings.json.bak-"+stamp), "old", 0o644)
	write(t, filepath.Join(f.acc2, "settings.json.bak-"+stamp+"-2"), "older", 0o644)
	write(t, filepath.Join(f.acc2, "skills.bak-"+stamp, "own.md"), "own skill", 0o644)
	write(t, filepath.Join(f.acc2, "settings.json.bak-old"), "decoy", 0o644)
	write(t, filepath.Join(f.acc2, ".claude.json.bak-"+stamp), "{}", 0o600)
	write(t, filepath.Join(f.acc2, ".claude.json.backup"), "decoy", 0o600)
	write(t, filepath.Join(f.acc2, "notes.bak-"+stamp), "decoy", 0o600)

	write(t, f.bin, "binary", 0o755)
	write(t, f.bin+".bak-"+stamp, "old binary", 0o755)
	write(t, f.bin+".bak-decoy", "decoy", 0o644)
	write(t, filepath.Join(f.root, "bin", "other"), "other", 0o755)
	return f
}

func (f fixture) options(execute bool, out *bytes.Buffer) Options {
	return Options{
		RegistryPath: f.regPath,
		Execute:      execute,
		Out:          out,
		Executable:   func() (string, error) { return f.bin, nil },
		TempDir:      f.fakeTmp,
	}
}

func (f fixture) mustRun(t *testing.T, execute bool) string {
	t.Helper()
	var out bytes.Buffer
	if failed := Run(f.options(execute, &out)); failed != 0 {
		t.Fatalf("failed = %d, output:\n%s", failed, out.String())
	}
	return out.String()
}

// snapshot records every path under root with its mode, link target and
// content so two trees can be compared exactly.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	tree := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		desc := info.Mode().String()
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			desc += " -> " + target
		case info.Mode().IsRegular():
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			desc += " " + string(raw)
		}
		tree[path] = desc
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

func assertSameTree(t *testing.T, before, after map[string]string) {
	t.Helper()
	if len(before) != len(after) {
		t.Errorf("entry count changed: %d -> %d", len(before), len(after))
	}
	for path, want := range before {
		if got := after[path]; got != want {
			t.Errorf("%s changed: %q -> %q", path, want, got)
		}
	}
}

func isSymlink(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}

func leftoverTemps(t *testing.T, dir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, ".*.uninstall-*"))
	if err != nil {
		t.Fatal(err)
	}
	asides, err := filepath.Glob(filepath.Join(dir, "*"+asideSuffix))
	if err != nil {
		t.Fatal(err)
	}
	return append(matches, asides...)
}

func TestDryRunChangesNothing(t *testing.T) {
	f := newFixture(t, "")
	before := snapshot(t, f.root)

	out := f.mustRun(t, false)

	assertSameTree(t, before, snapshot(t, f.root))
	for _, want := range []string{"預覽模式", "將會：", "ach uninstall --yes", ".agent-handoffs", "ach usage record"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestDryRunListsEveryAction(t *testing.T) {
	f := newFixture(t, "")
	out := f.mustRun(t, false)

	for _, want := range []string{
		filepath.Join(f.acc2, "settings.json"),
		filepath.Join(f.acc2, "skills"),
		"保留 " + filepath.Join(f.acc2, "settings.json.bak-"+stamp),
		f.regPath,
		filepath.Join(f.config, "auth-cache.json"),
		"刪除已清空的設定目錄 " + f.config,
		f.bin,
		f.bin + ".bak-" + stamp,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("plan lacks %s:\n%s", want, out)
		}
	}
	if strings.Contains(out, "將會：把 "+filepath.Join(f.acc2, "commands")) {
		t.Error("foreign link listed as an action")
	}
	if strings.Contains(out, "刪除備份") {
		t.Error("a sharing backup is planned for deletion")
	}
}

func TestExecute(t *testing.T) {
	tests := []struct {
		name  string
		check func(t *testing.T, f fixture, out string, primaryBefore map[string]string)
	}{
		{
			name: "symlinked file becomes a real copy",
			check: func(t *testing.T, f fixture, _ string, _ map[string]string) {
				path := filepath.Join(f.acc2, "settings.json")
				if isSymlink(path) {
					t.Fatal("settings.json is still a symlink")
				}
				raw, err := os.ReadFile(path)
				if err != nil || string(raw) != "primary settings" {
					t.Fatalf("content = %q, err = %v", raw, err)
				}
				if info, _ := os.Stat(path); info.Mode().Perm() != 0o640 {
					t.Errorf("mode = %v, want 0640", info.Mode().Perm())
				}
			},
		},
		{
			name: "symlinked dir becomes a real copy with modes kept",
			check: func(t *testing.T, f fixture, _ string, _ map[string]string) {
				dir := filepath.Join(f.acc2, "skills")
				if isSymlink(dir) {
					t.Fatal("skills is still a symlink")
				}
				for rel, want := range map[string]string{"a.txt": "skill a", "sub/run.sh": "#!/bin/sh"} {
					raw, err := os.ReadFile(filepath.Join(dir, rel))
					if err != nil || string(raw) != want {
						t.Errorf("%s = %q, err = %v", rel, raw, err)
					}
				}
				if info, _ := os.Stat(filepath.Join(dir, "sub", "run.sh")); info.Mode().Perm() != 0o755 {
					t.Errorf("run.sh mode = %v, want 0755", info.Mode().Perm())
				}
				if left := leftoverTemps(t, f.acc2); len(left) != 0 {
					t.Errorf("swap left %v", left)
				}
			},
		},
		{
			name: "relative link in a copied dir still points at the same file",
			check: func(t *testing.T, f fixture, _ string, _ map[string]string) {
				target, err := os.Readlink(filepath.Join(f.acc2, "skills", "rel"))
				if err != nil || target != filepath.Join(f.acc1, "skills", "a.txt") {
					t.Fatalf("rel -> %q, err = %v", target, err)
				}
			},
		},
		{
			name: "foreign symlink is left alone",
			check: func(t *testing.T, f fixture, _ string, _ map[string]string) {
				path := filepath.Join(f.acc2, "commands")
				target, err := os.Readlink(path)
				if err != nil || target != f.elsewhere {
					t.Fatalf("commands -> %q, err = %v", target, err)
				}
			},
		},
		{
			name: "primary account is untouched",
			check: func(t *testing.T, f fixture, _ string, primaryBefore map[string]string) {
				assertSameTree(t, primaryBefore, snapshot(t, f.acc1))
			},
		},
		{
			name: "sharing backups are kept and listed",
			check: func(t *testing.T, f fixture, out string, _ map[string]string) {
				for _, kept := range []string{
					filepath.Join(f.acc2, "settings.json.bak-"+stamp),
					filepath.Join(f.acc2, "settings.json.bak-"+stamp+"-2"),
					filepath.Join(f.acc2, "skills.bak-"+stamp),
					filepath.Join(f.acc2, ".claude.json.bak-"+stamp),
				} {
					if !exists(kept) {
						t.Errorf("backup %s was deleted", kept)
					}
					if !strings.Contains(out, "      "+kept) {
						t.Errorf("backup %s not listed under manual:\n%s", kept, out)
					}
				}
				for _, decoy := range []string{
					filepath.Join(f.acc2, "settings.json.bak-old"),
					filepath.Join(f.acc2, ".claude.json.backup"),
					filepath.Join(f.acc2, "notes.bak-"+stamp),
				} {
					if !exists(decoy) || strings.Contains(out, decoy) {
						t.Errorf("decoy %s was deleted or listed", decoy)
					}
				}
				if !strings.Contains(out, "共用前自己的設定") {
					t.Errorf("manual lacks the backup explanation:\n%s", out)
				}
			},
		},
		{
			name: "config dir holding only ach files is removed",
			check: func(t *testing.T, f fixture, _ string, _ map[string]string) {
				if exists(f.config) {
					t.Error("config dir survived")
				}
			},
		},
		{
			name: "binary and its install backups are removed",
			check: func(t *testing.T, f fixture, _ string, _ map[string]string) {
				if exists(f.bin) || exists(f.bin+".bak-"+stamp) {
					t.Error("binary or its backup survived")
				}
				if !exists(f.bin+".bak-decoy") || !exists(filepath.Join(f.root, "bin", "other")) {
					t.Error("a decoy beside the binary was deleted")
				}
			},
		},
		{
			name: "account homes and credentials stay",
			check: func(t *testing.T, f fixture, _ string, _ map[string]string) {
				if !exists(f.acc1) || !exists(f.acc2) || !exists(filepath.Join(f.acc1, ".credentials.json")) {
					t.Error("an account home or credential file was deleted")
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, "")
			primaryBefore := snapshot(t, f.acc1)
			out := f.mustRun(t, true)
			tt.check(t, f, out, primaryBefore)
		})
	}
}

func TestConfigDirKeepsForeignEntries(t *testing.T) {
	f := newFixture(t, "")
	write(t, filepath.Join(f.config, "notes.txt"), "mine", 0o644)
	write(t, filepath.Join(f.config, "project", "main.go"), "package main", 0o644)
	before := snapshot(t, filepath.Join(f.config, "project"))

	out := f.mustRun(t, true)

	for _, gone := range []string{f.regPath, f.regPath + ".lock", filepath.Join(f.config, "auth-cache.json"), filepath.Join(f.config, "usage"), filepath.Join(f.config, "accounts.json.corrupt-20260101T000000000000Z")} {
		if exists(gone) {
			t.Errorf("%s survived", gone)
		}
	}
	if raw, err := os.ReadFile(filepath.Join(f.config, "notes.txt")); err != nil || string(raw) != "mine" {
		t.Errorf("notes.txt = %q, err = %v", raw, err)
	}
	assertSameTree(t, before, snapshot(t, filepath.Join(f.config, "project")))
	if !strings.Contains(out, "裡面還有：notes.txt、project") {
		t.Errorf("leftovers not listed:\n%s", out)
	}
}

func TestRegistryNotNamedAccountsJSONIsRefused(t *testing.T) {
	f := newFixture(t, "")
	project := filepath.Join(f.root, "project")
	write(t, filepath.Join(project, "package.json"), "{}", 0o644)
	write(t, filepath.Join(project, "src", "index.js"), "x", 0o644)
	before := snapshot(t, f.root)

	var out bytes.Buffer
	opts := f.options(true, &out)
	opts.RegistryPath = filepath.Join(project, "package.json")
	if failed := Run(opts); failed != Refused {
		t.Fatalf("package.json was accepted:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "拒絕執行") {
		t.Errorf("refusal not explained:\n%s", out.String())
	}
	assertSameTree(t, before, snapshot(t, f.root))
}

func TestUnverifiableConfigDirIsRefused(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can stat anything")
	}
	f := newFixture(t, "")
	parent := filepath.Join(f.root, "locked")
	write(t, filepath.Join(parent, "config", "accounts.json"), "{}", 0o644)
	if err := os.Chmod(parent, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(parent, 0o755) })

	var out bytes.Buffer
	opts := f.options(true, &out)
	opts.RegistryPath = filepath.Join(parent, "config", "accounts.json")
	if failed := Run(opts); failed != Refused {
		t.Fatalf("want Refused, got %d:\n%s", failed, out.String())
	}
	if !strings.Contains(out.String(), "無法確認設定目錄") {
		t.Errorf("refusal not explained:\n%s", out.String())
	}
	if strings.Contains(out.String(), "步驟") {
		t.Errorf("steps ran after the refusal:\n%s", out.String())
	}
}

func TestProjectRegistryDeletesOnlyItself(t *testing.T) {
	f := newFixture(t, "")
	project := filepath.Join(f.root, "project")
	writeRegistry(t, filepath.Join(project, "accounts.json"), "")
	write(t, filepath.Join(project, "src", "main.go"), "package main", 0o644)
	write(t, filepath.Join(project, "README.md"), "readme", 0o644)
	before := snapshot(t, filepath.Join(project, "src"))

	var out bytes.Buffer
	opts := f.options(true, &out)
	opts.RegistryPath = filepath.Join(project, "accounts.json")
	if failed := Run(opts); failed != 0 {
		t.Fatalf("failed = %d, output:\n%s", failed, out.String())
	}
	if exists(filepath.Join(project, "accounts.json")) || exists(filepath.Join(project, "accounts.json.lock")) {
		t.Error("the registry or its lock survived")
	}
	if !exists(filepath.Join(project, "README.md")) {
		t.Error("README.md was deleted")
	}
	assertSameTree(t, before, snapshot(t, filepath.Join(project, "src")))
}

func TestFailureKeepsRegistryForRerun(t *testing.T) {
	f := newFixture(t, "")
	if err := os.Chmod(f.acc2, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(f.acc2, 0o755) })

	var out bytes.Buffer
	if failed := Run(f.options(true, &out)); failed == 0 {
		t.Fatalf("expected failures, output:\n%s", out.String())
	}
	if !isSymlink(filepath.Join(f.acc2, "settings.json")) {
		t.Error("a failed replacement must leave the link in place")
	}
	if !exists(f.regPath) || !exists(f.bin) {
		t.Fatalf("registry or binary deleted after a step-1 failure:\n%s", out.String())
	}
	if exists(filepath.Join(f.config, "auth-cache.json")) {
		t.Error("step 3 did not run after a failure")
	}
	if !strings.Contains(out.String(), "重跑 ach uninstall --yes") {
		t.Errorf("footer lacks the re-run hint:\n%s", out.String())
	}

	if err := os.Chmod(f.acc2, 0o755); err != nil {
		t.Fatal(err)
	}
	f.mustRun(t, true)
	if isSymlink(filepath.Join(f.acc2, "settings.json")) || isSymlink(filepath.Join(f.acc2, "skills")) {
		t.Error("re-run did not finish step 1")
	}
	if exists(f.config) || exists(f.bin) {
		t.Error("re-run did not finish steps 3 and 4")
	}
}

func TestUnreadableRegistry(t *testing.T) {
	f := newFixture(t, "")
	write(t, f.regPath, "{not json", 0o600)

	out := f.mustRun(t, true)

	if !isSymlink(filepath.Join(f.acc2, "settings.json")) {
		t.Error("step 1 ran without a readable registry")
	}
	if raw, err := os.ReadFile(f.regPath); err != nil || string(raw) != "{not json" {
		t.Errorf("unreadable registry was not kept as is: %q, %v", raw, err)
	}
	if exists(filepath.Join(f.config, "auth-cache.json")) || exists(filepath.Join(f.config, "usage")) {
		t.Error("step 3 did not delete ach's other files")
	}
	if !exists(f.bin) {
		t.Error("binary deleted although the registry is kept for a re-run")
	}
	corrupt := f.regPath + ".corrupt-20260101T000000000000Z"
	if matches, _ := filepath.Glob(f.regPath + ".corrupt-*"); len(matches) != 1 || matches[0] != corrupt {
		t.Errorf("corrupt backups = %v, want only the old one kept and no new one written", matches)
	}
	if !strings.Contains(out, "損壞 registry 的備份 "+corrupt+"（") {
		t.Errorf("kept corrupt backup not listed:\n%s", out)
	}
	if !strings.Contains(out, "無法讀取 registry") {
		t.Errorf("unreadable registry not reported:\n%s", out)
	}
}

func TestUnreadableRegistryDryRunWritesNothing(t *testing.T) {
	f := newFixture(t, "")
	write(t, f.regPath, "{not json", 0o600)
	before := snapshot(t, f.root)

	f.mustRun(t, false)

	assertSameTree(t, before, snapshot(t, f.root))
}

func TestMissingRegistry(t *testing.T) {
	f := newFixture(t, "")
	if err := os.Remove(f.regPath); err != nil {
		t.Fatal(err)
	}

	out := f.mustRun(t, true)

	if !strings.Contains(out, "沒有 registry，沒有共用設定要處理") {
		t.Errorf("missing registry not reported:\n%s", out)
	}
	if strings.Contains(out, "無法讀取") {
		t.Errorf("missing registry reported as unreadable:\n%s", out)
	}
	if !isSymlink(filepath.Join(f.acc2, "settings.json")) {
		t.Error("step 1 ran without a registry")
	}
}

func TestInterruptedDirSwap(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, f fixture)
	}{
		{
			name: "link moved aside, temp copy half built",
			setup: func(t *testing.T, f fixture) {
				skills := filepath.Join(f.acc2, "skills")
				if err := os.Rename(skills, skills+asideSuffix); err != nil {
					t.Fatal(err)
				}
				write(t, filepath.Join(f.acc2, ".skills.uninstall-123", "a.txt"), "partial", 0o644)
			},
		},
		{
			name: "copy swapped in, aside link not removed",
			setup: func(t *testing.T, f fixture) {
				skills := filepath.Join(f.acc2, "skills")
				if err := os.Rename(skills, skills+asideSuffix); err != nil {
					t.Fatal(err)
				}
				write(t, filepath.Join(skills, "a.txt"), "skill a", 0o644)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, "")
			tt.setup(t, f)

			f.mustRun(t, true)

			skills := filepath.Join(f.acc2, "skills")
			if isSymlink(skills) {
				t.Fatal("skills is still a link")
			}
			if raw, err := os.ReadFile(filepath.Join(skills, "a.txt")); err != nil || string(raw) != "skill a" {
				t.Errorf("a.txt = %q, err = %v", raw, err)
			}
			if left := leftoverTemps(t, f.acc2); len(left) != 0 {
				t.Errorf("leftovers remain: %v", left)
			}
		})
	}
}

func TestSwapFailureRestoresLink(t *testing.T) {
	f := newFixture(t, "")
	skills := filepath.Join(f.acc2, "skills")
	rename = func(oldpath, newpath string) error {
		if newpath == skills {
			return errors.New("injected")
		}
		return os.Rename(oldpath, newpath)
	}
	t.Cleanup(func() { rename = os.Rename })

	var out bytes.Buffer
	if failed := Run(f.options(true, &out)); failed == 0 {
		t.Fatalf("expected a failure:\n%s", out.String())
	}
	target, err := os.Readlink(skills)
	if err != nil || target != filepath.Join(f.acc1, "skills") {
		t.Fatalf("skills -> %q, err = %v", target, err)
	}
	if left := leftoverTemps(t, f.acc2); len(left) != 0 {
		t.Errorf("failed swap left %v", left)
	}
}

func TestCopyFailureRemovesTempTree(t *testing.T) {
	f := newFixture(t, "")
	locked := filepath.Join(f.acc1, "skills", "locked")
	write(t, filepath.Join(locked, "secret"), "x", 0o000)
	if err := os.Chmod(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })

	var out bytes.Buffer
	if failed := Run(f.options(true, &out)); failed == 0 {
		t.Fatalf("expected a failure:\n%s", out.String())
	}
	if !isSymlink(filepath.Join(f.acc2, "skills")) {
		t.Error("link not kept after a failed copy")
	}
	if left := leftoverTemps(t, f.acc2); len(left) != 0 {
		t.Errorf("failed copy left %v", left)
	}
}

func TestReadOnlySourceDirKeepsItsMode(t *testing.T) {
	f := newFixture(t, "")
	ro := filepath.Join(f.acc1, "skills", "ro")
	write(t, filepath.Join(ro, "x.md"), "x", 0o644)
	if err := os.Chmod(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	copied := filepath.Join(f.acc2, "skills", "ro")
	t.Cleanup(func() {
		os.Chmod(ro, 0o755)
		os.Chmod(copied, 0o755)
	})

	f.mustRun(t, true)

	info, err := os.Stat(copied)
	if err != nil || info.Mode().Perm() != 0o500 {
		t.Fatalf("copied ro dir: %v, err = %v", info, err)
	}
}

func TestKindMismatchKeepsLink(t *testing.T) {
	f := newFixture(t, "")
	skills := filepath.Join(f.acc1, "skills")
	if err := os.RemoveAll(skills); err != nil {
		t.Fatal(err)
	}
	write(t, skills, "a file", 0o644)

	var out bytes.Buffer
	if failed := Run(f.options(true, &out)); failed == 0 {
		t.Fatalf("expected a failure:\n%s", out.String())
	}
	if !isSymlink(filepath.Join(f.acc2, "skills")) || !strings.Contains(out.String(), "種類不符") {
		t.Errorf("kind mismatch not kept or not reported:\n%s", out.String())
	}
}

func TestMissingPrimaryTargetKeepsTheLink(t *testing.T) {
	f := newFixture(t, "")
	if err := os.RemoveAll(filepath.Join(f.acc1, "skills")); err != nil {
		t.Fatal(err)
	}

	out := f.mustRun(t, true)

	if !isSymlink(filepath.Join(f.acc2, "skills")) {
		t.Error("dangling link was replaced")
	}
	if !strings.Contains(out, "保留連結") {
		t.Errorf("missing target not reported:\n%s", out)
	}
}

func TestDefaultClaudeAccountBackupIsListed(t *testing.T) {
	f := newFixture(t, "")
	defaultHome := filepath.Join(f.home, ".claude")
	mkdir(t, defaultHome)
	writeRegistry(t, f.regPath, "", f.acc1, f.acc2, defaultHome)
	backup := filepath.Join(f.home, ".claude.json.bak-"+stamp)
	write(t, backup, "{}", 0o600)

	out := f.mustRun(t, true)

	if !exists(backup) || !strings.Contains(out, "      "+backup) {
		t.Errorf("%s deleted or not listed:\n%s", backup, out)
	}
}

func TestUsageDir(t *testing.T) {
	tests := []struct {
		name     string
		usageDir func(f fixture) string
		gone     func(f fixture) []string
		kept     func(f fixture) []string
	}{
		{
			name:     "outside the config dir survives",
			usageDir: func(f fixture) string { return filepath.Join(f.root, "usage-out") },
			gone:     func(f fixture) []string { return []string{f.regPath} },
			kept:     func(f fixture) []string { return []string{filepath.Join(f.root, "usage-out", "snap.json")} },
		},
		{
			name:     "the default dir named explicitly is deleted",
			usageDir: func(f fixture) string { return filepath.Join(f.config, "usage") },
			gone:     func(f fixture) []string { return []string{f.config} },
		},
		{
			name:     "another dir inside the config dir is kept",
			usageDir: func(f fixture) string { return filepath.Join(f.config, "custom") },
			gone:     func(f fixture) []string { return []string{f.regPath} },
			kept: func(f fixture) []string {
				return []string{filepath.Join(f.config, "custom", "snap.json"), filepath.Join(f.config, "usage", "snap.json")}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := newFixture(t, "")
			usage := tt.usageDir(root)
			writeRegistry(t, root.regPath, usage, root.acc1, root.acc2)
			write(t, filepath.Join(usage, "snap.json"), "{}", 0o600)

			out := root.mustRun(t, true)

			for _, path := range tt.gone(root) {
				if exists(path) {
					t.Errorf("%s survived:\n%s", path, out)
				}
			}
			if tt.kept == nil {
				return
			}
			for _, path := range tt.kept(root) {
				if !exists(path) {
					t.Errorf("%s was deleted:\n%s", path, out)
				}
			}
		})
	}
}

func TestRelativeRegistryPath(t *testing.T) {
	f := newFixture(t, "")
	t.Chdir(f.root)

	var out bytes.Buffer
	opts := f.options(true, &out)
	opts.RegistryPath = filepath.Join("config", "accounts.json")
	if failed := Run(opts); failed != 0 {
		t.Fatalf("failed = %d, output:\n%s", failed, out.String())
	}
	if exists(f.config) {
		t.Errorf("config dir survived:\n%s", out.String())
	}
}

func TestRerunIsIdempotent(t *testing.T) {
	f := newFixture(t, "")
	f.mustRun(t, true)
	before := snapshot(t, f.root)

	out := f.mustRun(t, true)

	assertSameTree(t, before, snapshot(t, f.root))
	if !strings.Contains(out, "設定目錄不存在") {
		t.Errorf("second run did not report the missing config dir:\n%s", out)
	}
}

func TestBinary(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, f fixture) (exe string, opts func(*Options))
		check func(t *testing.T, f fixture, exe, out string)
	}{
		{
			name: "under the temp dir is skipped",
			setup: func(t *testing.T, f fixture) (string, func(*Options)) {
				return f.bin, func(o *Options) { o.TempDir = f.root }
			},
			check: func(t *testing.T, f fixture, exe, _ string) {
				if !exists(exe) {
					t.Error("a binary under the temp dir was deleted")
				}
			},
		},
		{
			name: "a go-build path is skipped",
			setup: func(t *testing.T, f fixture) (string, func(*Options)) {
				exe := filepath.Join(f.root, "go-build123", "b001", "exe", "ach")
				write(t, exe, "go run", 0o755)
				return exe, nil
			},
			check: func(t *testing.T, f fixture, exe, _ string) {
				if !exists(exe) {
					t.Error("a go-build binary was deleted")
				}
			},
		},
		{
			name: "a symlinked exe loses only the link",
			setup: func(t *testing.T, f fixture) (string, func(*Options)) {
				built := filepath.Join(f.root, "repo", "ach")
				write(t, built, "built", 0o755)
				if err := os.Remove(f.bin); err != nil {
					t.Fatal(err)
				}
				link(t, built, f.bin)
				return f.bin, nil
			},
			check: func(t *testing.T, f fixture, exe, out string) {
				built := filepath.Join(f.root, "repo", "ach")
				if exists(exe) || exists(f.bin+".bak-"+stamp) {
					t.Error("link or its install backup survived")
				}
				if !exists(built) {
					t.Error("the link target was deleted")
				}
				if !strings.Contains(out, "指向的 "+built) {
					t.Errorf("link target not listed under manual:\n%s", out)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, "")
			exe, adjust := tt.setup(t, f)
			var out bytes.Buffer
			opts := f.options(true, &out)
			opts.Executable = func() (string, error) { return exe, nil }
			if adjust != nil {
				adjust(&opts)
			}
			if failed := Run(opts); failed != 0 {
				t.Fatalf("failed = %d, output:\n%s", failed, out.String())
			}
			tt.check(t, f, exe, out.String())
		})
	}
}

func TestConfigDirGuards(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, f fixture) (regPath, wantOut string)
	}{
		{
			name: "home dir",
			setup: func(t *testing.T, f fixture) (string, string) {
				regPath := filepath.Join(f.home, "accounts.json")
				writeRegistry(t, regPath, "", f.acc1, f.acc2)
				return regPath, "拒絕執行：不處理 " + f.home + "，因為它是家目錄或其上層"
			},
		},
		{
			name: "holds an account home",
			setup: func(t *testing.T, f fixture) (string, string) {
				holder := filepath.Join(f.root, "holder")
				nested := filepath.Join(holder, "acc3")
				mkdir(t, nested)
				regPath := filepath.Join(holder, "accounts.json")
				writeRegistry(t, regPath, "", f.acc1, f.acc2, nested)
				return regPath, "拒絕執行：不處理 " + holder + "，因為帳號目錄 " + nested + " 在裡面"
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, "")
			regPath, wantOut := tt.setup(t, f)
			before := snapshot(t, f.root)

			var out bytes.Buffer
			opts := f.options(true, &out)
			opts.RegistryPath = regPath
			failed := Run(opts)

			if failed == 0 || !strings.Contains(out.String(), wantOut) {
				t.Fatalf("failed = %d, want %q in output:\n%s", failed, wantOut, out.String())
			}
			if strings.Contains(out.String(), "步驟") {
				t.Errorf("a step ran although the directory was refused:\n%s", out.String())
			}
			if !isSymlink(filepath.Join(f.acc2, "settings.json")) || !isSymlink(filepath.Join(f.acc2, "skills")) {
				t.Error("a shared link was replaced although the directory was refused")
			}
			assertSameTree(t, before, snapshot(t, f.root))
		})
	}
}

func TestResolveInvoked(t *testing.T) {
	tests := []struct {
		name string
		// setup returns argv0, the running executable, and the path wanted
		// back; want is empty when a notRunningError is expected.
		setup func(t *testing.T, dir string) (argv0, running, want string)
	}{
		{
			name: "bare name found on PATH is the running file",
			setup: func(t *testing.T, dir string) (string, string, string) {
				exe := filepath.Join(dir, "ach")
				write(t, exe, "bin", 0o755)
				return "ach", exe, exe
			},
		},
		{
			name: "a symlink to the running file is returned as invoked",
			setup: func(t *testing.T, dir string) (string, string, string) {
				real := filepath.Join(dir, "real", "ach")
				write(t, real, "bin", 0o755)
				alias := filepath.Join(dir, "ach")
				link(t, real, alias)
				return alias, real, alias
			},
		},
		{
			name: "a different file is refused",
			setup: func(t *testing.T, dir string) (string, string, string) {
				running := filepath.Join(dir, "running", "ach")
				write(t, running, "bin", 0o755)
				other := filepath.Join(dir, "ach")
				write(t, other, "bin", 0o755)
				return other, running, ""
			},
		},
		{
			name: "a missing invoked path is refused",
			setup: func(t *testing.T, dir string) (string, string, string) {
				running := filepath.Join(dir, "running", "ach")
				write(t, running, "bin", 0o755)
				return filepath.Join(dir, "gone"), running, ""
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			argv0, running, want := tt.setup(t, dir)
			lookPath := func(name string) (string, error) { return filepath.Join(dir, name), nil }

			got, err := resolveInvoked(argv0, lookPath, func() (string, error) { return running, nil })

			var notRunning *notRunningError
			if want == "" {
				if !errors.As(err, &notRunning) {
					t.Fatalf("got %q, %v; want a notRunningError", got, err)
				}
				return
			}
			if err != nil || got != want {
				t.Fatalf("got %q, %v; want %q", got, err, want)
			}
		})
	}
}

func TestBinaryNotTheRunningFile(t *testing.T) {
	f := newFixture(t, "")
	running := filepath.Join(f.root, "running", "ach")
	write(t, running, "running", 0o755)
	var out bytes.Buffer
	opts := f.options(true, &out)
	opts.Executable = func() (string, error) {
		return resolveInvoked(f.bin, nil, func() (string, error) { return running, nil })
	}

	if failed := Run(opts); failed != 0 {
		t.Fatalf("failed = %d, output:\n%s", failed, out.String())
	}
	if !exists(f.bin) || !exists(running) || !exists(f.bin+".bak-"+stamp) {
		t.Errorf("an executable was deleted although invoked and running differ:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "執行檔 "+f.bin+" 與執行中的 "+running+" 不是同一個檔案") {
		t.Errorf("mismatch not listed under manual:\n%s", out.String())
	}
}

func TestLeftoverTemps(t *testing.T) {
	f := newFixture(t, "")
	primaryTemp := filepath.Join(f.acc1, ".skills.uninstall-7")
	write(t, filepath.Join(primaryTemp, "x"), "x", 0o644)
	sharedTemp := filepath.Join(f.acc2, ".skills.uninstall-8")
	write(t, filepath.Join(sharedTemp, "x"), "x", 0o644)
	foreignTemp := filepath.Join(f.acc2, ".notes.uninstall-9")
	write(t, foreignTemp, "x", 0o644)

	out := f.mustRun(t, true)

	if exists(sharedTemp) {
		t.Error("temp copy of a shared entry survived")
	}
	if !exists(primaryTemp) {
		t.Error("a temp-looking entry in the primary home was deleted")
	}
	if !exists(foreignTemp) || !strings.Contains(out, foreignTemp+" 看似中斷留下的暫存複本") {
		t.Errorf("temp of a non-shared entry deleted or not listed:\n%s", out)
	}
}

func TestDryRunPreviewsCopyAfterRestoringAside(t *testing.T) {
	f := newFixture(t, "")
	skills := filepath.Join(f.acc2, "skills")
	if err := os.Rename(skills, skills+asideSuffix); err != nil {
		t.Fatal(err)
	}

	out := f.mustRun(t, false)

	if strings.Contains(out, "略過 "+skills+"：不存在") {
		t.Errorf("preview reports the entry missing although the aside link is restored first:\n%s", out)
	}
	if !strings.Contains(out, "將會：放回連結後，把 "+skills+" 換成") {
		t.Errorf("preview lacks the copy after restoring:\n%s", out)
	}
}

func TestDefaultUsageDirContents(t *testing.T) {
	tests := []struct {
		name     string
		extra    string
		wantKept bool
	}{
		{name: "snapshot temp file only", extra: ".snapshot-123.tmp", wantKept: false},
		{name: "a foreign file", extra: "notes.txt", wantKept: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, "")
			usage := filepath.Join(f.config, "usage")
			write(t, filepath.Join(usage, tt.extra), "x", 0o600)

			out := f.mustRun(t, true)

			if exists(usage) != tt.wantKept {
				t.Errorf("usage kept = %v, want %v:\n%s", exists(usage), tt.wantKept, out)
			}
			if tt.wantKept && !strings.Contains(out, "用量資料目錄 "+usage+"（裡面有 ach 以外的東西") {
				t.Errorf("kept usage dir not listed:\n%s", out)
			}
		})
	}
}

func TestRegistryDeleteFailureKeepsBinary(t *testing.T) {
	f := newFixture(t, "")
	removeFile = func(path string) error {
		if path == f.regPath {
			return errors.New("injected")
		}
		return os.Remove(path)
	}
	t.Cleanup(func() { removeFile = os.Remove })

	var out bytes.Buffer
	if failed := Run(f.options(true, &out)); failed == 0 {
		t.Fatalf("expected a failure:\n%s", out.String())
	}
	if !exists(f.regPath) || !exists(f.bin) {
		t.Errorf("registry or binary gone after the registry delete failed:\n%s", out.String())
	}
}

func TestRerunCommand(t *testing.T) {
	tests := []struct {
		name    string
		adjust  func(t *testing.T, f fixture, o *Options)
		execute bool
		want    func(f fixture) string
	}{
		{
			name: "dry-run footer uses the invoked name",
			adjust: func(t *testing.T, f fixture, o *Options) {
				o.Executable = func() (string, error) { return filepath.Join(f.root, "bin", "ach-dev"), nil }
			},
			want: func(fixture) string { return "確認無誤後執行：ach-dev uninstall --yes" },
		},
		{
			name: "an explicit registry is passed on",
			adjust: func(t *testing.T, f fixture, o *Options) {
				o.RegistrySet = true
			},
			want: func(f fixture) string {
				return "確認無誤後執行：ach uninstall --registry " + f.regPath + " --yes"
			},
		},
		{
			name: "a registry path with a space is quoted",
			adjust: func(t *testing.T, f fixture, o *Options) {
				regPath := filepath.Join(f.root, "my config", "accounts.json")
				writeRegistry(t, regPath, "", f.acc1, f.acc2)
				o.RegistryPath = regPath
				o.RegistrySet = true
			},
			want: func(f fixture) string {
				return "ach uninstall --registry '" + filepath.Join(f.root, "my config", "accounts.json") + "' --yes"
			},
		},
		{
			name: "a single quote in the registry path is escaped",
			adjust: func(t *testing.T, f fixture, o *Options) {
				o.RegistryPath = "/tmp/it's/accounts.json"
				o.RegistrySet = true
			},
			want: func(fixture) string {
				return `ach uninstall --registry '/tmp/it'\''s/accounts.json' --yes`
			},
		},
		{
			name: "a command name with a space is quoted",
			adjust: func(t *testing.T, f fixture, o *Options) {
				o.Executable = func() (string, error) { return filepath.Join(f.root, "bin", "my ach"), nil }
			},
			want: func(fixture) string { return "'my ach' uninstall --yes" },
		},
		{
			name: "invoked path that is not the running file still names the command",
			adjust: func(t *testing.T, f fixture, o *Options) {
				o.Executable = func() (string, error) {
					return "", &notRunningError{invoked: filepath.Join(f.root, "bin", "ach2"), running: f.bin}
				}
			},
			want: func(fixture) string { return "ach2 uninstall --yes" },
		},
		{
			name:    "re-run hint after a failure",
			execute: true,
			adjust: func(t *testing.T, f fixture, o *Options) {
				if err := os.Chmod(f.acc2, 0o500); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.Chmod(f.acc2, 0o755) })
				o.RegistrySet = true
			},
			want: func(f fixture) string { return "重跑 ach uninstall --registry " + f.regPath + " --yes" },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, "")
			var out bytes.Buffer
			opts := f.options(tt.execute, &out)
			tt.adjust(t, f, &opts)

			Run(opts)

			if want := tt.want(f); !strings.Contains(out.String(), want) {
				t.Errorf("output lacks %q:\n%s", want, out.String())
			}
		})
	}
}
