package cmd

import (
	"bytes"
	"context"
	"testing"

	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
)

// TestTagsDeleteRemoteRefusesWhenTheDefaultTagCannotBeRead: the guard that
// keeps the default tag's ACL owner rule reads config.json strictly. With a
// trailing comma or a default_tag typo, the lenient reader answered
// tag:tsmain, so the real default tag (tag:web here) passed the guard and its
// owner rule could be deleted.
func TestTagsDeleteRemoteRefusesWhenTheDefaultTagCannotBeRead(t *testing.T) {
	for name, raw := range map[string]string{
		"trailing comma": `{"default_tag":"tag:web",}`,
		"key typo":       `{"default_tags":"tag:web"}`,
	} {
		t.Run(name, func(t *testing.T) {
			writeGlobalConfigFixture(t, raw)
			setTagsMocks(t)
			mockRegistryWithServices(nil)
			tagsRegistryPathFn = func() (string, error) { return "/nonexistent/registry.json", nil }
			deleted := 0
			tagsDeleteTagFn = func(context.Context, string) error {
				deleted++
				return nil
			}

			err := tagsDeleteRemoteRun(context.Background(), &bytes.Buffer{}, "tag:web", true, true, true)
			if deleted != 0 {
				t.Fatalf("deleted the tag %d time(s) although config.json could not be read", deleted)
			}
			if code, _ := registry.ErrorCode(err); code != registry.CodeConfigLoadFailed {
				t.Fatalf("error = %v, want %s", err, registry.CodeConfigLoadFailed)
			}
			if output.ExitCode(err) != output.ExitUsage {
				t.Fatalf("exit = %d, want %d", output.ExitCode(err), output.ExitUsage)
			}
		})
	}

	// Control: with a readable config the same call deletes a tag that is
	// not the default, and still refuses the default itself.
	writeGlobalConfigFixture(t, `{"default_tag":"tag:web"}`)
	setTagsMocks(t)
	mockRegistryWithServices(nil)
	tagsRegistryPathFn = func() (string, error) { return "/nonexistent/registry.json", nil }
	deleted := 0
	tagsDeleteTagFn = func(context.Context, string) error {
		deleted++
		return nil
	}
	if err := tagsDeleteRemoteRun(context.Background(), &bytes.Buffer{}, "tag:other", true, true, true); err != nil || deleted != 1 {
		t.Fatalf("control delete of tag:other: err=%v deleted=%d, want nil and 1", err, deleted)
	}
	err := tagsDeleteRemoteRun(context.Background(), &bytes.Buffer{}, "tag:web", true, true, true)
	if deleted != 1 || output.ExitCode(err) != output.ExitConflict {
		t.Fatalf("control delete of the default tag: err=%v deleted=%d, want a conflict and no delete", err, deleted)
	}
}
