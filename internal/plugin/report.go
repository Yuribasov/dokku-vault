package plugin

import (
	"fmt"
	"io"
	"os"
	"time"
)

func (p *Plugin) commandReport(args []string, stdout io.Writer) error {
	if len(args) > 1 {
		return fmt.Errorf("usage: vault-agent:report [APP]")
	}
	if len(args) == 0 {
		lock, err := p.lockGlobal()
		if err != nil {
			return err
		}
		defer unlockFile(lock)
		config, err := p.State.LoadGlobal()
		if err != nil {
			if isNotExist(err) {
				fmt.Fprintln(stdout, "Configured: false")
				return nil
			}
			return err
		}
		fmt.Fprintln(stdout, "Configured: true")
		fmt.Fprintf(stdout, "Vault address: %s\nAgent image: %s\nCA configured: %t\n", config.VaultAddress, config.Image, exists(p.State.CAPath()))
		return nil
	}
	app := args[0]
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
		if isNotExist(err) {
			fmt.Fprintf(stdout, "App: %s\nEnabled: false\n", app)
			return nil
		}
		return err
	}
	roleReady := exists(p.State.RoleIDPath(app))
	templateCount := len(config.Templates)
	templateReady := templateCount > 0
	if config.TemplateMode == "custom" {
		templateReady = exists(p.State.CustomHCLPath(app))
	}
	fmt.Fprintf(stdout, "App: %s\nEnabled: %t\nMount path: %s\nStorage entry: %s\nRoleID configured: %t\nTemplate mode: %s\nTemplates ready: %t\nManaged template count: %d\n",
		config.AppName, config.Enabled, config.MountPath, config.StorageEntry, roleReady, config.TemplateMode, templateReady, templateCount)
	var pending StagedCredential
	if err := readJSON(p.State.PendingMetadataPath(app), &pending); err == nil {
		status := "ready"
		if time.Now().After(pending.ExpiresAt) {
			status = "expired"
		}
		if _, err := os.Stat(p.State.PendingTokenPath(app)); err != nil {
			status = "incomplete"
		}
		fmt.Fprintf(stdout, "Pending credential: %s\nExpected revision: %s\nExpires at: %s\n", status, pending.Revision, pending.ExpiresAt.Format(time.RFC3339))
	} else if isNotExist(err) {
		fmt.Fprintln(stdout, "Pending credential: none")
	} else {
		return err
	}
	return nil
}
