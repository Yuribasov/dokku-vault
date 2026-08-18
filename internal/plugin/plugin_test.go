package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUninstallCheckRefusesConfiguredAppsAndOrphanedSecrets(t *testing.T) {
	plugin := newTestPlugin(t.TempDir(), nil)
	config := AppConfig{AppName: "sample", Enabled: true, StorageEntry: storageEntryName("sample"), TemplateMode: DefaultTemplateMode}
	if err := plugin.State.SaveApp(config); err != nil {
		t.Fatal(err)
	}
	if err := plugin.checkUninstallSafe(); err == nil || !strings.Contains(err.Error(), "sample") {
		t.Fatalf("configured app did not block uninstall: %v", err)
	}
	if err := plugin.State.RemoveApp("sample"); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(plugin.State.renderedRoot(), "orphan")
	if err := os.MkdirAll(orphan, 0700); err != nil {
		t.Fatal(err)
	}
	if err := plugin.checkUninstallSafe(); err == nil || !strings.Contains(err.Error(), "orphaned") {
		t.Fatalf("orphaned secret data did not block uninstall: %v", err)
	}
	if err := os.RemoveAll(orphan); err != nil {
		t.Fatal(err)
	}
	if err := plugin.checkUninstallSafe(); err != nil {
		t.Fatalf("clean state blocked uninstall: %v", err)
	}
}
