package plugin

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type Plugin struct {
	State  *State
	Runner CommandRunner
}

func New() (*Plugin, error) {
	root := os.Getenv("DOKKU_VAULT_AGENT_DATA_ROOT")
	if root == "" {
		root = DefaultDataRoot
	}
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("DOKKU_VAULT_AGENT_DATA_ROOT must be absolute")
	}
	return &Plugin{State: NewState(filepath.Clean(root)), Runner: OSCommandRunner{}}, nil
}

func IsTrigger(name string) bool {
	switch name {
	case "pre-release-builder", "pre-delete", "post-app-clone-setup", "post-app-rename-setup":
		return true
	default:
		return false
	}
}

func (p *Plugin) Run(mode, action string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	switch mode {
	case "internal":
		if action != "install" {
			return fmt.Errorf("unknown internal action %q", action)
		}
		return p.State.Setup()
	case "command":
		return p.runCommand(action, args, stdin, stdout, stderr)
	case "trigger":
		return p.runTrigger(action, args, stdout, stderr)
	default:
		return fmt.Errorf("unknown invocation mode %q", mode)
	}
}

func (p *Plugin) runCommand(action string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	switch action {
	case "report":
		return p.commandReport(args, stdout)
	default:
		return fmt.Errorf("command %q is not implemented yet", action)
	}
}

func (p *Plugin) runTrigger(action string, args []string, stdout, stderr io.Writer) error {
	switch action {
	case "pre-release-builder", "pre-delete", "post-app-clone-setup", "post-app-rename-setup":
		return nil
	default:
		return fmt.Errorf("unknown trigger %q", action)
	}
}

func (p *Plugin) commandReport(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		config, err := p.State.LoadGlobal()
		if err != nil {
			if isNotExist(err) {
				fmt.Fprintln(stdout, "Vault Agent plugin is not configured")
				return nil
			}
			return err
		}
		fmt.Fprintf(stdout, "Vault address: %s\nAgent image: %s\n", config.VaultAddress, config.Image)
		return nil
	}
	config, err := p.State.LoadApp(args[0])
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "App: %s\nEnabled: %t\nMount path: %s\nTemplate mode: %s\n", config.AppName, config.Enabled, config.MountPath, config.TemplateMode)
	return nil
}
