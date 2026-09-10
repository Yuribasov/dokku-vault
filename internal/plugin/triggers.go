package plugin

import (
	"fmt"
	"io"
	"os"
)

func (p *Plugin) triggerPreReleaseBuilder(args []string, stdout, stderr io.Writer) error {
	if len(args) < 2 {
		return fmt.Errorf("pre-release-builder requires BUILDER_TYPE APP [IMAGE]")
	}
	app := args[1]
	if _, err := p.State.LoadApp(app); err != nil {
		if isNotExist(err) {
			return nil
		}
		return err
	}
	return p.renderAppForDeployment(app, stdout, stderr)
}

func (p *Plugin) triggerPreDelete(args []string, stdout, stderr io.Writer) error {
	if len(args) < 1 {
		return fmt.Errorf("pre-delete requires APP")
	}
	return p.purgeApp(args[0], stdout, stderr)
}

func (p *Plugin) triggerPostClone(args []string, stdout, stderr io.Writer) error {
	if len(args) < 2 {
		return fmt.Errorf("post-app-clone-setup requires SOURCE_APP DESTINATION_APP")
	}
	source, destination := args[0], args[1]
	locks, err := p.lockApps(source, destination)
	if err != nil {
		return err
	}
	defer unlockFiles(locks)
	config, err := p.State.LoadApp(source)
	if err != nil {
		if isNotExist(err) {
			return nil
		}
		return err
	}
	env := commandEnvironment()
	dokku := executableFromEnv("DOKKU_BIN", "dokku")
	cloneConfig := config
	cloneConfig.AppName = destination
	if err := p.Runner.Run(storageUnmountCommand(dokku, cloneConfig, config.MountPath, env, stdout, stderr)); err != nil {
		return fmt.Errorf("remove cloned Vault storage attachment: %w", err)
	}
	return nil
}

func (p *Plugin) triggerPostRename(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("post-app-rename-setup requires OLD_APP NEW_APP")
	}
	oldApp, newApp := args[0], args[1]
	locks, err := p.lockApps(oldApp, newApp)
	if err != nil {
		return err
	}
	defer unlockFiles(locks)
	config, err := p.State.LoadApp(oldApp)
	if err != nil {
		if isNotExist(err) {
			return nil
		}
		return err
	}
	if err := secureRemove(p.State.PendingTokenPath(oldApp)); err != nil {
		return fmt.Errorf("remove pending token before rename: %w", err)
	}
	if err := os.Remove(p.State.PendingMetadataPath(oldApp)); err != nil && !isNotExist(err) {
		return fmt.Errorf("remove pending metadata before rename: %w", err)
	}
	config.AppName = newApp
	if err := p.State.RenameApp(oldApp, newApp, config); err != nil {
		return err
	}
	return nil
}

func (p *Plugin) triggerPostDeploy(args []string, stderr io.Writer) error {
	if len(args) < 1 {
		return fmt.Errorf("post-deploy requires APP")
	}
	app := args[0]
	lock, err := p.lockApp(app)
	if err != nil {
		return err
	}
	defer unlockFile(lock)
	config, err := p.State.LoadApp(app)
	if err != nil {
		if isNotExist(err) {
			return nil
		}
		return err
	}
	if !config.Enabled {
		return nil
	}
	livePath := p.State.RenderedDir(config.StorageEntry)
	generation, err := ensureExistingGenerationStorage(livePath)
	if err != nil {
		return fmt.Errorf("resolve deployed secret generation: %w", err)
	}
	previous := config.ActiveGeneration
	config.ActiveGeneration = generation
	config.PendingGeneration = ""
	if err := p.State.SaveApp(config); err != nil {
		return fmt.Errorf("promote deployed secret generation: %w", err)
	}
	if err := cleanupGenerations(livePath, generation, previous); err != nil {
		fmt.Fprintf(stderr, "vault-agent: unable to remove obsolete secret generations; they will be retried after a later deploy: %v\n", err)
	}
	return nil
}
