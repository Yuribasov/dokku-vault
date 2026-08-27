# dokku-vault-agent

> **WARNING:** This plugin was coded solely by Codex, use at your own risk!

This is an experimental Dokku plugin that renders files from HashiCorp Vault immediately before a Dokku release. It runs Vault Agent once in a hardened host-side Docker container, validates its outputs, publishes them to plugin-managed storage, and exits. The Vault binary is not added to the application image.

The plugin is intended for infrequently rotated files such as Java JKS keystores. Your application image remains responsible for checking that every required file exists and is valid before the Java process starts.

This project has unit tests, including a simulated release render, but has not yet been exercised here against a real Dokku and Vault installation. Test it on a non-production host first.

## Requirements

- Linux and Dokku 0.38.25 or newer.
- Dokku's `docker-local` scheduler. Other schedulers are rejected.
- Docker access on the Dokku host.
- A reachable Vault server over HTTPS. Plain HTTP Vault addresses are rejected.
- One AppRole and one least-privilege Vault policy per Dokku application.
- CI credentials that can create only response-wrapped SecretIDs for the corresponding AppRole.
- `make` on the Dokku host. Go 1.25.13 or newer is optional; installation falls back to a pinned official Go builder image when host Go is missing or older.

The plugin stores state under `/var/lib/dokku/data/vault-agent`. Application secret storage is mounted read-only in Dokku's `deploy` and `run` phases.

## How it works

1. An operator configures the Vault URL, digest-pinned Vault image, app mount, RoleID, and templates.
2. CI creates a response-wrapped, one-use AppRole SecretID.
3. CI sends only the wrapping token over SSH stdin to `vault-agent:stage`, bound to the full Git SHA and a local TTL.
4. The ordinary Git push builds the application image.
5. Dokku invokes the plugin's `pre-release-builder` hook.
6. The plugin locks the app, compares Dokku's Git revision, and consumes the staged token.
7. A one-shot Vault Agent container unwraps the SecretID, authenticates, renders all files, and exits.
8. The plugin validates regular-file type, non-empty content, paths, and modes, builds a complete immutable generation, and atomically switches the named-storage path to it.
9. New containers mount the new generation while already-running containers retain their previous bind-mounted generation.
10. After a successful deployment, `post-deploy` promotes the generation and safely retires generations older than the immediately previous successful deployment.

Any failure aborts the new Dokku release. Per-file publication never changes the currently running containers, and an incomplete generation is never exposed through the live storage path.

A staged credential is single-attempt. A retry requires a newly wrapped SecretID.

## Installation

Install the plugin from its GitHub repository:

```sh
sudo dokku plugin:install https://github.com/Yuribasov/dokku-vault.git --name vault-agent
```

For a local checkout on the Dokku host:

```sh
sudo dokku plugin:install file:///path/to/dokku-vault --name vault-agent
```

Installation uses host Go only when it is version 1.25.13 or newer. Otherwise it builds with the digest-pinned image declared in `install`. To override that builder, supply another official, digest-pinned image:

```sh
sudo env DOKKU_VAULT_AGENT_BUILD_IMAGE='golang:1.26.5-alpine3.23@sha256:YOUR_64_HEX_DIGEST' \
  dokku plugin:install https://github.com/Yuribasov/dokku-vault.git --name vault-agent
```

Plugin updates run the same version check and build path:

```sh
sudo dokku plugin:update vault-agent
```

## Initial configuration

### 1. Pin and configure Vault Agent

The image must have the exact form `hashicorp/vault:<tag>@sha256:<64 lowercase hex characters>`. Tags without a digest are rejected. Resolve the multi-architecture manifest digest through your registry tooling, then configure it:

```sh
sudo dokku vault-agent:configure \
  --vault-address https://vault.example.com \
  --image 'hashicorp/vault:1.20.2@sha256:YOUR_64_HEX_MANIFEST_DIGEST'
```

The plugin pulls the image immediately. Deploys then use that immutable local reference.

Only `https://` Vault addresses are accepted. Use a valid private CA with `vault-agent:ca:set` instead of disabling transport security.

For a private Vault CA:

```sh
sudo dokku vault-agent:ca:set < vault-ca.pem
```

To return to the image's system trust store:

```sh
sudo dokku vault-agent:ca:clear
```

### 2. Enable one application

```sh
sudo dokku vault-agent:enable myapp \
  --mount-path /app/runtime-secrets \
  --role-name myapp \
  --approle-mount auth/approle
```

This creates a deterministic named storage entry backed by a plugin-owned host directory. The mount path must be absolute and normalized; `/`, `/proc`, `/sys`, and `/dev` are rejected.

Set the AppRole RoleID over stdin. The RoleID is persistent and is not treated as a secret, but the plugin still stores it with mode `0600`:

```sh
vault read -field=role_id auth/approle/role/myapp/role-id |
  sudo dokku vault-agent:role-id:set myapp
```

### 3. Add managed file mappings

