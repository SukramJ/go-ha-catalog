// SPDX-License-Identifier: MIT
// Copyright (C) 2026 go-ha-catalog authors.

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// write lays down a minimal catalog directory: the two files hadiff reads for
// identities, plus one it must treat as opaque data.
func write(t *testing.T, platforms []string, enums map[string][]string, extra string) string {
	t.Helper()
	dir := t.TempDir()

	mqtt := map[string]any{"supported_components": platforms}
	for name, payload := range map[string]any{
		"mqtt.json":     mqtt,
		"enums.json":    enums,
		"snapshot.json": map[string]string{"version": "2026.9.1", "ref": "x"},
	} {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), raw, 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "relations.json"), []byte(extra), 0o600); err != nil {
		t.Fatalf("write relations.json: %v", err)
	}
	return dir
}

// TestIdenticalSnapshots is the case that must produce no release at all: Home
// Assistant ships many patch releases that never touch the vocabulary, and a
// tag for each of them is exactly the noise the diff exists to prevent.
func TestIdenticalSnapshots(t *testing.T) {
	enums := map[string][]string{"sensor_device_class": {"energy", "power"}}
	a := write(t, []string{"sensor", "switch"}, enums, `{"k":1}`)
	b := write(t, []string{"sensor", "switch"}, enums, `{"k":1}`)

	added, removed, changed := classify(t, a, b)
	if len(added) != 0 || len(removed) != 0 || changed {
		t.Errorf("identical snapshots reported added=%v removed=%v dataChanged=%v", added, removed, changed)
	}
}

// TestRemovedConstantIsBreaking pins the one condition that maps to a major
// bump: a value that became a Go constant is gone, which is a compile error in
// every consumer.
func TestRemovedConstantIsBreaking(t *testing.T) {
	a := write(t, []string{"sensor", "switch"},
		map[string][]string{"sensor_device_class": {"energy", "power"}}, `{}`)
	b := write(t, []string{"sensor"},
		map[string][]string{"sensor_device_class": {"power"}}, `{}`)

	_, removed, _ := classify(t, a, b)
	for _, want := range []string{"platform/switch", "sensor_device_class/energy"} {
		if !slices.Contains(removed, want) {
			t.Errorf("removed = %v, want it to contain %q", removed, want)
		}
	}
}

// TestAddedConstantIsAdditive covers the common case: Home Assistant grows a
// device class, nothing disappears.
func TestAddedConstantIsAdditive(t *testing.T) {
	a := write(t, []string{"sensor"},
		map[string][]string{"sensor_device_class": {"power"}}, `{}`)
	b := write(t, []string{"sensor", "valve"},
		map[string][]string{"sensor_device_class": {"power", "energy"}}, `{}`)

	added, removed, _ := classify(t, a, b)
	if len(removed) != 0 {
		t.Errorf("removed = %v, want none", removed)
	}
	for _, want := range []string{"platform/valve", "sensor_device_class/energy"} {
		if !slices.Contains(added, want) {
			t.Errorf("added = %v, want it to contain %q", added, want)
		}
	}
}

// TestTableChangeWithoutVocabularyChange is the middle case: schema keys or
// relations moved but no constant did. That still warrants a release — a
// consumer validating against the schema wants the new keys — and it can never
// be breaking.
func TestTableChangeWithoutVocabularyChange(t *testing.T) {
	enums := map[string][]string{"sensor_device_class": {"power"}}
	a := write(t, []string{"sensor"}, enums, `{"precision":1}`)
	b := write(t, []string{"sensor"}, enums, `{"precision":2}`)

	added, removed, changed := classify(t, a, b)
	if len(added) != 0 || len(removed) != 0 {
		t.Errorf("vocabulary moved unexpectedly: added=%v removed=%v", added, removed)
	}
	if !changed {
		t.Error("table change went unnoticed — the release would be skipped")
	}
}

// TestSnapshotJSONIsIgnored guards the one exclusion: snapshot.json records the
// Home Assistant version and so differs on every single release. Counting it
// would make every run look like a change and defeat the whole diff.
func TestSnapshotJSONIsIgnored(t *testing.T) {
	enums := map[string][]string{"sensor_device_class": {"power"}}
	a := write(t, []string{"sensor"}, enums, `{}`)
	b := write(t, []string{"sensor"}, enums, `{}`)

	raw, _ := json.Marshal(map[string]string{"version": "2026.10.0", "ref": "y"})
	if err := os.WriteFile(filepath.Join(b, "snapshot.json"), raw, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, _, changed := classify(t, a, b)
	if changed {
		t.Error("a snapshot.json-only difference counted as a catalog change")
	}
}

func classify(t *testing.T, oldDir, newDir string) (added, removed []string, dataChanged bool) {
	t.Helper()
	oldIDs, err := load(oldDir)
	if err != nil {
		t.Fatalf("load(old): %v", err)
	}
	newIDs, err := load(newDir)
	if err != nil {
		t.Fatalf("load(new): %v", err)
	}
	dataChanged, err = treesDiffer(oldDir, newDir)
	if err != nil {
		t.Fatalf("treesDiffer: %v", err)
	}
	return diff(newIDs, oldIDs), diff(oldIDs, newIDs), dataChanged
}
