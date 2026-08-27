package plugin

import (
	"fmt"
	"os"
)

func (p *Plugin) snapshotGlobalRenderConfiguration() (GlobalConfig, []byte, error) {
	lock, err := p.lockGlobal()
	if err != nil {
		return GlobalConfig{}, nil, err
	}
	defer unlockFile(lock)

	global, err := p.State.LoadGlobal()
	if err != nil {
		return GlobalConfig{}, nil, fmt.Errorf("Vault Agent plugin is not configured: %w", err)
	}
	if err := validateVaultAddress(global.VaultAddress); err != nil {
		return GlobalConfig{}, nil, err
	}
	if err := validateImage(global.Image); err != nil {
		return GlobalConfig{}, nil, err
	}

	caFile, err := os.Open(p.State.CAPath())
	if isNotExist(err) {
		return global, nil, nil
	}
	if err != nil {
		return GlobalConfig{}, nil, fmt.Errorf("open Vault CA: %w", err)
	}
	caCertificate, readErr := readBounded(caFile, 1024*1024)
	closeErr := caFile.Close()
	if readErr != nil {
		return GlobalConfig{}, nil, fmt.Errorf("read Vault CA: %w", readErr)
	}
	if closeErr != nil {
		return GlobalConfig{}, nil, fmt.Errorf("close Vault CA: %w", closeErr)
	}
	return global, caCertificate, nil
}
