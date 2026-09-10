package plugin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

const (
	maximumRenderedFileSize = 128 * 1024 * 1024
	maximumAgentRenderTime  = 5 * time.Minute
	containerCleanupTime    = 10 * time.Second
)

type renderOutput struct {
	Relative string
	Perms    string
}

func (p *Plugin) commandRender(args []string, stdout, stderr io.Writer) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: vault-agent:render APP")
	}
	return p.renderApp(args[0], stdout, stderr)
}

func (p *Plugin) renderApp(app string, stdout, stderr io.Writer) error {
	return p.renderAppWithInvocation(app, false, "", stdout, stderr)
}

func (p *Plugin) renderAppForDeployment(app string, stdout, stderr io.Writer) error {
	return p.renderAppWithInvocation(app, true, os.Getenv("DOKKU_BUILD_SOURCE"), stdout, stderr)
}

func (p *Plugin) renderAppWithInvocation(app string, deployment bool, deploymentSource string, stdout, stderr io.Writer) error {
	if err := validateAppName(app); err != nil {
		return err
	}
	lock, err := p.lockApp(app)
	if err != nil {
		return err
	}
	defer unlockFile(lock)
	config, err := p.State.LoadApp(app)
	if err != nil {
		return fmt.Errorf("app integration is not enabled: %w", err)
	}
	if !config.Enabled {
		return fmt.Errorf("Vault Agent integration is disabled for %q", app)
	}
	if err := validateStorageEntry(config.StorageEntry); err != nil {
		return err
	}
	if err := validateAppRoleMount(config.AppRoleMount); err != nil {
		return err
	}
	global, caCertificate, err := p.snapshotGlobalRenderConfiguration()
	if err != nil {
		return err
	}
	roleID, err := readCredentialFile(p.State.RoleIDPath(app), "RoleID")
	if err != nil {
		return err
	}
	if config.TemplateMode == DefaultTemplateMode && len(config.Templates) == 0 {
		return fmt.Errorf("no managed templates are configured for %q", app)
	}
	if config.TemplateMode != DefaultTemplateMode && config.TemplateMode != "custom" {
		return fmt.Errorf("unknown template mode %q", config.TemplateMode)
	}
	livePath := p.State.RenderedDir(config.StorageEntry)
	current, err := ensureGenerationStorage(livePath)
	if err != nil {
		return fmt.Errorf("prepare rendered storage: %w", err)
	}
	if config.ActiveGeneration == "" {
		config.ActiveGeneration = current
		if err := p.State.SaveApp(config); err != nil {
			return err
		}
	}

	credential, token, err := p.consumePending(app)
	if err != nil {
		return err
	}
	if time.Now().After(credential.ExpiresAt) {
		return fmt.Errorf("staged credential expired at %s", credential.ExpiresAt.Format(time.RFC3339))
	}
	if err := p.verifyStagedBinding(app, credential, deployment, deploymentSource, stderr); err != nil {
		return err
	}

	work, err := os.MkdirTemp(p.State.tempRoot(), app+"-render-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	if err := os.Chmod(work, 0700); err != nil {
		return err
	}
	configDir := filepath.Join(work, "config")
	authDir := filepath.Join(work, "auth")
	outputDir := filepath.Join(work, "rendered")
	for _, directory := range []string{configDir, authDir, outputDir} {
		if err := os.Mkdir(directory, 0700); err != nil {
			return err
		}
	}
	if err := writeFileAtomic(filepath.Join(configDir, "role-id"), []byte(roleID+"\n"), 0400); err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(authDir, "secret-id"), []byte(token+"\n"), 0400); err != nil {
		return err
	}
	token = ""
	hasCA := caCertificate != nil
	if hasCA {
		if err := writeFileAtomic(filepath.Join(configDir, "vault-ca.pem"), caCertificate, 0400); err != nil {
			return err
		}
	}
	baseHCL := buildBaseHCL(global, config, hasCA)
	if err := writeFileAtomic(filepath.Join(configDir, "base.hcl"), []byte(baseHCL), 0400); err != nil {
		return err
	}
	templateHCL, outputs, err := p.templateConfiguration(app, config)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(configDir, "templates.hcl"), templateHCL, 0400); err != nil {
		return err
	}

	docker := executableFromEnv("DOCKER_BIN", "docker")
	uid, gid := dokkuUserIDs()
	if err := ensureWorkspaceOwnership(work, uid, gid); err != nil {
		return err
	}
	dockerArgs := []string{
		"container", "run", "--rm",
		"--name", "dokku-vault-agent-" + filepath.Base(work),
		"--entrypoint", "vault",
		"--read-only",
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges:true",
		"--tmpfs", "/tmp:rw,noexec,nosuid,size=16m",
		"--user", uid + ":" + gid,
		"--userns=host",
		"--label", "com.dokku.app-name=" + app,
		"--label", "com.dokku.process-type=vault-agent",
		"--mount", bindMount(configDir, "/vault/config", true),
		"--mount", bindMount(authDir, "/vault/auth", false),
		"--mount", bindMount(outputDir, "/vault/rendered", false),
		global.Image,
		"agent", "-config=/vault/config/base.hcl", "-config=/vault/config/templates.hcl",
	}
	fmt.Fprintf(stdout, "-----> Rendering Vault secrets for %s\n", app)
	now := time.Now()
	deadline := now.Add(maximumAgentRenderTime)
	if credential.ExpiresAt.Before(deadline) {
		deadline = credential.ExpiresAt
	}
	if !deadline.After(now) {
		return fmt.Errorf("staged credential expired before Vault Agent could start")
	}
	renderContext, cancelRender := context.WithDeadline(context.Background(), deadline)
	defer cancelRender()
	if err := p.Runner.Run(CommandSpec{Context: renderContext, Name: docker, Args: dockerArgs, Stdout: stdout, Stderr: stderr}); err != nil {
		if errors.Is(renderContext.Err(), context.DeadlineExceeded) {
			cleanupContext, cancelCleanup := context.WithTimeout(context.Background(), containerCleanupTime)
			cleanupErr := p.Runner.Run(CommandSpec{
				Context: cleanupContext, Name: docker,
				Args:   []string{"container", "rm", "--force", "dokku-vault-agent-" + filepath.Base(work)},
				Stdout: io.Discard, Stderr: stderr,
			})
			cancelCleanup()
			return errors.Join(fmt.Errorf("Vault Agent render timed out after %s", deadline.Sub(now).Round(time.Millisecond)), cleanupErr)
		}
		return fmt.Errorf("Vault Agent render failed: %w", err)
	}
	if err := validateRenderedOutputs(outputDir, outputs); err != nil {
		return err
	}
	generation, err := publishRenderedOutputs(outputDir, livePath, outputs)
	if err != nil {
		return err
	}
	config.PendingGeneration = generation
	if err := p.State.SaveApp(config); err != nil {
		return fmt.Errorf("record rendered generation: %w", err)
	}
	if err := cleanupSupersededGenerations(livePath, maximumRetainedSupersededGenerations, config.ActiveGeneration, config.PendingGeneration); err != nil {
		fmt.Fprintf(stderr, "vault-agent: unable to remove superseded secret generations; they will be retried after a later render: %v\n", err)
	}
	fmt.Fprintf(stdout, "-----> Published %d rendered file(s) for %s\n", len(outputs), app)
	return nil
}

