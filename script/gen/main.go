// SPDX-License-Identifier: MIT
// Copyright (C) 2026 go-ha-catalog authors.

// Command gen turns the extracted JSON vocabularies into typed Go constants.
//
// Only the closed, stable vocabularies are generated — the ones a consumer
// spells out in code and wants the compiler to check. The relational tables
// (device class to unit, discovery schemas, icons) stay data: they are large,
// they are looked up rather than written, and turning them into identifiers
// would buy nothing.
//
// Units are deliberately NOT generated. Their values are symbols such as "°C",
// "µg/m³" and "W/m²", which make poor Go identifiers, and a consumer that
// hard-codes a unit spelling is exactly the bug AmbiguousUnits exists to catch.
//
// Run it through `make regenerate`, after script/extract.py.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode"
)

// vocab describes one generated Go type: the JSON key it reads, the Go type
// name, and the doc comment that type carries.
type vocab struct {
	key  string // key in enums.json
	name string // Go type name
	doc  string
}

var vocabs = []vocab{
	{"entity_category", "EntityCategory", "EntityCategory is Home Assistant's entity category: a hint that an entity is\nconfiguration or diagnostics rather than primary state."},
	{"sensor_state_class", "StateClass", "StateClass tells Home Assistant how to aggregate a sensor over time. Getting\nit wrong corrupts long-term statistics irreversibly, so check it against\n[Relations.SensorDeviceClassStateClasses] rather than guessing."},
	{"sensor_device_class", "SensorDeviceClass", "SensorDeviceClass is the device class vocabulary of the sensor platform."},
	{"binary_sensor_device_class", "BinarySensorDeviceClass", "BinarySensorDeviceClass is the device class vocabulary of the binary_sensor platform."},
	{"number_device_class", "NumberDeviceClass", "NumberDeviceClass is the device class vocabulary of the number platform."},
	{"cover_device_class", "CoverDeviceClass", "CoverDeviceClass is the device class vocabulary of the cover platform."},
	{"switch_device_class", "SwitchDeviceClass", "SwitchDeviceClass is the device class vocabulary of the switch platform."},
	{"button_device_class", "ButtonDeviceClass", "ButtonDeviceClass is the device class vocabulary of the button platform."},
	{"event_device_class", "EventDeviceClass", "EventDeviceClass is the device class vocabulary of the event platform."},
	{"update_device_class", "UpdateDeviceClass", "UpdateDeviceClass is the device class vocabulary of the update platform."},
	{"humidifier_device_class", "HumidifierDeviceClass", "HumidifierDeviceClass is the device class vocabulary of the humidifier platform."},
	{"valve_device_class", "ValveDeviceClass", "ValveDeviceClass is the device class vocabulary of the valve platform."},
	{"media_player_device_class", "MediaPlayerDeviceClass", "MediaPlayerDeviceClass is the device class vocabulary of the media_player platform."},
	{"number_mode", "NumberMode", "NumberMode selects how Home Assistant renders a number entity."},
	{"text_mode", "TextMode", "TextMode selects how Home Assistant renders a text entity."},
	{"device_tracker_source_type", "SourceType", "SourceType is a device_tracker's positioning source. \"router\" is what makes a\ntracker usable for presence without any coordinates."},
	{"hvac_mode", "HVACMode", "HVACMode is a climate entity's operating mode."},
	{"hvac_action", "HVACAction", "HVACAction is what a climate entity is currently doing, as opposed to what it\nis set to."},
	{"color_mode", "ColorMode", "ColorMode is a light's color capability. Unlike most mode lists, MQTT does\nconstrain this one: supported_color_modes is schema-validated."},
	{"cover_state", "CoverState", "CoverState is a cover's reported state."},
	{"valve_state", "ValveState", "ValveState is a valve's reported state."},
	{"lock_state", "LockState", "LockState is a lock's reported state."},
	{"alarm_state", "AlarmState", "AlarmState is an alarm control panel's reported state."},
	{"vacuum_activity", "VacuumActivity", "VacuumActivity is a vacuum's reported activity."},
	{"lawn_mower_activity", "LawnMowerActivity", "LawnMowerActivity is a lawn mower's reported activity."},
	{"humidifier_action", "HumidifierAction", "HumidifierAction is what a humidifier is currently doing."},
	{"media_player_state", "MediaPlayerState", "MediaPlayerState is a media player's reported state."},
}

// initialisms Go style wants fully upper-cased inside an identifier.
var initialisms = map[string]string{
	"aqi": "AQI", "co": "CO", "co2": "CO2", "cpu": "CPU", "gps": "GPS",
	"hvac": "HVAC", "id": "ID", "ip": "IP", "no": "NO", "no2": "NO2",
	"nox": "NOx", "o3": "O3", "ph": "PH", "pm1": "PM1", "pm10": "PM10",
	"pm25": "PM25", "ppm": "PPM", "so2": "SO2", "tv": "TV", "url": "URL",
	"uv": "UV", "vocs": "VOCs", "ac": "AC", "dc": "DC",
}

