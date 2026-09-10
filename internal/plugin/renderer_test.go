package plugin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

func TestRenderConsumesCredentialAndPublishesFile(t *testing.T) {
	root := t.TempDir()
	revision := strings.Repeat("a", 40)
	image := "hashicorp/vault:1.20.2@sha256:" + strings.Repeat("b", 64)
	var sawDocker bool
	runner := &fakeRunner{}
	runner.outputFn = func(spec CommandSpec) ([]byte, error) {
		if strings.Join(spec.Args, " ") != "trigger git-revision sample" {
			return nil, fmt.Errorf("unexpected output command: %v", spec.Args)
		}
		return []byte(revision + "\n"), nil
	}
	runner.runFn = func(spec CommandSpec) error {
		if len(spec.Args) < 2 || spec.Args[0] != "container" || spec.Args[1] != "run" {
			return fmt.Errorf("unexpected run command: %s %v", spec.Name, spec.Args)
		}
		sawDocker = true
		joined := strings.Join(spec.Args, " ")
		for _, required := range []string{"--read-only", "--cap-drop ALL", "no-new-privileges:true", "--userns=host", image} {
			if !strings.Contains(joined, required) {
				return fmt.Errorf("Docker arguments lack %q: %s", required, joined)
			}
		}
		if strings.Contains(joined, "wrapped-token") {
			return fmt.Errorf("wrapping token leaked into Docker arguments")
		}
		var outputRoot string
		for index, arg := range spec.Args {
			if arg == "--mount" && index+1 < len(spec.Args) && strings.Contains(spec.Args[index+1], "dst=/vault/rendered") {
				for _, component := range strings.Split(spec.Args[index+1], ",") {
					if strings.HasPrefix(component, "src=") {
						outputRoot = strings.TrimPrefix(component, "src=")
					}
				}
			}
		}
		if outputRoot == "" {
			return fmt.Errorf("render output mount not found")
		}
		target := filepath.Join(outputRoot, "mongo", "client.jks")
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		if err := os.WriteFile(target, []byte("binary-keystore"), 0444); err != nil {
			return err
		}
		return os.Chmod(target, 0444)
	}
	plugin := newTestPlugin(root, runner)
	if err := plugin.State.SaveGlobal(GlobalConfig{VaultAddress: "https://vault.example.test", Image: image}); err != nil {
		t.Fatal(err)
	}
	config := AppConfig{
		AppName: "sample", Enabled: true, MountPath: "/app/secrets", RoleName: "sample",
		AppRoleMount: DefaultAppRoleMount, StorageEntry: storageEntryName("sample"),
		TemplateMode: DefaultTemplateMode,
		Templates: []ManagedTemplate{{
			Name: "keystore", SecretPath: "secret/data/sample", Field: "jks",
			Destination: "mongo/client.jks", Decode: "base64", Perms: "0444",
		}},
	}
	if err := plugin.State.SaveApp(config); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(plugin.State.RoleIDPath("sample"), []byte("role-id\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := plugin.commandStage([]string{"sample", "--revision", revision, "--ttl-seconds", "300"}, strings.NewReader("wrapped-token\n")); err != nil {
		t.Fatal(err)
	}
	if current, err := user.Current(); err == nil {
		t.Setenv("DOKKU_SYSTEM_USER", current.Username)
	}
	if err := plugin.renderApp("sample", io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !sawDocker {
		t.Fatal("Docker runner was not invoked")
	}
	published := filepath.Join(plugin.State.RenderedDir(config.StorageEntry), "mongo", "client.jks")
	data, err := os.ReadFile(published)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "binary-keystore" {
		t.Fatalf("unexpected published data: %q", data)
	}
	if exists(plugin.State.PendingTokenPath("sample")) || exists(plugin.State.PendingMetadataPath("sample")) {
		t.Fatal("staged credential was not consumed")
	}
	if err := plugin.renderApp("sample", io.Discard, io.Discard); err == nil {
		t.Fatal("a replay without a new staged credential succeeded")
	}
}

func TestStageReplacesTokenWithoutReportingIt(t *testing.T) {
	plugin := newTestPlugin(t.TempDir(), nil)
	config := AppConfig{AppName: "sample", Enabled: true, StorageEntry: storageEntryName("sample"), TemplateMode: DefaultTemplateMode}
	if err := plugin.State.SaveApp(config); err != nil {
		t.Fatal(err)
	}
	firstRevision := strings.Repeat("a", 40)
	secondRevision := strings.Repeat("b", 40)
	if err := plugin.commandStage([]string{"sample", "--revision", firstRevision, "--ttl-seconds", "60"}, strings.NewReader("first-token")); err != nil {
		t.Fatal(err)
	}
	if err := plugin.commandStage([]string{"sample", "--revision", secondRevision, "--ttl-seconds", "60"}, strings.NewReader("second-token")); err != nil {
		t.Fatal(err)
	}
	var report strings.Builder
	if err := plugin.commandReport([]string{"sample"}, &report); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(report.String(), "second-token") || !strings.Contains(report.String(), secondRevision) {
		t.Fatalf("report leaked a token or omitted revision: %s", report.String())
	}
	data, err := os.ReadFile(plugin.State.PendingTokenPath("sample"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != "second-token" {
		t.Fatal("new staged token did not replace the old token")
	}
}

func TestStageAcceptsImmutableSourceImageAndReportsBinding(t *testing.T) {
	plugin := newTestPlugin(t.TempDir(), nil)
	config := AppConfig{AppName: "sample", Enabled: true, StorageEntry: storageEntryName("sample"), TemplateMode: DefaultTemplateMode}
	if err := plugin.State.SaveApp(config); err != nil {
		t.Fatal(err)
	}
	sourceImage := "registry.example.test/team/sample:build-42@sha256:" + strings.Repeat("a", 64)
	if err := plugin.commandStage(
		[]string{"sample", "--source-image", sourceImage, "--ttl-seconds", "60"},
		strings.NewReader("wrapped-token"),
	); err != nil {
		t.Fatal(err)
	}
	var credential StagedCredential
	if err := readJSON(plugin.State.PendingMetadataPath("sample"), &credential); err != nil {
		t.Fatal(err)
	}
	if credential.SourceImage != sourceImage || credential.Revision != "" {
		t.Fatalf("staged binding = %#v, want source image %q", credential, sourceImage)
	}
	var report strings.Builder
	if err := plugin.commandReport([]string{"sample"}, &report); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report.String(), "Expected source image: "+sourceImage) || strings.Contains(report.String(), "Expected revision:") {
		t.Fatalf("report returned unexpected source-image binding:\n%s", report.String())
	}
}

func TestStageRequiresExactlyOneImmutableDeploymentBinding(t *testing.T) {
	plugin := newTestPlugin(t.TempDir(), nil)
	config := AppConfig{AppName: "sample", Enabled: true, StorageEntry: storageEntryName("sample"), TemplateMode: DefaultTemplateMode}
	if err := plugin.State.SaveApp(config); err != nil {
		t.Fatal(err)
	}
	revision := strings.Repeat("a", 40)
	digestImage := "registry.example.test/team/sample@sha256:" + strings.Repeat("b", 64)
	tests := []struct {
		name string
		args []string
	}{
		{name: "neither", args: []string{"sample", "--ttl-seconds", "60"}},
		{name: "both", args: []string{"sample", "--revision", revision, "--source-image", digestImage, "--ttl-seconds", "60"}},
		{name: "mutable-image", args: []string{"sample", "--source-image", "registry.example.test/team/sample:latest", "--ttl-seconds", "60"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := plugin.commandStage(test.args, strings.NewReader("wrapped-token")); err == nil {
				t.Fatal("invalid deployment binding was accepted")
			}
		})
	}
}

func TestVerifySourceImageBindingForImageDeployment(t *testing.T) {
	sourceImage := "registry.example.test/team/sample@sha256:" + strings.Repeat("c", 64)
	currentImage := sourceImage
	runner := &fakeRunner{outputFn: func(spec CommandSpec) ([]byte, error) {
		if strings.Join(spec.Args, " ") != "trigger git-get-property sample source-image" {
			return nil, fmt.Errorf("unexpected output command: %v", spec.Args)
		}
		return []byte(currentImage + "\n"), nil
	}}
	plugin := newTestPlugin(t.TempDir(), runner)
	credential := StagedCredential{SourceImage: sourceImage}
	if err := plugin.verifyStagedBinding("sample", credential, true, "git:from-image", io.Discard); err != nil {
		t.Fatalf("matching git:from-image binding failed: %v", err)
	}
	if err := plugin.verifyStagedBinding("sample", credential, true, "git:load-image", io.Discard); err != nil {
		t.Fatalf("matching git:load-image binding failed: %v", err)
	}
	err := plugin.verifyStagedBinding("sample", credential, true, "git-hook", io.Discard)
	if err == nil || !strings.Contains(err.Error(), "requires git:from-image or git:load-image") {
		t.Fatalf("source-image credential accepted for Git deployment: %v", err)
	}
	currentImage = "registry.example.test/team/sample@sha256:" + strings.Repeat("d", 64)
	err = plugin.verifyStagedBinding("sample", credential, true, "git:from-image", io.Discard)
	if err == nil || !strings.Contains(err.Error(), "does not match current source image") {
		t.Fatalf("mismatched source image was accepted: %v", err)
	}
	for _, invalid := range []StagedCredential{
		{},
		{Revision: strings.Repeat("e", 40), SourceImage: sourceImage},
	} {
		if err := plugin.verifyStagedBinding("sample", invalid, false, "", io.Discard); err == nil || !strings.Contains(err.Error(), "invalid deployment binding") {
			t.Fatalf("invalid stored binding returned unexpected result: %v", err)
		}
	}
}

func TestStageWaitsForInFlightRender(t *testing.T) {
	root := t.TempDir()
	revision := strings.Repeat("c", 40)
	image := "hashicorp/vault:1.20.2@sha256:" + strings.Repeat("d", 64)
	started := make(chan struct{})
	release := make(chan struct{})
	runner := &fakeRunner{
		outputFn: func(CommandSpec) ([]byte, error) { return []byte(revision + "\n"), nil },
		runFn: func(spec CommandSpec) error {
			close(started)
			<-release
			var outputRoot string
			for index, arg := range spec.Args {
				if arg == "--mount" && index+1 < len(spec.Args) && strings.Contains(spec.Args[index+1], "dst=/vault/rendered") {
					for _, component := range strings.Split(spec.Args[index+1], ",") {
						if strings.HasPrefix(component, "src=") {
							outputRoot = strings.TrimPrefix(component, "src=")
						}
					}
				}
			}
			if outputRoot == "" {
				return fmt.Errorf("render output mount not found")
			}
			target := filepath.Join(outputRoot, "secret.txt")
			if err := os.WriteFile(target, []byte("first"), 0444); err != nil {
				return err
			}
			return os.Chmod(target, 0444)
		},
	}
	plugin := newTestPlugin(root, runner)
	if err := plugin.State.SaveGlobal(GlobalConfig{VaultAddress: "https://vault.example.test", Image: image}); err != nil {
		t.Fatal(err)
	}
	config := AppConfig{
		AppName: "sample", Enabled: true, MountPath: "/app/secrets", RoleName: "sample",
		AppRoleMount: DefaultAppRoleMount, StorageEntry: storageEntryName("sample"),
		TemplateMode: DefaultTemplateMode,
		Templates:    []ManagedTemplate{{Name: "secret", SecretPath: "secret/data/sample", Field: "value", Destination: "secret.txt", Decode: "none", Perms: "0444"}},
	}
	if err := plugin.State.SaveApp(config); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(plugin.State.RoleIDPath("sample"), []byte("role-id\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := plugin.commandStage([]string{"sample", "--revision", revision, "--ttl-seconds", "300"}, strings.NewReader("first-token\n")); err != nil {
		t.Fatal(err)
	}

	renderDone := make(chan error, 1)
	go func() { renderDone <- plugin.renderApp("sample", io.Discard, io.Discard) }()
	<-started
	stageDone := make(chan error, 1)
	go func() {
		stageDone <- plugin.commandStage([]string{"sample", "--revision", revision, "--ttl-seconds", "300"}, strings.NewReader("second-token\n"))
	}()
	select {
	case err := <-stageDone:
		t.Fatalf("stage completed while render held the app lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-renderDone; err != nil {
		t.Fatal(err)
	}
	if err := <-stageDone; err != nil {
		t.Fatal(err)
	}
	token, err := os.ReadFile(plugin.State.PendingTokenPath("sample"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(token)) != "second-token" {
		t.Fatalf("pending token = %q, want second-token", token)
	}
}

func TestRenderTimeoutForcesNamedContainerCleanup(t *testing.T) {
	root := t.TempDir()
	revision := strings.Repeat("e", 40)
	image := "hashicorp/vault:1.20.2@sha256:" + strings.Repeat("f", 64)
	var sawDeadline, sawCleanup bool
	runner := &fakeRunner{
		outputFn: func(CommandSpec) ([]byte, error) { return []byte(revision + "\n"), nil },
		runFn: func(spec CommandSpec) error {
			joined := strings.Join(spec.Args, " ")
			if strings.HasPrefix(joined, "container run ") {
				if spec.Context == nil {
					return fmt.Errorf("render command has no context")
				}
				if _, ok := spec.Context.Deadline(); !ok {
					return fmt.Errorf("render command has no deadline")
				}
				sawDeadline = true
				<-spec.Context.Done()
				return spec.Context.Err()
			}
			if strings.HasPrefix(joined, "container rm --force dokku-vault-agent-") {
				sawCleanup = true
				return nil
			}
			return fmt.Errorf("unexpected command: %s", joined)
		},
	}
	plugin := newTestPlugin(root, runner)
	if err := plugin.State.SaveGlobal(GlobalConfig{VaultAddress: "https://vault.example.test", Image: image}); err != nil {
		t.Fatal(err)
	}
	config := AppConfig{
		AppName: "sample", Enabled: true, MountPath: "/app/secrets", RoleName: "sample",
		AppRoleMount: DefaultAppRoleMount, StorageEntry: storageEntryName("sample"), TemplateMode: DefaultTemplateMode,
		Templates: []ManagedTemplate{{Name: "secret", SecretPath: "secret/data/sample", Field: "value", Destination: "secret", Decode: "none", Perms: "0444"}},
	}
	if err := plugin.State.SaveApp(config); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(plugin.State.RoleIDPath("sample"), []byte("role-id\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := plugin.commandStage([]string{"sample", "--revision", revision, "--ttl-seconds", "300"}, strings.NewReader("wrapped-token\n")); err != nil {
		t.Fatal(err)
	}
	var staged StagedCredential
	if err := readJSON(plugin.State.PendingMetadataPath("sample"), &staged); err != nil {
		t.Fatal(err)
	}
	staged.ExpiresAt = time.Now().Add(time.Second)
	if err := writeJSONAtomic(plugin.State.PendingMetadataPath("sample"), staged, 0600); err != nil {
		t.Fatal(err)
	}
	err := plugin.renderApp("sample", io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "Vault Agent render timed out") {
		t.Fatalf("unexpected timeout result: %v", err)
	}
	if !sawDeadline || !sawCleanup {
		t.Fatalf("deadline=%t cleanup=%t", sawDeadline, sawCleanup)
	}
}

func TestStoredCustomTemplateReadRemainsBounded(t *testing.T) {
	plugin := newTestPlugin(t.TempDir(), nil)
	config := AppConfig{AppName: "sample", TemplateMode: "custom", CustomHCLFile: "templates.hcl"}
	if err := plugin.State.EnsureApp("sample"); err != nil {
		t.Fatal(err)
	}
	oversized := make([]byte, 1024*1024+1)
	if err := os.WriteFile(plugin.State.CustomHCLPath("sample"), oversized, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := plugin.templateConfiguration("sample", config); err == nil {
		t.Fatal("oversized stored custom HCL was accepted")
	}
}

func TestManagedTemplatePreservesHCLInterpolationMarkers(t *testing.T) {
	plugin := newTestPlugin(t.TempDir(), nil)
	secretPath := `secret/data/${tenant}/%{segment}`
	field := `value"${field}%{literal}`
	destination := `nested/${destination}/%{literal}.txt`
	config := AppConfig{
		TemplateMode: DefaultTemplateMode,
		Templates: []ManagedTemplate{{
			SecretPath:  secretPath,
			Field:       field,
			Destination: destination,
			Decode:      "none",
			Perms:       "0444",
		}},
	}

	data, outputs, err := plugin.templateConfiguration("sample", config)
	if err != nil {
		t.Fatal(err)
	}
	if len(outputs) != 1 || outputs[0].Relative != destination {
		t.Fatalf("outputs = %#v, want relative path %q", outputs, destination)
	}

	file, diagnostics := hclsyntax.ParseConfig(data, "templates.hcl", hcl.Pos{Line: 1, Column: 1})
	if diagnostics.HasErrors() {
		t.Fatalf("generated HCL did not parse: %s\n%s", diagnostics.Error(), data)
	}
	body := file.Body.(*hclsyntax.Body)
	if len(body.Blocks) != 1 {
		t.Fatalf("generated HCL contains %d blocks, want 1", len(body.Blocks))
	}
	block := body.Blocks[0]
	wantValues := map[string]string{
		"contents":    fmt.Sprintf(`{{ with secret %q }}{{ index .Data.data %q }}{{ end }}`, secretPath, field),
		"destination": "/vault/rendered/" + destination,
		"perms":       "0444",
	}
	for name, want := range wantValues {
		attribute, ok := block.Body.Attributes[name]
		if !ok {
			t.Fatalf("generated HCL lacks %q attribute", name)
		}
		value, valueDiagnostics := attribute.Expr.Value(nil)
		if valueDiagnostics.HasErrors() {
			t.Fatalf("evaluate %q: %s", name, valueDiagnostics.Error())
		}
		if got := value.AsString(); got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestRenderReleasesGlobalLockBeforeVaultAgentRun(t *testing.T) {
	root := t.TempDir()
	revision := strings.Repeat("1", 40)
	image := "hashicorp/vault:1.20.2@sha256:" + strings.Repeat("2", 64)
	probeError := errors.New("stop after lock probe")
	var plugin *Plugin
	runner := &fakeRunner{
		outputFn: func(CommandSpec) ([]byte, error) { return []byte(revision + "\n"), nil },
		runFn: func(spec CommandSpec) error {
			if len(spec.Args) < 2 || spec.Args[0] != "container" || spec.Args[1] != "run" {
				return fmt.Errorf("unexpected command: %v", spec.Args)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			globalLock, err := lockFileContext(ctx, plugin.State.GlobalLockPath())
			if err != nil {
				return fmt.Errorf("global lock remained held during Vault Agent run: %w", err)
			}
			unlockFile(globalLock)
			return probeError
		},
	}
	plugin = newTestPlugin(root, runner)
	if err := plugin.State.SaveGlobal(GlobalConfig{VaultAddress: "https://vault.example.test", Image: image}); err != nil {
		t.Fatal(err)
	}
	config := AppConfig{
		AppName: "sample", Enabled: true, MountPath: "/app/secrets", RoleName: "sample",
		AppRoleMount: DefaultAppRoleMount, StorageEntry: storageEntryName("sample"), TemplateMode: DefaultTemplateMode,
		Templates: []ManagedTemplate{{Name: "secret", SecretPath: "secret/data/sample", Field: "value", Destination: "secret", Decode: "none", Perms: "0444"}},
	}
	if err := plugin.State.SaveApp(config); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(plugin.State.RoleIDPath("sample"), []byte("role-id\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := plugin.commandStage([]string{"sample", "--revision", revision, "--ttl-seconds", "300"}, strings.NewReader("wrapped-token\n")); err != nil {
		t.Fatal(err)
	}
	if current, err := user.Current(); err == nil {
		t.Setenv("DOKKU_SYSTEM_USER", current.Username)
	}
	err := plugin.renderApp("sample", io.Discard, io.Discard)
	if !errors.Is(err, probeError) {
		t.Fatalf("render error = %v, want probe error", err)
	}
}