func (p *Plugin) verifyStagedBinding(app string, credential StagedCredential, deployment bool, deploymentSource string, stderr io.Writer) error {
	if credential.Revision != "" && credential.SourceImage == "" {
		revisionBytes, err := p.Runner.Output(CommandSpec{
			Name: executableFromEnv("PLUGN_BIN", "plugn"),
			Args: []string{"trigger", "git-revision", app},
			Env:  commandEnvironment(), Stderr: stderr,
		})
		if err != nil {
			return fmt.Errorf("read current Git revision: %w", err)
		}
		revision := strings.TrimSpace(string(revisionBytes))
		if revision != credential.Revision {
			return fmt.Errorf("staged revision %s does not match current revision %s", credential.Revision, revision)
		}
		return nil
	}
	if credential.SourceImage != "" && credential.Revision == "" {
		if deployment && deploymentSource != "git:from-image" && deploymentSource != "git:load-image" {
			return fmt.Errorf("source-image credential requires git:from-image or git:load-image deployment (current source: %s)", valueOrNone(deploymentSource))
		}
		imageBytes, err := p.Runner.Output(CommandSpec{
			Name: executableFromEnv("PLUGN_BIN", "plugn"),
			Args: []string{"trigger", "git-get-property", app, "source-image"},
			Env:  commandEnvironment(), Stderr: stderr,
		})
		if err != nil {
			return fmt.Errorf("read current source image: %w", err)
		}
		image := strings.TrimSpace(string(imageBytes))
		if image != credential.SourceImage {
			return fmt.Errorf("staged source image %s does not match current source image %s", credential.SourceImage, valueOrNone(image))
		}
		return nil
	}
	return fmt.Errorf("staged credential has invalid deployment binding")
}

