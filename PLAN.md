# Dokku One-Shot Vault Agent Plugin

## Summary

Create a standalone `dokku-vault-agent` Go plugin targeting Dokku 0.38.25+ with the `docker-local` scheduler. It will:

- Manage per-app secret storage and a configurable read-only container mount.
- Accept a response-wrapped, single-use AppRole SecretID from CI over Dokku SSH stdin.
- Bind each staged token to an exact Git commit.
- Run a pinned Vault Agent image during `pre-release-builder`.
- Render and validate one or more files before allowing deployment.
- Support either managed template mappings or validated custom template-only HCL.
- Fail deployment when credentials, rendering, or validation fail.
- Purge all files and configuration on disable or app destruction.

Vault policy, AppRole creation, and CI authentication remain external provisioning concerns.

## Plugin Interface and Configuration

Implement these public commands:

- `vault-agent:configure --vault-address URL --image IMAGE`
  - Set the global Vault address and Vault Agent image.
  - Require an immutable `hashicorp/vault:<version>@sha256:<digest>` image reference.
  - Pull and inspect the image immediately so deployments do not depend on registry availability.
- `vault-agent:ca:set` and `vault-agent:ca:clear`
  - Read an optional Vault CA certificate from stdin.
- `vault-agent:enable APP --mount-path PATH --role-name ROLE [--approle-mount auth/approle]`
  - Validate that the app exists and uses `docker-local`.
  - Create a plugin-owned rendered directory and named Dokku storage entry.
  - Mount it read-only into deploy and run containers at the requested absolute path.
  - Reject `/`, `/proc`, `/sys`, and `/dev` mount targets.
- `vault-agent:role-id:set APP`
  - Read the persistent, non-secret AppRole RoleID from stdin.
- `vault-agent:template:add APP NAME --secret-path PATH --field FIELD --destination FILE [--decode base64|none] [--perms 0444]`
  - Add a managed file mapping. Default decoding is `base64`; default permissions are `0444`.
  - Permit only relative, traversal-free destinations and modes `0400`, `0440`, or `0444`.
- `vault-agent:template:list APP` and `vault-agent:template:remove APP NAME`.
- `vault-agent:template:set-custom APP [--replace]`
  - Read custom template-only HCL from stdin.
  - Managed mappings and custom HCL are mutually exclusive.
  - `--replace` explicitly removes existing managed mappings when switching modes.
- `vault-agent:template:clear-custom APP`
  - Return the app to empty managed mode.
- `vault-agent:stage APP --revision FULL_SHA --ttl-seconds N`
  - Read one wrapping token from stdin.
  - Require a full 40- or 64-character hexadecimal Git SHA.
  - Store token, revision, staging time, and local expiry atomically.
  - A new stage replaces and securely removes any older pending token.
- `vault-agent:render APP`
  - Manually consume a staged token and run the same rendering path as deployment.
- `vault-agent:report [APP]`
  - Show sanitized readiness, template mode, storage/mount settings, expected revision, and pending-token expiry; never reveal credentials.
- `vault-agent:disable APP`
  - Unmount storage, unregister its entry, and delete rendered files, RoleID, pending token, templates, and app configuration.

Store state under `/var/lib/dokku/data/vault-agent`:

- Global directory mode `0700`, owned by the Dokku system user.
- Per-app JSON configuration written through atomic rename.
- RoleID and wrapping-token files mode `0600`.
- Rendered app directory inside the protected parent, with files using configured read permissions.
- Use a deterministic storage-entry name derived from a truncated app name plus hash to satisfy Dokku's 45-character limit.

Use the documented `dokku storage:create|mount|unmount|destroy` CLI only from explicit setup and cleanup commands. Invoke it without a shell, clear inherited `DOKKU_APP_NAME`, and pass the validated app explicitly.

## Rendering and Deployment Behavior

- Package one static Go dispatcher binary with command and trigger symlinks, `plugin.toml`, help output, install/update hooks, and vendored dependencies. Build during installation with a digest-pinned Go 1.26 builder image.
- Register `pre-release-builder BUILDER_TYPE APP IMAGE`.
  - Return immediately for apps without enabled integration.
  - Acquire an app-specific file lock.
  - Require complete global/app configuration and a non-expired staged token.
  - Read Dokku's current `git-revision` and compare it exactly with the staged SHA.
  - Atomically consume the pending token before invoking Vault; every attempt requires a new token.
- Generate a protected temporary Agent configuration containing:
  - `exit_after_auth = true`.
  - Global Vault address and optional CA.
  - AppRole RoleID file and wrapped SecretID file.
  - `secret_id_response_wrapping_path` equal to `<approle-mount>/role/<role-name>/secret-id`.
  - `remove_secret_id_file_after_reading = true`.
  - `exit_on_retry_failure = true`.
