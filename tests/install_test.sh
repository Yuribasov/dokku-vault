#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT=$(cd "$(dirname "$0")/.." && pwd)
TEST_ROOT=$(mktemp -d /tmp/dokku-vault-install-test.XXXXXX)

cleanup() {
  case "$TEST_ROOT" in
    /tmp/dokku-vault-install-test.*) rm -rf -- "$TEST_ROOT" ;;
    *) echo "refusing to remove unexpected test path: $TEST_ROOT" >&2 ;;
  esac
}
trap cleanup EXIT

AGENT_STUB="$TEST_ROOT/agent-stub"
printf '%s\n' '#!/usr/bin/env bash' 'exit 0' >"$AGENT_STUB"
chmod +x "$AGENT_STUB"

AGENT_FAIL_STUB="$TEST_ROOT/agent-fail-stub"
printf '%s\n' '#!/usr/bin/env bash' 'exit 42' >"$AGENT_FAIL_STUB"
chmod +x "$AGENT_FAIL_STUB"

prepare_case() {
  local case_dir=$1
  mkdir -p "$case_dir/plugin/triggers" "$case_dir/stubs"
  cp "$REPO_ROOT/install" "$case_dir/plugin/install"
  cp "$REPO_ROOT/triggers/pre-release-builder" "$case_dir/plugin/triggers/pre-release-builder"
  chmod +x "$case_dir/plugin/install"
  chmod +x "$case_dir/plugin/triggers/pre-release-builder"
  printf '%s\n' '#!/usr/bin/env bash' 'printf "dokku version 0.38.25\\n"' >"$case_dir/stubs/dokku"
  chmod +x "$case_dir/stubs/dokku"
  printf '%s\n' '#!/usr/bin/env bash' 'echo "make must not be called" >&2' 'exit 93' >"$case_dir/stubs/make"
  chmod +x "$case_dir/stubs/make"
}

write_go_stub() {
  local destination=$1
  cat >"$destination" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
if [[ ${1:-} == env && ${2:-} == GOVERSION ]]; then
  printf 'go%s\n' "$TEST_GO_VERSION"
  exit 0
fi
if [[ ${1:-} != build ]]; then
  exit 91
fi
output=
while (($#)); do
  if [[ $1 == -o ]]; then
    output=$2
    break
  fi
  shift
done
[[ -n $output ]]
mkdir -p "$(dirname "$output")"
cp "$TEST_AGENT_STUB" "$output"
STUB
  chmod +x "$destination"
}

assert_links() {
  local plugin_dir=$1
  local commands=(ca:clear ca:set configure disable enable help render report role-id:set stage template:add template:clear-custom template:list template:remove template:set-custom)
  local triggers=(post-app-clone-setup post-app-rename-setup post-deploy pre-delete)
  local name
  [[ -L "$plugin_dir/subcommands/default" ]] || { echo "missing default command link" >&2; return 1; }
  [[ $(readlink "$plugin_dir/subcommands/default") == ../bin/dokku-vault-agent ]] || return 1
  for name in "${commands[@]}"; do
    [[ -L "$plugin_dir/subcommands/$name" ]] || { echo "missing command link: $name" >&2; return 1; }
    [[ $(readlink "$plugin_dir/subcommands/$name") == ../bin/dokku-vault-agent ]] || return 1
  done
  for name in "${triggers[@]}"; do
    [[ -L "$plugin_dir/$name" ]] || { echo "missing trigger link: $name" >&2; return 1; }
    [[ $(readlink "$plugin_dir/$name") == bin/dokku-vault-agent ]] || return 1
  done
  [[ -L "$plugin_dir/pre-release-builder" ]] || { echo "missing pre-release-builder wrapper link" >&2; return 1; }
  [[ $(readlink "$plugin_dir/pre-release-builder") == triggers/pre-release-builder ]] || return 1
  [[ -x "$plugin_dir/pre-release-builder" ]] || { echo "pre-release-builder wrapper is not executable" >&2; return 1; }
}

assert_pre_release_failure() {
  local plugin_dir=$1
  local stderr_path="$TEST_ROOT/pre-release.stderr"
  local status=0

  "$plugin_dir/pre-release-builder" dockerfile sample sample-image >/dev/null 2>&1
  cp "$AGENT_FAIL_STUB" "$plugin_dir/bin/dokku-vault-agent"
  if "$plugin_dir/pre-release-builder" dockerfile sample sample-image 2>"$stderr_path"; then
    echo "pre-release-builder swallowed the Agent failure" >&2
    return 1
  else
    status=$?
  fi
  if [[ $status -ne 42 ]]; then
    echo "pre-release-builder returned $status, expected 42" >&2
    return 1
  fi
  if ! grep -Fq "Vault Agent pre-release validation failed; aborting deployment" "$stderr_path"; then
    echo "pre-release-builder omitted the fatal deployment message" >&2
    return 1
  fi
}

host_case="$TEST_ROOT/host-go"
prepare_case "$host_case"
write_go_stub "$host_case/stubs/go"
ln -s bin/dokku-vault-agent "$host_case/plugin/pre-release-builder"
env PATH="$host_case/stubs:/usr/bin:/bin" TEST_GO_VERSION=1.25.13 TEST_AGENT_STUB="$AGENT_STUB" DOKKU_BIN=dokku "$host_case/plugin/install" >/dev/null
assert_links "$host_case/plugin"
assert_pre_release_failure "$host_case/plugin"

docker_case="$TEST_ROOT/docker"
prepare_case "$docker_case"
write_go_stub "$docker_case/stubs/go"
cat >"$docker_case/stubs/docker" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
mkdir -p "$TEST_PLUGIN_DIR/bin"
cp "$TEST_AGENT_STUB" "$TEST_PLUGIN_DIR/bin/dokku-vault-agent"
STUB
chmod +x "$docker_case/stubs/docker"
env PATH="$docker_case/stubs:/usr/bin:/bin" TEST_GO_VERSION=1.24.0 TEST_AGENT_STUB="$AGENT_STUB" TEST_PLUGIN_DIR="$docker_case/plugin" DOKKU_BIN=dokku DOCKER_BIN=docker "$docker_case/plugin/install" >/dev/null
assert_links "$docker_case/plugin"

printf 'installer and hook tests passed\n'