func (p *Plugin) consumePending(app string) (StagedCredential, string, error) {
	var credential StagedCredential
	if err := readJSON(p.State.PendingMetadataPath(app), &credential); err != nil {
		return credential, "", fmt.Errorf("no usable staged credential: %w", err)
	}
	token, err := readCredentialFile(p.State.PendingTokenPath(app), "wrapping token")
	if err != nil {
		_ = os.Remove(p.State.PendingMetadataPath(app))
		return credential, "", err
	}
	removeTokenErr := secureRemove(p.State.PendingTokenPath(app))
	removeMetadataErr := os.Remove(p.State.PendingMetadataPath(app))
	if removeTokenErr != nil {
		return credential, "", removeTokenErr
	}
	if removeMetadataErr != nil && !isNotExist(removeMetadataErr) {
		return credential, "", removeMetadataErr
	}
	if !tokenPattern.MatchString(token) {
		return credential, "", fmt.Errorf("stored wrapping token is invalid")
	}
	return credential, token, nil
}

func readCredentialFile(path, label string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("%s is not configured: %w", label, err)
	}
	defer file.Close()
	return readSingleLine(file, maximumCredentialSize, label)
}

func buildBaseHCL(global GlobalConfig, app AppConfig, hasCA bool) string {
	var builder strings.Builder
	builder.WriteString("exit_after_auth = true\n\n")
	builder.WriteString("vault {\n")
	fmt.Fprintf(&builder, "  address = %s\n", hclStringLiteral(global.VaultAddress))
	if hasCA {
		builder.WriteString("  ca_cert = \"/vault/config/vault-ca.pem\"\n")
	}
	builder.WriteString("}\n\n")
	builder.WriteString("auto_auth {\n")
	builder.WriteString("  method \"approle\" {\n")
	fmt.Fprintf(&builder, "    mount_path = %s\n", hclStringLiteral(app.AppRoleMount))
	builder.WriteString("    config = {\n")
	builder.WriteString("      role_id_file_path = \"/vault/config/role-id\"\n")
	builder.WriteString("      secret_id_file_path = \"/vault/auth/secret-id\"\n")
	fmt.Fprintf(&builder, "      secret_id_response_wrapping_path = %s\n", hclStringLiteral(app.AppRoleMount+"/role/"+app.RoleName+"/secret-id"))
	builder.WriteString("      remove_secret_id_file_after_reading = true\n")
	builder.WriteString("    }\n")
	builder.WriteString("    exit_on_err = true\n")
	builder.WriteString("  }\n")
	builder.WriteString("}\n\n")
	builder.WriteString("template_config {\n  exit_on_retry_failure = true\n}\n")
	return builder.String()
}

