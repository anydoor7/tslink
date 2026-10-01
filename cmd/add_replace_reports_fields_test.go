package cmd

import (
	"context"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/registry"
)

// TestReplacingAddReportsWhatItDropped is A1's run4: re-running add to change
// the port silently dropped the allow list and the tags, opening the service
// to every tailnet member. Replace stays the contract; the result now says
// what it replaced.
func TestReplacingAddReportsWhatItDropped(t *testing.T) {
	regPath := stubAddWritePaths(t)
	paths := mcpSharePaths(t)
	paths.Registry = regPath
	actions := defaultMCPActions(paths, io.Discard)
	add := func(params AddParams) AddResult {
		t.Helper()
		params.NoDaemonInstall = true
		result, err := actions.add(context.Background(), params, true)
		if err != nil {
			t.Fatalf("add %+v: %v", params, err)
		}
		validateAgainstToolOutputSchema(t, "add", result)
		return result.(AddResult)
	}

	first := add(AddParams{Name: "api", Proxy: "127.0.0.1:8080", Tags: "tag:web", Allow: "alice@example.com"})
	if encoded, err := json.Marshal(first); err != nil || !first.Created || !strings.Contains(string(encoded), `"replaced_fields":[]`) {
		t.Fatalf("first add = %s (%v), want created with replaced_fields []", encoded, err)
	}
	for _, warning := range first.Warnings {
		if warning.Code == inspect.WarningCodeAccessChangedOnReplace || warning.Code == inspect.WarningCodeIdentityResetOnReplace {
			t.Fatalf("first add warns about a replace: %+v", warning)
		}
	}

	second := add(AddParams{Name: "api", Proxy: "127.0.0.1:9090"})
	if second.Created {
		t.Fatal("second add reports created")
	}
	if want := []string{"allowed_users", "tags", "target"}; !reflect.DeepEqual([]string(second.ReplacedFields), want) {
		t.Fatalf("replaced_fields = %v, want %v", second.ReplacedFields, want)
	}
	byCode := map[string]string{}
	for _, warning := range second.Warnings {
		byCode[warning.Code] = warning.Message
	}
	access, ok := byCode[inspect.WarningCodeAccessChangedOnReplace]
	if !ok || !strings.Contains(access, "allowed_users") || !strings.Contains(access, "tags") {
		t.Fatalf("warnings = %+v, want %s naming allowed_users and tags", second.Warnings, inspect.WarningCodeAccessChangedOnReplace)
	}
	if identity, ok := byCode[inspect.WarningCodeIdentityResetOnReplace]; !ok || !strings.Contains(identity, "tags") {
		t.Fatalf("warnings = %+v, want %s naming tags", second.Warnings, inspect.WarningCodeIdentityResetOnReplace)
	}

	// Repeating the same settings replaces nothing.
	third := add(AddParams{Name: "api", Proxy: "127.0.0.1:9090"})
	if len(third.ReplacedFields) != 0 || len(replaceWarnings("api", third.ReplacedFields)) != 0 {
		t.Fatalf("identical add replaced_fields = %v", third.ReplacedFields)
	}
}

func TestChangedFieldsComparesStoredSettings(t *testing.T) {
	before := registry.Service{Name: "api", Type: registry.TypeProxy, Target: "http://127.0.0.1:8080", Tags: []string{"tag:a", "tag:b"}, Ephemeral: true}
	after := before
	after.Tags = []string{"tag:b", "tag:a"}
	after.CreatedAt = before.CreatedAt.AddDate(1, 0, 0)
	if changed, err := registry.ChangedFields(before, after); err != nil || len(changed) != 0 {
		t.Fatalf("reordered tags and created_at: ChangedFields = %v, %v; want none", changed, err)
	}
	after.Ephemeral = false
	after.ControlURL = "https://headscale.example.com"
	if changed, err := registry.ChangedFields(before, after); err != nil || !reflect.DeepEqual(changed, []string{"control_url", "ephemeral"}) {
		t.Fatalf("ChangedFields = %v, %v; want [control_url ephemeral]", changed, err)
	}
}

func TestAddHelpSaysItReplaces(t *testing.T) {
	command, _, err := rootCmd.Find([]string{"add"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(command.Long, "add replaces it") || !strings.Contains(command.Long, "replaced_fields") {
		t.Fatalf("add --help does not say it replaces an existing service:\n%s", command.Long)
	}
}
