package plugin

import (
	"fmt"
	"io"
	"strings"
)

type helpEntry struct {
	Name    string
	Usage   string
	Summary string
	Details string
}

var helpEntries = []helpEntry{
	{Name: "configure", Usage: "configure --vault-address URL --image IMAGE", Summary: "Configure the Vault endpoint and pinned Agent image.", Details: "Both flags are required. URL must use HTTPS. IMAGE must be an official hashicorp/vault tag pinned by sha256 digest. The image is pulled immediately."},
	{Name: "ca:set", Usage: "ca:set < vault-ca.pem", Summary: "Set the CA certificates used to verify Vault.", Details: "Reads one or more PEM CERTIFICATE blocks from stdin. Input is limited to 1 MiB."},
	{Name: "ca:clear", Usage: "ca:clear", Summary: "Remove the configured Vault CA certificates.", Details: "Future renders use the container's standard trust store after the custom CA is removed."},
	{Name: "enable", Usage: "enable APP --mount-path PATH --role-name ROLE [--approle-mount PATH]", Summary: "Enable or update Vault rendering for a Dokku application.", Details: "APP must exist and use docker-local. Creates plugin-managed named storage mounted read-only in deploy and run containers. Repeating the command updates mount and AppRole parameters. Changing AppRole parameters clears the stored RoleID and any staged credential. --approle-mount defaults to auth/approle."},
	{Name: "disable", Usage: "disable APP", Summary: "Disable integration and purge the application's Vault state.", Details: "Unmounts and destroys plugin-managed storage, then removes rendered files, RoleID, staged credentials, templates, and app configuration. Safe to retry after partial cleanup."},
	{Name: "role-id:set", Usage: "role-id:set APP < role-id-file", Summary: "Set the persistent AppRole RoleID for an application.", Details: "Reads exactly one RoleID line from stdin. The RoleID is not passed to the application container."},
	{Name: "template:add", Usage: "template:add APP NAME --secret-path PATH --field FIELD --destination FILE [--decode base64|none] [--perms MODE]", Summary: "Add or update a managed Vault KV v2 file template.", Details: "Required flags select the Vault path, data field, and relative output path. Repeating a template name updates that mapping. --decode defaults to base64. --perms defaults to 0444 and accepts 0400, 0440, or 0444."},
	{Name: "template:list", Usage: "template:list APP", Summary: "List configured templates for an application.", Details: "Managed templates are printed one per line. Custom mode reports that custom template HCL is active."},
	{Name: "template:remove", Usage: "template:remove APP NAME", Summary: "Remove one managed template mapping.", Details: "The command is available only in managed-template mode."},
	{Name: "template:set-custom", Usage: "template:set-custom APP [--replace] < templates.hcl", Summary: "Install validated custom Vault Agent template HCL.", Details: "Reads HCL from stdin. Only safe template blocks targeting /vault/rendered are accepted. Use --replace to discard existing managed mappings."},
	{Name: "template:clear-custom", Usage: "template:clear-custom APP", Summary: "Remove custom HCL and return to empty managed mode.", Details: "Add managed mappings with vault-agent:template:add before the next render."},
	{Name: "stage", Usage: "stage APP (--revision FULL_SHA | --source-image IMAGE@sha256:DIGEST) --ttl-seconds N < wrapping-token-file", Summary: "Stage a wrapped AppRole SecretID for the next render.", Details: "Reads one response-wrapping token from stdin. Use --revision with a full lowercase 40- or 64-character Git SHA for Git deployments. Use --source-image with the exact immutable application image passed to git:from-image or git:load-image. N must be between 1 and 86400. The credential is single-attempt."},
	{Name: "render", Usage: "render APP", Summary: "Consume the staged credential and render immediately.", Details: "Runs the same one-shot Vault Agent path used by pre-release-builder. A new wrapped SecretID is required after every attempt."},
	{Name: "report", Usage: "report [APP]", Summary: "Show sanitized global or application readiness information.", Details: "Without APP, shows global Vault configuration. With APP, shows integration, generation, template, and staged-credential status without revealing credentials."},
	{Name: "help", Usage: "help [COMMAND]", Summary: "Show general or command-specific Vault Agent help.", Details: "COMMAND may be written as configure or vault-agent:configure."},
}

func (p *Plugin) commandHelp(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return writeHelpOverview(stdout)
	}
	if len(args) != 1 {
		return fmt.Errorf("usage: vault-agent:help [COMMAND]")
	}
	requested := args[0]
	if strings.HasPrefix(requested, "vault-agent:") {
		requested = strings.TrimPrefix(requested, "vault-agent:")
	}
	for _, entry := range helpEntries {
		if entry.Name == requested {
			fmt.Fprintf(stdout, "Usage: dokku vault-agent:%s\n\n", entry.Usage)
			fmt.Fprintln(stdout, entry.Summary)
			if entry.Details != "" {
				fmt.Fprintf(stdout, "\n%s\n", entry.Details)
			}
			return nil
		}
	}
	return fmt.Errorf("unknown vault-agent command %q; run 'dokku vault-agent:help' to list commands", args[0])
}

func writeHelpOverview(stdout io.Writer) error {
	fmt.Fprintln(stdout, "Usage: dokku vault-agent:<command> [arguments]")
	fmt.Fprintln(stdout, "\nRender application files with a one-shot Vault Agent during Dokku releases.")
	fmt.Fprintln(stdout, "\nCommands:")
	for _, entry := range helpEntries {
		fmt.Fprintf(stdout, "  %-36s %s\n", "vault-agent:"+entry.Name, entry.Summary)
	}
	fmt.Fprintln(stdout, "\nRun 'dokku vault-agent:help COMMAND' for detailed help.")
	return nil
}
