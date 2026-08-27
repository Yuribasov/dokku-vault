package plugin

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerationPublicationDoesNotExposePartialFileSet(t *testing.T) {
	root := t.TempDir()
	live := filepath.Join(root, "rendered", "vault-sample")
	oldSource := filepath.Join(root, "old")
	if err := os.MkdirAll(oldSource, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldSource, "first"), []byte("old-first"), 0444); err != nil {
		t.Fatal(err)
	}
	oldGeneration, err := publishRenderedOutputs(oldSource, live, []renderOutput{{Relative: "first", Perms: "0444"}})
	if err != nil {
		t.Fatal(err)
	}

	newSource := filepath.Join(root, "new")
	if err := os.MkdirAll(newSource, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newSource, "first"), []byte("new-first"), 0444); err != nil {
		t.Fatal(err)
	}
	_, err = publishRenderedOutputs(newSource, live, []renderOutput{
		{Relative: "first", Perms: "0444"},
		{Relative: "missing-second", Perms: "0444"},
	})
	if err == nil {
		t.Fatal("publication succeeded with a missing second output")
	}
	current, err := currentGeneration(live)
	if err != nil {
		t.Fatal(err)
	}
	if current != oldGeneration {
		t.Fatalf("live generation changed after partial publication: got %q want %q", current, oldGeneration)
	}
	data, err := os.ReadFile(filepath.Join(live, "first"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "old-first" {
		t.Fatalf("live file changed after failed publication: %q", data)
	}
}

func TestPostDeployPromotesGenerationAndRetainsPrevious(t *testing.T) {
	plugin := newTestPlugin(t.TempDir(), nil)
	entry := storageEntryName("sample")
	live := plugin.State.RenderedDir(entry)
	initial, err := ensureGenerationStorage(live)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "source")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(source, "secret")
	if err := os.WriteFile(secret, []byte("one"), 0444); err != nil {
		t.Fatal(err)
	}
	first, err := publishRenderedOutputs(source, live, []renderOutput{{Relative: "secret", Perms: "0444"}})
	if err != nil {
		t.Fatal(err)
	}
	config := AppConfig{
		AppName: "sample", Enabled: true, StorageEntry: entry, TemplateMode: DefaultTemplateMode,
		ActiveGeneration: initial, PendingGeneration: first,
	}
	if err := plugin.State.SaveApp(config); err != nil {
		t.Fatal(err)
	}
	if err := plugin.triggerPostDeploy([]string{"sample"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	promoted, err := plugin.State.LoadApp("sample")
	if err != nil {
		t.Fatal(err)
	}
	if promoted.ActiveGeneration != first || promoted.PendingGeneration != "" {
		t.Fatalf("generation was not promoted: %#v", promoted)
	}
	for _, generation := range []string{initial, first} {
		if info, err := os.Stat(filepath.Join(generationsDir(live), generation)); err != nil || !info.IsDir() {
			t.Fatalf("required generation %q was removed: %v", generation, err)
		}
	}

	if err := os.Remove(secret); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secret, []byte("two"), 0444); err != nil {
		t.Fatal(err)
	}
	second, err := publishRenderedOutputs(source, live, []renderOutput{{Relative: "secret", Perms: "0444"}})
	if err != nil {
		t.Fatal(err)
	}
	promoted.PendingGeneration = second
	if err := plugin.State.SaveApp(promoted); err != nil {
		t.Fatal(err)
	}
	if err := plugin.triggerPostDeploy([]string{"sample"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(generationsDir(live), initial)) {
		t.Fatal("generation older than the previous successful deploy was not cleaned")
	}
	for _, generation := range []string{first, second} {
		if !exists(filepath.Join(generationsDir(live), generation)) {
			t.Fatalf("current or previous generation %q was removed", generation)
		}
	}
}

func TestExistingRenderedDirectoryMigratesToGenerationSymlink(t *testing.T) {
	live := filepath.Join(t.TempDir(), "vault-sample")
	if err := os.MkdirAll(live, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(live, "secret"), []byte("legacy"), 0444); err != nil {
		t.Fatal(err)
	}
	generation, err := ensureGenerationStorage(live)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(live)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("legacy rendered directory was not replaced by a symlink")
	}
	if generation == "" {
		t.Fatal("migration returned an empty generation")
	}
	data, err := os.ReadFile(filepath.Join(live, "secret"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "legacy" {
		t.Fatalf("migration changed rendered content: %q", data)
	}
}

func TestSupersededGenerationRetentionIsBounded(t *testing.T) {
	root := t.TempDir()
	live := filepath.Join(root, "rendered", "vault-sample")
	active, err := ensureGenerationStorage(live)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "source")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(source, "secret")
	var pending string
	for index := 0; index < 8; index++ {
		if err := os.WriteFile(secret, []byte{byte(index + 1)}, 0444); err != nil {
			t.Fatal(err)
		}
		generation, err := publishRenderedOutputs(source, live, []renderOutput{{Relative: "secret", Perms: "0444"}})
		if err != nil {
			t.Fatal(err)
		}
		pending = generation
		if err := cleanupSupersededGenerations(live, maximumRetainedSupersededGenerations, active, pending); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(generationsDir(live))
	if err != nil {
		t.Fatal(err)
	}
	maximum := 2 + maximumRetainedSupersededGenerations
	if len(entries) > maximum {
		t.Fatalf("retained %d generations after repeated failed deploys, want at most %d", len(entries), maximum)
	}
	for _, generation := range []string{active, pending} {
		if !exists(filepath.Join(generationsDir(live), generation)) {
			t.Fatalf("required generation %q was removed", generation)
		}
	}
}
