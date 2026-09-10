package plugin

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
)

const maximumRetainedSupersededGenerations = 2

func generationsDir(livePath string) string {
	return livePath + ".generations"
}

func ensureGenerationStorage(livePath string) (string, error) {
	info, err := os.Lstat(livePath)
	if isNotExist(err) {
		return initializeGenerationStorage(livePath)
	}
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return currentGeneration(livePath)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("rendered storage path %s is neither a directory nor a symlink", livePath)
	}

	root := generationsDir(livePath)
	if err := os.MkdirAll(root, 0755); err != nil {
		return "", err
	}
	if err := chownToSystemUser(root); err != nil {
		return "", err
	}
	placeholder, err := os.MkdirTemp(root, ".legacy-")
	if err != nil {
		return "", err
	}
	if err := os.Remove(placeholder); err != nil {
		return "", err
	}
	if err := os.Rename(livePath, placeholder); err != nil {
		return "", fmt.Errorf("migrate rendered storage: %w", err)
	}
	generation := filepath.Base(placeholder)
	if err := replaceLiveSymlink(livePath, generation); err != nil {
		rollbackErr := os.Rename(placeholder, livePath)
		return "", fmt.Errorf("migrate rendered storage link: %w (rollback: %v)", err, rollbackErr)
	}
	return generation, nil
}

func ensureExistingGenerationStorage(livePath string) (string, error) {
	info, err := os.Lstat(livePath)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return currentGeneration(livePath)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("rendered storage path %s is neither a directory nor a symlink", livePath)
	}
	return ensureGenerationStorage(livePath)
}

func initializeGenerationStorage(livePath string) (string, error) {
	root := generationsDir(livePath)
	if err := os.MkdirAll(root, 0755); err != nil {
		return "", err
	}
	if err := chownToSystemUser(root); err != nil {
		return "", err
	}
	initial, err := os.MkdirTemp(root, ".initial-")
	if err != nil {
		return "", err
	}
	if err := os.Chmod(initial, 0755); err != nil {
		return "", err
	}
	if err := chownToSystemUser(initial); err != nil {
		return "", err
	}
	generation := filepath.Base(initial)
	if err := replaceLiveSymlink(livePath, generation); err != nil {
		_ = os.RemoveAll(initial)
		return "", err
	}
	return generation, nil
}

func currentGeneration(livePath string) (string, error) {
	target, err := os.Readlink(livePath)
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(target) {
		return "", fmt.Errorf("rendered storage link must be relative")
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(livePath), target))
	root := filepath.Clean(generationsDir(livePath))
	if filepath.Dir(resolved) != root {
		return "", fmt.Errorf("rendered storage link escapes its generation root")
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("rendered generation is not a secure directory")
	}
	return filepath.Base(resolved), nil
}

func publishRenderedOutputs(sourceRoot, livePath string, outputs []renderOutput) (string, error) {
	if _, err := ensureGenerationStorage(livePath); err != nil {
		return "", err
	}
	root := generationsDir(livePath)
	generationPath, err := os.MkdirTemp(root, ".generation-")
	if err != nil {
		return "", err
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(generationPath)
		}
	}()
	if err := os.Chmod(generationPath, 0755); err != nil {
		return "", err
	}
	if err := chownToSystemUser(generationPath); err != nil {
		return "", err
	}
	for _, output := range outputs {
		source := filepath.Join(sourceRoot, filepath.FromSlash(output.Relative))
		destination := filepath.Join(generationPath, filepath.FromSlash(output.Relative))
		if err := ensurePathWithin(generationPath, destination); err != nil {
			return "", err
		}
		if err := secureMkdirParents(generationPath, filepath.Dir(destination)); err != nil {
			return "", err
		}
		file, err := os.Open(source)
		if err != nil {
			return "", err
		}
		data, readErr := readBounded(file, maximumRenderedFileSize)
		closeErr := file.Close()
		if readErr != nil {
			return "", readErr
		}
		if closeErr != nil {
			return "", closeErr
		}
		modeValue, _ := strconv.ParseUint(output.Perms, 8, 32)
		if err := writeFileAtomic(destination, data, fs.FileMode(modeValue)); err != nil {
			return "", err
		}
	}
	generation := filepath.Base(generationPath)
	if err := replaceLiveSymlink(livePath, generation); err != nil {
		return "", err
	}
	published = true
	return generation, nil
}

func replaceLiveSymlink(livePath, generation string) error {
	root := generationsDir(livePath)
	targetPath := filepath.Join(root, generation)
	if err := ensurePathWithin(root, targetPath); err != nil {
		return err
	}
	info, err := os.Lstat(targetPath)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("generation %q is not a secure directory", generation)
	}
	parent := filepath.Dir(livePath)
	tmp, err := os.CreateTemp(parent, ".live-link-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Remove(tmpPath); err != nil {
		return err
	}
	defer os.Remove(tmpPath)
	relativeTarget, err := filepath.Rel(parent, targetPath)
	if err != nil {
		return err
	}
	if err := os.Symlink(relativeTarget, tmpPath); err != nil {
		return err
	}
	if err := chownToSystemUser(tmpPath); err != nil {
		return err
	}
	return os.Rename(tmpPath, livePath)
}

func cleanupGenerations(livePath string, keep ...string) error {
	wanted := make(map[string]bool, len(keep))
	for _, generation := range keep {
		if generation != "" {
			wanted[generation] = true
		}
	}
	entries, err := os.ReadDir(generationsDir(livePath))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if wanted[entry.Name()] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(generationsDir(livePath), entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func cleanupSupersededGenerations(livePath string, retain int, keep ...string) error {
	if retain < 0 {
		return fmt.Errorf("retained generation count must not be negative")
	}
	wanted := make(map[string]bool, len(keep))
	for _, generation := range keep {
		if generation != "" {
			wanted[generation] = true
		}
	}
	entries, err := os.ReadDir(generationsDir(livePath))
	if err != nil {
		return err
	}
	type candidate struct {
		name     string
		modified int64
	}
	candidates := make([]candidate, 0, len(entries))
	for _, entry := range entries {
		if wanted[entry.Name()] {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		candidates = append(candidates, candidate{name: entry.Name(), modified: info.ModTime().UnixNano()})
	}
	sort.Slice(candidates, func(left, right int) bool {
		if candidates[left].modified == candidates[right].modified {
			return candidates[left].name > candidates[right].name
		}
		return candidates[left].modified > candidates[right].modified
	})
	if len(candidates) <= retain {
		return nil
	}
	for _, candidate := range candidates[retain:] {
		if err := os.RemoveAll(filepath.Join(generationsDir(livePath), candidate.name)); err != nil {
			return err
		}
	}
	return nil
}

func removeRenderedStorage(livePath string) error {
	info, err := os.Lstat(livePath)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			err = os.Remove(livePath)
		} else {
			err = os.RemoveAll(livePath)
		}
	}
	if err != nil && !isNotExist(err) {
		return err
	}
	return os.RemoveAll(generationsDir(livePath))
}
