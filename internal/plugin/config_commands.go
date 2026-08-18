package plugin

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"strings"
)

func (p *Plugin) commandConfigure(args []string, stdout, stderr io.Writer) error {
	flags, booleans, err := splitFlags(args)
	if err != nil {
		return err
	}
	if len(booleans) != 0 {
		return fmt.Errorf("--replace is not valid for configure")
	}
	if err := rejectUnknownFlags(flags, "vault-address", "image"); err != nil {
		return err
	}
	address, err := requireFlag(flags, "vault-address")
	if err != nil {
		return err
	}
	image, err := requireFlag(flags, "image")
	if err != nil {
		return err
	}
	if err := validateVaultAddress(address); err != nil {
		return err
	}
	if err := validateImage(image); err != nil {
		return err
	}
	docker := os.Getenv("DOCKER_BIN")
	if docker == "" {
		docker = "docker"
	}
	if err := p.Runner.Run(CommandSpec{Name: docker, Args: []string{"image", "pull", image}, Stdout: stdout, Stderr: stderr}); err != nil {
		return fmt.Errorf("pull Vault image: %w", err)
	}
	return p.State.SaveGlobal(GlobalConfig{VaultAddress: strings.TrimRight(address, "/"), Image: image})
}

func (p *Plugin) commandCASet(args []string, stdin io.Reader) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: vault-agent:ca:set")
	}
	data, err := readBounded(stdin, 1024*1024)
	if err != nil {
		return err
	}
	if err := validatePEMCertificates(data); err != nil {
		return err
	}
	if err := p.State.Setup(); err != nil {
		return err
	}
	return writeFileAtomic(p.State.CAPath(), data, 0600)
}

func validatePEMCertificates(data []byte) error {
	remaining := data
	count := 0
	for len(strings.TrimSpace(string(remaining))) > 0 {
		block, rest := pem.Decode(remaining)
		if block == nil {
			return fmt.Errorf("CA input contains invalid PEM data")
		}
		if block.Type != "CERTIFICATE" {
			return fmt.Errorf("CA input may contain only CERTIFICATE PEM blocks")
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			return fmt.Errorf("parse CA certificate: %w", err)
		}
		count++
		remaining = rest
	}
	if count == 0 {
		return fmt.Errorf("CA input contains no certificates")
	}
	return nil
}

func (p *Plugin) commandCAClear(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: vault-agent:ca:clear")
	}
	if err := os.Remove(p.State.CAPath()); err != nil && !isNotExist(err) {
		return err
	}
	return nil
}

func (p *Plugin) commandRoleIDSet(args []string, stdin io.Reader) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: vault-agent:role-id:set APP")
	}
	app := args[0]
	if err := validateAppName(app); err != nil {
		return err
	}
	if _, err := p.State.LoadApp(app); err != nil {
		return fmt.Errorf("app integration is not enabled: %w", err)
	}
	roleID, err := readSingleLine(stdin, maximumCredentialSize, "RoleID")
	if err != nil {
		return err
	}
	if err := p.State.EnsureApp(app); err != nil {
		return err
	}
	return writeFileAtomic(p.State.RoleIDPath(app), []byte(roleID+"\n"), 0600)
}