func ident(value string) string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	var b strings.Builder
	for _, f := range fields {
		lower := strings.ToLower(f)
		if up, ok := initialisms[lower]; ok {
			b.WriteString(up)
			continue
		}
		r := []rune(lower)
		r[0] = unicode.ToUpper(r[0])
		b.WriteString(string(r))
	}
	out := b.String()
	if out == "" {
		return "Unknown"
	}
	// A Go identifier cannot start with a digit; the vocabularies contain a few
	// values that do (pm1, pm10 are caught by initialisms, but be defensive).
	if unicode.IsDigit(rune(out[0])) {
		out = "N" + out
	}
	return out
}

func main() {
	dataDir := flag.String("data", "data", "directory holding the extracted JSON")
	out := flag.String("out", "gen_vocab.go", "Go file to write")
	flag.Parse()

	enums := map[string][]string{}
	if err := readJSON(filepath.Join(*dataDir, "enums.json"), &enums); err != nil {
		fatal(err)
	}
	var mqtt struct {
		SupportedComponents []string `json:"supported_components"`
	}
	if err := readJSON(filepath.Join(*dataDir, "mqtt.json"), &mqtt); err != nil {
		fatal(err)
	}
	var snapshot struct {
		Version string `json:"version"`
		Ref     string `json:"ref"`
	}
	if err := readJSON(filepath.Join(*dataDir, "snapshot.json"), &snapshot); err != nil {
		fatal(err)
	}

	var b bytes.Buffer
	fmt.Fprintf(&b, `// SPDX-License-Identifier: MIT
// Copyright (C) 2026 go-ha-catalog authors.

// Code generated by script/gen from Home Assistant %s. DO NOT EDIT.

package hacatalog

`, snapshot.Version)

	// Platform first: it is the vocabulary every consumer touches.
	writeType(&b, "Platform", "Platform is an MQTT-capable Home Assistant platform. It is the value of a\ndevice bundle's components[*].platform, and the whitelist Home Assistant\nvalidates that key against.", mqtt.SupportedComponents)
	fmt.Fprintf(&b, "// Platforms lists every MQTT-capable platform, in Home Assistant's own order.\nvar Platforms = []Platform{\n")
	for _, v := range mqtt.SupportedComponents {
		fmt.Fprintf(&b, "\tPlatform%s,\n", ident(v))
	}
	fmt.Fprintf(&b, "}\n\n")

	for _, v := range vocabs {
		values, ok := enums[v.key]
		if !ok {
			fmt.Fprintf(os.Stderr, "warning: enums.json has no %q; skipping %s\n", v.key, v.name)
			continue
		}
		writeType(&b, v.name, v.doc, values)
	}

	src, err := format.Source(b.Bytes())
	if err != nil {
		// Write the unformatted source so the syntax error is inspectable.
		// #nosec G306 -- a debug dump of generated Go source in the developer's
		// own working copy; 0644 is the mode git will record for it anyway.
		_ = os.WriteFile(*out+".broken", b.Bytes(), 0o644)
		fatal(fmt.Errorf("gofmt: %w (unformatted source at %s.broken)", err, *out))
	}
	// #nosec G306 -- this writes a checked-in Go source file; 0600 would make
	// the working copy differ from every other file in the repository.
	if err := os.WriteFile(*out, src, 0o644); err != nil {
		fatal(err)
	}
	fmt.Printf("wrote %s (%d bytes)\n", *out, len(src))
}

func writeType(b *bytes.Buffer, name, doc string, values []string) {
	for _, line := range strings.Split(doc, "\n") {
		fmt.Fprintf(b, "// %s\n", line)
	}
	fmt.Fprintf(b, "type %s string\n\n", name)

	// Constant names are deduplicated: two vocabularies can share a value, but
	// within one type a collision would not compile, and silently dropping one
	// would lose a legal value.
	seen := map[string]string{}
	sorted := slices.Clone(values)
	sort.Strings(sorted)

	fmt.Fprintf(b, "const (\n")
	for _, v := range sorted {
		id := name + ident(v)
		if prev, dup := seen[id]; dup {
			fmt.Fprintf(os.Stderr, "warning: %s: %q and %q both map to %s; skipping the latter\n", name, prev, v, id)
			continue
		}
		seen[id] = v
		fmt.Fprintf(b, "\t%s %s = %q\n", id, name, v)
	}
	fmt.Fprintf(b, ")\n\n")

	fmt.Fprintf(b, "// String returns the wire value.\nfunc (v %s) String() string { return string(v) }\n\n", name)

	fmt.Fprintf(b, "// Valid reports whether v is one of the values Home Assistant declared at\n// this catalog's snapshot. A false result is not proof of an illegal value on a\n// newer Home Assistant \u2014 check SnapshotVersion before treating it as one.\nfunc (v %s) Valid() bool {\n\t_, ok := valid%s[v]\n\treturn ok\n}\n\n", name, name)
	fmt.Fprintf(b, "var valid%s = map[%s]struct{}{\n", name, name)
	for _, v := range sorted {
		id := name + ident(v)
		if seen[id] != v {
			continue
		}
		fmt.Fprintf(b, "\t%s: {},\n", id)
	}
	fmt.Fprintf(b, "}\n\n")
}

func readJSON(path string, into any) error {
	// #nosec G304 -- build-time generator; the path comes from a developer-
	// supplied flag, not from untrusted input.
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, into)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "gen:", err)
	os.Exit(1)
}
