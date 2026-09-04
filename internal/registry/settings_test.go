package registry

import (
	"os"
	"path/filepath"
	"testing"
)

func account(id, provider, home string, number int) Account {
	return Account{ID: id, Provider: provider, Number: number, Home: home}
}

// homes builds a source home holding a settings document and an empty
// target home, the shape account creation produces.
func homes(t *testing.T, provider, content string) (Account, Account) {
	t.Helper()
	dir := t.TempDir()
	sourceHome := filepath.Join(dir, "source")
	targetHome := filepath.Join(dir, "target")
	if err := os.MkdirAll(sourceHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(targetHome, 0o755); err != nil {
		t.Fatal(err)
	}
	source := account("src-1", provider, sourceHome, 1)
	if content != "" {
		if err := os.WriteFile(SettingsPath(source), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return source, account("tgt-2", provider, targetHome, 2)
}

// entryNamed finds one entry's result by name, failing the test if
// ShareSettings did not return it -- a missing entry is itself a bug, not a
// case for the caller to handle.
func entryNamed(t *testing.T, shares []EntryShare, name string) EntryShare {
	t.Helper()
	for _, share := range shares {
		if share.Name == name {
			return share
		}
	}
	t.Fatalf("no %q entry in %+v", name, shares)
	return EntryShare{}
}

func assertLinkedTo(t *testing.T, target Account, path, want string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s is not a symlink", path)
	}
	link, err := os.Readlink(path)
	if err != nil {
		t.Fatal(err)
	}
	if link != want {
		t.Fatalf("expected the link to point at %q, got %q", want, link)
	}
}

func TestShareSettingsLinksAFreshAccountHome(t *testing.T) {
	source, target := homes(t, "claude", `{"statusLine":{"command":"x"}}`)

	shares, err := ShareSettings(source, target, false)
	if err != nil {
		t.Fatal(err)
	}
	share := entryNamed(t, shares, "settings.json")
	if share.Before != EntryMissing || !share.Linked || share.Backup != "" {
		t.Fatalf("expected a plain link of an empty home, got %+v", share)
	}
	assertLinkedTo(t, target, SettingsPath(target), SettingsPath(source))

	// Reading through the link must give the source document, and a write
	// to the source must be visible through it.
	if err := os.WriteFile(SettingsPath(source), []byte(`{"theme":"dark"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(SettingsPath(target))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"theme":"dark"}` {
		t.Fatalf("the accounts are not reading one file, got %q", raw)
	}
}

func TestShareSettingsIsANoOpWhenAlreadyShared(t *testing.T) {
	source, target := homes(t, "claude", `{"a":1}`)
	if _, err := ShareSettings(source, target, false); err != nil {
		t.Fatal(err)
	}

	shares, err := ShareSettings(source, target, false)
	if err != nil {
		t.Fatal(err)
	}
	share := entryNamed(t, shares, "settings.json")
	if share.Before != EntryShared || share.Linked || share.Backup != "" {
		t.Fatalf("expected an already-shared no-op, got %+v", share)
	}
}

// Account creation must never consume a document the account already has.
func TestShareSettingsRefusesAnAccountsOwnDocumentUnlessReplacing(t *testing.T) {
	source, target := homes(t, "claude", `{"a":1}`)
	targetPath := SettingsPath(target)
	if err := os.WriteFile(targetPath, []byte(`{"mine":true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	shares, err := ShareSettings(source, target, false)
	if err != nil {
		t.Fatal(err)
	}
	share := entryNamed(t, shares, "settings.json")
	if share.Err == nil {
		t.Fatal("expected sharing to refuse an account's own settings")
	}
	if share.Before != EntryOwn || share.Linked {
		t.Fatalf("expected the refusal to report an own document, got %+v", share)
	}
	raw, _ := os.ReadFile(targetPath)
	if string(raw) != `{"mine":true}` {
		t.Fatalf("the account's own settings were modified: %q", raw)
	}
}

func TestShareSettingsMovesAnOwnDocumentAsideWhenReplacing(t *testing.T) {
	source, target := homes(t, "claude", `{"a":1}`)
	targetPath := SettingsPath(target)
	if err := os.WriteFile(targetPath, []byte(`{"mine":true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	shares, err := ShareSettings(source, target, true)
	if err != nil {
		t.Fatal(err)
	}
	share := entryNamed(t, shares, "settings.json")
	if share.Before != EntryOwn || !share.Linked || share.Backup == "" {
		t.Fatalf("expected a replaced document with a backup, got %+v", share)
	}
	assertLinkedTo(t, target, targetPath, SettingsPath(source))
	raw, err := os.ReadFile(share.Backup)
	if err != nil {
		t.Fatalf("the backup named in the result is not readable: %v", err)
	}
	if string(raw) != `{"mine":true}` {
		t.Fatalf("the backup does not hold the original document: %q", raw)
	}
}

// A link someone pointed somewhere deliberately is not this code's to
// repoint, replace or no replace.
func TestShareSettingsRefusesALinkToSomewhereElse(t *testing.T) {
	source, target := homes(t, "claude", `{"a":1}`)
	elsewhere := filepath.Join(t.TempDir(), "elsewhere.json")
	if err := os.WriteFile(elsewhere, []byte(`{"other":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	targetPath := SettingsPath(target)
	if err := os.Symlink(elsewhere, targetPath); err != nil {
		t.Fatal(err)
	}

	for _, replace := range []bool{false, true} {
		shares, err := ShareSettings(source, target, replace)
		if err != nil {
			t.Fatal(err)
		}
		share := entryNamed(t, shares, "settings.json")
		if share.Err == nil {
			t.Fatalf("expected a refusal with replace=%v", replace)
		}
		if share.Before != EntryForeign {
			t.Fatalf("expected a foreign link to be reported, got %+v", share)
		}
		link, _ := os.Readlink(targetPath)
		if link != elsewhere {
			t.Fatalf("the existing link was repointed to %q", link)
		}
	}
}

// Linking to a file that is not there would leave a broken link behind,
// which reads as "shared" forever after. With several entries per
// provider, a missing source is now a per-entry refusal rather than a
// whole-call failure -- ShareSettings itself still succeeds, and every
// entry (there being nothing else on this bare source either) is refused
// on its own.
func TestShareSettingsRefusesASourceWithNoDocument(t *testing.T) {
	source, target := homes(t, "claude", "")

	shares, err := ShareSettings(source, target, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, share := range shares {
		if share.Err == nil {
			t.Fatalf("expected entry %q to be refused against a bare source", share.Name)
		}
		if share.Linked {
			t.Fatalf("expected no entry to link against a bare source, got %+v", share)
		}
	}
	if _, err := os.Lstat(SettingsPath(target)); !os.IsNotExist(err) {
		t.Fatal("expected no link to be created against a missing source")
	}
}

func TestShareSettingsRefusesAnAccountSharingWithItself(t *testing.T) {
	source, _ := homes(t, "claude", `{"a":1}`)

	if _, err := ShareSettings(source, source, true); err == nil {
		t.Fatal("expected an account not to share settings with itself")
	}
}

func TestShareSettingsUsesTheProvidersOwnSettingsFile(t *testing.T) {
	source, target := homes(t, "codex", "model = \"gpt-5\"\n")
	if filepath.Base(SettingsPath(source)) != "config.toml" {
		t.Fatalf("expected codex to use config.toml, got %q", SettingsPath(source))
	}

	shares, err := ShareSettings(source, target, false)
	if err != nil {
		t.Fatal(err)
	}
	share := entryNamed(t, shares, "config.toml")
	if !share.Linked {
		t.Fatalf("expected config.toml to link, got %+v", share)
	}
	assertLinkedTo(t, target, SettingsPath(target), SettingsPath(source))
}

func TestSettingsPathIsEmptyForAnUnknownProvider(t *testing.T) {
	if path := SettingsPath(account("x-1", "gemini", "/tmp/x", 1)); path != "" {
		t.Fatalf("expected no settings path for an unknown provider, got %q", path)
	}
}

// A relative link is still a link to the source; resolving it against the
// directory holding it is what makes that visible.
func TestShareSettingsRecognisesARelativeLinkToTheSource(t *testing.T) {
	source, target := homes(t, "claude", `{"a":1}`)
	relative, err := filepath.Rel(target.Home, SettingsPath(source))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(relative, SettingsPath(target)); err != nil {
		t.Fatal(err)
	}

	shares, err := ShareSettings(source, target, false)
	if err != nil {
		t.Fatal(err)
	}
	share := entryNamed(t, shares, "settings.json")
	if share.Before != EntryShared || share.Linked {
		t.Fatalf("expected a relative link to count as already shared, got %+v", share)
	}
}

func TestPrimaryAccountIsTheLowestNumberedOfItsProvider(t *testing.T) {
	r := Registry{Accounts: []Account{
		{ID: "codex-1", Provider: "codex", Number: 1},
		{ID: "claude-3", Provider: "claude", Number: 3},
		{ID: "claude-1", Provider: "claude", Number: 1},
		{ID: "claude-2", Provider: "claude", Number: 2},
	}}
	found, ok := PrimaryAccount(r, "claude")
	if !ok || found.ID != "claude-1" {
		t.Fatalf("expected claude-1, got %q (found=%v)", found.ID, ok)
	}
	if _, ok := PrimaryAccount(r, "gemini"); ok {
		t.Fatal("expected no account for an unregistered provider")
	}
}

// Two registry entries can name one physical home when one path reaches it
// through a symlink. Comparing ids alone let that through, and the result
// was the source document renamed away and replaced by a link to itself.
func TestShareSettingsRefusesTwoAccountsOnOnePhysicalHome(t *testing.T) {
	dir := t.TempDir()
	realHome := filepath.Join(dir, "claude")
	aliasHome := filepath.Join(dir, "claude-alias")
	if err := os.MkdirAll(realHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realHome, aliasHome); err != nil {
		t.Fatal(err)
	}
	source := account("claude-1", "claude", realHome, 1)
	alias := account("claude-2", "claude", aliasHome, 2)
	if err := os.WriteFile(SettingsPath(source), []byte(`{"real":true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := ShareSettings(source, alias, true); err == nil {
		t.Fatal("expected sharing to refuse two accounts on one home")
	}
	info, err := os.Lstat(SettingsPath(source))
	if err != nil {
		t.Fatalf("the source settings document is gone: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("the source settings document was replaced by a symlink")
	}
	raw, err := os.ReadFile(SettingsPath(source))
	if err != nil || string(raw) != `{"real":true}` {
		t.Fatalf("the source settings document was damaged: %q (%v)", raw, err)
	}
	entries, err := os.ReadDir(realHome)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected nothing moved aside, found %d entries", len(entries))
	}
}

// os.Rename overwrites silently, so a backup name that is already taken
// must not be handed out again. Driving freeBackupPath directly keeps this
// off the wall clock -- through ShareSettings the two calls could straddle
// a second boundary and pass whether or not the guard is there.
func TestFreeBackupPathNeverHandsOutATakenName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	first, err := freeBackupPath(path, EntryFile)
	if err != nil {
		t.Fatal(err)
	}
	// The name has to be claimed, not merely found free: testing and then
	// using it lets a second process take it in between.
	info, err := os.Lstat(first)
	if err != nil {
		t.Fatalf("freeBackupPath returned a name it had not claimed: %v", err)
	}
	if info.Size() != 0 {
		t.Fatalf("expected an empty placeholder, got %d bytes", info.Size())
	}
	if err := os.WriteFile(first, []byte("FIRST"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := freeBackupPath(path, EntryFile)
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatalf("the same backup name was handed out twice: %s", first)
	}
	raw, err := os.ReadFile(first)
	if err != nil {
		t.Fatalf("the first backup is gone: %v", err)
	}
	if string(raw) != "FIRST" {
		t.Fatalf("the first backup was overwritten: %q", raw)
	}
}

// The name is claimed by creating it, so a share that succeeds must leave
// the account's real document as the backup and not the empty placeholder.
func TestShareSettingsLeavesNoEmptyPlaceholderBesideTheBackup(t *testing.T) {
	source, target := homes(t, "claude", `{"shared":true}`)
	if err := os.WriteFile(SettingsPath(target), []byte(`{"mine":true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	shares, err := ShareSettings(source, target, true)
	if err != nil {
		t.Fatal(err)
	}
	share := entryNamed(t, shares, "settings.json")
	entries, err := os.ReadDir(target.Home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected the link and one backup, found %d entries", len(entries))
	}
	info, err := os.Stat(share.Backup)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Fatal("the backup is the empty placeholder, not the account's document")
	}
}

// Re-sharing after something replaced the link with an identical copy is
// the ordinary repair. Keeping a backup of a file the link already points
// at leaves the user a .bak to diff for no reason.
func TestShareSettingsKeepsNoBackupOfAnIdenticalDocument(t *testing.T) {
	source, target := homes(t, "claude", `{"shared":true}`)
	targetPath := SettingsPath(target)
	if err := os.WriteFile(targetPath, []byte(`{"shared":true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	shares, err := ShareSettings(source, target, true)
	if err != nil {
		t.Fatal(err)
	}
	share := entryNamed(t, shares, "settings.json")
	if !share.Linked {
		t.Fatalf("expected the account to be relinked, got %+v", share)
	}
	if share.Backup != "" {
		t.Fatalf("expected no backup of an identical document, got %s", share.Backup)
	}
	entries, err := os.ReadDir(target.Home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected only the link to remain, found %d entries", len(entries))
	}
}

// Renaming a whole directory aside and calling it a backed-up settings
// document would describe the wrong thing to the user.
func TestShareSettingsRefusesADirectoryWhereTheDocumentBelongs(t *testing.T) {
	source, target := homes(t, "claude", `{"a":1}`)
	targetPath := SettingsPath(target)
	if err := os.MkdirAll(filepath.Join(targetPath, "inside"), 0o755); err != nil {
		t.Fatal(err)
	}

	shares, err := ShareSettings(source, target, true)
	if err != nil {
		t.Fatal(err)
	}
	share := entryNamed(t, shares, "settings.json")
	if share.Err == nil {
		t.Fatal("expected sharing to refuse a directory")
	}
	if share.Before != EntryWrongKind {
		t.Fatalf("expected the directory to be classified as such, got %v", share.Before)
	}
	if _, err := os.Stat(filepath.Join(targetPath, "inside")); err != nil {
		t.Fatalf("the directory was moved aside: %v", err)
	}
}

// A plain file sitting where a shared directory belongs is the mirror
// image of the case above, and must be refused the same way.
func TestShareSettingsRefusesAFileWhereASharedDirectoryBelongs(t *testing.T) {
	source, target := homes(t, "claude", `{"a":1}`)
	if err := os.MkdirAll(filepath.Join(source.Home, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	targetSkills := filepath.Join(target.Home, "skills")
	if err := os.WriteFile(targetSkills, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	shares, err := ShareSettings(source, target, true)
	if err != nil {
		t.Fatal(err)
	}
	share := entryNamed(t, shares, "skills")
	if share.Err == nil {
		t.Fatal("expected sharing to refuse a file where a directory belongs")
	}
	if share.Before != EntryWrongKind {
		t.Fatalf("expected the file to be classified as the wrong kind, got %v", share.Before)
	}
	raw, err := os.ReadFile(targetSkills)
	if err != nil || string(raw) != "not a directory" {
		t.Fatalf("the file was moved aside: %q (%v)", raw, err)
	}
}

// Path strings are not identity. A case-insensitive volume (the macOS
// default) gives one file two spellings that Clean and EvalSymlinks both
// leave distinct, and the destructive branch then renames the source away
// and links it to itself.
func TestShareSettingsRefusesOneHomeReachedByADifferentSpelling(t *testing.T) {
	dir := t.TempDir()
	lower := filepath.Join(dir, "claudehome")
	upper := filepath.Join(dir, "CLAUDEHOME")
	if err := os.MkdirAll(lower, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(upper); err != nil {
		t.Skip("filesystem is case-sensitive; this route to one home does not exist here")
	}

	source := account("claude-1", "claude", lower, 1)
	other := account("claude-2", "claude", upper, 2)
	if err := os.WriteFile(SettingsPath(source), []byte(`{"real":true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := ShareSettings(source, other, true); err == nil {
		t.Fatal("expected sharing to refuse one home under two spellings")
	}
	assertSourceIntact(t, source, `{"real":true}`)
}

// The homes are genuinely different here; it is the documents that are one
// file, because the source's is a symlink onto the target's.
func TestShareSettingsRefusesWhenTheDocumentsAreAlreadyOneFile(t *testing.T) {
	source, target := homes(t, "claude", "")
	targetPath := SettingsPath(target)
	if err := os.WriteFile(targetPath, []byte(`{"only":"copy"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(targetPath, SettingsPath(source)); err != nil {
		t.Fatal(err)
	}

	shares, err := ShareSettings(source, target, true)
	if err != nil {
		t.Fatal(err)
	}
	share := entryNamed(t, shares, "settings.json")
	if share.Err == nil {
		t.Fatal("expected sharing to refuse two names for one document")
	}
	raw, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("the only copy of the document is unreadable: %v", err)
	}
	if string(raw) != `{"only":"copy"}` {
		t.Fatalf("the only copy was damaged: %q", raw)
	}
}

// A directory in the source's document position was refused on the target
// side but linked on the source side, handing every sibling a link to a
// directory and calling it a share.
func TestShareSettingsRefusesADirectoryInTheSourcesDocumentPosition(t *testing.T) {
	source, target := homes(t, "claude", "")
	if err := os.MkdirAll(SettingsPath(source), 0o755); err != nil {
		t.Fatal(err)
	}

	shares, err := ShareSettings(source, target, true)
	if err != nil {
		t.Fatal(err)
	}
	share := entryNamed(t, shares, "settings.json")
	if share.Err == nil {
		t.Fatal("expected sharing to refuse a directory as the source document")
	}
	if _, err := os.Lstat(SettingsPath(target)); !os.IsNotExist(err) {
		t.Fatal("expected no link to a directory to be created")
	}
}

// A directory entry links the same way a file entry does: the whole
// directory becomes a symlink onto the source's.
func TestShareSettingsLinksADirectoryEntry(t *testing.T) {
	source, target := homes(t, "claude", `{"a":1}`)
	sourceSkills := filepath.Join(source.Home, "skills")
	if err := os.MkdirAll(filepath.Join(sourceSkills, "go-test-style"), 0o755); err != nil {
		t.Fatal(err)
	}

	shares, err := ShareSettings(source, target, false)
	if err != nil {
		t.Fatal(err)
	}
	share := entryNamed(t, shares, "skills")
	if share.Before != EntryMissing || !share.Linked || share.Backup != "" {
		t.Fatalf("expected the skills directory to link outright, got %+v", share)
	}
	targetSkills := filepath.Join(target.Home, "skills")
	assertLinkedTo(t, target, targetSkills, sourceSkills)
	if _, err := os.Stat(filepath.Join(targetSkills, "go-test-style")); err != nil {
		t.Fatalf("the linked directory does not show the source's contents: %v", err)
	}
}

// Nothing recursively compares directory contents, so a directory backup
// is kept even when replace is true and the two directories happen to
// agree -- unlike a file backup, which is discarded when it is redundant.
func TestShareSettingsKeepsADirectoryBackupEvenWhenIdentical(t *testing.T) {
	source, target := homes(t, "claude", `{"a":1}`)
	sourceSkills := filepath.Join(source.Home, "skills")
	targetSkills := filepath.Join(target.Home, "skills")
	if err := os.MkdirAll(filepath.Join(sourceSkills, "shared-skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(targetSkills, "shared-skill"), 0o755); err != nil {
		t.Fatal(err)
	}

	shares, err := ShareSettings(source, target, true)
	if err != nil {
		t.Fatal(err)
	}
	share := entryNamed(t, shares, "skills")
	if !share.Linked || share.Backup == "" {
		t.Fatalf("expected the directory to be relinked with a backup kept, got %+v", share)
	}
	if _, err := os.Stat(filepath.Join(share.Backup, "shared-skill")); err != nil {
		t.Fatalf("the directory backup was not kept intact: %v", err)
	}
	assertLinkedTo(t, target, targetSkills, sourceSkills)
}

// One entry being refused must not stop the rest from linking: a source
// that has settings.json but none of the optional shared directories still
// shares the file while each missing directory is refused on its own.
func TestShareSettingsLinksOneEntryWhileAnotherFails(t *testing.T) {
	source, target := homes(t, "claude", `{"a":1}`)

	shares, err := ShareSettings(source, target, false)
	if err != nil {
		t.Fatal(err)
	}
	settings := entryNamed(t, shares, "settings.json")
	if !settings.Linked || settings.Err != nil {
		t.Fatalf("expected settings.json to link despite the other entries failing, got %+v", settings)
	}
	for _, name := range []string{"skills", "commands", "agents"} {
		share := entryNamed(t, shares, name)
		if share.Err == nil || share.Linked {
			t.Fatalf("expected %q to be refused for having no source directory, got %+v", name, share)
		}
	}
}

func assertSourceIntact(t *testing.T, source Account, want string) {
	t.Helper()
	path := SettingsPath(source)
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("the source settings document is gone: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("the source settings document was replaced by a symlink")
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != want {
		t.Fatalf("the source settings document was damaged: %q (%v)", raw, err)
	}
}
