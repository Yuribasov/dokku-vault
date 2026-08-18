# Project Handoff

## Current status

The first complete implementation of `dokku-vault-agent` is committed. It includes:

- a normal Dokku plugin dispatcher, subcommands, triggers, install and update hooks;
- digest-pinned one-shot Vault Agent rendering in `pre-release-builder`;
- per-app named read-only storage for `docker-local`;
- response-wrapped credential staging over stdin, exact Git revision binding, TTL checks, and one-attempt consumption;
- managed multi-file KV v2 templates and validated custom template-only HCL;
- output validation, symlink/traversal defenses, and atomic per-file publication;
- app disable, delete, clone, and rename lifecycle handling;
- sanitized reports and manual rendering;
- a containerized, digest-pinned Go build fallback and vendored Go dependencies;
- unit tests, race-detector verification, and operator documentation in [README.md](README.md).

The implementation commits are recorded in Git. The worktree should be clean after the documentation commit.

## Verification completed

The following checks pass in the development workspace:

```text
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/dokku-vault-agent
git diff --check
```

Unit statement coverage is 53.2%. The suite includes a fake end-to-end release render that confirms the wrapping token is absent from Docker argv, validates hardening flags, publishes a rendered file, consumes the credential, and rejects replay.

HashiCorp's current documentation confirms that `exit_after_auth = true` waits for configured templates to render before Vault Agent exits. Dokku's current documentation confirms the trigger argument order and named-storage command forms used here.

## Work still required before production

No real Dokku/Vault integration environment was available in this workspace. Before production use:

1. Install on a disposable Dokku 0.38.25+ `docker-local` host.
2. Exercise the complete CI wrapping, SSH staging, Git push, and release path against a non-development Vault.
3. Verify Dokku plugin installation and update through both host-Go and container-builder paths.
4. Test missing, expired, replayed, wrong-revision, and already-unwrapped credentials.
5. Test app rename, clone, destruction, `ps:restart`, and `ps:rebuild`.
6. Confirm file readability with the exact UID/GID used by both Java images.
7. Test amd64 and arm64 hosts.
8. Add CI and release automation once the repository's permanent Git hosting location is known.

The warning in [README.md](README.md) is intentional: the code is not yet production-proven.

## Design references

- [PLAN.md](PLAN.md) contains the full design and integration test matrix.
- [README.md](README.md) is the operator and security guide.
- Dokku plugin triggers: <https://dokku.com/docs/development/plugin-triggers/>
- Dokku persistent storage: <https://dokku.com/docs/advanced-usage/persistent-storage/>
- Vault Agent templates: <https://developer.hashicorp.com/vault/docs/agent-and-proxy/agent/template>
- Vault AppRole auto-auth: <https://developer.hashicorp.com/vault/docs/agent-and-proxy/autoauth/methods/approle>
- Vault response wrapping: <https://developer.hashicorp.com/vault/docs/concepts/response-wrapping>
