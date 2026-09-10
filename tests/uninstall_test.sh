#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT=$(cd "$(dirname "$0")/.." && pwd)
TEST_ROOT=$(mktemp -d /tmp/dokku-vault-uninstall-test.XXXXXX)

cleanup() {
  case "$TEST_ROOT" in
    /tmp/dokku-vault-uninstall-test.*) rm -rf -- "$TEST_ROOT" ;;
    *) echo "refusing to remove unexpected test path: $TEST_ROOT" >&2 ;;
  esac
}
trap cleanup EXIT

prepare_case() {
  local name=$1
  local case_dir="$TEST_ROOT/$name"
  mkdir -p "$case_dir/vault-agent" "$case_dir/state"
  cp "$REPO_ROOT/uninstall" "$case_dir/vault-agent/uninstall"
  chmod +x "$case_dir/vault-agent/uninstall"
  printf '%s\n' "$case_dir"
}

clean_case=$(prepare_case clean)
DOKKU_VAULT_AGENT_DATA_ROOT="$clean_case/state" "$clean_case/vault-agent/uninstall" vault-agent

app_state_case=$(prepare_case app-state)
mkdir -p "$app_state_case/state/apps/sample"
if DOKKU_VAULT_AGENT_DATA_ROOT="$app_state_case/state" "$app_state_case/vault-agent/uninstall" vault-agent 2>/dev/null; then
  echo "uninstall accepted configured app state without a plugin binary" >&2
  exit 1
fi

rendered_state_case=$(prepare_case rendered-state)
mkdir -p "$rendered_state_case/state/rendered/vault-sample"
if DOKKU_VAULT_AGENT_DATA_ROOT="$rendered_state_case/state" "$rendered_state_case/vault-agent/uninstall" vault-agent 2>/dev/null; then
  echo "uninstall accepted rendered secret state without a plugin binary" >&2
  exit 1
fi

binary_case=$(prepare_case binary)
mkdir -p "$binary_case/vault-agent/bin"
printf '%s\n' '#!/usr/bin/env bash' '[[ $1 == internal && $2 == uninstall-check ]]' 'exit 42' >"$binary_case/vault-agent/bin/dokku-vault-agent"
chmod +x "$binary_case/vault-agent/bin/dokku-vault-agent"
status=0
"$binary_case/vault-agent/uninstall" vault-agent 2>/dev/null || status=$?
if [[ $status -ne 42 ]]; then
  echo "uninstall did not preserve binary check status: $status" >&2
  exit 1
fi

printf 'partial uninstall tests passed\n'
