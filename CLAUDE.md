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
opens an auto-merge pull request from a `regen/ha-<tag>` branch. Betas are
skipped. Every regeneration gets a PR, vocabulary-neutral ones included —
without the committed stamp the poll would re-extract the same release forever.

Tagging is a second workflow, `tag-on-regen-merge.yml`, firing when a `regen/`
branch lands on main. It recomputes hadiff's verdict from the diff that
actually merged rather than trusting the one the regeneration run computed, so
a release happens **only when the catalog actually changed** while the stamp
merges either way.

Both need `RELEASE_PAT` with `contents: write` and `pull-requests: write` on
this repository. `GITHUB_TOKEN` cannot do either half: a branch it pushes and
a PR it opens trigger neither `ci.yml` nor `codeql.yml`, so the five required
checks never report and auto-merge waits forever; and a tag it pushes does not
trigger `release-on-tag.yml`. A token without write access fails at the branch
push, not at checkout — this repo is public, so the clone succeeds either way.

## Which model does which work

Set by the owner for the go-fabric family on 2026-10-10 (go-fabric's
`CLAUDE.md`); the review split of 2026-11-08 (go-fabric #456) likewise.
Adopted here on 2026-11-08. A statement about Home Assistant's MQTT discovery vocabulary that
lands in this repo is cited later as fact — a comment that names a caller
or a consumer, a docstring that states what the other side does, a
release note — so the work that produces such statements gets
the strongest model, and the work a guard checks may use a cheaper one. The
agents in [Sub-Agent Delegation](#sub-agent-delegation) carry a default
model each; the rows below decide when a call overrides it (`model:` on the
Agent tool). Implementation defaults to Opus.

| Work | Model |
| --- | --- |
| Analysis, planning, reviews of design, audits; anything written into `NOTICE.md`, `README.md` or the settled sections of `CLAUDE.md`, or a comment that names a Home Assistant core schema, platform or unit; the extractor (`script/extract.py`, `script/gen/`) whose output every consumer compiles against, the release workflows with `RELEASE_PAT`; *which* guard gets built and its bite line; rebases and merge conflicts where `main` may have overtaken the change | **Fable** (the default for this repo; also the `hunt` agent) |
| Implementation of a finished plan with guards behind it: a meta-schema rule once the pattern exists, a test on the generated vocabulary, the release note for a finished diff | **Opus** (the `impl` default; also what fast mode runs on) |
| **Review of a slice's pull request, split (owner decision 2026-11-08, go-fabric #456).** The code review — correctness, concurrency, error paths, tests, the fixed report shape — is **Opus**. On top of it **Fable** reads, bounded: every new comment or docstring that names a Home Assistant core schema, platform or unit against Home Assistant core at `SnapshotVersion`, every line that enters `NOTICE.md`, `README.md` or the settled sections of `CLAUDE.md`, the paths of the first row, and the brief's decisions against the reference — the behavioural gaps a code review does not see. The load-bearing claims of the Opus review are verified before they reach the implementer. | **Opus**, then **Fable** |
| Work a test or generator checks immediately: `make regenerate HA_CORE=../core`, `make generate`, `gofumpt`; search and first reading of a tree whose result a stronger model or the owner reads again | **Sonnet** (the `guard` default; `impl` with `model: "sonnet"`) |
| Mechanical inventories whose answer is a list the caller can re-grep: which files reference X, which links are dead, which entries lack a counterpart | **Haiku** (`sweep` default) |

Sonnet never writes a claim about a Home Assistant core schema, platform or unit into the repo, and
never touches the paths named in the first row. Haiku returns lists, never
verdicts. When in doubt about which row a task is in, it is in the first.

---

## Sub-Agent Delegation

As go-fabric's and openccu-loom's rule (owner, 2026-10-10). The main
conversation owns planning, contract and wire decisions, guard
specifications, sub-agent steering, and final verification. It does not own
typing.

**Delegate what a gate can prove; keep what only a reader can accept.**
Delegate freely: table cases, fakes, scaffolding, fixture updates, doc and
link cleanups, version bumps across the changelog set, inventories and grep
sweeps. Never delegate: the extractor's rules (probatio vs voluptuous, the deprecated units), the workflows, the licence boundary (`NOTICE.md`), and *which* guard gets
built. Locating is delegable; reading is not.

| Agent | Default model | Use for |
|---|---|---|
| `impl` | Opus | scoped implementation with a stated acceptance command |
| `guard` | Sonnet | tests from a caller-written guard spec, plus the bite proof |
| `sweep` | Haiku | read-only inventories and grep sweeps, high fan-out |
| `hunt` | Fable | adversarial read-only defect hunt, ranked candidates |

Each default is one row of
[Which model does which work](#which-model-does-which-work); a call that
falls into another row passes `model:` on the Agent tool — `impl` with
`model: "sonnet"` for work a generator or formatter checks immediately.

**Size CPU-bound fan-out from the host, never from a constant** — the owner
works from a 4-core box and a 14-core box:

```sh
cores=$(nproc); agents=$(( (cores - 1) / share ))   # share = cores per agent
```

Check the 1-minute load first; if it is at or above the core count, run one
CPU-bound agent and queue the rest. Eight concurrent agents is a practical
ceiling whatever the core count. Every CPU-bound agent must **pin** its share
in the command it runs (`GOMAXPROCS=<share> go test -p <share> ./internal/x/...`, `golangci-lint run --concurrency=<share>`) — handing out a number without
pinning it changes nothing. Read-only agents cost no core worth counting;
6–8 is a practical ceiling there.

Two hard rules: **no sub-agent runs the full gate (`make check`) or a
repo-wide lint** — the full gate runs once, in the main conversation, at the
end of the slice — and **no two writing agents in the same package**
(disjoint file sets, or `isolation: "worktree"`).

Every brief carries five things: the files and public surface plus one style
reference; the acceptance command the agent runs itself; the invariants it
touches; **stop conditions** (new dependency, API or wire change, credential
handling, an ADR-shaped decision — a stop is a successful outcome); and a
report format ≤ 250 words, large artefacts to the scratchpad as a path.

**A report is a claim.** Read the diff, run the gate yourself. For `guard`,
the acceptance artefact is not a green test but the **bite proof**: the named
production line removed, the test observed red with its message, the line
restored, green again.

---

## Token use

Set by the owner on 2026-10-14 ("Wichtig ist mir eine effiziente Nutzung
der Token"), as go-fabric's rule of the same date. A slice costs on
the order of half a million to a million tokens per implementing agent
before the owner's review; the rules below are what keeps that from
doubling.

- **Read slices, not files.** `sed -n a,bp`, `grep -n` with a tight
  pattern and `head`; a whole file only when the task is the file. Never
  re-read what is already in context; cite the earlier read.
- **Delegate first reads.** A source tree's inventory (Home Assistant core, go-hamqtt, this repo)
  is a `sweep` or Sonnet read written to the scratchpad with `file:line`
  for every fact; the owner spot-checks the load-bearing lines and verifies
  every fact about Home Assistant's MQTT discovery vocabulary before it enters a brief or
  `NOTICE.md`, `README.md` or the settled sections of `CLAUDE.md` — the agent's result is a map, never a citation.
- **Logs by their summary.** The gate (`make check`) runs in the
  background with a summary file (one line per gate, `FAIL`/`ok` lines
  only); read the summary, and open a log only on failure, with `grep` for
  the failure markers and the error strings — never `cat`. The same for CI:
  the job log is fetched once to a file and grepped.
- **One turn, every independent call.** Reads, checks and background
  starts that do not depend on each other go out together; a wait is a
  `Monitor` or an `until` loop in the background, not polling by hand.
- **Short artefacts.** A brief names files, decisions and the acceptance
  command, not prose; a report follows the fixed shape of
  [Sub-Agent Delegation](#sub-agent-delegation) (≤ 250 words, large
  artefacts as a scratchpad path); status messages to the owner are short,
  outcome first, in German.
- **Skip what cannot have changed.** No regeneration for a comment change;
  no second gate run for a notes-only commit — say what was skipped and
  why.
- **Edit by script.** Several replacements in several files are one
  scripted call with asserted anchors, not a chain of interactive edits;
  a generated file (`gen_vocab.go`, `data/`) is regenerated, never patched.
- **Agents read what the brief names.** An implementing agent gets the
  file list and the pinned sources in the brief, reads those, and reports
  in the fixed shape; it does not survey the tree. One agent per brief;
  two agents only on disjoint paths, at most one Opus agent at a time.
- **Push, pull request and tag are the session's own calls.** An
  implementing agent works locally — branch, commits, guards — and
  reports; the owner's session reads the diff, runs the gate, pushes,
  opens the pull request and tags the merge commit; the owner merges. A
  delegated push, pull request or tag is refused by the auto-mode
  classifier ("Create Public Surface", 2026-11-07), and the review in
  [Which model does which work](#which-model-does-which-work) sits
  between the agent's report and the push anyway.

---

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
