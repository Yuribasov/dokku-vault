package plugin

import (
	"io"
	"strings"
	"testing"
)

func TestHelpOverviewListsEveryCommand(t *testing.T) {
	plugin := newTestPlugin(t.TempDir(), nil)
	var stdout strings.Builder
	if err := plugin.commandHelp(nil, &stdout); err != nil {
		t.Fatal(err)
	}
	output := stdout.String()
	for _, entry := range helpEntries {
		if !strings.Contains(output, "vault-agent:"+entry.Name) || !strings.Contains(output, entry.Summary) {
			t.Fatalf("overview lacks command %q or its summary:\n%s", entry.Name, output)
		}
	}
	hint := "dokku vault-agent:help COMMAND"
	if !strings.Contains(output, hint) {
		t.Fatalf("overview lacks detailed-help hint %q:\n%s", hint, output)
	}
}

func TestCommandHelpContainsUsageAndDetails(t *testing.T) {
	plugin := newTestPlugin(t.TempDir(), nil)
	for _, entry := range helpEntries {
		entry := entry
		t.Run(entry.Name, func(t *testing.T) {
			var stdout strings.Builder
			if err := plugin.commandHelp([]string{entry.Name}, &stdout); err != nil {
				t.Fatal(err)
			}
			output := stdout.String()
			usage := "Usage: dokku vault-agent:" + entry.Usage
			for _, want := range []string{usage, entry.Summary, entry.Details} {
				if !strings.Contains(output, want) {
					t.Fatalf("help for %q lacks %q:\n%s", entry.Name, want, output)
				}
			}
		})
	}
}

func TestCommandHelpAcceptsPrefixedNameAndRejectsInvalidRequests(t *testing.T) {
	plugin := newTestPlugin(t.TempDir(), nil)
	var stdout strings.Builder
	if err := plugin.commandHelp([]string{"vault-agent:configure"}, &stdout); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Usage: dokku vault-agent:configure") {
		t.Fatalf("prefixed command returned unexpected help:\n%s", stdout.String())
	}
	if err := plugin.commandHelp([]string{"missing"}, io.Discard); err == nil || !strings.Contains(err.Error(), "unknown vault-agent command") {
		t.Fatalf("unknown command returned unexpected error: %v", err)
	}
	if err := plugin.commandHelp([]string{"configure", "extra"}, io.Discard); err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("extra help argument returned unexpected error: %v", err)
	}
}

func TestDefaultAndHelpCommandRoutes(t *testing.T) {
	plugin := newTestPlugin(t.TempDir(), nil)
	cases := []struct {
		action string
		args   []string
	}{
		{action: "default", args: []string{"vault-agent"}},
		{action: "help"},
	}
	for _, test := range cases {
		var stdout strings.Builder
		if err := plugin.runCommand(test.action, test.args, strings.NewReader(""), &stdout, io.Discard); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(stdout.String(), "vault-agent:configure") {
			t.Fatalf("%s route returned unexpected help:\n%s", test.action, stdout.String())
		}
	}
}
