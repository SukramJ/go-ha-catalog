// SPDX-License-Identifier: MIT
// Copyright (C) 2026 go-ha-catalog authors.

package hacatalog

import (
	"encoding/json"
	"slices"
	"testing"
)

// TestSnapshotCarriesEveryTable pins the artifact set. A missing file means the
// extractor silently skipped a stage — the failure mode this catalog exists to
// prevent, since a consumer would then validate against an empty table and
// accept anything.
func TestSnapshotCarriesEveryTable(t *testing.T) {
	for _, name := range []string{
		"device_classes.json",
		"sensor.json",
		"icons.json",
		"enums.json",
		"features.json",
		"units.json",
		"relations.json",
		"mqtt.json",
		"snapshot.json",
	} {
		raw, err := ReadFile(name)
		if err != nil {
			t.Errorf("ReadFile(%q): %v", name, err)
			continue
		}
		if len(raw) == 0 {
			t.Errorf("ReadFile(%q): empty", name)
		}
	}
}

// TestEveryTableDecodes proves each accessor's Go types actually match the
// extracted JSON — a mismatch would otherwise surface as a zero-valued table in
// a consumer rather than as an error here.
func TestEveryTableDecodes(t *testing.T) {
	if _, err := LoadMQTT(); err != nil {
		t.Fatalf("LoadMQTT: %v", err)
	}
	if _, err := LoadRelations(); err != nil {
		t.Fatalf("LoadRelations: %v", err)
	}
	if _, err := LoadUnits(); err != nil {
		t.Fatalf("LoadUnits: %v", err)
	}
	if _, err := LoadSensor(); err != nil {
		t.Fatalf("LoadSensor: %v", err)
	}
	if _, err := LoadDeviceClasses(); err != nil {
		t.Fatalf("LoadDeviceClasses: %v", err)
	}
	if _, err := LoadIcons(); err != nil {
		t.Fatalf("LoadIcons: %v", err)
	}
	if _, err := LoadFeatures(); err != nil {
		t.Fatalf("LoadFeatures: %v", err)
	}
	if _, err := LoadEnums(); err != nil {
		t.Fatalf("LoadEnums: %v", err)
	}
}

// TestEveryPlatformHasASchema is the completeness check that matters: a
// platform present in SupportedComponents but missing from Platforms would let
// a consumer publish an unvalidated component.
func TestEveryPlatformHasASchema(t *testing.T) {
	mqtt, err := LoadMQTT()
	if err != nil {
		t.Fatalf("LoadMQTT: %v", err)
	}
	if len(mqtt.SupportedComponents) == 0 {
		t.Fatal("SupportedComponents empty")
	}
	for _, component := range mqtt.SupportedComponents {
		schema, ok := mqtt.Platforms[component]
		if !ok {
			t.Errorf("platform %q has no schema", component)
			continue
		}
		switch {
		case len(schema.Keys) > 0:
			// A platform with a handful of keys is a red flag: `light` and
			// `infrared` both carry a meta-schema that only validates the
			// discriminator, and reading that instead of the sub-schemas is a
			// silent extraction bug.
			if len(schema.Keys) < 4 {
				t.Errorf("platform %q has only %d keys — meta-schema read instead of the real one?",
					component, len(schema.Keys))
			}
		case len(schema.Variants) > 0:
			if schema.Discriminator == "" {
				t.Errorf("platform %q has variants but no discriminator", component)
			}
			for name, variant := range schema.Variants {
				if len(variant.Keys) == 0 {
					t.Errorf("platform %q variant %q has no keys", component, name)
				}
			}
		default:
			t.Errorf("platform %q has neither keys nor variants", component)
		}
	}
}

