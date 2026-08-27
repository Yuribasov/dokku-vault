package plugin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGlobalMutationWaitsForRenderConfigurationLock(t *testing.T) {
	plugin := newTestPlugin(t.TempDir(), nil)
	if err := plugin.State.Setup(); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(plugin.State.CAPath(), []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	lock, err := plugin.lockGlobal()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- plugin.commandCAClear(nil) }()
	select {
	case err := <-done:
		t.Fatalf("global mutation completed while lock was held: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	unlockFile(lock)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(plugin.State.CAPath()); !isNotExist(err) {
		t.Fatalf("CA was not cleared after lock release: %v", err)
	}
}

func TestLockFileContextTimesOut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")
	held, err := lockFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer unlockFile(held)

	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()
	started := time.Now()
	contended, err := lockFileContext(ctx, path)
	if contended != nil {
		unlockFile(contended)
		t.Fatal("contended lock unexpectedly succeeded")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lock error = %v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("lock timeout took %s, want less than one second", elapsed)
	}
}
