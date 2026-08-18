package plugin

import "time"

const (
	PluginName                   = "vault-agent"
	DefaultDataRoot              = "/var/lib/dokku/data/vault-agent"
	DefaultAppRoleMount          = "auth/approle"
	DefaultTemplateMode          = "managed"
	DefaultOutputMode            = "0444"
	MinimumDokkuVersion          = "0.38.25"
	maximumCredentialSize        = 16 * 1024
	cleanupPhasePending          = "pending"
	cleanupPhaseStorageDestroyed = "storage-destroyed"
)

type GlobalConfig struct {
	VaultAddress string `json:"vault_address"`
	Image        string `json:"image"`
}

type ManagedTemplate struct {
	Name        string `json:"name"`
	SecretPath  string `json:"secret_path"`
	Field       string `json:"field"`
	Destination string `json:"destination"`
	Decode      string `json:"decode"`
	Perms       string `json:"perms"`
}

type AppConfig struct {
	AppName       string            `json:"app_name"`
	Enabled       bool              `json:"enabled"`
	MountPath     string            `json:"mount_path"`
	RoleName      string            `json:"role_name"`
	AppRoleMount  string            `json:"approle_mount"`
	StorageEntry  string            `json:"storage_entry"`
	TemplateMode  string            `json:"template_mode"`
	Templates     []ManagedTemplate `json:"templates,omitempty"`
	CustomHCLFile string            `json:"custom_hcl_file,omitempty"`
	CleanupPhase  string            `json:"cleanup_phase,omitempty"`
}

type StagedCredential struct {
	Revision  string    `json:"revision"`
	StagedAt  time.Time `json:"staged_at"`
	ExpiresAt time.Time `json:"expires_at"`
}
