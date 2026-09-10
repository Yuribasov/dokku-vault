package plugin

import (
	"strings"
	"testing"
)

func TestStorageEntryNameIsStableBoundedAndDistinct(t *testing.T) {
	long := "application-" + strings.Repeat("a", 100)
	first := storageEntryName(long)
	if len(first) > 45 {
		t.Fatalf("storage entry is %d characters: %q", len(first), first)
	}
	if first != storageEntryName(long) {
		t.Fatal("storage entry name is not deterministic")
	}
	if first == storageEntryName(long+"b") {
		t.Fatal("different app names produced the same storage entry")
	}
	if err := validateStorageEntry(first); err != nil {
		t.Fatalf("generated entry is invalid: %v", err)
	}
}

func TestSecuritySensitiveValidation(t *testing.T) {
	validImage := "hashicorp/vault:1.20.2@sha256:" + strings.Repeat("a", 64)
	if err := validateImage(validImage); err != nil {
		t.Fatalf("valid image rejected: %v", err)
	}
	for _, image := range []string{"hashicorp/vault:latest", "evil/vault:1@sha256:" + strings.Repeat("a", 64)} {
		if validateImage(image) == nil {
			t.Errorf("unsafe image accepted: %q", image)
		}
	}
	validSourceImage := "registry.example.test:5000/team/sample:build-42@sha256:" + strings.Repeat("b", 64)
	if err := validateSourceImage(validSourceImage); err != nil {
		t.Fatalf("valid source image rejected: %v", err)
	}
	for _, image := range []string{
		"registry.example.test/team/sample:latest",
		"registry.example.test/team/sample@sha256:" + strings.Repeat("B", 64),
		"registry.example.test/team/sample@sha256:short",
	} {
		if validateSourceImage(image) == nil {
			t.Errorf("mutable or invalid source image accepted: %q", image)
		}
	}
	for _, mount := range []string{"/", "/proc/1", "/sys", "/dev/shm", "relative", "/safe/../escape"} {
		if validateMountPath(mount) == nil {
			t.Errorf("unsafe mount accepted: %q", mount)
		}
	}
	if err := validateMountPath("/app/secrets"); err != nil {
		t.Fatalf("safe mount rejected: %v", err)
	}
	for _, destination := range []string{"", ".", "..", "/absolute", "../escape", "a/../../escape", "a/../b"} {
		if validateDestination(destination) == nil {
			t.Errorf("unsafe destination accepted: %q", destination)
		}
	}
	if err := validateDestination("mongo/client.jks"); err != nil {
		t.Fatalf("safe destination rejected: %v", err)
	}
}

func TestVaultAddressRequiresHTTPS(t *testing.T) {
	for _, address := range []string{
		"http://vault.example.test",
		"https://user:password@vault.example.test",
		"https://vault.example.test?token=value",
		"https://vault.example.test#fragment",
	} {
		if validateVaultAddress(address) == nil {
			t.Errorf("unsafe Vault address accepted: %q", address)
		}
	}
	if err := validateVaultAddress("https://vault.example.test:8200"); err != nil {
		t.Fatalf("safe Vault address rejected: %v", err)
	}
}

func TestAppRoleMountValidation(t *testing.T) {
	for _, mount := range []string{"", ".", "approle", "/auth/approle", "auth/../approle", "auth/approle/", "auth/approle role"} {
		if validateAppRoleMount(mount) == nil {
			t.Errorf("invalid AppRole mount accepted: %q", mount)
		}
	}
	for _, mount := range []string{"auth/approle", "auth/team/approle-v2"} {
		if err := validateAppRoleMount(mount); err != nil {
			t.Errorf("valid AppRole mount rejected: %q: %v", mount, err)
		}
	}
}

func TestRoleNameValidation(t *testing.T) {
	for _, role := range []string{"", " ", "role/name", "role name", ".hidden", "-option"} {
		if validateRoleName(role) == nil {
			t.Errorf("invalid role name accepted: %q", role)
		}
	}
	for _, role := range []string{"sample", "sample-role", "sample_role.v2"} {
		if err := validateRoleName(role); err != nil {
			t.Errorf("valid role name rejected: %q: %v", role, err)
		}
	}
}
