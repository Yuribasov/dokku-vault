# Project Handoff

## Current status

The complete implementation and the full-review repair series are committed. It includes:

- a normal Dokku plugin dispatcher, subcommands, triggers, install and update hooks;
- digest-pinned one-shot Vault Agent rendering with explicit non-zero failure propagation from `pre-release-builder`;
- per-app named read-only storage for `docker-local`;
- response-wrapped credential staging over stdin, exact Git revision or immutable source-image binding, TTL checks, and one-attempt consumption;
- stable source-image binding for `ps:rebuild` of image-origin applications instead of Dokku's synthetic Git revision;
- managed multi-file KV v2 templates and validated custom template-only HCL;
- output validation, symlink/traversal defenses, immutable generation publication, and an atomic live symlink switch;
- stable per-app and global locks covering render, staging, configuration, templates, rename, disable, and cleanup;
- retryable cleanup tombstones and explicit recovery from failed enable rollback;
- bounded five-minute Agent execution with forced named-container cleanup;
- app disable, delete, clone, transactional rename, successful-deploy generation promotion, and guarded plugin uninstall;
- exact named-storage attachment removal during clone, rollback, and purge;
- legacy rendered-directory migration during post-deploy and umask-independent nested output permissions;
- orphaned-token cleanup, disabled-state staging rejection, and strict invalid-command failure;
- HTTPS-only Vault endpoints and effective `docker-local` scheduler detection through `scheduler-detect`;
- install/update ownership repair for state created by root while release hooks run as the Dokku user;
- sanitized reports and manual rendering;
- idempotent enable and managed-template upserts, including rollback-protected mount-path replacement;
- a containerized, digest-pinned Go build fallback, a patched Go 1.25.13 minimum, vendored dependencies, and a clean `govulncheck` result;
- unit tests, race-detector verification, and operator documentation in [README.md](README.md);
- GitHub Actions verification and gated, checksummed release automation.

The implementation commits are recorded in Git. The worktree should be clean after the documentation commit.

## Verification completed

The following checks pass in the development workspace:

```text
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/dokku-vault-agent
govulncheck ./...
bash -n install update uninstall commands triggers/pre-release-builder tests/*.sh
tests/install_test.sh
tests/uninstall_test.sh
git diff --check
```

The suite includes a fake end-to-end release render, Git and source-image deployment binding through the real pre-release entry point, image-origin `ps:rebuild`, concurrent stage/render locking, exact storage attachment cleanup, cleanup failure and retry, rollback tombstones, atomic multi-file generation switching, legacy-directory migration during post-deploy, restrictive-umask handling, orphan-token cleanup, timeout cleanup, root-to-Dokku ownership migration, rename failure safety, partial-install uninstall handling, and uninstall refusal. It confirms the wrapping token is absent from Docker argv, validates hardening flags, consumes the credential, and rejects replay.

A source-only Claude Opus review was completed after commit `739cc35`.
Confirmed findings were repaired and regression-tested. The suggested
`DOKKU_BUILD_SOURCE=""` fallback was intentionally not adopted: Dokku 0.38.25
exports this marker before nested release triggers, and failing closed prevents
a stale source-image property from authorizing another deployment type.

HashiCorp's current documentation confirms that `exit_after_auth = true` waits for configured templates to render before Vault Agent exits. Dokku's current documentation confirms the trigger argument order and named-storage command forms used here.

## Work still required before production

No real Dokku/Vault integration environment was available in this workspace. Before production use:

1. Install on a disposable Dokku 0.38.25+ `docker-local` host.
2. Exercise the complete CI wrapping, SSH staging, Git push, and release path against a non-development Vault.
3. Verify Dokku plugin installation and update through both host-Go and container-builder paths.
4. Test missing, expired, replayed, wrong-revision, wrong-source-image, and already-unwrapped credentials across Git push, `git:from-image`, and image-origin `ps:rebuild`.
5. Test app rename, clone, destruction, cleanup retry, plugin uninstall refusal, `ps:restart`, and `ps:rebuild`.
6. Confirm file readability with the exact UID/GID used by both Java images.
7. Test amd64 and arm64 hosts.
8. Verify Docker resolves the plugin's relative live-generation symlink as expected for bind mounts and that old containers retain their previous generation through `post-deploy`.

The warning in [README.md](README.md) is intentional: the code is not yet production-proven.

## Design references

- [PLAN.md](PLAN.md) contains the full design and integration test matrix.
- [README.md](README.md) is the operator and security guide.
- Dokku plugin triggers: <https://dokku.com/docs/development/plugin-triggers/>
- Dokku persistent storage: <https://dokku.com/docs/advanced-usage/persistent-storage/>
- Vault Agent templates: <https://developer.hashicorp.com/vault/docs/agent-and-proxy/agent/template>
- Vault AppRole auto-auth: <https://developer.hashicorp.com/vault/docs/agent-and-proxy/autoauth/methods/approle>
- Vault response wrapping: <https://developer.hashicorp.com/vault/docs/concepts/response-wrapping>
