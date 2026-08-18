# Restart Handoff

## Current Status

No plugin code has been implemented yet. The workspace was initially empty, and the completed design is saved in [PLAN.md](PLAN.md).

The task is to build a new standalone Go repository named `dokku-vault-agent` for Dokku 0.38.25+ using the `docker-local` scheduler.

## Decisions Already Made

- Vault Agent runs once during Dokku's `pre-release-builder` hook and exits after rendering.
- Normal deployment remains a Git push with the Dockerfile built on the Dokku host.
- CI creates a response-wrapped, single-use AppRole SecretID before each deployment.
- CI transfers the wrapping token through a restricted Dokku SSH plugin command over stdin, never through Dokku application config.
- Wrapping and SecretID TTL must cover worst-case upload, queue, Docker build, and five minutes of buffer.
- Every staged token is bound to the full expected Git SHA.
- `ps:rebuild` requires a new token and uses the existing SHA; `ps:restart` does not invoke the plugin hook.
- Each Dokku application has its own AppRole and limited Vault policy.
- The plugin manages named storage and accepts a configurable container mount path.
- Storage is mounted read-only in application containers.
- Rendering supports two mutually exclusive modes:
  - Managed mappings for multiple Vault fields/files.
  - Validated custom template-only HCL.
- A missing, expired, replayed, or mismatched token fails the deployment.
- Disable and app destruction always purge configuration, staged credentials, rendered files, and storage.
- Java images already contain startup checks for required rendered files.

## Research Findings

- `pre-release-builder` is the current supported pre-release hook; older `pre-deploy` and builder-specific release hooks are deprecated.
- Dokku 0.38 named storage supports a custom container mount path and read-only attachment.
- Current Dokku storage is implemented as a Go core plugin whose module is not cleanly importable by a separately installed plugin because it relies on workspace-local module replacements.
- The chosen compromise is a standalone Go plugin that calls the documented `dokku storage:*` CLI only during explicit enable/disable lifecycle commands. It must clear inherited `DOKKU_APP_NAME` and pass validated arguments without a shell.
- `ps:rebuild` calls `receive-app`, rebuilds, releases, and invokes `pre-release-builder`. `ps:restart` deploys the existing image without that release hook.

## Next Implementation Tasks

1. Initialize the Go repository, plugin metadata, dispatcher layout, Makefile, install/update hooks, and help output.
2. Implement global and per-app state with strict permissions, atomic writes, validation, and per-app locks.
3. Implement global Vault/image/CA configuration and image pre-pull validation.
4. Implement enable/disable plus the narrow Dokku storage CLI adapter and lifecycle rollback.
5. Implement RoleID input, managed template mappings, and restricted custom-HCL parsing.
6. Implement wrapping-token staging through stdin with SHA/TTL metadata and one-attempt consumption.
7. Implement the hardened one-shot Vault Agent container runner, output validation, and atomic publication.
8. Add `pre-release-builder`, app delete, clone, and rename triggers.
9. Add sanitized reporting and manual render commands.
10. Add unit tests, Dokku/Vault integration tests, CI builds for amd64/arm64, and operator documentation.

## Reference Documentation

- Dokku plugin creation: <https://dokku.com/docs/development/plugin-creation/>
- Dokku plugin triggers: <https://dokku.com/docs/development/plugin-triggers/>
- Dokku persistent storage: <https://dokku.com/docs/advanced-usage/persistent-storage/>
- Vault Agent templates: <https://developer.hashicorp.com/vault/docs/agent-and-proxy/agent/template>
- Vault Agent AppRole auto-auth: <https://developer.hashicorp.com/vault/docs/agent-and-proxy/autoauth/methods/approle>
- Vault response wrapping: <https://developer.hashicorp.com/vault/docs/concepts/response-wrapping>

