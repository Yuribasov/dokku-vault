package plugin

import (
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

func (p *Plugin) commandTemplateAdd(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: vault-agent:template:add APP NAME --secret-path PATH --field FIELD --destination FILE [--decode base64|none] [--perms MODE]")
	}
	app, name := args[0], args[1]
	if err := validateAppName(app); err != nil {
		return err
	}
	if err := validateTemplateName(name); err != nil {
		return err
	}
	flags, booleans, err := splitFlags(args[2:])
	if err != nil {
		return err
	}
	if len(booleans) != 0 {
		return fmt.Errorf("--replace is not valid for template:add")
	}
	if err := rejectUnknownFlags(flags, "secret-path", "field", "destination", "decode", "perms"); err != nil {
		return err
	}
	secretPath, err := requireFlag(flags, "secret-path")
	if err != nil {
		return err
	}
	field, err := requireFlag(flags, "field")
	if err != nil {
		return err
	}
	destination, err := requireFlag(flags, "destination")
	if err != nil {
		return err
	}
	if strings.ContainsAny(secretPath, "\r\n") || strings.ContainsAny(field, "\r\n") {
		return fmt.Errorf("secret path and field must not contain newlines")
	}
	if err := validateDestination(destination); err != nil {
		return err
	}
	decode := flags["decode"]
	if decode == "" {
		decode = "base64"
	}
	if decode != "base64" && decode != "none" {
		return fmt.Errorf("--decode must be base64 or none")
	}
	perms := flags["perms"]
	if perms == "" {
		perms = DefaultOutputMode
	}
	if err := validateMode(perms); err != nil {
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
	if config.TemplateMode == "custom" {
		return fmt.Errorf("custom template mode is active; clear it before adding managed templates")
	}
	replacement := ManagedTemplate{
		Name: name, SecretPath: secretPath, Field: field, Destination: destination, Decode: decode, Perms: perms,
	}
	templates := make([]ManagedTemplate, 0, len(config.Templates)+1)
	replaced := false
	for _, template := range config.Templates {
		if template.Name == name {
			if !replaced {
				templates = append(templates, replacement)
				replaced = true
			}
			continue
		}
		if template.Destination == destination {
			return fmt.Errorf("destination %q is already used by template %q", destination, template.Name)
		}
		templates = append(templates, template)
	}
	if !replaced {
		templates = append(templates, replacement)
	}
	config.TemplateMode = DefaultTemplateMode
	config.Templates = templates
	sort.Slice(config.Templates, func(i, j int) bool { return config.Templates[i].Name < config.Templates[j].Name })
	return p.State.SaveApp(config)
}

func (p *Plugin) commandTemplateList(args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: vault-agent:template:list APP")
	}
	lock, err := p.lockApp(args[0])
	if err != nil {
		return err
	}
	defer unlockFile(lock)
	config, err := p.State.LoadApp(args[0])
	if err != nil {
		return err
	}
	if config.TemplateMode == "custom" {
		fmt.Fprintln(stdout, "custom template HCL")
		return nil
	}
	if len(config.Templates) == 0 {
		fmt.Fprintln(stdout, "No managed templates")
		return nil
	}
	for _, template := range config.Templates {
		fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\t%s\t%s\n", template.Name, template.SecretPath, template.Field, template.Destination, template.Decode, template.Perms)
	}
	return nil
}

func (p *Plugin) commandTemplateRemove(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: vault-agent:template:remove APP NAME")
	}
	lock, err := p.lockApp(args[0])
	if err != nil {
		return err
	}
	defer unlockFile(lock)
	config, err := p.State.LoadApp(args[0])
	if err != nil {
		return err
	}
	if config.TemplateMode == "custom" {
		return fmt.Errorf("managed templates cannot be removed while custom mode is active")
	}
	templates := make([]ManagedTemplate, 0, len(config.Templates))
	found := false
	for _, template := range config.Templates {
		if template.Name == args[1] {
			found = true
			continue
		}
		templates = append(templates, template)
	}
	if !found {
		return fmt.Errorf("template %q does not exist", args[1])
	}
	config.Templates = templates
	return p.State.SaveApp(config)
}

func (p *Plugin) commandTemplateSetCustom(args []string, stdin io.Reader) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: vault-agent:template:set-custom APP [--replace]")
	}
	app := args[0]
	flags, booleans, err := splitFlags(args[1:])
	if err != nil {
		return err
	}
	if len(flags) != 0 {
		for name := range flags {
			return fmt.Errorf("unknown flag --%s", name)
		}
	}
	data, err := readBounded(stdin, 1024*1024)
	if err != nil {
		return err
	}
	if _, err := validateCustomHCL(data); err != nil {
		return err
	}
	lock, err := p.lockApp(app)
	if err != nil {
		return err
	}
	defer unlockFile(lock)
	config, err := p.State.LoadApp(app)
	if err != nil {
		return err
	}
	if len(config.Templates) > 0 && !booleans["replace"] {
		return fmt.Errorf("managed templates exist; pass --replace to remove them")
	}
	if err := writeFileAtomic(p.State.CustomHCLPath(app), data, 0600); err != nil {
		return err
	}
	config.TemplateMode = "custom"
	config.Templates = nil
	config.CustomHCLFile = "templates.hcl"
	return p.State.SaveApp(config)
}