Managed mode assumes a KV v2 response and reads fields from `.Data.data`. The default decoding mode is `base64`, which is suitable for binary JKS data stored as base64 text.

```sh
sudo dokku vault-agent:template:add myapp mongodb-jks \
  --secret-path secret/data/apps/myapp/mongodb \
  --field client_jks_base64 \
  --destination mongodb/client.jks \
  --decode base64 \
  --perms 0444

sudo dokku vault-agent:template:add myapp mongodb-properties \
  --secret-path secret/data/apps/myapp/mongodb \
  --field java_properties \
  --destination mongodb/mongodb.properties \
  --decode none \
  --perms 0444
```

The application then sees:

```text
/app/runtime-secrets/mongodb/client.jks
/app/runtime-secrets/mongodb/mongodb.properties
```

Destinations are relative, traversal-free paths. Allowed modes are `0400`, `0440`, and `0444`. Application containers commonly need `0444` because their runtime UID may differ from the Dokku host UID.

Inspect or remove managed mappings with:

```sh
sudo dokku vault-agent:template:list myapp
sudo dokku vault-agent:template:remove myapp mongodb-properties
```

### Custom template mode

For KV v1, PKI, combined output files, or more complex Consul Template expressions, supply template-only HCL over stdin:

```hcl
template {
  contents = "{{ with secret \"secret/data/apps/myapp/mongodb\" }}{{ index .Data.data \"client_jks_base64\" | base64Decode }}{{ end }}"
  destination = "/vault/rendered/mongodb/client.jks"
  perms = "0444"
  backup = false
  create_dest_dirs = true
  error_on_missing_key = true
}
```

```sh
sudo dokku vault-agent:template:set-custom myapp < templates.hcl
```

If managed mappings already exist, switching modes requires explicit replacement:

```sh
sudo dokku vault-agent:template:set-custom myapp --replace < templates.hcl
```

Custom HCL is deliberately restricted:

- Only unlabeled `template` blocks are accepted.
- `contents`, `destination`, `perms`, and `backup` are required constant values.
- Destinations must be below `/vault/rendered/`.
- `backup` must be `false`.
- Only read-only modes `0400`, `0440`, and `0444` are accepted.
- `source`, `command`, `exec`, listeners, sinks, auto-auth, nested blocks, duplicate destinations, and top-level configuration are rejected.

The AppRole's Vault policy remains the final authority over which secrets any template can read.

Return to empty managed mode with:

```sh
sudo dokku vault-agent:template:clear-custom myapp
```

## Vault setup

The following examples are starting points, not a substitute for reviewing your own Vault threat model.

### Application token policy

For a KV v2 secret dedicated to `myapp`:

```hcl
path "secret/data/apps/myapp/mongodb" {
  capabilities = ["read"]
}
```

Create an AppRole whose resulting token has only that policy:

```sh
vault write auth/approle/role/myapp \
  token_policies='myapp-secrets' \
  secret_id_num_uses=1 \
  secret_id_ttl=45m \
  token_num_uses=0 \
  token_ttl=10m \
  token_max_ttl=15m
```

`token_num_uses=0` is intentional: Vault Agent may make multiple API requests while rendering several templates. Keep the token policy narrow and its TTL short. Add SecretID and token CIDR restrictions where your network layout permits them.

### CI broker policy

CI should not possess the application token policy. Give it a separate identity that can only create wrapped SecretIDs for one exact role:

```hcl
path "auth/approle/role/myapp/secret-id" {
  capabilities = ["update"]
  min_wrapping_ttl = "5m"
  max_wrapping_ttl = "45m"
}
```

Use a distinct broker policy and AppRole endpoint per Dokku application.

## CI deployment sequence

Choose a TTL that covers the worst-case Git upload, Dokku queue, Docker build, and at least five minutes of buffer. The example requires `vault`, `jq`, `ssh`, and `git`:

```sh
set +x

APP=myapp
DOKKU_HOST=dokku.example.com
REVISION=$(git rev-parse HEAD)
TTL_SECONDS=2700

WRAPPING_TOKEN=$(
  vault write -format=json \
    -wrap-ttl="${TTL_SECONDS}s" \
    "auth/approle/role/${APP}/secret-id" \
    "ttl=${TTL_SECONDS}s" |
  jq -er '.wrap_info.token'
)

printf '%s\n' "$WRAPPING_TOKEN" |
  ssh "dokku@${DOKKU_HOST}" \
    vault-agent:stage "$APP" \
    --revision "$REVISION" \
    --ttl-seconds "$TTL_SECONDS"

unset WRAPPING_TOKEN

git push "dokku@${DOKKU_HOST}:${APP}" HEAD:master
```

Keep shell tracing disabled for the entire wrapping and staging section. Never place a wrapping token or SecretID in:

- command-line arguments;
- Dokku config or application environment variables;
- the Git repository or Docker build context;
- CI artifacts, caches, or logs.

