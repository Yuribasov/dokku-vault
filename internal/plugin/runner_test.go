package plugin

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestOSCommandRunnerKillsAndReapsTimedOutProcess(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	err := (OSCommandRunner{}).Run(CommandSpec{
		Context: ctx,
		Name:    "sh",
		Args:    []string{"-c", "printf '%s' $$ > \"$1\"; exec sleep 30", "sh", pidFile},
	})
	if err == nil {
		t.Fatal("timed command succeeded")
	}
	if ctx.Err() != context.DeadlineExceeded {
		t.Fatalf("context error = %v", ctx.Err())
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
		t.Fatalf("timed-out child %d was not reaped: %v", pid, err)
	}
}
