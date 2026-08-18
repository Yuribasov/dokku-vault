package plugin

import (
	"fmt"
	"io"
	"os"
	"strings"
)

func commandEnvironment() []string {
	env := make([]string, 0, len(os.Environ()))
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "DOKKU_APP_NAME=") {
			env = append(env, value)
		}
	}
	return env
}

func executableFromEnv(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func (p *Plugin) commandEnable(args []string, stdout, stderr io.Writer) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: vault-agent:enable APP --mount-path PATH --role-name ROLE [--approle-mount PATH]")
	}
	app := args[0]
	if err := validateAppName(app); err != nil {
		return err
	}
	flags, booleans, err := splitFlags(args[1:])
	if err != nil {
		return err
	}
	if len(booleans) != 0 {
		return fmt.Errorf("--replace is not valid for enable")
	}
	if err := rejectUnknownFlags(flags, "mount-path", "role-name", "approle-mount"); err != nil {
		return err
	}
	mountPath, err := requireFlag(flags, "mount-path")
	if err != nil {
		return err
	}
	roleName, err := requireFlag(flags, "role-name")
	if err != nil {
		return err
	}
	approleMount := flags["approle-mount"]
	if approleMount == "" {
		approleMount = DefaultAppRoleMount
	}
	if err := validateMountPath(mountPath); err != nil {
		return err
	}
	if err := validateAppRoleMount(approleMount); err != nil {
		return err
	}
	if strings.ContainsAny(roleName, "\r\n/") {
		return fmt.Errorf("role name must not contain slashes or newlines")
	}
	if _, err := p.State.LoadApp(app); err == nil {
		return fmt.Errorf("Vault Agent integration is already enabled for %q", app)
	} else if !isNotExist(err) {
		return err
	}

	env := commandEnvironment()
	plugn := executableFromEnv("PLUGN_BIN", "plugn")
	if err := p.Runner.Run(CommandSpec{Name: plugn, Args: []string{"trigger", "app-exists", app}, Env: env, Stdout: stdout, Stderr: stderr}); err != nil {
		return fmt.Errorf("Dokku app %q does not exist: %w", app, err)
	}
	selected, err := p.Runner.Output(CommandSpec{Name: plugn, Args: []string{"trigger", "scheduler-get-property", app, "selected"}, Env: env, Stderr: stderr})
	if err != nil {
		return fmt.Errorf("read scheduler for %q: %w", app, err)
	}
	scheduler := strings.TrimSpace(string(selected))
	if scheduler == "" {
		scheduler = "docker-local"
	}
	if scheduler != "docker-local" {
		return fmt.Errorf("app %q uses unsupported scheduler %q; only docker-local is supported", app, scheduler)
	}

	entry := storageEntryName(app)
	rendered := p.State.RenderedDir(entry)
	if err := p.State.Setup(); err != nil {
		return err
	}
	if err := os.MkdirAll(rendered, 0755); err != nil {
		return err
	}
	if err := os.Chmod(rendered, 0755); err != nil {
		return err
	}
	dokku := executableFromEnv("DOKKU_BIN", "dokku")
	create := CommandSpec{Name: dokku, Args: []string{"storage:create", entry, rendered, "--scheduler", "docker-local", "--reclaim-policy", "Retain"}, Env: env, Stdout: stdout, Stderr: stderr}
	if err := p.Runner.Run(create); err != nil {
		_ = os.RemoveAll(rendered)
		return fmt.Errorf("create Dokku storage: %w", err)
	}
	mounted := false
	defer func() {
		if !mounted {
			_ = p.Runner.Run(CommandSpec{Name: dokku, Args: []string{"storage:destroy", entry, "--force"}, Env: env, Stdout: io.Discard, Stderr: io.Discard})
			_ = os.RemoveAll(rendered)
		}
	}()
	mount := CommandSpec{Name: dokku, Args: []string{"storage:mount", app, entry, "--container-dir", mountPath, "--phase", "deploy,run", "--volume-readonly"}, Env: env, Stdout: stdout, Stderr: stderr}
	if err := p.Runner.Run(mount); err != nil {
		return fmt.Errorf("mount Dokku storage: %w", err)
	}
	config := AppConfig{
		AppName: app, Enabled: true, MountPath: mountPath, RoleName: roleName,
		AppRoleMount: approleMount, StorageEntry: entry, TemplateMode: DefaultTemplateMode,
	}
	if err := p.State.SaveApp(config); err != nil {
		_ = p.Runner.Run(CommandSpec{Name: dokku, Args: []string{"storage:unmount", app, entry}, Env: env, Stdout: io.Discard, Stderr: io.Discard})
		return err
	}
	mounted = true
	return nil
}

func (p *Plugin) commandDisable(args []string, stdout, stderr io.Writer) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: vault-agent:disable APP")
	}
	return p.purgeApp(args[0], stdout, stderr)
}

func (p *Plugin) purgeApp(app string, stdout, stderr io.Writer) error {
	if err := validateAppName(app); err != nil {
		return err
	}
	config, err := p.State.LoadApp(app)
	if err != nil {
		if isNotExist(err) {
			return nil
		}
		return err
	}
	env := commandEnvironment()
	dokku := executableFromEnv("DOKKU_BIN", "dokku")
	var firstErr error
	if err := p.Runner.Run(CommandSpec{Name: dokku, Args: []string{"storage:unmount", app, config.StorageEntry}, Env: env, Stdout: stdout, Stderr: stderr}); err != nil {
		firstErr = fmt.Errorf("unmount Dokku storage: %w", err)
	}
	if err := p.Runner.Run(CommandSpec{Name: dokku, Args: []string{"storage:destroy", config.StorageEntry, "--force"}, Env: env, Stdout: stdout, Stderr: stderr}); err != nil && firstErr == nil {
		firstErr = fmt.Errorf("destroy Dokku storage: %w", err)
	}
	_ = secureRemove(p.State.PendingTokenPath(app))
	if err := os.RemoveAll(p.State.RenderedDir(config.StorageEntry)); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := p.State.RemoveApp(app); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}
