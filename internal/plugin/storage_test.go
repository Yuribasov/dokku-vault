package plugin

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPurgeToleratesAttachmentAlreadyRemoved(t *testing.T) {
	root := t.TempDir()
	calls := 0
	runner := &fakeRunner{runFn: func(spec CommandSpec) error {
		calls++
		if len(spec.Args) > 0 && spec.Args[0] == "storage:unmount" {
			return errors.New("attachment not found")
		}
		return nil
	}}
	plugin := newTestPlugin(root, runner)
	config := AppConfig{
		AppName: "sample", Enabled: true, StorageEntry: storageEntryName("sample"),
		TemplateMode: DefaultTemplateMode,
	}
	if err := plugin.State.SaveApp(config); err != nil {
		t.Fatal(err)
	}
	if err := plugin.purgeApp("sample", io.Discard, io.Discard); err != nil {
		t.Fatalf("purge failed after a harmless unmount error: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected unmount and destroy calls, got %d", calls)
	}
	if _, err := plugin.State.LoadApp("sample"); !isNotExist(err) {
		t.Fatalf("app state still exists: %v", err)
	}
}

func TestEnableUsesExplicitAppAndClearsInheritedRouting(t *testing.T) {
	t.Setenv("DOKKU_APP_NAME", "wrong-app")
	root := t.TempDir()
	runner := &fakeRunner{}
	runner.outputFn = func(spec CommandSpec) ([]byte, error) {
		return []byte("docker-local\n"), nil
	}
	plugin := newTestPlugin(root, runner)
	err := plugin.commandEnable([]string{
		"sample", "--mount-path", "/app/secrets", "--role-name", "sample-role",
	}, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.specs) != 4 {
		t.Fatalf("expected app check, scheduler check, create, and mount; got %d", len(runner.specs))
	}
	if got := strings.Join(runner.specs[1].Args, " "); got != "trigger scheduler-detect sample" {
		t.Fatalf("unexpected scheduler detection command: %s", got)
	}
	for _, spec := range runner.specs {
		for _, value := range spec.Env {
			if strings.HasPrefix(value, "DOKKU_APP_NAME=") {
				t.Fatalf("inherited app routing leaked to %s %v", spec.Name, spec.Args)
			}
		}
	}
	create := strings.Join(runner.specs[2].Args, " ")
	mount := strings.Join(runner.specs[3].Args, " ")
	if !strings.Contains(create, "storage:create "+storageEntryName("sample")) {
		t.Fatalf("unexpected create command: %s", create)
	}
	for _, expected := range []string{"storage:mount sample", "--container-dir /app/secrets", "--phase deploy,run", "--volume-readonly"} {
		if !strings.Contains(mount, expected) {
			t.Fatalf("mount command lacks %q: %s", expected, mount)
		}
	}
	config, err := plugin.State.LoadApp("sample")
	if err != nil {
		t.Fatal(err)
	}
	if !config.Enabled || config.StorageEntry != storageEntryName("sample") {
		t.Fatalf("unexpected saved config: %#v", config)
	}
}

func TestEnableRejectsUnsupportedOrMissingScheduler(t *testing.T) {
	for _, scheduler := range []string{"k3s", ""} {
		t.Run(scheduler, func(t *testing.T) {
			runner := &fakeRunner{outputFn: func(CommandSpec) ([]byte, error) {
				return []byte(scheduler + "\n"), nil
			}}
			plugin := newTestPlugin(t.TempDir(), runner)
			err := plugin.commandEnable([]string{
				"sample", "--mount-path", "/app/secrets", "--role-name", "sample-role",
			}, io.Discard, io.Discard)
			if err == nil {
				t.Fatalf("scheduler %q was accepted", scheduler)
			}
			if len(runner.specs) != 2 {
				t.Fatalf("expected only app and scheduler checks, got %d calls", len(runner.specs))
			}
		})
	}
}

