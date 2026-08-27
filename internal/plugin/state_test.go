package plugin

import (
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
)

func TestStateSetupAndAtomicWritesUseSystemOwner(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("cross-user ownership test requires root")
	}
	account, err := user.Lookup("nobody")
	if err != nil {
		t.Skipf("nobody account is unavailable: %v", err)
	}
	expectedUID, err := strconv.Atoi(account.Uid)
	if err != nil {
		t.Fatal(err)
	}
	expectedGID, err := strconv.Atoi(account.Gid)
	if err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(t.TempDir(), "state")
	t.Setenv("DOKKU_SYSTEM_USER", account.Username)
	t.Setenv("DOKKU_SYSTEM_GROUP", "")
	t.Setenv("DOKKU_VAULT_AGENT_DATA_ROOT", root)

	legacyDir := filepath.Join(root, "apps", "legacy")
	if err := os.MkdirAll(legacyDir, 0700); err != nil {
		t.Fatal(err)
	}
	legacyFile := filepath.Join(legacyDir, "role-id")
	if err := os.WriteFile(legacyFile, []byte("legacy\n"), 0600); err != nil {
		t.Fatal(err)
	}

	state := NewState(root)
	if err := state.RepairOwnership(); err != nil {
		t.Fatal(err)
	}
	nestedDir := filepath.Join(state.RenderedDir("vault-sample"), "nested", "deeper")
	if err := os.MkdirAll(nestedDir, 0755); err != nil {
		t.Fatal(err)
	}
	renderedFile := filepath.Join(nestedDir, "client.jks")
	if err := writeFileAtomic(renderedFile, []byte("keystore"), 0440); err != nil {
		t.Fatal(err)
	}

	paths := []string{
		root,
		state.appsRoot(),
		state.renderedRoot(),
		state.tempRoot(),
		state.locksRoot(),
		legacyDir,
		legacyFile,
		state.RenderedDir("vault-sample"),
		filepath.Dir(nestedDir),
		nestedDir,
		renderedFile,
	}
	for _, path := range paths {
		assertOwnership(t, path, expectedUID, expectedGID)
	}
	if info, err := os.Stat(renderedFile); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm() != 0440 {
		t.Fatalf("rendered file mode = %04o, want 0440", info.Mode().Perm())
	}
}

func TestPathWithinRootRejectsEscapes(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "state")

	tests := map[string]struct {
		path string
		want bool
	}{
		"root":    {path: root, want: true},
		"nested":  {path: filepath.Join(root, "apps", "sample"), want: true},
		"parent":  {path: base, want: false},
		"sibling": {path: filepath.Join(base, "state-other"), want: false},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if got := pathWithinRoot(root, test.path); got != test.want {
				t.Fatalf("pathWithinRoot(%q, %q) = %t, want %t", root, test.path, got, test.want)
			}
		})
	}
}

func TestStateLockPathIsOutsideAppDirectory(t *testing.T) {
	state := NewState(filepath.Join(t.TempDir(), "state"))
	want := filepath.Join(state.Root, "locks", "sample.lock")
	if got := state.LockPath("sample"); got != want {
		t.Fatalf("LockPath() = %q, want %q", got, want)
	}
	if filepath.Dir(state.LockPath("sample")) == state.appDir("sample") {
		t.Fatal("lock must not be stored in the deletable application directory")
	}
}

func TestStateSetupRejectsUnknownExplicitSystemUser(t *testing.T) {
	t.Setenv("DOKKU_SYSTEM_USER", "dokku-vault-agent-user-that-does-not-exist")
	t.Setenv("DOKKU_SYSTEM_GROUP", "")
	state := NewState(filepath.Join(t.TempDir(), "state"))
	if err := state.Setup(); err == nil {
		t.Fatal("Setup() succeeded with an unknown explicit DOKKU_SYSTEM_USER")
	}
}

func assertOwnership(t *testing.T, path string, wantUID, wantGID int) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("stat data for %s has type %T", path, info.Sys())
	}
	if int(stat.Uid) != wantUID || int(stat.Gid) != wantGID {
		t.Fatalf("ownership of %s = %d:%d, want %d:%d", path, stat.Uid, stat.Gid, wantUID, wantGID)
	}
}
