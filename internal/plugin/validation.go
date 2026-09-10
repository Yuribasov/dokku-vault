package plugin

import (
	"crypto/sha256"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
)

var (
	appNamePattern      = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	templateNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	revisionPattern     = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
	imagePattern        = regexp.MustCompile(`^hashicorp/vault:[A-Za-z0-9][A-Za-z0-9._-]*@sha256:[0-9a-f]{64}$`)
	sourceImagePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]*@sha256:[0-9a-f]{64}$`)
	tokenPattern        = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	appRoleMountPattern = regexp.MustCompile(`^auth/[A-Za-z0-9][A-Za-z0-9._-]*(?:/[A-Za-z0-9][A-Za-z0-9._-]*)*$`)
	roleNamePattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	storageEntryPattern = regexp.MustCompile(`^vault-[a-z0-9-]+-[0-9a-f]{8}$`)
)

func validateAppName(app string) error {
	if len(app) > 128 || !appNamePattern.MatchString(app) {
		return fmt.Errorf("invalid Dokku app name %q", app)
	}
	return nil
}

func validateTemplateName(name string) error {
	if len(name) > 128 || !templateNamePattern.MatchString(name) {
		return fmt.Errorf("invalid template name %q", name)
	}
	return nil
}

func validateVaultAddress(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return fmt.Errorf("Vault address must be an absolute HTTPS URL without credentials")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("Vault address must not contain a query or fragment")
	}
	return nil
}

func validateImage(image string) error {
	if !imagePattern.MatchString(image) {
		return fmt.Errorf("image must be hashicorp/vault:<tag>@sha256:<64 lowercase hex digest>")
	}
	return nil
}

func validateSourceImage(image string) error {
	if len(image) > 1024 || !sourceImagePattern.MatchString(image) {
		return fmt.Errorf("source image must be an immutable IMAGE@sha256:<64 lowercase hex digest> reference")
	}
	return nil
}

func validateMountPath(value string) error {
	if !path.IsAbs(value) || path.Clean(value) != value {
		return fmt.Errorf("mount path must be an absolute normalized path")
	}
	for _, forbidden := range []string{"/", "/proc", "/sys", "/dev"} {
		if value == forbidden || strings.HasPrefix(value, forbidden+"/") {
			return fmt.Errorf("mount path %q is not allowed", value)
		}
	}
	return nil
}

func validateDestination(value string) error {
	if value == "" || path.IsAbs(value) || path.Clean(value) != value || value == "." || value == ".." || strings.HasPrefix(value, "../") {
		return fmt.Errorf("destination must be a normalized relative path without traversal")
	}
	return nil
}

func validateAppRoleMount(value string) error {
	if len(value) > 256 || !appRoleMountPattern.MatchString(value) {
		return fmt.Errorf("AppRole mount must be a normalized Vault auth path containing only letters, digits, dots, underscores, and hyphens")
	}
	return nil
}

func validateRoleName(value string) error {
	if len(value) > 128 || !roleNamePattern.MatchString(value) {
		return fmt.Errorf("role name must contain only letters, digits, dots, underscores, and hyphens")
	}
	return nil
}

func validateStorageEntry(value string) error {
	if len(value) > 45 || !storageEntryPattern.MatchString(value) {
		return fmt.Errorf("invalid plugin storage entry %q", value)
	}
	return nil
}

func validateMode(value string) error {
	switch value {
	case "0400", "0440", "0444":
		return nil
	default:
		return fmt.Errorf("permissions must be one of 0400, 0440, or 0444")
	}
}

func storageEntryName(app string) string {
	prefix := app
	if len(prefix) > 29 {
		prefix = prefix[:29]
	}
	digest := sha256.Sum256([]byte(app))
	return fmt.Sprintf("vault-%s-%x", prefix, digest[:4])
}