func TestEnableIsIdempotentForExistingConfiguration(t *testing.T) {
	runner := &fakeRunner{outputFn: func(CommandSpec) ([]byte, error) {
		return []byte("docker-local\n"), nil
	}}
	plugin := newTestPlugin(t.TempDir(), runner)
	config := AppConfig{
		AppName: "sample", Enabled: true, MountPath: "/app/secrets", RoleName: "sample-role",
		AppRoleMount: DefaultAppRoleMount, StorageEntry: storageEntryName("sample"), TemplateMode: DefaultTemplateMode,
	}
	if err := plugin.State.SaveApp(config); err != nil {
		t.Fatal(err)
	}

	if err := plugin.commandEnable([]string{
		"sample", "--mount-path", "/app/secrets", "--role-name", "sample-role",
	}, io.Discard, io.Discard); err != nil {
		t.Fatalf("repeated enable failed: %v", err)
	}
	if len(runner.specs) != 2 {
		t.Fatalf("repeated enable should only validate the app and scheduler, got %d calls", len(runner.specs))
	}
	loaded, err := plugin.State.LoadApp("sample")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.MountPath != config.MountPath || loaded.RoleName != config.RoleName || loaded.AppRoleMount != config.AppRoleMount {
		t.Fatalf("idempotent enable changed configuration: %#v", loaded)
	}
}

func TestEnableUpdatesExistingConfiguration(t *testing.T) {
	runner := &fakeRunner{outputFn: func(CommandSpec) ([]byte, error) {
		return []byte("docker-local\n"), nil
	}}
	plugin := newTestPlugin(t.TempDir(), runner)
	config := AppConfig{
		AppName: "sample", Enabled: true, MountPath: "/app/old-secrets", RoleName: "old-role",
		AppRoleMount: DefaultAppRoleMount, StorageEntry: storageEntryName("sample"), TemplateMode: DefaultTemplateMode,
	}
	if err := plugin.State.SaveApp(config); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(plugin.State.PendingTokenPath("sample"), []byte("wrapped-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeJSONAtomic(plugin.State.PendingMetadataPath("sample"), StagedCredential{}, 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(plugin.State.RoleIDPath("sample"), []byte("old-role-id\n"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := plugin.commandEnable([]string{
		"sample", "--mount-path", "/app/new-secrets", "--role-name", "new-role",
		"--approle-mount", "auth/apps",
	}, io.Discard, io.Discard); err != nil {
		t.Fatalf("enable update failed: %v", err)
	}
	if len(runner.specs) != 4 {
		t.Fatalf("expected app check, scheduler check, new mount, and old unmount; got %d calls", len(runner.specs))
	}
	mount := strings.Join(runner.specs[2].Args, " ")
	unmount := strings.Join(runner.specs[3].Args, " ")
	if !strings.Contains(mount, "storage:mount sample "+config.StorageEntry+" --container-dir /app/new-secrets") {
		t.Fatalf("unexpected updated mount command: %s", mount)
	}
	if !strings.Contains(unmount, "storage:unmount sample "+config.StorageEntry+" --container-dir /app/old-secrets") {
		t.Fatalf("unexpected previous unmount command: %s", unmount)
	}
	loaded, err := plugin.State.LoadApp("sample")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.MountPath != "/app/new-secrets" || loaded.RoleName != "new-role" || loaded.AppRoleMount != "auth/apps" {
		t.Fatalf("configuration was not updated: %#v", loaded)
	}
	if exists(plugin.State.PendingTokenPath("sample")) || exists(plugin.State.PendingMetadataPath("sample")) {
		t.Fatal("AppRole update retained a staged credential for the old response-wrapping path")
	}
	if exists(plugin.State.RoleIDPath("sample")) {
		t.Fatal("AppRole update retained the old role's RoleID")
	}
}

