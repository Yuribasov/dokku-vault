package plugin

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
	case "pre-release-builder", "pre-delete", "post-app-clone-setup", "post-app-rename-setup", "post-deploy":
		return true
	default:
		return false
	}
}

func (p *Plugin) Run(mode, action string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	switch mode {
	case "internal":
		switch action {
		case "install":
			return p.State.RepairOwnership()
		case "uninstall-check":
			return p.checkUninstallSafe()
		default:
			return fmt.Errorf("unknown internal action %q", action)
		}
	case "command":
		return p.runCommand(action, args, stdin, stdout, stderr)
	case "trigger":
		return p.runTrigger(action, args, stdout, stderr)
	default:
		return fmt.Errorf("unknown invocation mode %q", mode)
	}
}

func (p *Plugin) checkUninstallSafe() error {
	if err := p.State.Setup(); err != nil {
		return err
	}
	entries, err := os.ReadDir(p.State.appsRoot())
	if err != nil {
		return err
	}
	apps := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			return fmt.Errorf("unexpected state entry %s prevents safe uninstall", entry.Name())
		}
		if err := validateAppName(entry.Name()); err != nil {
			return fmt.Errorf("unrecognized app state %s prevents safe uninstall", entry.Name())
		}
		if _, err := p.State.LoadApp(entry.Name()); err != nil {
			return fmt.Errorf("unreadable state for %s prevents safe uninstall: %w", entry.Name(), err)
		}
		apps = append(apps, entry.Name())
	}
	if len(apps) > 0 {
		sort.Strings(apps)
		return fmt.Errorf("refusing uninstall while app integrations exist: %s; run vault-agent:disable for each app first", strings.Join(apps, ", "))
	}
	rendered, err := os.ReadDir(p.State.renderedRoot())
	if err != nil {
		return err
	}
	if len(rendered) > 0 {
		return fmt.Errorf("refusing uninstall while orphaned rendered secret data exists under %s", p.State.renderedRoot())
	}
	return nil
}

func (p *Plugin) runCommand(action string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	switch action {
	case "configure":
		return p.commandConfigure(args, stdout, stderr)
	case "ca:set":
		return p.commandCASet(args, stdin)
	case "ca:clear":
		return p.commandCAClear(args)
	case "enable":
		return p.commandEnable(args, stdout, stderr)
	case "disable":
		return p.commandDisable(args, stdout, stderr)
	case "role-id:set":
		return p.commandRoleIDSet(args, stdin)
	case "template:add":
		return p.commandTemplateAdd(args)
	case "template:list":
		return p.commandTemplateList(args, stdout)
	case "template:remove":
		return p.commandTemplateRemove(args)
	case "template:set-custom":
		return p.commandTemplateSetCustom(args, stdin)
	case "template:clear-custom":
		return p.commandTemplateClearCustom(args)
	case "stage":
		return p.commandStage(args, stdin)
	case "render":
		return p.commandRender(args, stdout, stderr)
	case "report":
		return p.commandReport(args, stdout)
	default:
		return fmt.Errorf("unknown command %q", action)
	}
}

func (p *Plugin) runTrigger(action string, args []string, stdout, stderr io.Writer) error {
	switch action {
	case "pre-release-builder":
		return p.triggerPreReleaseBuilder(args, stdout, stderr)
	case "pre-delete":
		return p.triggerPreDelete(args, stdout, stderr)
	case "post-app-clone-setup":
		return p.triggerPostClone(args, stdout, stderr)
	case "post-app-rename-setup":
		return p.triggerPostRename(args)
	case "post-deploy":
		return p.triggerPostDeploy(args, stderr)
	default:
		return fmt.Errorf("unknown trigger %q", action)
	}
}
