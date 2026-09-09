# CLAUDE.md

Guidance for Claude Code (claude.ai/code) when working in this repository.

## Commands

```sh
make check                        # vet + fmt-check + lint + race tests — the gate
make test                         # race tests only
make regenerate HA_CORE=../core   # full regeneration: extract + codegen + stamp + test
make generate                     # only re-run the Go codegen from committed data/
make snapshot-version             # the embedded HA CalVer
go test -run TestName ./...       # a single test
```

No external Go dependencies; `go.mod` has no `require` block, and it stays that
way. The extractor's Python venv (`.venv/`) is gitignored and built on demand.

## What this repository is

A **data artifact, not a lookup framework** — the constraint is deliberate and
should be preserved. It ships the Home Assistant MQTT-discovery vocabulary as
an embedded Go module: typed constants for the closed vocabularies, decoded
tables for everything relational. It does **not** decide which device class
suits a parameter, validate a payload, or build a topic. That belongs in
`go-hamqtt`. Resist adding resolution logic here — six consumers pin this
module and each needs a different policy on top of the same facts.

Recorded as ADR 0070 in openccu-loom.

## Architecture

- `hacatalog.go` — the whole hand-written API: `//go:embed all:data`, the
  `SnapshotVersion`/`SnapshotRef` constants, `ReadFile`/`ReadDir`/`FS`, the
  table types, and the lazily-decoding `Load*` accessors. The `all:` prefix on
  the embed is required, not stylistic.
- `gen_vocab.go` — **generated, never hand-edited.** 28 string types with
  constants, a `String()` and a `Valid()`.
- `data/` — **generated, never hand-edited.**
- `script/extract.py` — the two-stage extractor. Stage 0 reads the JSON Home
  Assistant itself generates (hassfest keeps it current in HA's CI); Stage 1
  imports `homeassistant` and introspects it.
- `script/gen/` — turns `data/enums.json` plus the platform list into Go.
- `script/regenerate.sh` — owns the venv, runs both, stamps the constants.

## Two traps in the extractor, both silent

1. **probatio, not voluptuous.** HA 2026.x aliases `voluptuous` in
   `sys.modules` from `homeassistant/__init__.py`. `import homeassistant` must
   come first, or every `isinstance` check fails and every platform reports
   zero keys with no error. That is why `extract.py` opens with a bare
   `import homeassistant` and a `ruff: noqa: E402`.
2. **`_DEPRECATED_*` unit constants.** `PERCENTAGE` is now
   `UnitOfRatio.PERCENTAGE.value`; `ppm`/`ppb` moved into `UnitOfRatio`. The
   old names survive as placeholder objects. Walking module globals without
   skipping them bakes dead constants into the catalog.

A third is generated *around* rather than worked around: `AMBIGUOUS_UNITS`
rewrites the legacy micro sign U+00B5 to U+03BC. Home Assistant discards a
whole discovery config over the wrong one, with no error anywhere.

## Meta-schemas

`light` and `infrared` carry a `DISCOVERY_SCHEMA` that only validates a
discriminator before dispatching to sub-schemas. Reading it instead of the
sub-schemas yields one key and looks plausible — so `_platform_variants()` is
checked **before** `DISCOVERY_SCHEMA`, and `TestEveryPlatformHasASchema` fails
any platform with fewer than four keys.

## What cannot be generated

These are Python control flow, not data, and belong in `go-hamqtt` as
hand-ported Go with a source reference comment:

- the cross-field `validate_*` functions in each platform's `vol.All(...)`
  (sensor's `options`/`state_class`/`unit_of_measurement` exclusions, and the
  equivalents in climate, cover, fan, humidifier, text, valve, vacuum,
  device_tracker)
- the `~` topic-base rule: substituted only in values whose key ends in
  `"topic"`
- the loose `PRESET_*` / `FAN_*` / `SWING_*` constants, which HA does not
  treat as exhaustive

## Versioning and release

Module tags are SemVer; `SnapshotVersion` is HA's CalVer. Additive catalog
changes bump the minor, a removed constant bumps the major — a removal is a
compile error downstream, and consumers' `dependabot-auto-merge` only
auto-merges non-major bumps, which is where the review gate belongs.

`.github/workflows/regenerate-on-ha-release.yml` polls the
`home-assistant/core` releases API daily, regenerates on a new stable tag, and
commits, tags and releases **only when the catalog actually changed**. Betas
are skipped.

## Conventions

- Every `.go` file starts with:
  ```go
  // SPDX-License-Identifier: MIT
  // Copyright (C) 2026 go-ha-catalog authors.
  ```
- gofumpt; `golangci-lint` v2 with the shared config copied from go-mqtt.
- Tests are table-free, stdlib-only, whitebox, and each pins *a fact*, not an
  implementation. Prefer a test that fails loudly on a stale generation over
  one that restates what the code does.
- Licensing is split: code MIT, extracted data Apache-2.0 from Home Assistant.
  See `NOTICE.md` before moving or redistributing anything under `data/`.
