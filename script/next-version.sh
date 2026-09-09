#!/usr/bin/env bash
# Print the next module tag for a catalog regeneration.
#
# Usage: script/next-version.sh <bump>      bump ∈ {major, minor, none}
#
# `bump` is hadiff's verdict, not a hand-picked level: "major" means a value
# that script/gen turned into a Go constant disappeared, which is a compile
# error in every consumer.
#
# The module stays in v0.x deliberately. Go cannot express a SemVer major
# without changing the import path (v2 requires a `/v2` module-path suffix), so
# an automatic major bump would rewrite every consumer's imports over a single
# removed device class. In v0.x, SemVer already promises no compatibility, and
# consumers pin exact versions per ADR 0050's fan-out rule — so the mapping is:
#
#   hadiff "major" (a removal)   -> minor bump   v0.2.3 -> v0.3.0
#   hadiff "minor" (additive)    -> patch bump   v0.2.3 -> v0.2.4
#
# The removal is not hidden by this: the release title says so, and hadiff's
# report is the release body.
set -euo pipefail

BUMP="${1:?usage: script/next-version.sh <major|minor|none>}"

LATEST="$(git tag --list 'v0.*' --sort=-v:refname | head -1)"
if [ -z "$LATEST" ]; then
	# First release of a brand-new catalog.
	echo "v0.1.0"
	exit 0
fi

VERSION="${LATEST#v}"
IFS='.' read -r MAJOR MINOR PATCH <<<"$VERSION"

case "$MAJOR" in
'' | *[!0-9]*)
	echo "cannot parse latest tag: $LATEST" >&2
	exit 1
	;;
esac

if [ "$MAJOR" != "0" ]; then
	echo "latest tag $LATEST is past v0.x — this script's v0 mapping no longer applies" >&2
	exit 1
fi

case "$BUMP" in
major) echo "v0.$((MINOR + 1)).0" ;;
minor) echo "v0.${MINOR}.$((PATCH + 1))" ;;
none)
	echo "nothing to release" >&2
	exit 1
	;;
*)
	echo "unknown bump: $BUMP" >&2
	exit 1
	;;
esac
