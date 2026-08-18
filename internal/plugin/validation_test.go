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
	for _, mount := range []string{"/", "/proc/1", "/sys", "/dev/shm", "relative", "/safe/../escape"} {
		if validateMountPath(mount) == nil {
			t.Errorf("unsafe mount accepted: %q", mount)
		}
	}
	if err := validateMountPath("/app/secrets"); err != nil {
		t.Fatalf("safe mount rejected: %v", err)
	}
	for _, destination := range []string{"", "/absolute", "../escape", "a/../../escape", "a/../b"} {
		if validateDestination(destination) == nil {
			t.Errorf("unsafe destination accepted: %q", destination)
		}
	}
	if err := validateDestination("mongo/client.jks"); err != nil {
		t.Fatalf("safe destination rejected: %v", err)
	}
}
