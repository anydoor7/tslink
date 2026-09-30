package cmd

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
)

func TestTagsDeleteRemoteRefusalExitBothModes(t *testing.T) {
	for _, isJSON := range []bool{false, true} {
		for _, reason := range []string{"default", "in use", "--force", "--manage-acl"} {
			t.Run(fmt.Sprintf("json=%v/%s", isJSON, reason), func(t *testing.T) {
				setTagsMocks(t)
				mockDefaults()
				mockRegistryWithServices(nil)
				tag, force := "tag:shared", false
				switch reason {
				case "default":
					tag = "tag:tsmain"
				case "in use":
					mockRegistryWithServices([]registry.Service{{Name: "app", Tags: []string{tag}}})
				case "--manage-acl":
					force = true
				}
				tagsDeleteTagFn = func(context.Context, string) error { t.Fatal("refusal must not mutate remote ACL"); return nil }
				err := tagsDeleteRemoteRun(context.Background(), &bytes.Buffer{}, tag, force, false, isJSON)
				if output.ExitCode(err) != output.ExitConflict || !strings.Contains(err.Error(), reason) {
					t.Fatalf("refusal exit = %d, error = %v; want exit 4 with %q", output.ExitCode(err), err, reason)
				}
			})
		}
	}
}
