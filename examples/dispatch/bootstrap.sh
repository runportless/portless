#!/usr/bin/env bash
set -euo pipefail

example_root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
workspace="${DISPATCH_WORKSPACE:-${example_root}/workspace}"
install_dependencies=false

usage() {
  cat <<'EOF'
Usage: ./bootstrap.sh [--workspace PATH] [--install]

Materialize console, operations, and maps as independent Git checkouts.
The destination is never overwritten. --install runs each checkout's locked
dependency installation after creating it.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --workspace)
      [[ $# -ge 2 ]] || { echo "bootstrap: --workspace requires a path" >&2; exit 2; }
      workspace="$2"
      shift 2
      ;;
    --install)
      install_dependencies=true
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "bootstrap: unknown argument $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

[[ -n "$workspace" && "$workspace" != "/" ]] || { echo "bootstrap: unsafe workspace path" >&2; exit 2; }
if [[ -e "$workspace" ]]; then
  echo "bootstrap: destination already exists: $workspace" >&2
  echo "Choose another --workspace path or keep using the existing workspace." >&2
  exit 1
fi

mkdir -p "$workspace/checkouts" "$workspace/worktrees"
for source in console operations maps; do
  destination="$workspace/checkouts/$source"
  mkdir -p "$destination"
  tar -C "$example_root/templates/$source" \
    --exclude='.next' --exclude='node_modules' --exclude='.venv' \
    --exclude='__pycache__' --exclude='.pytest_cache' \
    -cf - . | tar -C "$destination" -xf -
  git -C "$destination" init -q -b main
  git -C "$destination" add .
  git -C "$destination" -c user.name="Portless Example" -c user.email="example@portless.local" commit -qm "Initial dispatch $source checkout"
done
printf 'dispatch example workspace\n' > "$workspace/.dispatch-example"

if [[ "$install_dependencies" == true ]]; then
  command -v npm >/dev/null || { echo "bootstrap: npm is required for --install" >&2; exit 1; }
  command -v uv >/dev/null || { echo "bootstrap: uv is required for --install" >&2; exit 1; }
  npm --prefix "$workspace/checkouts/console" ci
  npm --prefix "$workspace/checkouts/operations" ci
  uv sync --project "$workspace/checkouts/operations/api" --frozen
fi

console="$(cd "$workspace/checkouts/console" && pwd -P)"
operations="$(cd "$workspace/checkouts/operations" && pwd -P)"
maps="$(cd "$workspace/checkouts/maps" && pwd -P)"

cat <<EOF
Dispatch workspace created at $workspace

Create the logical project with:

  portless project create dispatch \\
    --source console=$console \\
    --source operations=$operations \\
    --source maps=$maps

Then start it with:

  portless --env dispatch/local up
EOF
