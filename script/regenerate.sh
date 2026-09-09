#!/usr/bin/env bash
# Regenerate the embedded catalog from a home-assistant/core checkout.
#
# Usage: script/regenerate.sh <path-to-core-checkout>
#
# Builds (or reuses) a venv holding Home Assistant's own runtime
# requirements, runs the two extraction stages, regenerates the typed Go
# vocabularies, and stamps SnapshotVersion / SnapshotRef in hacatalog.go.
#
# Home Assistant is imported from the checkout rather than installed from PyPI:
# the checkout is what `git describe` can identify, and SnapshotRef exists
# precisely so a catalog cut from a beta is distinguishable from one cut from
# the matching release.
set -euo pipefail

CORE="${1:?usage: script/regenerate.sh <path-to-home-assistant/core checkout>}"
CORE="$(cd "$CORE" && pwd)"
[ -f "$CORE/homeassistant/const.py" ] || {
	echo "not a home-assistant/core checkout: $CORE" >&2
	exit 1
}

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
VENV="${HA_CATALOG_VENV:-$ROOT/.venv}"
PYTHON="${PYTHON:-python3}"

# paho-mqtt is not in core's requirements.txt — it is the mqtt integration's own
# manifest dependency, and importing the platform modules needs it.
PAHO_VERSION="$(sed -n 's/.*"paho-mqtt==\([0-9.]*\)".*/\1/p' \
	"$CORE/homeassistant/components/mqtt/manifest.json" | head -1)"
PAHO_VERSION="${PAHO_VERSION:-2.1.0}"

if [ ! -x "$VENV/bin/python" ]; then
	echo "creating venv at $VENV"
	"$PYTHON" -m venv "$VENV"
fi

echo "installing Home Assistant runtime requirements (this is slow the first time)"
"$VENV/bin/pip" install --quiet --upgrade pip
"$VENV/bin/pip" install --quiet -r "$CORE/requirements.txt"
"$VENV/bin/pip" install --quiet "paho-mqtt==$PAHO_VERSION"

echo "extracting"
PYTHONPATH="$CORE" "$VENV/bin/python" "$ROOT/script/extract.py" \
	--core "$CORE" --out "$ROOT/data"

echo "generating typed vocabularies"
(cd "$ROOT" && go run ./script/gen -data data -out gen_vocab.go)

# Stamp the two constants from what the extractor recorded, so hacatalog.go can
# never disagree with data/snapshot.json.
VERSION="$(sed -n 's/.*"version": "\(.*\)".*/\1/p' "$ROOT/data/snapshot.json")"
REF="$(sed -n 's/.*"ref": "\(.*\)".*/\1/p' "$ROOT/data/snapshot.json")"
[ -n "$VERSION" ] || {
	echo "could not read version from data/snapshot.json" >&2
	exit 1
}
[ -n "$REF" ] || REF="$VERSION"

sed -i.bak "s/^const SnapshotVersion = \".*\"$/const SnapshotVersion = \"$VERSION\"/" "$ROOT/hacatalog.go"
sed -i.bak "s/^const SnapshotRef = \".*\"$/const SnapshotRef = \"$REF\"/" "$ROOT/hacatalog.go"
rm -f "$ROOT/hacatalog.go.bak"

(cd "$ROOT" && gofmt -w hacatalog.go gen_vocab.go)

echo "catalog regenerated from Home Assistant $VERSION ($REF)"