// TestDispatchingPlatforms pins the two platforms that select a sub-schema
// instead of validating directly. They are the extractor's sharpest edge: read
// in the wrong order they yield one key each and look plausible.
func TestDispatchingPlatforms(t *testing.T) {
	mqtt, err := LoadMQTT()
	if err != nil {
		t.Fatalf("LoadMQTT: %v", err)
	}
	for _, tc := range []struct {
		platform      string
		discriminator string
		variants      []string
	}{
		{"light", "schema", []string{"basic", "json", "template"}},
		{"infrared", "device_class", []string{"emitter", "receiver"}},
	} {
		schema, ok := mqtt.Platforms[tc.platform]
		if !ok {
			t.Errorf("no schema for %q", tc.platform)
			continue
		}
		if schema.Discriminator != tc.discriminator {
			t.Errorf("%s discriminator = %q, want %q", tc.platform, schema.Discriminator, tc.discriminator)
		}
		for _, variant := range tc.variants {
			if _, ok := schema.Variants[variant]; !ok {
				t.Errorf("%s missing variant %q", tc.platform, variant)
			}
		}
	}
}

// TestDeviceBundleSchema pins the device-based discovery contract — the only
// discovery shape this ecosystem publishes. `origin` being required is what
// distinguishes it from the per-entity form, where origin is optional.
func TestDeviceBundleSchema(t *testing.T) {
	mqtt, err := LoadMQTT()
	if err != nil {
		t.Fatalf("LoadMQTT: %v", err)
	}
	for _, key := range []string{"device", "components", "origin"} {
		entry, ok := mqtt.DeviceDiscoverySchema[key]
		if !ok {
			t.Errorf("device bundle schema missing %q", key)
			continue
		}
		if !entry.Required {
			t.Errorf("device bundle key %q is optional — want required", key)
		}
	}
	if _, ok := mqtt.DeviceInfoSchema["identifiers"]; !ok {
		t.Error("device info schema missing identifiers")
	}
	if _, ok := mqtt.OriginInfoSchema["name"]; !ok {
		t.Error("origin info schema missing name")
	}
}

// TestAbbreviationsAreInvertible guards the abbreviation tables: two short keys
// mapping to the same long key would make a round-trip ambiguous, and a
// consumer expanding a payload would silently drop one of them.
func TestAbbreviationsAreInvertible(t *testing.T) {
	mqtt, err := LoadMQTT()
	if err != nil {
		t.Fatalf("LoadMQTT: %v", err)
	}
	for label, table := range map[string]map[string]string{
		"abbreviations":        mqtt.Abbreviations,
		"device_abbreviations": mqtt.DeviceAbbreviations,
		"origin_abbreviations": mqtt.OriginAbbreviations,
	} {
		if len(table) == 0 {
			t.Errorf("%s is empty", label)
			continue
		}
		seen := make(map[string]string, len(table))
		for short, long := range table {
			if prev, dup := seen[long]; dup {
				t.Errorf("%s: %q and %q both expand to %q", label, prev, short, long)
			}
			seen[long] = short
		}
	}
}

// TestEnergyAdmitsTotalStateClasses pins the single relation that corrupts data
// when wrong: an energy sensor published as `measurement` produces long-term
// statistics Home Assistant cannot retroactively repair.
func TestEnergyAdmitsTotalStateClasses(t *testing.T) {
	rel, err := LoadRelations()
	if err != nil {
		t.Fatalf("LoadRelations: %v", err)
	}
	got, ok := rel.SensorDeviceClassStateClasses[string(SensorDeviceClassEnergy)]
	if !ok {
		t.Fatal("no state classes for device class energy")
	}
	for _, want := range []StateClass{StateClassTotal, StateClassTotalIncreasing} {
		if !slices.Contains(got, string(want)) {
			t.Errorf("energy does not admit %s (got %v)", want, got)
		}
	}
	if slices.Contains(got, string(StateClassMeasurement)) {
		t.Errorf("energy unexpectedly admits measurement (got %v)", got)
	}
}

// TestAmbiguousUnitsCarryTheMicroSign pins the trap that costs a whole config:
// Home Assistant rewrites the legacy micro sign U+00B5 to U+03BC, and a payload
// that disagrees is discarded with no error anywhere.
func TestAmbiguousUnitsCarryTheMicroSign(t *testing.T) {
	rel, err := LoadRelations()
	if err != nil {
		t.Fatalf("LoadRelations: %v", err)
	}
	if len(rel.AmbiguousUnits) == 0 {
		t.Fatal("AmbiguousUnits is empty")
	}
	var found bool
	for from, to := range rel.AmbiguousUnits {
		for _, r := range from {
			if r == 'µ' {
				found = true
				if !containsRune(to, 'μ') {
					t.Errorf("%q maps to %q, which is not the canonical micro sign", from, to)
				}
			}
		}
	}
	if !found {
		t.Error("no U+00B5 spelling in AmbiguousUnits — has HA stopped normalising it?")
	}
}