func TestEnableAppRoleOnlyUpdateDoesNotRemountStorage(t *testing.T) {
	runner := &fakeRunner{outputFn: func(CommandSpec) ([]byte, error) {
		return []byte("docker-local\n"), nil
	}}
	plugin := newTestPlugin(t.TempDir(), runner)
	config := AppConfig{
		AppName: "sample", Enabled: true, MountPath: "/app/secrets", RoleName: "old-role",
		AppRoleMount: DefaultAppRoleMount, StorageEntry: storageEntryName("sample"), TemplateMode: DefaultTemplateMode,
	}
	if err := plugin.State.SaveApp(config); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(plugin.State.RoleIDPath("sample"), []byte("old-role-id\n"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := plugin.commandEnable([]string{
		"sample", "--mount-path", "/app/secrets", "--role-name", "new-role",
	}, io.Discard, io.Discard); err != nil {
		t.Fatalf("AppRole-only enable update failed: %v", err)
	}
	if len(runner.specs) != 2 {
		t.Fatalf("AppRole-only update should not mutate storage, got %d calls", len(runner.specs))
	}
	loaded, err := plugin.State.LoadApp("sample")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.RoleName != "new-role" {
		t.Fatalf("role name was not updated: %#v", loaded)
	}
	if exists(plugin.State.RoleIDPath("sample")) {
		t.Fatal("AppRole-only update retained the old role's RoleID")
	}
}

func TestEnableMountUpdateRollsBackWhenPreviousUnmountFails(t *testing.T) {
	runner := &fakeRunner{
		outputFn: func(CommandSpec) ([]byte, error) { return []byte("docker-local\n"), nil },
		runFn: func(spec CommandSpec) error {
			joined := strings.Join(spec.Args, " ")
			if strings.Contains(joined, "storage:unmount") && strings.Contains(joined, "--container-dir /app/old-secrets") {
				return errors.New("unmount rejected")
			}
			return nil
		},
	}
	plugin := newTestPlugin(t.TempDir(), runner)
	config := AppConfig{
		AppName: "sample", Enabled: true, MountPath: "/app/old-secrets", RoleName: "sample-role",
		AppRoleMount: DefaultAppRoleMount, StorageEntry: storageEntryName("sample"), TemplateMode: DefaultTemplateMode,
	}
	if err := plugin.State.SaveApp(config); err != nil {
		t.Fatal(err)
	}

	err := plugin.commandEnable([]string{
		"sample", "--mount-path", "/app/new-secrets", "--role-name", "sample-role",
	}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "unmount previous") {
		t.Fatalf("unexpected mount update result: %v", err)
	}
	if len(runner.specs) != 6 {
		t.Fatalf("expected validation, update, and two rollback calls; got %d calls", len(runner.specs))
	}
	restore := strings.Join(runner.specs[4].Args, " ")
	remove := strings.Join(runner.specs[5].Args, " ")
	if !strings.Contains(restore, "storage:mount sample "+config.StorageEntry+" --container-dir /app/old-secrets") {
		t.Fatalf("previous mount was not restored: %s", restore)
	}
	if !strings.Contains(remove, "storage:unmount sample "+config.StorageEntry+" --container-dir /app/new-secrets") {
		t.Fatalf("updated mount was not rolled back: %s", remove)
	}
	loaded, loadErr := plugin.State.LoadApp("sample")
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if loaded.MountPath != config.MountPath {
		t.Fatalf("failed update changed stored mount path to %q", loaded.MountPath)
	}
}

func TestEnableDoesNotReviveCleanupTombstone(t *testing.T) {
	runner := &fakeRunner{outputFn: func(CommandSpec) ([]byte, error) {
		return []byte("docker-local\n"), nil
	}}
	plugin := newTestPlugin(t.TempDir(), runner)
	config := AppConfig{
		AppName: "sample", Enabled: false, CleanupPhase: cleanupPhasePending,
		MountPath: "/app/secrets", RoleName: "sample-role", AppRoleMount: DefaultAppRoleMount,
		StorageEntry: storageEntryName("sample"), TemplateMode: DefaultTemplateMode,
	}
	if err := plugin.State.SaveApp(config); err != nil {
		t.Fatal(err)
	}

	err := plugin.commandEnable([]string{
		"sample", "--mount-path", "/app/new-secrets", "--role-name", "new-role",
	}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "incomplete cleanup") {
		t.Fatalf("cleanup tombstone returned unexpected result: %v", err)
	}
	if len(runner.specs) != 2 {
		t.Fatalf("cleanup tombstone should not mutate storage, got %d calls", len(runner.specs))
	}
}

func TestPurgeRetainsStateAndSecretsUntilStorageDestroyCanBeRetried(t *testing.T) {
	destroyAttempts := 0
	runner := &fakeRunner{runFn: func(spec CommandSpec) error {
		if len(spec.Args) > 0 && spec.Args[0] == "storage:destroy" {
			destroyAttempts++
			if destroyAttempts == 1 {
				return errors.New("storage backend unavailable")
			}
		}
		return nil
	}}
	plugin := newTestPlugin(t.TempDir(), runner)
	entry := storageEntryName("sample")
	config := AppConfig{AppName: "sample", Enabled: true, StorageEntry: entry, TemplateMode: DefaultTemplateMode}
	if err := plugin.State.SaveApp(config); err != nil {
		t.Fatal(err)
	}
	renderedFile := filepath.Join(plugin.State.RenderedDir(entry), "client.jks")
	if err := os.MkdirAll(filepath.Dir(renderedFile), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(renderedFile, []byte("keystore"), 0444); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(plugin.State.PendingTokenPath("sample"), []byte("token\n"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := plugin.purgeApp("sample", io.Discard, io.Discard); err == nil {
		t.Fatal("purge succeeded despite storage destroy failure")
	}
	retained, err := plugin.State.LoadApp("sample")
	if err != nil {
		t.Fatalf("cleanup state was lost: %v", err)
	}
	if retained.Enabled || retained.CleanupPhase != cleanupPhasePending {
		t.Fatalf("unexpected cleanup tombstone: %#v", retained)
	}
	for _, path := range []string{renderedFile, plugin.State.PendingTokenPath("sample")} {
		if !exists(path) {
			t.Fatalf("secret recovery data was removed after failed cleanup: %s", path)
		}
	}
	if err := plugin.purgeApp("sample", io.Discard, io.Discard); err != nil {
		t.Fatalf("cleanup retry failed: %v", err)
	}
	if _, err := plugin.State.LoadApp("sample"); !isNotExist(err) {
		t.Fatalf("state remains after successful retry: %v", err)
	}
	if exists(renderedFile) || exists(plugin.State.PendingTokenPath("sample")) {
		t.Fatal("secrets remain after successful cleanup retry")
	}
}

func TestEnableRecordsTombstoneWhenRollbackDestroyFails(t *testing.T) {
	runner := &fakeRunner{
		outputFn: func(CommandSpec) ([]byte, error) { return []byte("docker-local\n"), nil },
		runFn: func(spec CommandSpec) error {
			if len(spec.Args) > 0 {
				switch spec.Args[0] {
				case "storage:mount":
					return errors.New("mount rejected")
				case "storage:destroy":
					return errors.New("storage backend unavailable")
				}
			}
			return nil
		},
	}
	plugin := newTestPlugin(t.TempDir(), runner)
	err := plugin.commandEnable([]string{
		"sample", "--mount-path", "/app/secrets", "--role-name", "sample-role",
	}, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("enable succeeded despite mount and rollback failures")
	}
	config, loadErr := plugin.State.LoadApp("sample")
	if loadErr != nil {
		t.Fatalf("rollback tombstone was not saved: %v", loadErr)
	}
	if config.Enabled || config.CleanupPhase != cleanupPhasePending {
		t.Fatalf("unexpected rollback tombstone: %#v", config)
	}
	if !exists(plugin.State.RenderedDir(config.StorageEntry)) {
		t.Fatal("rendered directory was discarded while storage entry may still reference it")
	}
}
