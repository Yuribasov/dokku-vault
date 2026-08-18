package plugin

import (
	"os"
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
