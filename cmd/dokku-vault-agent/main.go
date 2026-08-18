package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/example/dokku-vault-agent/internal/plugin"
)

func main() {
	app, err := plugin.New()
	if err != nil {
		fmt.Fprintf(os.Stderr, "vault-agent: %v\n", err)
		os.Exit(1)
	}

	name := filepath.Base(os.Args[0])
	args := os.Args[1:]
	mode := "command"
	action := name

	if name == "dokku-vault-agent" {
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: dokku-vault-agent <command|trigger|internal> <action> [args...]")
			os.Exit(2)
		}
		mode, action, args = args[0], args[1], args[2:]
	} else if plugin.IsTrigger(name) {
		mode = "trigger"
	} else if len(args) > 0 && strings.HasPrefix(args[0], "vault-agent:") {
		args = args[1:]
	}

	if err := app.Run(mode, action, args, os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "vault-agent: %v\n", err)
		os.Exit(1)
	}
}
