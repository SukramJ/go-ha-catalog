// SPDX-License-Identifier: MIT
// Copyright (C) 2026 go-ha-catalog authors.

// Command hadiff compares two catalog snapshots and classifies the change.
//
// It exists to drive the unattended release: the workflow needs to know whether
// a regenerated catalog is a minor bump, a major one, or nothing at all.
//
// The classification is deliberately narrow. "Breaking" means exactly one
// thing: a value disappeared that [script/gen] had turned into a Go constant —
// a platform name or a vocabulary member. Those are compile errors in every
// consumer. Everything else — a new schema key, a changed default, a unit
// moving between device classes — is data a consumer looks up at runtime, and
// no amount of it can stop a build.
//
// Anything else would either cry wolf (schema defaults churn between HA
// releases) or stay silent on the one change that actually breaks a build.
//
// Exit codes: 0 identical, 1 additive, 2 breaking, 3 error.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

const (
	exitIdentical = 0
	exitAdditive  = 1
	exitBreaking  = 2
	exitError     = 3
)

// identities is the set of names that become Go constants, keyed
// "<vocabulary>/<value>" so a value moving between vocabularies is visible.
type identities map[string]struct{}

func main() {
	oldDir := flag.String("old", "", "previous data/ directory")
	newDir := flag.String("new", "", "regenerated data/ directory")
	format := flag.String("format", "text", "text or json")
	flag.Parse()

	if *oldDir == "" || *newDir == "" {
		fmt.Fprintln(os.Stderr, "hadiff: -old and -new are required")
		os.Exit(exitError)
	}

	oldIDs, err := load(*oldDir)
	if err != nil {
		fatal(err)
	}
	newIDs, err := load(*newDir)
	if err != nil {
		fatal(err)
	}

	added := diff(newIDs, oldIDs)
	removed := diff(oldIDs, newIDs)

	// Data churn outside the generated identities still warrants a release —
	// a consumer validating against a schema wants the new keys — it just is
	// never breaking.
	dataChanged, err := treesDiffer(*oldDir, *newDir)
	if err != nil {
		fatal(err)
	}

	result := struct {
		Verdict     string   `json:"verdict"`
		Bump        string   `json:"bump"`
		Added       []string `json:"added"`
		Removed     []string `json:"removed"`
		DataChanged bool     `json:"data_changed"`
	}{Added: added, Removed: removed, DataChanged: dataChanged}

	code := exitIdentical
	switch {
	case len(removed) > 0:
		result.Verdict, result.Bump, code = "breaking", "major", exitBreaking
	case len(added) > 0 || dataChanged:
		result.Verdict, result.Bump, code = "additive", "minor", exitAdditive
	default:
		result.Verdict, result.Bump = "identical", "none"
	}

	if *format == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			fatal(err)
		}
		os.Exit(code)
	}

	fmt.Printf("verdict: %s (bump: %s)\n", result.Verdict, result.Bump)
	if len(added) > 0 {
		fmt.Printf("\nadded (%d):\n", len(added))
		for _, id := range added {
			fmt.Printf("  + %s\n", id)
		}
	}
	if len(removed) > 0 {
		fmt.Printf("\nremoved (%d) — these are compile errors for consumers:\n", len(removed))
		for _, id := range removed {
			fmt.Printf("  - %s\n", id)
		}
	}
	if len(added) == 0 && len(removed) == 0 && dataChanged {
		fmt.Println("\nno vocabulary change; tables changed (schema keys, defaults, relations)")
	}
	os.Exit(code)
}

// load collects the identities that script/gen turns into Go constants.
func load(dir string) (identities, error) {
	out := identities{}

	var mqtt struct {
		SupportedComponents []string `json:"supported_components"`
	}
	if err := readJSON(filepath.Join(dir, "mqtt.json"), &mqtt); err != nil {
		return nil, err
	}
	for _, p := range mqtt.SupportedComponents {
		out["platform/"+p] = struct{}{}
	}

	enums := map[string][]string{}
	if err := readJSON(filepath.Join(dir, "enums.json"), &enums); err != nil {
		return nil, err
	}
	for vocabulary, values := range enums {
		for _, v := range values {
			out[vocabulary+"/"+v] = struct{}{}
		}
	}
	return out, nil
}

func diff(a, b identities) []string {
	var out []string
	for id := range a {
		if _, ok := b[id]; !ok {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// treesDiffer reports whether any catalog file changed, ignoring snapshot.json:
// that file records the Home Assistant version and therefore differs on every
// single release, which would make every run look like a change.
func treesDiffer(oldDir, newDir string) (bool, error) {
	names, err := catalogFiles(newDir)
	if err != nil {
		return false, err
	}
	oldNames, err := catalogFiles(oldDir)
	if err != nil {
		return false, err
	}
	if !slices.Equal(names, oldNames) {
		return true, nil
	}
	for _, name := range names {
		a, err := os.ReadFile(filepath.Join(oldDir, name)) // #nosec G304 -- names come from the catalog directory listing.
		if err != nil {
			return false, err
		}
		b, err := os.ReadFile(filepath.Join(newDir, name)) // #nosec G304 -- as above.
		if err != nil {
			return false, err
		}
		if !bytes.Equal(a, b) {
			return true, nil
		}
	}
	return false, nil
}

func catalogFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") || name == "snapshot.json" {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

func readJSON(path string, into any) error {
	raw, err := os.ReadFile(path) // #nosec G304 -- build-time tool; path comes from a developer-supplied flag.
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, into)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "hadiff:", err)
	os.Exit(exitError)
}
