package plugin

import (
	"io"
	"os"
	"strings"
	"testing"
)

func TestPreReleaseBuilderFailsWhenRoleIDIsMissing(t *testing.T) {
	plugin := newTestPlugin(t.TempDir(), nil)
	global := GlobalConfig{
		VaultAddress: "https://vault.example.test",
		Image:        "hashicorp/vault:1.20.2@sha256:" + strings.Repeat("a", 64),
	}
	if err := plugin.State.SaveGlobal(global); err != nil {
		t.Fatal(err)
	}
	config := AppConfig{
		AppName: "sample", Enabled: true, MountPath: "/app/secrets", RoleName: "sample",
		AppRoleMount: DefaultAppRoleMount, StorageEntry: storageEntryName("sample"), TemplateMode: DefaultTemplateMode,
	}
	if err := plugin.State.SaveApp(config); err != nil {
		t.Fatal(err)
	}

	err := plugin.triggerPreReleaseBuilder(
		[]string{"dockerfile", "sample", "sample-image"},
		io.Discard,
		io.Discard,
	)
	if err == nil || !strings.Contains(err.Error(), "RoleID is not configured") {
		t.Fatalf("missing RoleID returned unexpected result: %v", err)
	}
}

func TestPostRenameUpdatesConfigurationInsideRenamedState(t *testing.T) {
	plugin := newTestPlugin(t.TempDir(), nil)
	config := AppConfig{AppName: "old-app", Enabled: true, StorageEntry: storageEntryName("old-app"), TemplateMode: DefaultTemplateMode}
	if err := plugin.State.SaveApp(config); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(plugin.State.PendingTokenPath("old-app"), []byte("token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeJSONAtomic(plugin.State.PendingMetadataPath("old-app"), StagedCredential{}, 0600); err != nil {
		t.Fatal(err)
	}
	if err := plugin.triggerPostRename([]string{"old-app", "new-app"}); err != nil {
		t.Fatal(err)
	}
	renamed, err := plugin.State.LoadApp("new-app")
	if err != nil {
		t.Fatal(err)
	}
	if renamed.AppName != "new-app" {
		t.Fatalf("renamed config app = %q", renamed.AppName)
	}
	if _, err := os.Stat(plugin.State.appDir("old-app")); !isNotExist(err) {
		t.Fatalf("old state still exists: %v", err)
	}
	if exists(plugin.State.PendingTokenPath("new-app")) || exists(plugin.State.PendingMetadataPath("new-app")) {
		t.Fatal("pending credential moved to renamed app")
	}
}

func TestPostRenameStopsWhenPendingCredentialCannotBeRemoved(t *testing.T) {
	plugin := newTestPlugin(t.TempDir(), nil)
	config := AppConfig{AppName: "old-app", Enabled: true, StorageEntry: storageEntryName("old-app"), TemplateMode: DefaultTemplateMode}
	if err := plugin.State.SaveApp(config); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(plugin.State.PendingTokenPath("old-app"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := plugin.triggerPostRename([]string{"old-app", "new-app"}); err == nil {
		t.Fatal("rename succeeded despite an unremovable pending credential")
	}
	if _, err := plugin.State.LoadApp("old-app"); err != nil {
		t.Fatalf("old state was moved after cleanup failure: %v", err)
	}
}
