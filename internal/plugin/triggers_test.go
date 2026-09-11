package plugin

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"strings"
	"testing"
)

func TestPostCloneUnmountsExactVaultAttachment(t *testing.T) {
	runner := &fakeRunner{}
	plugin := newTestPlugin(t.TempDir(), runner)
	config := AppConfig{
		AppName: "source", Enabled: true, MountPath: "/app/secrets",
		StorageEntry: storageEntryName("source"), TemplateMode: DefaultTemplateMode,
	}
	if err := plugin.State.SaveApp(config); err != nil {
		t.Fatal(err)
	}
	if err := plugin.triggerPostClone([]string{"source", "destination"}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(runner.specs) != 1 {
		t.Fatalf("clone cleanup made %d calls, want one", len(runner.specs))
	}
	want := "storage:unmount destination " + config.StorageEntry + " --container-dir /app/secrets"
	if got := strings.Join(runner.specs[0].Args, " "); got != want {
		t.Fatalf("clone cleanup command = %q, want %q", got, want)
	}
}

func TestPreReleaseBuilderUsesDokkuBuildSourceForImageBinding(t *testing.T) {
	renderStop := errors.New("stop after source-image verification")
	sourceImage := "registry.example.test/team/sample@sha256:" + strings.Repeat("b", 64)
	runner := &fakeRunner{
		outputFn: func(spec CommandSpec) ([]byte, error) {
			if strings.Join(spec.Args, " ") != "trigger git-get-property sample source-image" {
				return nil, fmt.Errorf("unexpected output command: %v", spec.Args)
			}
			return []byte(sourceImage + "\n"), nil
		},
		runFn: func(spec CommandSpec) error {
			if len(spec.Args) >= 2 && spec.Args[0] == "container" && spec.Args[1] == "run" {
				return renderStop
			}
			return fmt.Errorf("unexpected run command: %v", spec.Args)
		},
	}
	plugin := newTestPlugin(t.TempDir(), runner)
	global := GlobalConfig{
		VaultAddress: "https://vault.example.test",
		Image:        "hashicorp/vault:1.20.2@sha256:" + strings.Repeat("a", 64),
	}
	if err := plugin.State.SaveGlobal(global); err != nil {
		t.Fatal(err)
	}
	config := AppConfig{
		AppName: "sample", Enabled: true, MountPath: "/app/secrets", RoleName: "sample",
		AppRoleMount: DefaultAppRoleMount, StorageEntry: storageEntryName("sample"),
		TemplateMode: DefaultTemplateMode,
		Templates: []ManagedTemplate{{
			Name: "secret", SecretPath: "secret/data/sample", Field: "value",
			Destination: "secret", Decode: "none", Perms: "0444",
		}},
	}
	if err := plugin.State.SaveApp(config); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(plugin.State.RoleIDPath("sample"), []byte("role-id\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, deploymentSource := range []string{"git:from-image", "ps:rebuild"} {
		t.Run(deploymentSource, func(t *testing.T) {
			if err := plugin.commandStage(
				[]string{"sample", "--source-image", sourceImage, "--ttl-seconds", "300"},
				strings.NewReader("wrapped-token\n"),
			); err != nil {
				t.Fatal(err)
			}
			if current, err := user.Current(); err == nil {
				t.Setenv("DOKKU_SYSTEM_USER", current.Username)
			}
			t.Setenv("DOKKU_BUILD_SOURCE", deploymentSource)
			err := plugin.triggerPreReleaseBuilder([]string{"dockerfile", "sample", "sample-image"}, io.Discard, io.Discard)
			if !errors.Is(err, renderStop) {
				t.Fatalf("image-bound pre-release returned %v, want render sentinel", err)
			}
		})
	}
}

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
