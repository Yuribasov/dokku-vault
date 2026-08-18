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
