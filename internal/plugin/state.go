package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
)

type fileOwner struct {
	uid int
	gid int
}

type State struct {
	Root string
}

func NewState(root string) *State {
	return &State{Root: root}
}

func (s *State) Setup() error {
	for _, path := range []string{s.Root, s.appsRoot(), s.renderedRoot(), s.tempRoot(), s.locksRoot()} {
		if err := os.MkdirAll(path, 0700); err != nil {
			return fmt.Errorf("create state directory %s: %w", path, err)
		}
		if err := os.Chmod(path, 0700); err != nil {
			return fmt.Errorf("secure state directory %s: %w", path, err)
		}
		if err := chownToSystemUser(path); err != nil {
			return err
		}
	}
	return nil
}

// RepairOwnership is used by install and update hooks to migrate state created
// by older plugin versions or root-invoked commands. Routine commands use
// Setup, avoiding an unlocked walk across live rendered secrets.
func (s *State) RepairOwnership() error {
	if err := s.Setup(); err != nil {
		return err
	}
	return chownTreeToSystemUser(s.Root)
}

func (s *State) appsRoot() string     { return filepath.Join(s.Root, "apps") }
func (s *State) renderedRoot() string { return filepath.Join(s.Root, "rendered") }
func (s *State) tempRoot() string     { return filepath.Join(s.Root, "tmp") }
func (s *State) locksRoot() string    { return filepath.Join(s.Root, "locks") }
func (s *State) appDir(app string) string {
	return filepath.Join(s.appsRoot(), app)
}
func (s *State) appConfigPath(app string) string {
	return filepath.Join(s.appDir(app), "config.json")
}
func (s *State) GlobalConfigPath() string { return filepath.Join(s.Root, "config.json") }
func (s *State) CAPath() string           { return filepath.Join(s.Root, "vault-ca.pem") }
func (s *State) RoleIDPath(app string) string {
	return filepath.Join(s.appDir(app), "role-id")
}
func (s *State) CustomHCLPath(app string) string {
	return filepath.Join(s.appDir(app), "templates.hcl")
}
func (s *State) PendingTokenPath(app string) string {
	return filepath.Join(s.appDir(app), "pending-token")
}
func (s *State) PendingMetadataPath(app string) string {
	return filepath.Join(s.appDir(app), "pending.json")
}
func (s *State) LockPath(app string) string {
	return filepath.Join(s.locksRoot(), app+".lock")
}
func (s *State) RenderedDir(entry string) string {
	return filepath.Join(s.renderedRoot(), entry)
}

func (s *State) EnsureApp(app string) error {
	if err := s.Setup(); err != nil {
		return err
	}
	if err := os.MkdirAll(s.appDir(app), 0700); err != nil {
		return err
	}
	if err := os.Chmod(s.appDir(app), 0700); err != nil {
		return err
	}
	return chownToSystemUser(s.appDir(app))
}

func (s *State) LoadGlobal() (GlobalConfig, error) {
	var config GlobalConfig
	err := readJSON(s.GlobalConfigPath(), &config)
	return config, err
}

func (s *State) SaveGlobal(config GlobalConfig) error {
	if err := s.Setup(); err != nil {
		return err
	}
	return writeJSONAtomic(s.GlobalConfigPath(), config, 0600)
}

func (s *State) LoadApp(app string) (AppConfig, error) {
	if err := validateAppName(app); err != nil {
		return AppConfig{}, err
	}
	var config AppConfig
	err := readJSON(s.appConfigPath(app), &config)
	return config, err
}

func (s *State) SaveApp(config AppConfig) error {
	if err := validateAppName(config.AppName); err != nil {
		return err
	}
	if err := s.EnsureApp(config.AppName); err != nil {
		return err
	}
	return writeJSONAtomic(s.appConfigPath(config.AppName), config, 0600)
}

func (s *State) RemoveApp(app string) error {
	if err := validateAppName(app); err != nil {
		return err
	}
	return os.RemoveAll(s.appDir(app))
}