Treat every failed deployment as consuming the credential. Generate and stage a new wrapped SecretID before retrying; staging a new credential replaces any old pending credential with a best-effort overwrite followed by unlink.

The local `--ttl-seconds` check is defense in depth. Vault independently enforces the response-wrapping TTL and SecretID TTL.

## Operations

Show global configuration:

```sh
sudo dokku vault-agent:report
```

Show sanitized app readiness, expected revision, and pending expiry:

```sh
sudo dokku vault-agent:report myapp
```

The report never prints the RoleID, wrapping token, SecretID, or rendered file contents.

Manually run the same one-shot render path after staging a token:

```sh
sudo dokku vault-agent:render myapp
```

Manual rendering consumes the staged credential.

To rotate a keystore, update Vault, stage a new wrapped SecretID, and perform a normal deploy. No plugin daemon or host Vault Agent service is required.

Behavior of common Dokku operations:

- `ps:rebuild` invokes the release hook and requires a new token bound to the existing full Git SHA.
- `ps:restart` does not invoke the release hook and does not consume a staged token. It mounts the latest complete generation currently selected by the plugin.
- App rename preserves configuration and the storage association but discards any pending credential.
- App clone removes the cloned Vault storage attachment and leaves the clone without Vault integration.
- App destruction invokes cleanup through `pre-delete`.

Disable and purge an app integration:

```sh
sudo dokku vault-agent:disable myapp
```

Disable removes the attachment and named storage entry, pending credential, RoleID, templates, app configuration, and rendered files. This is destructive and rendered files are not recoverable unless they can be regenerated from Vault.

Cleanup is retryable. If Dokku cannot destroy the named storage entry, the command returns an error but retains a disabled cleanup tombstone, credentials, configuration, and rendered data. Fix the storage error and rerun `vault-agent:disable APP`; state is removed only after external storage and local secret cleanup both succeed.

Dokku plugin uninstall is refused while any app integration or orphaned rendered-secret data remains. Disable every configured app successfully before running:

```sh
sudo dokku plugin:uninstall vault-agent
```

## Security properties and limitations

- The response-wrapped token is accepted only on stdin, stored as `0600`, bound to a full 40- or 64-character lowercase Git SHA, and consumed before Vault Agent starts. Filesystem-level secure erasure is not guaranteed; use encrypted host storage when that matters.
- The token is never passed in Docker argv or environment.
- Vault Agent runs with a read-only root filesystem, all capabilities dropped, `no-new-privileges`, a private `/tmp`, no Docker socket, and the Dokku UID/GID.
- The Vault image reference must be an immutable `hashicorp/vault` digest.
- Render outputs must be non-empty regular files with expected read-only modes. Symlinks and traversal are rejected.
- Publication creates a complete immutable generation and atomically switches a relative symlink only after every file has been copied and validated. The current and immediately previous successful generations are retained so in-flight old containers keep their original files.
- App and global configuration mutations use stable host file locks. Staging cannot replace a credential while a render is consuming it, and disable/rename cannot remove a live lock inode.
- Vault Agent execution is bounded by the earlier of five minutes or the staged credential expiry. A timed-out named Agent container is force-removed through a separately bounded cleanup command.
- Install and update repair older root-owned state, and atomic writes preserve the Dokku system UID/GID even when an operator invokes commands through `sudo`.
- Host root, the Dokku account, Docker daemon administrators, and anyone able to replace this plugin are trusted.
- This version supports only the effective `docker-local` scheduler, has no Vault namespace option, and supports rendered files rather than direct environment-variable injection.
- Managed templates target KV v2. Use restricted custom HCL for other engines.
- One rendered file is limited to 128 MiB.
- Files are mounted for deploy/run, not Dockerfile build. The Java startup process should fail closed when files are absent or invalid.

## Commands

```text
vault-agent:configure
vault-agent:ca:set
vault-agent:ca:clear
vault-agent:enable APP
vault-agent:disable APP
vault-agent:role-id:set APP
vault-agent:template:add APP NAME
vault-agent:template:list APP
vault-agent:template:remove APP NAME
vault-agent:template:set-custom APP
vault-agent:template:clear-custom APP
vault-agent:stage APP
vault-agent:render APP
vault-agent:report [APP]
```

Run `dokku help` for the short command listing. Invalid or incomplete commands fail with a usage error.

## Development

Go 1.25.13 or newer is required for local builds. The patch-level minimum avoids known standard-library vulnerabilities in older Go 1.25 releases:

```sh
make test
make build
```

Run the stronger verification used during development:

```sh
go vet ./...
go test -race ./...
go test -cover ./internal/plugin
```

Dependencies are committed under `vendor/`. Regenerate them only after intentionally updating `go.mod` and `go.sum`:

```sh
go mod tidy
go mod vendor
```

Real integration testing should cover Dokku 0.38.25+ with Vault on both amd64 and arm64 before production use. See [PLAN.md](PLAN.md) for the complete integration matrix and [HANDOFF.md](HANDOFF.md) for current implementation status.
