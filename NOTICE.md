# NOTICE

This repository ships two kinds of files governed by two different licenses.
Please respect both when redistributing.

## 1. Original code (MIT License)

Everything written for this repository is original work under the
[MIT License](./LICENSE):

- `hacatalog.go` — the embed, the accessors, the table types
- `script/extract.py` — the two-stage extractor
- `script/gen/` — the Go vocabulary generator
- `script/regenerate.sh`
- `hacatalog_test.go`
- the Makefile, workflows and repository configuration

## 2. Extracted vocabulary (Apache License 2.0)

The files under `data/`, and the constants generated from them into
`gen_vocab.go`, are derived from
[Home Assistant Core](https://github.com/home-assistant/core), which is
licensed under the [Apache License 2.0](https://www.apache.org/licenses/LICENSE-2.0).

Specifically:

| File | Derived from |
| --- | --- |
| `data/device_classes.json` | `homeassistant/generated/device_classes.json` |
| `data/sensor.json` | `homeassistant/generated/sensor.json` |
| `data/icons.json` | `homeassistant/components/*/icons.json` |
| `data/enums.json` | the `StrEnum` vocabularies across `homeassistant/components/*` and `homeassistant/const.py` |
| `data/features.json` | the `*EntityFeature` `IntFlag` declarations |
| `data/units.json` | `homeassistant/const.py` (`UnitOf*`) and `homeassistant/util/unit_conversion.py` |
| `data/relations.json` | `homeassistant/components/{sensor,number}/const.py` |
| `data/mqtt.json` | `homeassistant/components/mqtt/` (`abbreviations.py`, `schemas.py`, `const.py`, and each platform's `DISCOVERY_SCHEMA`) |

`gen_vocab.go` is a mechanical transcription of `data/enums.json` and the
platform list into Go identifiers, so it carries the same provenance.

The Apache License 2.0 permits this use, including redistribution, provided
the license and this attribution accompany the derived files — which is what
this file is for. The extraction takes names, enumerations and schema
structure; it copies no Home Assistant source code.

`data/snapshot.json`, and the `SnapshotVersion` / `SnapshotRef` constants,
record exactly which Home Assistant release a given catalog was derived from.

## Trademarks

"Home Assistant" is a trademark of the Open Home Foundation. This project is
not affiliated with or endorsed by the Open Home Foundation or the Home
Assistant project.