- Managed mode generates one Vault `template` block per mapping using `index` for safe field access and optional `base64Decode`.
- Custom mode parses HCL before execution:
  - Allow only `template` blocks with inline `contents`.
  - Require every destination below `/vault/rendered`.
  - Reject `source`, `exec`, `command`, listeners, sinks, authentication, Vault configuration, environment templates, path traversal, duplicate destinations, executable/writeable output modes, and backups.
- Run the Vault image as the unprivileged Dokku UID/GID with:
  - `--rm`, read-only root filesystem, all capabilities dropped, `no-new-privileges`, private tmpfs, Dokku labels, and no Docker socket.
  - Read-only config/CA mounts, temporary auth mount, and a temporary output directory mounted at `/vault/rendered`.
  - `--userns=host` for this non-root hardened container so plugin-owned credential files remain readable on user-namespace-enabled Docker hosts.
- After Agent exits successfully:
  - Verify every expected output is a non-empty regular file, not a symlink, and has an allowed mode.
  - Publish each file into the plugin-owned rendered directory using a same-directory temporary file plus atomic rename.
  - Remove obsolete files no longer declared by the active template configuration.
- Any validation, Agent, revision, or token failure returns non-zero and aborts the Dokku release. Clean up temporary credentials and rendered staging files on every path.
- `ps:rebuild` follows the full build/release path and therefore requires a newly staged token bound to the existing SHA.
- `ps:restart` does not run the release hook and does not rerender or consume a token.
- A successful render followed by a later Dokku scheduling failure leaves the newly rendered files published; the next deployment still requires a new wrapped SecretID.

Lifecycle handling:

- `pre-delete` purges plugin storage and state after Dokku's destructive confirmation but before app removal.
- App cloning removes the cloned plugin-owned storage attachment and leaves Vault integration disabled on the clone.
- App renaming moves plugin configuration and rendered storage association, retains the configured AppRole name, and deletes any pending token.
- Disable and destroy are idempotent but always purge.

## Vault and CI Contract

Document a per-app Vault policy granting only the required secret paths.

Configure each AppRole with:

- `secret_id_num_uses=1`.
- `token_num_uses=0`, required by Agent auto-auth.
- Short token TTL/max TTL.
- SecretID TTL long enough for worst-case Git upload, Dokku queue, Docker build, and five minutes of buffer.
- CIDR restrictions when possible.

Give CI a separate broker policy that can only update the exact AppRole SecretID endpoint and requires response wrapping through `min_wrapping_ttl` and `max_wrapping_ttl`.

Document the deployment sequence:

1. Determine the full source SHA.
2. Generate a response-wrapped SecretID with wrapping and SecretID TTL equal to measured worst-case build time plus five minutes.
3. Pipe the wrapping token to `ssh dokku@host vault-agent:stage APP --revision SHA --ttl-seconds N`.
4. Perform the normal Git push.
5. Treat any failed deployment as consuming the credential; generate a new wrapped SecretID before retrying.

Ensure examples disable shell tracing around token handling and never place the token in argv, Dokku config, app environment, or CI logs.

## Test Plan

### Unit tests

- Command validation, state permissions, atomic writes, app-name/storage-name generation, mount-path restrictions, and sanitized reports.
- Managed HCL escaping, multiple mappings, decoding modes, destination collisions, and mode validation.
- Custom HCL acceptance and rejection for every forbidden block, attribute, and path.
- Token staging, replacement, expiry, replay prevention, SHA mismatch, and unconditional cleanup.
- Docker argument construction proves no token appears in argv/environment and all hardening flags are present.
- Storage CLI adapter clears inherited routing and rolls back partial enable operations.
- Locking prevents concurrent renders for one app.

### Integration tests

Use Dokku 0.38.25+ docker-local and a real Vault development instance:

- Enable an app with a custom mount path and confirm the named storage attachment is read-only.
- Render multiple managed files from one AppRole and deploy successfully.
- Render equivalent files through custom HCL.
- Rotate Vault data, stage a new wrapped SecretID, and verify a new deploy publishes updated files.
- Verify `ps:rebuild` consumes a fresh token with the same SHA.
- Verify `ps:restart` neither renders nor consumes a staged token.
- Confirm missing, expired, replayed, wrong-path, wrong-revision, and already-unwrapped tokens abort deployment while the previous app remains active.
- Confirm disable and app destruction remove storage, rendered files, staged credentials, and configuration.
- Confirm a clone receives no integration and a rename retains configuration but discards pending credentials.

### Compatibility checks

- Fail plugin installation below Dokku 0.38.25.
- Fail enablement for non-`docker-local` apps.
- Test amd64 and arm64 builds.

## Assumptions

- This is a new `dokku-vault-agent` repository.
- Java containers already validate required rendered files at startup.
- Application containers can read `0444` files; tighter shared-GID ownership is deferred.
- Host administrators and the Dokku system user are trusted.
- Vault provisioning and CI authentication are documented but not performed by the plugin.
- Version 1 does not inject environment variables directly; dotenv, JSON, Java properties, or similar files may be rendered and consumed by the existing startup script.

