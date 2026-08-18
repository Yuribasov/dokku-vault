package plugin

import (
	"context"
	"io"
	"os"
	"os/exec"
)

type CommandSpec struct {
	Context context.Context
	Name    string
	Args    []string
	Env     []string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
}

type CommandRunner interface {
	Run(CommandSpec) error
	Output(CommandSpec) ([]byte, error)
}

type OSCommandRunner struct{}

func (OSCommandRunner) Run(spec CommandSpec) error {
	ctx := spec.Context
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, spec.Name, spec.Args...)
	cmd.Env = spec.Env
	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = spec.Stdin, spec.Stdout, spec.Stderr
	return cmd.Run()
}

func (OSCommandRunner) Output(spec CommandSpec) ([]byte, error) {
	ctx := spec.Context
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, spec.Name, spec.Args...)
	cmd.Env = spec.Env
	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}
	cmd.Stdin = spec.Stdin
	cmd.Stderr = spec.Stderr
	return cmd.Output()
}
