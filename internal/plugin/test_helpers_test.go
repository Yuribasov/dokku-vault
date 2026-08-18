package plugin

import (
	"io"
)

type fakeRunner struct {
	specs    []CommandSpec
	runFn    func(CommandSpec) error
	outputFn func(CommandSpec) ([]byte, error)
}

func (runner *fakeRunner) Run(spec CommandSpec) error {
	runner.specs = append(runner.specs, spec)
	if runner.runFn != nil {
		return runner.runFn(spec)
	}
	return nil
}

func (runner *fakeRunner) Output(spec CommandSpec) ([]byte, error) {
	runner.specs = append(runner.specs, spec)
	if runner.outputFn != nil {
		return runner.outputFn(spec)
	}
	return nil, nil
}

func newTestPlugin(root string, runner CommandRunner) *Plugin {
	if runner == nil {
		runner = &fakeRunner{}
	}
	return &Plugin{State: NewState(root), Runner: runner}
}

func discardWriters() (io.Writer, io.Writer) {
	return io.Discard, io.Discard
}
