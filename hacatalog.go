// SPDX-License-Identifier: MIT
// Copyright (C) 2026 go-ha-catalog authors.

// Package hacatalog embeds the Home Assistant MQTT-discovery vocabulary,
// extracted from a home-assistant/core checkout.
//
// It is a data artifact, not a lookup framework. The package hands out the
// tables as decoded Go values and the vocabularies as typed constants; it does
// not decide which device class suits a parameter, does not validate a payload
// and does not build a topic. That semantics belongs in the consumer — see
// go-hamqtt. Resist adding resolution logic here: six consumers pin this module
// and each needs a different policy on top of the same facts.
//
// The catalog tracks Home Assistant's monthly CalVer through [SnapshotVersion],
// while the module itself is tagged SemVer. The two are deliberately decoupled:
// an upstream CalVer can never be a Go major version.
//
// Every accessor decodes lazily and caches, so repeated calls are cheap and the
// init cost of an unused table is never paid. The returned values are shared —
// treat them as read-only.
package hacatalog

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"sync"
)

// SnapshotVersion is the Home Assistant release this catalog was generated
// from, in Home Assistant's own CalVer.
const SnapshotVersion = "2026.9.1"

// SnapshotRef is the exact core checkout the generator ran against, as
// `git describe --tags` reported it. It exists to diagnose drift: two catalogs
// can share a SnapshotVersion and still differ if one was cut from a beta.
const SnapshotRef = "2026.9.0b9-31-g76ca483aec0"

// The `all:` prefix is required. Without it `go:embed` silently skips files
// whose names begin with `_` or `.`, and the omission only surfaces at runtime
// as a missing table.
//
//go:embed all:data
var data embed.FS

// ReadFile returns the raw bytes of one catalog file, e.g. "mqtt.json".
// Callers that want a decoded table should use the typed accessors instead.
func ReadFile(name string) ([]byte, error) {
	return data.ReadFile("data/" + name)
}

// ReadDir lists the catalog files.
func ReadDir() ([]fs.DirEntry, error) {
	return data.ReadDir("data")
}

// FS exposes the embedded tree for callers that want to walk it themselves.
func FS() fs.FS { return data }

// ---------------------------------------------------------------------------
// table types
// ---------------------------------------------------------------------------

// SchemaKey describes one JSON key that Home Assistant's MQTT discovery accepts
// on a platform, as declared by that platform's DISCOVERY_SCHEMA.
type SchemaKey struct {
	// Required reports whether the key is vol.Required rather than vol.Optional.
	Required bool `json:"required"`
	// Default is the schema's default, or nil when the marker declares none.
	Default any `json:"default,omitempty"`
	// Allowed is the value whitelist when the validator is a vol.In(...);
	// nil means the schema does not constrain the value. Note that most HA
	// mode lists (hvac modes, fan modes) are deliberately unconstrained at the
	// schema level and only checked at entity setup.
	Allowed []string `json:"allowed,omitempty"`
	// Type names the JSON type the key's validator accepts: "bool", "int",
	// "float", "list" or "str". Empty when the validator says nothing useful.
	//
	// It exists because JSON cannot tell 1 from 1.0, and a consumer generating
	// typed structs from this catalog would otherwise have to guess whether
	// min_temp is an integer or a float.
	Type string `json:"type,omitempty"`
}

// PlatformSchema is the set of discovery keys one MQTT platform accepts.
// Exactly one of Keys or Variants is populated: `light` and `infrared` dispatch
// to sub-schemas instead of validating directly, and for those Discriminator
// names the payload key that selects the variant.
//
// The platform's *name* is the generated [Platform] type; this is its schema.
type PlatformSchema struct {
	Keys          map[string]SchemaKey `json:"keys,omitempty"`
	Discriminator string               `json:"discriminator,omitempty"`
	Variants      map[string]Variant   `json:"variants,omitempty"`
}

// Variant is one sub-schema of a dispatching platform.
type Variant struct {
	Keys map[string]SchemaKey `json:"keys"`
}

// MQTT is the extract of Home Assistant's MQTT integration: the discovery
// schemas, the abbreviation tables, and the device-bundle schema.
type MQTT struct {
	// Abbreviations maps the short discovery key to its long form
	// ("dev_cla" -> "device_class"). Home Assistant accepts either.
	Abbreviations map[string]string `json:"abbreviations"`
	// DeviceAbbreviations applies inside the `device` block only.
	DeviceAbbreviations map[string]string `json:"device_abbreviations"`
	// OriginAbbreviations applies inside the `origin` block only.
	OriginAbbreviations map[string]string `json:"origin_abbreviations"`

	// SupportedComponents is the whitelist for a device bundle's
	// components[*].platform.
	SupportedComponents []string `json:"supported_components"`

	Platforms map[string]PlatformSchema `json:"platforms"`

	// DeviceDiscoverySchema is the device-based bundle published at
	// <prefix>/device/<node_id>/config.
	DeviceDiscoverySchema map[string]SchemaKey `json:"device_discovery_schema"`
	// EntityCommonSchema holds the keys every entity accepts.
	EntityCommonSchema map[string]SchemaKey `json:"entity_common_schema"`
	// DeviceInfoSchema and OriginInfoSchema are the two nested blocks.
	DeviceInfoSchema map[string]SchemaKey `json:"device_info_schema"`
	OriginInfoSchema map[string]SchemaKey `json:"origin_info_schema"`

	// FeatureStrings covers the two platforms that carry feature names as
	// strings in the payload rather than inferring them from topic presence.
	FeatureStrings map[string][]string `json:"feature_strings"`
}

