#!/usr/bin/env bash
# check-version.sh — fail the release CI gate if the supplied tag is
# inconsistent with internal/version/VERSION. Two layered assertions:
#
#   1. Tag follows the canonical MAJOR.MINOR.PATCH shape (a leading
#      "v" is accepted but not required).
#   2. Tag matches `cat internal/version/VERSION` exactly after
#      stripping a leading "v" from each side. The VERSION file is
#      the source of truth for the binary's version (embedded via
#      //go:embed); a release whose tag drifts from VERSION ships
#      a binary that misreports its own version.
#
# Usage: scripts/check-version.sh v0.1.0   # or 0.1.0
set -euo pipefail

if [ "$#" -ne 1 ]; then
  echo "usage: $0 <release-tag>" >&2
  exit 2
fi
TAG="$1"
VERSION_FILE="internal/version/VERSION"

# 1. Shape.
if ! [[ "$TAG" =~ ^v?[0-9]+\.[0-9]+\.[0-9]+([-+].*)?$ ]]; then
  echo "check-version: tag $TAG does not match the canonical MAJOR.MINOR.PATCH format" >&2
  echo "               expected e.g. v0.1.0, 0.1.0, v0.1.0-rc1, 1.2.3+build.4" >&2
  exit 1
fi

# 2. Tag vs VERSION file. Compare with leading "v" stripped from
# each side so that 0.1.0 and v0.1.0 are treated as equivalent.
if [ ! -f "$VERSION_FILE" ]; then
  echo "check-version: $VERSION_FILE missing — cannot verify tag" >&2
  exit 1
fi
FILE_TAG=$(tr -d '[:space:]' < "$VERSION_FILE")
if [ "${TAG#v}" != "${FILE_TAG#v}" ]; then
  echo "check-version: release tag $TAG does not match $VERSION_FILE ($FILE_TAG)" >&2
  echo "               update $VERSION_FILE before tagging." >&2
  exit 1
fi

echo "check-version: ok — tag $TAG matches $VERSION_FILE"
