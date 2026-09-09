# go-ha-catalog

The Home Assistant MQTT-discovery vocabulary as a versioned Go artifact:
device classes, state classes, units, entity features, icons, the
abbreviation tables, and — the point of the exercise — the **per-platform
discovery schema**, which is the authoritative list of which JSON keys Home
Assistant accepts on which platform.

It is a **data artifact, not a lookup framework**. It hands out facts as typed
constants and decoded tables; it does not decide which device class suits a
parameter, does not validate a payload and does not build a topic. That
semantics belongs in the consumer — see
[go-hamqtt](https://github.com/SukramJ/go-hamqtt).

Zero dependencies. The catalog is generated from a `home-assistant/core`
checkout and regenerated automatically on every stable Home Assistant release.

## Usage

```go
import hacatalog "github.com/SukramJ/go-ha-catalog"

// Typed vocabularies — the compiler catches the typo.
dc := hacatalog.SensorDeviceClassEnergy
sc := hacatalog.StateClassTotalIncreasing

// Tables — decoded lazily, cached, shared (treat as read-only).
mqtt, err := hacatalog.LoadMQTT()
keys := mqtt.Platforms["sensor"].Keys        // every legal key on `sensor`
_, legal := keys["state_class"]

rel, err := hacatalog.LoadRelations()
rel.SensorDeviceClassStateClasses["energy"]  // ["total", "total_increasing"]
rel.AmbiguousUnits                           // the µ → μ rewrites HA applies silently
```

`light` and `infrared` do not validate directly — they dispatch to sub-schemas
on a discriminator key:

```go
light := mqtt.Platforms["light"]
light.Discriminator          // "schema"
light.Variants["basic"].Keys // the 74 keys of the basic schema
```

## What is generated, and what is not

**Generated as typed Go constants** (`gen_vocab.go`): the closed vocabularies a
consumer spells out in code — `Platform`, `SensorDeviceClass`, `StateClass`,
`EntityCategory`, `HVACMode`, `ColorMode` and 22 more.

**Kept as data**: the relational tables (device class → units, discovery
schemas, icons). They are looked up rather than written, and identifiers would
buy nothing.

**Deliberately not generated**: units. Their values are symbols like `°C`,
`µg/m³` and `W/m²`, which make poor Go identifiers — and a consumer that
hard-codes a unit spelling is exactly the bug `AmbiguousUnits` exists to catch.

## Versioning

Two decoupled schemes, as in
[go-openccu-data](https://github.com/SukramJ/go-openccu-data):

- Module tags are **SemVer**. Additive catalog changes bump the minor; a
  removed constant is a compile error for consumers, so it bumps the major.
- `SnapshotVersion` is Home Assistant's **CalVer** (`2026.9.1`) — an upstream
  CalVer can never be a Go major version. `SnapshotRef` records the exact
  checkout, so a catalog cut from a beta is distinguishable from one cut from
  the matching release.

## Regeneration

Automatic: a scheduled workflow polls the `home-assistant/core` releases API
daily, regenerates on a new stable tag, and **tags and releases only when the
catalog actually changed** — Home Assistant ships many patch releases that
never touch the vocabulary.

Manually, against a local checkout:

```sh
make regenerate HA_CORE=../core   # extract + codegen + stamp + test
make generate                     # only re-run the Go codegen, no venv needed
make snapshot-version
```

`make regenerate` builds a venv from Home Assistant's own `requirements.txt`;
the first run is slow.

## Licensing

Code is MIT. The extracted vocabulary under `data/` (and its transcription in
`gen_vocab.go`) is derived from Home Assistant Core and carries the Apache
License 2.0 — see [`NOTICE.md`](./NOTICE.md).