func (p *Plugin) templateConfiguration(app string, config AppConfig) ([]byte, []renderOutput, error) {
	if config.TemplateMode == "custom" {
		file, err := os.Open(p.State.CustomHCLPath(app))
		if err != nil {
			return nil, nil, err
		}
		data, readErr := readBounded(file, 1024*1024)
		closeErr := file.Close()
		if readErr != nil {
			return nil, nil, readErr
		}
		if closeErr != nil {
			return nil, nil, closeErr
		}
		custom, err := validateCustomHCL(data)
		if err != nil {
			return nil, nil, err
		}
		outputs := make([]renderOutput, 0, len(custom))
		for _, output := range custom {
			outputs = append(outputs, renderOutput{Relative: output.Destination, Perms: output.Perms})
		}
		return data, outputs, nil
	}
	var builder strings.Builder
	outputs := make([]renderOutput, 0, len(config.Templates))
	for _, template := range config.Templates {
		if err := validateDestination(template.Destination); err != nil {
			return nil, nil, err
		}
		if err := validateMode(template.Perms); err != nil {
			return nil, nil, err
		}
		expression := fmt.Sprintf(`{{ with secret %q }}{{ index .Data.data %q }}{{ end }}`, template.SecretPath, template.Field)
		if template.Decode == "base64" {
			expression = fmt.Sprintf(`{{ with secret %q }}{{ index .Data.data %q | base64Decode }}{{ end }}`, template.SecretPath, template.Field)
		} else if template.Decode != "none" {
			return nil, nil, fmt.Errorf("unknown decode mode %q", template.Decode)
		}
		builder.WriteString("template {\n")
		fmt.Fprintf(&builder, "  contents = %s\n", hclStringLiteral(expression))
		fmt.Fprintf(&builder, "  destination = %s\n", hclStringLiteral("/vault/rendered/"+template.Destination))
		fmt.Fprintf(&builder, "  perms = %s\n", hclStringLiteral(template.Perms))
		builder.WriteString("  backup = false\n")
		builder.WriteString("  create_dest_dirs = true\n")
		builder.WriteString("  error_on_missing_key = true\n")
		builder.WriteString("}\n\n")
		outputs = append(outputs, renderOutput{Relative: template.Destination, Perms: template.Perms})
	}
	return []byte(builder.String()), outputs, nil
}

func hclStringLiteral(value string) string {
	return string(hclwrite.TokensForValue(cty.StringVal(value)).Bytes())
}

func bindMount(source, destination string, readonly bool) string {
	value := "type=bind,src=" + source + ",dst=" + destination
	if readonly {
		value += ",readonly"
	}
	return value
}

func dokkuUserIDs() (string, string) {
	name := os.Getenv("DOKKU_SYSTEM_USER")
	if name == "" {
		name = "dokku"
	}
	if account, err := user.Lookup(name); err == nil {
		return account.Uid, account.Gid
	}
	return strconv.Itoa(os.Getuid()), strconv.Itoa(os.Getgid())
}

func ensureWorkspaceOwnership(root, uidValue, gidValue string) error {
	uid, err := strconv.Atoi(uidValue)
	if err != nil {
		return err
	}
	gid, err := strconv.Atoi(gidValue)
	if err != nil {
		return err
	}
	if uid == os.Getuid() && gid == os.Getgid() {
		return nil
	}
	if os.Geteuid() != 0 {
		return fmt.Errorf("plugin runs as uid %d but Vault container requires uid %d", os.Getuid(), uid)
	}
	return filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		return os.Lchown(current, uid, gid)
	})
}

func copyProtectedFile(source, destination string, mode fs.FileMode) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return writeFileAtomic(destination, data, mode)
}

func validateRenderedOutputs(root string, outputs []renderOutput) error {
	for _, output := range outputs {
		target := filepath.Join(root, filepath.FromSlash(output.Relative))
		if err := ensurePathWithin(root, target); err != nil {
			return err
		}
		if err := rejectSymlinkComponents(root, target); err != nil {
			return err
		}
		info, err := os.Lstat(target)
		if err != nil {
			return fmt.Errorf("rendered output %q is missing: %w", output.Relative, err)
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			return fmt.Errorf("rendered output %q must be a non-empty regular file", output.Relative)
		}
		expected, _ := strconv.ParseUint(output.Perms, 8, 32)
		if info.Mode().Perm() != fs.FileMode(expected) {
			return fmt.Errorf("rendered output %q has mode %04o, expected %s", output.Relative, info.Mode().Perm(), output.Perms)
		}
	}
	return nil
}

func ensurePathWithin(root, target string) error {
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path %q escapes render root", target)
	}
	return nil
}

func rejectSymlinkComponents(root, target string) error {
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return err
	}
	current := root
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("rendered path %q contains a symlink", relative)
		}
	}
	return nil
}

func secureMkdirParents(root, target string) error {
	if err := ensurePathWithin(root, target); err != nil {
		return err
	}
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return err
	}
	current := root
	if info, err := os.Lstat(root); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("render destination root is not a secure directory")
	}
	if relative == "." {
		return nil
	}
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if isNotExist(err) {
			if err := os.Mkdir(current, 0755); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("render destination path component %q is not a directory", current)
		}
	}
	return nil
}
