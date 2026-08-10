package cmd

import (
	"encoding/json"
	"sort"
	"testing"
)

func assertExactTopLevelJSONKeys(t *testing.T, raw string, want ...string) {
	t.Helper()

	var got map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal JSON object: %v\nraw: %s", err, raw)
	}
	if len(got) != len(want) {
		t.Fatalf("top-level keys = %v, want %v\nraw: %s", sortedJSONKeys(got), want, raw)
	}
	for _, key := range want {
		if _, ok := got[key]; !ok {
			t.Fatalf("top-level keys = %v, missing %q\nraw: %s", sortedJSONKeys(got), key, raw)
		}
	}
}

func sortedJSONKeys(raw map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(raw))
	for key := range raw {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
