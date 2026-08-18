package plugin

import (
	"io"
	"strings"
	"testing"
)

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