func containsRune(s string, want rune) bool {
	for _, r := range s {
		if r == want {
			return true
		}
	}
	return false
}

// TestGeneratedPlatformsMatchTheTable guards the codegen against the data: a
// stale gen_vocab.go would hand consumers compile-time constants that the
// embedded schemas no longer know.
func TestGeneratedPlatformsMatchTheTable(t *testing.T) {
	mqtt, err := LoadMQTT()
	if err != nil {
		t.Fatalf("LoadMQTT: %v", err)
	}
	if len(Platforms) != len(mqtt.SupportedComponents) {
		t.Fatalf("generated Platforms has %d entries, table has %d — run `make regenerate`",
			len(Platforms), len(mqtt.SupportedComponents))
	}
	for _, p := range Platforms {
		if !p.Valid() {
			t.Errorf("generated Platform %q fails its own Valid()", p)
		}
		if _, ok := mqtt.Platforms[string(p)]; !ok {
			t.Errorf("generated Platform %q has no schema — gen_vocab.go is stale", p)
		}
	}
}

// TestGeneratedVocabulariesMatchEnums checks the same staleness for the value
// vocabularies, using two that consumers touch most.
func TestGeneratedVocabulariesMatchEnums(t *testing.T) {
	enums, err := LoadEnums()
	if err != nil {
		t.Fatalf("LoadEnums: %v", err)
	}
	for _, tc := range []struct {
		key   string
		valid func(string) bool
	}{
		{"sensor_device_class", func(v string) bool { return SensorDeviceClass(v).Valid() }},
		{"sensor_state_class", func(v string) bool { return StateClass(v).Valid() }},
		{"entity_category", func(v string) bool { return EntityCategory(v).Valid() }},
		{"hvac_mode", func(v string) bool { return HVACMode(v).Valid() }},
	} {
		values, ok := enums[tc.key]
		if !ok {
			t.Errorf("enums.json has no %q", tc.key)
			continue
		}
		for _, v := range values {
			if !tc.valid(v) {
				t.Errorf("%s: %q is in the table but not generated — run `make regenerate`", tc.key, v)
			}
		}
	}
}

// TestSnapshotConstantsMatchTheData guards the sed-stamping in regenerate.sh.
// The constants and data/snapshot.json are written by two different steps, and
// nothing but this test notices when one of them is skipped.
func TestSnapshotConstantsMatchTheData(t *testing.T) {
	if SnapshotVersion == "" {
		t.Fatal("SnapshotVersion is empty")
	}
	if SnapshotRef == "" {
		t.Fatal("SnapshotRef is empty")
	}
	raw, err := ReadFile("snapshot.json")
	if err != nil {
		t.Fatalf("ReadFile(snapshot.json): %v", err)
	}
	var snap struct {
		Version string `json:"version"`
		Ref     string `json:"ref"`
	}
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatalf("decode snapshot.json: %v", err)
	}
	if snap.Version != SnapshotVersion {
		t.Errorf("SnapshotVersion = %q, snapshot.json says %q — stamping was skipped",
			SnapshotVersion, snap.Version)
	}
	if snap.Ref != SnapshotRef {
		t.Errorf("SnapshotRef = %q, snapshot.json says %q — stamping was skipped",
			SnapshotRef, snap.Ref)
	}
}

// TestIconsCoverTheCommonDomains keeps the Stage 0 icon harvest honest: an
// empty map would mean the glob stopped matching after an HA layout change.
func TestIconsCoverTheCommonDomains(t *testing.T) {
	icons, err := LoadIcons()
	if err != nil {
		t.Fatalf("LoadIcons: %v", err)
	}
	for _, domain := range []string{"sensor", "binary_sensor", "switch"} {
		entries, ok := icons[domain]
		if !ok || len(entries) == 0 {
			t.Errorf("no icons for domain %q", domain)
		}
	}
}
