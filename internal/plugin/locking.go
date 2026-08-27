package plugin

import (
	"context"
	"fmt"
	"os"
	"sort"
	"syscall"
	"time"
)

const (
	maximumLockWaitTime = 5 * time.Minute
	lockRetryInterval   = 50 * time.Millisecond
)

func (p *Plugin) lockApp(app string) (*os.File, error) {
	if err := validateAppName(app); err != nil {
		return nil, err
	}
	if err := p.State.Setup(); err != nil {
		return nil, err
	}
	return lockFile(p.State.LockPath(app))
}

func (p *Plugin) lockGlobal() (*os.File, error) {
	if err := p.State.Setup(); err != nil {
		return nil, err
	}
	return lockFile(p.State.GlobalLockPath())
}

func lockFile(path string) (*os.File, error) {
	ctx, cancel := context.WithTimeout(context.Background(), maximumLockWaitTime)
	defer cancel()
	return lockFileContext(ctx, path)
}

func lockFileContext(ctx context.Context, path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open app lock: %w", err)
	}
	if err := chownToSystemUser(file.Name()); err != nil {
		file.Close()
		return nil, err
	}
	retry := time.NewTicker(lockRetryInterval)
	defer retry.Stop()
	for {
		select {
		case <-ctx.Done():
			file.Close()
			return nil, fmt.Errorf("wait for lock %q: %w", path, ctx.Err())
		default:
		}

		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return file, nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			file.Close()
			return nil, fmt.Errorf("lock app: %w", err)
		}

		select {
		case <-ctx.Done():
			file.Close()
			return nil, fmt.Errorf("wait for lock %q: %w", path, ctx.Err())
		case <-retry.C:
		}
	}
}

func unlockFile(file *os.File) {
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	_ = file.Close()
}

func (p *Plugin) lockApps(apps ...string) ([]*os.File, error) {
	names := append([]string(nil), apps...)
	sort.Strings(names)
	locks := make([]*os.File, 0, len(names))
	for index, app := range names {
		if index > 0 && app == names[index-1] {
			continue
		}
		lock, err := p.lockApp(app)
		if err != nil {
			unlockFiles(locks)
			return nil, err
		}
		locks = append(locks, lock)
	}
	return locks, nil
}

func unlockFiles(files []*os.File) {
	for index := len(files) - 1; index >= 0; index-- {
		unlockFile(files[index])
	}
}
