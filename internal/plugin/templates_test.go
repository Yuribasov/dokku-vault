package plugin

import (
	"strings"
	"testing"
)

func TestValidateCustomHCL(t *testing.T) {
	valid := `
template {
  contents = "{{ with secret \"secret/data/app\" }}{{ index .Data.data \"jks\" | base64Decode }}{{ end }}"
  destination = "/vault/rendered/mongo/client.jks"
  perms = "0440"
  backup = false
  create_dest_dirs = true
  error_on_missing_key = true
}
`
	outputs, err := validateCustomHCL([]byte(valid))
	if err != nil {
		t.Fatalf("valid custom HCL rejected: %v", err)
	}
	if len(outputs) != 1 || outputs[0].Destination != "mongo/client.jks" || outputs[0].Perms != "0440" {
		t.Fatalf("unexpected outputs: %#v", outputs)
	}

	tests := map[string]string{
		"source":              strings.Replace(valid, "contents =", "source =", 1),
		"command":             strings.Replace(valid, "  backup", "  command = \"touch /tmp/pwned\"\n  backup", 1),
		"outside destination": strings.Replace(valid, "/vault/rendered/mongo/client.jks", "/tmp/client.jks", 1),
		"writable mode":       strings.Replace(valid, "0440", "0644", 1),
		"backup":              strings.Replace(valid, "backup = false", "backup = true", 1),
		"top-level vault":     valid + "\nvault {}\n",
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := validateCustomHCL([]byte(input)); err == nil {
				t.Fatal("unsafe custom HCL was accepted")
			}
		})
	}
}

func TestManagedAndCustomModesAreExclusive(t *testing.T) {
	plugin := newTestPlugin(t.TempDir(), nil)
	config := AppConfig{AppName: "sample", Enabled: true, TemplateMode: DefaultTemplateMode}
	if err := plugin.State.SaveApp(config); err != nil {
		t.Fatal(err)
	}
	if err := plugin.commandTemplateAdd([]string{
		"sample", "keystore", "--secret-path", "secret/data/sample",
		"--field", "jks", "--destination", "client.jks", "--decode", "base64",
	}); err != nil {
		t.Fatal(err)
	}
	custom := `template {
  contents = "fixed"
  destination = "/vault/rendered/fixed.txt"
  perms = "0444"
  backup = false
}`
	if err := plugin.commandTemplateSetCustom([]string{"sample"}, strings.NewReader(custom)); err == nil {
		t.Fatal("custom mode replaced managed templates without --replace")
	}
	if err := plugin.commandTemplateSetCustom([]string{"sample", "--replace"}, strings.NewReader(custom)); err != nil {
		t.Fatal(err)
	}
	loaded, err := plugin.State.LoadApp("sample")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.TemplateMode != "custom" || len(loaded.Templates) != 0 {
		t.Fatalf("unexpected mode after replacement: %#v", loaded)
	}
}

func TestTemplateAddUpdatesExistingTemplate(t *testing.T) {
	plugin := newTestPlugin(t.TempDir(), nil)
	config := AppConfig{AppName: "sample", Enabled: true, TemplateMode: DefaultTemplateMode}
	if err := plugin.State.SaveApp(config); err != nil {
		t.Fatal(err)
	}
	if err := plugin.commandTemplateAdd([]string{
		"sample", "keystore", "--secret-path", "secret/data/sample/old",
		"--field", "old_jks", "--destination", "old/client.jks",
	}); err != nil {
		t.Fatal(err)
	}
	update := []string{
		"sample", "keystore", "--secret-path", "secret/data/sample/new",
		"--field", "new_jks", "--destination", "new/client.jks",
		"--decode", "none", "--perms", "0400",
	}
	if err := plugin.commandTemplateAdd(update); err != nil {
		t.Fatalf("template update failed: %v", err)
	}
	if err := plugin.commandTemplateAdd(update); err != nil {
		t.Fatalf("idempotent template update failed: %v", err)
	}

	loaded, err := plugin.State.LoadApp("sample")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Templates) != 1 {
		t.Fatalf("template update created duplicates: %#v", loaded.Templates)
	}
	got := loaded.Templates[0]
	if got.Name != "keystore" || got.SecretPath != "secret/data/sample/new" ||
		got.Field != "new_jks" || got.Destination != "new/client.jks" ||
		got.Decode != "none" || got.Perms != "0400" {
		t.Fatalf("template was not updated: %#v", got)
	}
}

func TestTemplateAddUpdateRejectsAnotherTemplatesDestination(t *testing.T) {
	plugin := newTestPlugin(t.TempDir(), nil)
	config := AppConfig{AppName: "sample", Enabled: true, TemplateMode: DefaultTemplateMode}
	if err := plugin.State.SaveApp(config); err != nil {
		t.Fatal(err)
	}
	first := []string{
		"sample", "first", "--secret-path", "secret/data/sample",
		"--field", "first", "--destination", "first.bin",
	}
	second := []string{
		"sample", "second", "--secret-path", "secret/data/sample",
		"--field", "second", "--destination", "second.bin",
	}
	if err := plugin.commandTemplateAdd(first); err != nil {
		t.Fatal(err)
	}
	if err := plugin.commandTemplateAdd(second); err != nil {
		t.Fatal(err)
	}
	err := plugin.commandTemplateAdd([]string{
		"sample", "first", "--secret-path", "secret/data/sample/new",
		"--field", "replacement", "--destination", "second.bin",
	})
	if err == nil || !strings.Contains(err.Error(), "already used by template") {
		t.Fatalf("destination collision returned unexpected result: %v", err)
	}

	loaded, loadErr := plugin.State.LoadApp("sample")
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if len(loaded.Templates) != 2 {
		t.Fatalf("failed update changed template count: %#v", loaded.Templates)
	}
	for _, template := range loaded.Templates {
		if template.Name == "first" && (template.Field != "first" || template.Destination != "first.bin") {
			t.Fatalf("failed update changed the original template: %#v", template)
		}
	}
}
