#!/bin/sh
set -eu

if [ "$#" -ne 3 ]; then
  echo "usage: $0 VERSION SOURCE_SHA256 OUTPUT" >&2
  exit 2
fi

version=$1
source_sha256=$2
output=$3

script_directory=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
"$script_directory/validate-release-version.sh" "$version"

if ! printf '%s\n' "$source_sha256" | grep -Eq '^[[:xdigit:]]{64}$'; then
  echo "source archive checksum must be a 64-character SHA-256 value" >&2
  exit 2
fi

repository_root=$(CDPATH= cd -- "$script_directory/.." && pwd)
template=$repository_root/packaging/homebrew/portless.rb.tmpl

mkdir -p "$(dirname -- "$output")"
sed \
  -e "s/@VERSION@/$version/g" \
  -e "s/@SHA256@/$source_sha256/g" \
  "$template" >"$output"
