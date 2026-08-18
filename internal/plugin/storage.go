package plugin

import (
	"errors"
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
	if err := validateRoleName(roleName); err != nil {
		return err
	}
	lock, err := p.lockApp(app)
	if err != nil {
		return err
	}
	defer unlockFile(lock)
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
	selected, err := p.Runner.Output(CommandSpec{Name: plugn, Args: []string{"trigger", "scheduler-detect", app}, Env: env, Stderr: stderr})
	if err != nil {
		return fmt.Errorf("detect scheduler for %q: %w", app, err)
	}
	scheduler := strings.TrimSpace(string(selected))
	if scheduler == "" {
		return fmt.Errorf("detect scheduler for %q: Dokku returned an empty scheduler", app)
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
	if err := chownToSystemUser(rendered); err != nil {
		return err
	}
	config := AppConfig{
		AppName: app, Enabled: true, MountPath: mountPath, RoleName: roleName,
		AppRoleMount: approleMount, StorageEntry: entry, TemplateMode: DefaultTemplateMode,
	}
	dokku := executableFromEnv("DOKKU_BIN", "dokku")
	create := CommandSpec{Name: dokku, Args: []string{"storage:create", entry, rendered, "--scheduler", "docker-local", "--reclaim-policy", "Retain"}, Env: env, Stdout: stdout, Stderr: stderr}
	if err := p.Runner.Run(create); err != nil {
		removeErr := os.RemoveAll(rendered)
		return errors.Join(fmt.Errorf("create Dokku storage: %w", err), removeErr)
	}
	mount := CommandSpec{Name: dokku, Args: []string{"storage:mount", app, entry, "--container-dir", mountPath, "--phase", "deploy,run", "--volume-readonly"}, Env: env, Stdout: stdout, Stderr: stderr}
	if err := p.Runner.Run(mount); err != nil {
		return p.rollbackEnable(config, false, fmt.Errorf("mount Dokku storage: %w", err), env)
	}
	if err := p.State.SaveApp(config); err != nil {
		return p.rollbackEnable(config, true, err, env)
	}
	return nil
}

func (p *Plugin) rollbackEnable(config AppConfig, mounted bool, cause error, env []string) error {
	dokku := executableFromEnv("DOKKU_BIN", "dokku")
	var unmountErr error
	if mounted {
		if err := p.Runner.Run(CommandSpec{Name: dokku, Args: []string{"storage:unmount", config.AppName, config.StorageEntry}, Env: env, Stdout: io.Discard, Stderr: io.Discard}); err != nil {
			unmountErr = fmt.Errorf("rollback storage mount: %w", err)
		}
	}
	destroyErr := p.Runner.Run(CommandSpec{Name: dokku, Args: []string{"storage:destroy", config.StorageEntry, "--force"}, Env: env, Stdout: io.Discard, Stderr: io.Discard})
	if destroyErr != nil {
		config.Enabled = false
		config.CleanupPhase = cleanupPhasePending
		stateErr := p.State.SaveApp(config)
		return errors.Join(cause, unmountErr, fmt.Errorf("rollback storage destroy: %w", destroyErr), stateErr)
	}
	removeErr := os.RemoveAll(p.State.RenderedDir(config.StorageEntry))
	if removeErr != nil {
		config.Enabled = false
		config.CleanupPhase = cleanupPhaseStorageDestroyed
		stateErr := p.State.SaveApp(config)
		return errors.Join(cause, unmountErr, fmt.Errorf("rollback rendered directory: %w", removeErr), stateErr)
	}
	return errors.Join(cause, unmountErr)
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
	lock, err := p.lockApp(app)
	if err != nil {
		return err
	}
	defer unlockFile(lock)
	return p.purgeAppLocked(app, stdout, stderr)
}

func (p *Plugin) purgeAppLocked(app string, stdout, stderr io.Writer) error {
	config, err := p.State.LoadApp(app)
	if err != nil {
		if isNotExist(err) {
			return nil
		}
		return err
	}
	if err := validateStorageEntry(config.StorageEntry); err != nil {
		return err
	}
	if config.CleanupPhase != "" && config.CleanupPhase != cleanupPhasePending && config.CleanupPhase != cleanupPhaseStorageDestroyed {
		return fmt.Errorf("unknown cleanup phase %q", config.CleanupPhase)
	}
	env := commandEnvironment()
	dokku := executableFromEnv("DOKKU_BIN", "dokku")
	if config.CleanupPhase != cleanupPhaseStorageDestroyed {
		config.Enabled = false
		config.CleanupPhase = cleanupPhasePending
		if err := p.State.SaveApp(config); err != nil {
			return fmt.Errorf("record cleanup intent: %w", err)
		}
		if err := p.Runner.Run(CommandSpec{Name: dokku, Args: []string{"storage:unmount", app, config.StorageEntry}, Env: env, Stdout: stdout, Stderr: stderr}); err != nil {
			fmt.Fprintf(stderr, "vault-agent: storage unmount did not succeed; continuing cleanup: %v\n", err)
		}
		if err := p.Runner.Run(CommandSpec{Name: dokku, Args: []string{"storage:destroy", config.StorageEntry, "--force"}, Env: env, Stdout: stdout, Stderr: stderr}); err != nil {
			return fmt.Errorf("destroy Dokku storage; cleanup state was retained for retry: %w", err)
		}
		config.CleanupPhase = cleanupPhaseStorageDestroyed
		if err := p.State.SaveApp(config); err != nil {
			return fmt.Errorf("record destroyed storage: %w", err)
		}
	}
	if err := secureRemove(p.State.PendingTokenPath(app)); err != nil {
		return fmt.Errorf("remove pending token; cleanup state was retained for retry: %w", err)
	}
	if err := os.Remove(p.State.PendingMetadataPath(app)); err != nil && !isNotExist(err) {
		return fmt.Errorf("remove pending metadata; cleanup state was retained for retry: %w", err)
	}
	if err := os.RemoveAll(p.State.RenderedDir(config.StorageEntry)); err != nil {
		return fmt.Errorf("remove rendered secrets; cleanup state was retained for retry: %w", err)
	}
	if err := p.State.RemoveApp(app); err != nil {
		return fmt.Errorf("remove app state: %w", err)
	}
	return nil
}
