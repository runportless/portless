#!/bin/sh
set -eu

if [ "$#" -ne 1 ]; then
  echo "usage: $0 VERSION" >&2
  exit 2
fi

version=$1
core=$version
prerelease=
has_prerelease=false

case "$version" in
  *+*)
    echo "release version must not contain SemVer build metadata: $version" >&2
    exit 2
    ;;
  *-*)
    core=${version%%-*}
    prerelease=${version#*-}
    has_prerelease=true
    ;;
esac

if ! printf '%s\n' "$core" | grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'; then
  echo "release version must be Semantic Versioning such as 1.2.3 or 1.2.3-alpha.1" >&2
  exit 2
fi

if [ "$has_prerelease" = true ]; then
  if [ -z "$prerelease" ] || ! printf '%s\n' "$prerelease" | grep -Eq '^[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*$'; then
    echo "release version has an invalid SemVer prerelease: $version" >&2
    exit 2
  fi

  saved_ifs=$IFS
  IFS=.
  # Split the validated prerelease into its dot-delimited identifiers.
  # shellcheck disable=SC2086
  set -- $prerelease
  IFS=$saved_ifs
  for identifier in "$@"; do
    if printf '%s\n' "$identifier" | grep -Eq '^[0-9]+$' && ! printf '%s\n' "$identifier" | grep -Eq '^(0|[1-9][0-9]*)$'; then
      echo "release version has a numeric prerelease identifier with a leading zero: $version" >&2
      exit 2
    fi
  done
fi