func (s *State) RenameApp(oldApp, newApp string, config AppConfig) error {
	if err := validateAppName(oldApp); err != nil {
		return err
	}
	if err := validateAppName(newApp); err != nil {
		return err
	}
	if _, err := os.Stat(s.appDir(oldApp)); err != nil {
		return err
	}
	if err := s.Setup(); err != nil {
		return err
	}
	if exists(s.appDir(newApp)) {
		return fmt.Errorf("state already exists for app %q", newApp)
	}
	if config.AppName != newApp {
		return fmt.Errorf("renamed configuration must target app %q", newApp)
	}
	oldConfig, err := os.ReadFile(s.appConfigPath(oldApp))
	if err != nil {
		return err
	}
	if err := writeJSONAtomic(s.appConfigPath(oldApp), config, 0600); err != nil {
		return err
	}
	if err := os.Rename(s.appDir(oldApp), s.appDir(newApp)); err != nil {
		rollbackErr := writeFileAtomic(s.appConfigPath(oldApp), oldConfig, 0600)
		return errors.Join(fmt.Errorf("rename app state: %w", err), rollbackErr)
	}
	return nil
}

func readJSON(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func writeJSONAtomic(path string, value any, mode fs.FileMode) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFileAtomic(path, data, mode)
}

func writeFileAtomic(path string, data []byte, mode fs.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err := chownAtomicWriteParents(dir); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := chownToSystemUser(tmpName); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func resolveSystemOwner() (fileOwner, error) {
	name := os.Getenv("DOKKU_SYSTEM_USER")
	explicitUser := name != ""
	if name == "" {
		name = "dokku"
	}
	account, err := user.Lookup(name)
	if err != nil {
		if explicitUser {
			return fileOwner{}, fmt.Errorf("look up DOKKU_SYSTEM_USER %q: %w", name, err)
		}
		return fileOwner{uid: os.Getuid(), gid: os.Getgid()}, nil
	}
	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return fileOwner{}, fmt.Errorf("parse uid for DOKKU_SYSTEM_USER %q: %w", name, err)
	}
	gid, err := strconv.Atoi(account.Gid)
	if err != nil {
		return fileOwner{}, fmt.Errorf("parse gid for DOKKU_SYSTEM_USER %q: %w", name, err)
	}
	if groupName := os.Getenv("DOKKU_SYSTEM_GROUP"); groupName != "" {
		group, lookupErr := user.LookupGroup(groupName)
		if lookupErr != nil {
			return fileOwner{}, fmt.Errorf("look up DOKKU_SYSTEM_GROUP %q: %w", groupName, lookupErr)
		}
		gid, err = strconv.Atoi(group.Gid)
		if err != nil {
			return fileOwner{}, fmt.Errorf("parse gid for DOKKU_SYSTEM_GROUP %q: %w", groupName, err)
		}
	}
	return fileOwner{uid: uid, gid: gid}, nil
}

func chownToSystemUser(path string) error {
	owner, err := resolveSystemOwner()
	if err != nil {
		return err
	}
	return chownPath(path, owner)
}

func chownPath(path string, owner fileOwner) error {
	if owner.uid == os.Getuid() && owner.gid == os.Getgid() {
		return nil
	}
	if owner.uid == os.Getuid() {
		if err := os.Lchown(path, -1, owner.gid); err != nil {
			return fmt.Errorf("assign %s to Dokku system group %d: %w", path, owner.gid, err)
		}
		return nil
	}
	if os.Geteuid() != 0 {
		return fmt.Errorf("cannot assign %s to uid %d gid %d while running as uid %d", path, owner.uid, owner.gid, os.Geteuid())
	}
	if err := os.Lchown(path, owner.uid, owner.gid); err != nil {
		return fmt.Errorf("assign %s to Dokku system user: %w", path, err)
	}
	return nil
}

func chownTreeToSystemUser(root string) error {
	owner, err := resolveSystemOwner()
	if err != nil {
		return err
	}
	return filepath.WalkDir(root, func(path string, _ fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		return chownPath(path, owner)
	})
}

// Atomic writes are also used for nested rendered destinations. Repair every
// parent inside the configured plugin root so directories created by MkdirAll
// cannot remain owned by root after a manually invoked configuration command.
func chownAtomicWriteParents(dir string) error {
	owner, err := resolveSystemOwner()
	if err != nil {
		return err
	}
	root := os.Getenv("DOKKU_VAULT_AGENT_DATA_ROOT")
	if root == "" {
		root = DefaultDataRoot
	}
	root = filepath.Clean(root)
	dir = filepath.Clean(dir)
	relative, err := filepath.Rel(root, dir)
	if err != nil || relative == ".." || filepath.IsAbs(relative) || relative == "." {
		return chownPath(dir, owner)
	}
	for current := dir; ; current = filepath.Dir(current) {
		if err := chownPath(current, owner); err != nil {
			return err
		}
		if current == root {
			return nil
		}
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func isNotExist(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}