// Relations holds the cross-field tables: which state classes a device class
// admits, the default display precision per device class, and the unit
// normalisation Home Assistant applies behind the caller's back.
type Relations struct {
	// SensorDeviceClassStateClasses maps a sensor device class to the state
	// classes it admits. An empty slice means the device class admits none.
	SensorDeviceClassStateClasses map[string][]string `json:"sensor_device_class_state_classes"`
	// SensorUnitsPrecision is the default display precision per device class.
	SensorUnitsPrecision map[string]UnitPrecision `json:"sensor_units_precision"`
	// SensorDefaultPrecisionLimit is the fallback when no device class matches.
	SensorDefaultPrecisionLimit *int `json:"sensor_default_precision_limit"`
	// AmbiguousUnits maps a unit spelling Home Assistant silently rewrites to
	// the canonical form it rewrites it to — most importantly the legacy micro
	// sign U+00B5 to U+03BC.
	//
	// Emit the canonical form: it is what Home Assistant stores, so the two
	// planes agree without a translation step. But the rewrite is a
	// `.get(unit, unit)` in sensor/__init__.py's
	// _native_unit_of_measurement_compat, which means the legacy spelling is
	// accepted, not rejected — so a mismatch is an advisory, never a reason to
	// withhold the entity.
	AmbiguousUnits map[string]string `json:"ambiguous_units"`
	// NumberDeviceClassUnits is the number platform's device class to unit map.
	NumberDeviceClassUnits map[string][]string `json:"number_device_class_units"`
}

// UnitPrecision is a device class's canonical unit and its default display
// precision.
type UnitPrecision struct {
	Unit      string `json:"unit"`
	Precision int    `json:"precision"`
}

// Units holds the UnitOf* vocabularies and the converter unit sets.
type Units struct {
	// Enums maps the Python enum name ("UnitOfPower") to its member values.
	Enums map[string][]string `json:"enums"`
	// Converters maps a unit class ("power") to every unit convertible within it.
	Converters map[string][]string `json:"converters"`
}

// Sensor is Home Assistant's own generated sensor table.
type Sensor struct {
	DeviceClassUnits     map[string][]string `json:"device_class_units"`
	NumericDeviceClasses []string            `json:"numeric_device_classes"`
	StateClasses         []string            `json:"state_classes"`
	StateClassUnits      map[string][]string `json:"state_class_units"`
	ConvertibleUnits     map[string][]string `json:"convertible_units"`
}

// Icon is Home Assistant's icon declaration for one device class: a default
// MDI name, plus optional per-state and per-range overrides.
type Icon struct {
	Default string            `json:"default"`
	State   map[string]string `json:"state,omitempty"`
	Range   map[string]string `json:"range,omitempty"`
}

// ---------------------------------------------------------------------------
// accessors
// ---------------------------------------------------------------------------

type table[T any] struct {
	once  sync.Once
	value T
	err   error
}

func (t *table[T]) get(name string) (T, error) {
	t.once.Do(func() {
		raw, err := ReadFile(name)
		if err != nil {
			t.err = fmt.Errorf("hacatalog: read %s: %w", name, err)
			return
		}
		if err := json.Unmarshal(raw, &t.value); err != nil {
			t.err = fmt.Errorf("hacatalog: decode %s: %w", name, err)
		}
	})
	return t.value, t.err
}

var (
	mqttTable      table[MQTT]
	relationsTable table[Relations]
	unitsTable     table[Units]
	sensorTable    table[Sensor]
	// deviceClasses is HA's generated per-domain device-class list.
	deviceClassTable table[map[string][]string]
	// icons is keyed domain -> device_class (or "_" for the domain default).
	iconTable table[map[string]map[string]Icon]
	// features is keyed platform -> feature name -> bit value.
	featureTable table[map[string]map[string]int]
	// enums is keyed vocabulary name -> member values.
	enumTable table[map[string][]string]
)

// LoadMQTT returns the MQTT discovery extract.
func LoadMQTT() (MQTT, error) { return mqttTable.get("mqtt.json") }

// LoadRelations returns the cross-field tables.
func LoadRelations() (Relations, error) { return relationsTable.get("relations.json") }

// LoadUnits returns the unit vocabularies and converter sets.
func LoadUnits() (Units, error) { return unitsTable.get("units.json") }

// LoadSensor returns Home Assistant's own generated sensor table.
func LoadSensor() (Sensor, error) { return sensorTable.get("sensor.json") }

// LoadDeviceClasses returns the per-domain device-class lists, straight from
// Home Assistant's generated/device_classes.json — the table hassfest keeps
// current in HA's own CI.
func LoadDeviceClasses() (map[string][]string, error) {
	return deviceClassTable.get("device_classes.json")
}

// LoadIcons returns the icon declarations, keyed domain then device class.
// The key "_" is the domain's default icon.
func LoadIcons() (map[string]map[string]Icon, error) { return iconTable.get("icons.json") }

// LoadFeatures returns the entity feature bitmasks, keyed platform then feature
// name. MQTT does not transmit these — it infers supported features from topic
// presence — but a consumer projecting a device model onto MQTT still needs to
// know which capabilities exist.
func LoadFeatures() (map[string]map[string]int, error) { return featureTable.get("features.json") }

// LoadEnums returns the raw vocabulary lists. Prefer the generated typed
// constants; this is the escape hatch for iterating a vocabulary at runtime.
func LoadEnums() (map[string][]string, error) { return enumTable.get("enums.json") }
