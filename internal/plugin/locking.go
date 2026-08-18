package plugin

import (
	"fmt"
	"os"
	"sort"
	"syscall"
)

func (p *Plugin) lockApp(app string) (*os.File, error) {
	if err := validateAppName(app); err != nil {
		return nil, err
	}
	if err := p.State.Setup(); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(p.State.LockPath(app), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open app lock: %w", err)
	}
	if err := chownToSystemUser(file.Name()); err != nil {
		file.Close()
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		file.Close()
		return nil, fmt.Errorf("lock app: %w", err)
	}
	return file, nil
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
