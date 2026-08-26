#!/bin/sh
set -eu

if [ "$#" -ne 4 ]; then
  echo "usage: $0 VERSION SOURCE_SHA256 COMMIT OUTPUT" >&2
  exit 2
fi

version=$1
source_sha256=$2
commit=$3
output=$4

script_directory=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
"$script_directory/validate-release-version.sh" "$version"

if ! printf '%s\n' "$source_sha256" | grep -Eq '^[[:xdigit:]]{64}$'; then
  echo "source archive checksum must be a 64-character SHA-256 value" >&2
  exit 2
fi
if ! printf '%s\n' "$commit" | grep -Eq '^[[:xdigit:]]{40}$'; then
  echo "commit must be a 40-character hexadecimal revision" >&2
  exit 2
fi

repository_root=$(CDPATH= cd -- "$script_directory/.." && pwd)
template=$repository_root/packaging/homebrew/portless.rb.tmpl

mkdir -p "$(dirname -- "$output")"
sed \
  -e "s/@VERSION@/$version/g" \
  -e "s/@SHA256@/$source_sha256/g" \
  -e "s/@COMMIT@/$commit/g" \
  "$template" >"$output"
