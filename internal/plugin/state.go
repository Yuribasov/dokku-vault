package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

type State struct {
	Root string
}

func NewState(root string) *State {
	return &State{Root: root}
}

func (s *State) Setup() error {
	for _, path := range []string{s.Root, s.appsRoot(), s.renderedRoot(), s.tempRoot()} {
		if err := os.MkdirAll(path, 0700); err != nil {
			return fmt.Errorf("create state directory %s: %w", path, err)
		}
		if err := os.Chmod(path, 0700); err != nil {
			return fmt.Errorf("secure state directory %s: %w", path, err)
		}
	}
	return nil
}

func (s *State) appsRoot() string     { return filepath.Join(s.Root, "apps") }
func (s *State) renderedRoot() string { return filepath.Join(s.Root, "rendered") }
func (s *State) tempRoot() string     { return filepath.Join(s.Root, "tmp") }
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
	return filepath.Join(s.appDir(app), "render.lock")
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
	return os.Chmod(s.appDir(app), 0700)
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

func (s *State) RenameApp(oldApp, newApp string) error {
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
	return os.Rename(s.appDir(oldApp), s.appDir(newApp))
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

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func isNotExist(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}
