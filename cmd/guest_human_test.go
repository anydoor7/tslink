package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestGuestHumanSummaries(t *testing.T) {
	paths := guestCommandPaths(t)
	now := time.Date(2030, 7, 10, 12, 0, 0, 0, time.UTC)
	originalClock := durationNowFn
	durationNowFn = func() time.Time { return now }
	t.Cleanup(func() { durationNowFn = originalClock })
	result, err := createGuest(paths, guestArguments{App: "photos", For: "3d", Public: true, Label: "Aunt May"}, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"list"}, {"show", result.Grant.ID}, {"revoke", result.Grant.ID}} {
		cmd := newGuestCmd()
		out := &bytes.Buffer{}
		cmd.SetOut(out)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		text := out.String()
		for _, value := range []string{"Aunt May", "photos", "Jul 13, 2030", "UTC", "in 3 days", "USES"} {
			if !strings.Contains(text, value) {
				t.Errorf("%v missing %q: %s", args, value, text)
			}
		}
		if strings.Contains(text, "map[") || strings.Contains(text, "LastUsedAt") {
			t.Error("human output contains Go fields")
		}
		if args[0] == "revoke" && (!strings.Contains(text, "Revoked guest "+result.Grant.ID+".") || !strings.Contains(text, "revoked")) {
			t.Error("revoke confirmation missing")
		}
	}
	if strings.Count(result.Message, "Open") != 1 || !strings.Contains(result.Message, "Jul 13, 2030") || !strings.Contains(result.Message, "UTC") {
		t.Fatal("sendable expiry unclear", result.Message)
	}
}