func (p *Plugin) commandTemplateClearCustom(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: vault-agent:template:clear-custom APP")
	}
	lock, err := p.lockApp(args[0])
	if err != nil {
		return err
	}
	defer unlockFile(lock)
	config, err := p.State.LoadApp(args[0])
	if err != nil {
		return err
	}
	if err := os.Remove(p.State.CustomHCLPath(args[0])); err != nil && !isNotExist(err) {
		return err
	}
	config.TemplateMode = DefaultTemplateMode
	config.CustomHCLFile = ""
	config.Templates = nil
	return p.State.SaveApp(config)
}

type customTemplateOutput struct {
	Destination string
	Perms       string
}

func validateCustomHCL(data []byte) ([]customTemplateOutput, error) {
	file, diagnostics := hclsyntax.ParseConfig(data, "templates.hcl", hcl.Pos{Line: 1, Column: 1})
	if diagnostics.HasErrors() {
		return nil, fmt.Errorf("parse custom template HCL: %s", diagnostics.Error())
	}
	body, ok := file.Body.(*hclsyntax.Body)
	if !ok {
		return nil, fmt.Errorf("unsupported HCL body")
	}
	if len(body.Attributes) != 0 {
		return nil, fmt.Errorf("custom HCL may contain only template blocks")
	}
	if len(body.Blocks) == 0 {
		return nil, fmt.Errorf("custom HCL must contain at least one template block")
	}
	seen := make(map[string]bool)
	outputs := make([]customTemplateOutput, 0, len(body.Blocks))
	for _, block := range body.Blocks {
		if block.Type != "template" || len(block.Labels) != 0 {
			return nil, fmt.Errorf("custom HCL may contain only unlabeled template blocks")
		}
		if len(block.Body.Blocks) != 0 {
			return nil, fmt.Errorf("nested blocks are not allowed in template blocks")
		}
		allowed := map[string]bool{
			"contents": true, "destination": true, "perms": true, "backup": true,
			"create_dest_dirs": true, "error_on_missing_key": true,
			"left_delimiter": true, "right_delimiter": true,
		}
		for name := range block.Body.Attributes {
			if !allowed[name] {
				return nil, fmt.Errorf("attribute %q is not allowed in custom template HCL", name)
			}
		}
		for _, required := range []string{"contents", "destination", "perms", "backup"} {
			if block.Body.Attributes[required] == nil {
				return nil, fmt.Errorf("template block requires %q", required)
			}
		}
		if _, err := constantString(block.Body.Attributes["contents"], "contents"); err != nil {
			return nil, err
		}
		destination, err := constantString(block.Body.Attributes["destination"], "destination")
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(destination, "/vault/rendered/") || path.Clean(destination) != destination {
			return nil, fmt.Errorf("custom template destination %q must be a normalized path below /vault/rendered", destination)
		}
		relative := strings.TrimPrefix(destination, "/vault/rendered/")
		if err := validateDestination(relative); err != nil {
			return nil, fmt.Errorf("custom template destination %q: %w", destination, err)
		}
		perms, err := constantString(block.Body.Attributes["perms"], "perms")
		if err != nil {
			return nil, err
		}
		if err := validateMode(perms); err != nil {
			return nil, err
		}
		backup, err := constantBool(block.Body.Attributes["backup"], "backup")
		if err != nil {
			return nil, err
		}
		if backup {
			return nil, fmt.Errorf("custom templates must set backup = false")
		}
		for _, name := range []string{"create_dest_dirs", "error_on_missing_key"} {
			if attribute := block.Body.Attributes[name]; attribute != nil {
				if _, err := constantBool(attribute, name); err != nil {
					return nil, err
				}
			}
		}
		for _, name := range []string{"left_delimiter", "right_delimiter"} {
			if attribute := block.Body.Attributes[name]; attribute != nil {
				if _, err := constantString(attribute, name); err != nil {
					return nil, err
				}
			}
		}
		if seen[relative] {
			return nil, fmt.Errorf("duplicate custom template destination %q", destination)
		}
		seen[relative] = true
		outputs = append(outputs, customTemplateOutput{Destination: relative, Perms: perms})
	}
	return outputs, nil
}

func constantString(attribute *hclsyntax.Attribute, name string) (string, error) {
	value, diagnostics := attribute.Expr.Value(nil)
	if diagnostics.HasErrors() || !value.IsKnown() || value.IsNull() || value.Type() != cty.String {
		return "", fmt.Errorf("attribute %q must be a constant string", name)
	}
	return value.AsString(), nil
}

func constantBool(attribute *hclsyntax.Attribute, name string) (bool, error) {
	value, diagnostics := attribute.Expr.Value(nil)
	if diagnostics.HasErrors() || !value.IsKnown() || value.IsNull() || value.Type() != cty.Bool {
		return false, fmt.Errorf("attribute %q must be a constant boolean", name)
	}
	return value.True(), nil
}
