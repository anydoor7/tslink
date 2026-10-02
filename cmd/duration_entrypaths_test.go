package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
)

func TestPrivateToPublicDryRunUsesFiniteDefault(t *testing.T) {
	binary := compiledTSLinkBinary(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.json")
	fixture := `{"schema_version":1,"services":[{"name":"preview","type":"proxy","target":"http://localhost:3000"}]}`
	if err := os.WriteFile(path, []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}
	run := e2eRunBinary(t, binary, dir, "", e2eEnv(dir), "add", "preview", "--proxy", "localhost:3000", "--funnel", "--public", "--dry-run", "--json")
	frame, data := e2eDecodeEnvelope(t, run, "add")
	service, ok := data["service"].(map[string]any)
	if run.ExitCode != 0 || !frame.OK || !ok || service["funnel_expires_at"] == nil || service["funnel_expires_at"] == "never" {
		t.Fatalf("permanent dry run: %+v %+v", frame, data)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != fixture {
		t.Fatal("dry run wrote registry", err)
	}
}

func TestPublicLifetimeEntryPaths(t *testing.T) {
	for _, entry := range []string{"mcp_add", "share", "recipe", "extend"} {
		t.Run(entry, func(t *testing.T) {
			path := recipeTestRegistry(t)
			paths := sharePaths{Registry: path, PID: filepath.Join(filepath.Dir(path), "tslink.pid"), Snapshot: filepath.Join(filepath.Dir(path), "runtime.json")}
			now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
			oldClock := durationNowFn
			durationNowFn = func() time.Time { return now }
			t.Cleanup(func() { durationNowFn = oldClock })
			private := registry.Service{Name: "preview", Type: registry.TypeProxy, Target: "http://localhost:3000"}
			if _, err := registry.Add(path, private); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			switch entry {
			case "mcp_add":
				result, err := callMCPTool(context.Background(), defaultMCPActions(paths, io.Discard), "add", json.RawMessage(`{"name":"preview","type":"proxy","target":"localhost:3000","funnel":true,"public_ack":true,"no_daemon_install":true}`))
				if err != nil || result.IsError {
					t.Fatal(result, err)
				}
				reg, err := registry.Load(path)
				if err != nil || len(reg.Services) != 1 || !reg.Services[0].Funnel || reg.Services[0].FunnelExpiresAt == nil || !reg.Services[0].FunnelExpiresAt.Equal(now.Add(24*time.Hour)) {
					t.Fatalf("MCP public default: %+v %v", reg, err)
				}
				return
			case "share":
				spec, err := applyShareExposure(shareTargetSpec{Service: private, NameBase: "preview"}, shareRequest{Funnel: true, PublicAck: true, Now: now})
				if err != nil || spec.Service.FunnelExpiresAt == nil {
					t.Fatal(spec, err)
				}
				_, err = registerShareWithOutcome(path, spec, "preview")
				var conflict *output.CodeError
				if !errors.As(err, &conflict) || conflict.Code != output.ExitConflict {
					t.Fatal("private/public share must conflict", err)
				}
			case "recipe":
				result, err := applyRecipe(context.Background(), recipeRequest{RecipeID: "jellyfin", Name: "preview", Funnel: true, PublicAck: true, Now: now}, path, false, io.Discard)
				if err != nil || result.Action != templateActionSkipExisting || result.Applied {
					t.Fatal(result, err)
				}
			case "extend":
				_, err := extendLifetime(path, extendArguments{Service: "preview", For: ptrString("3d")}, now)
				if err == nil || !strings.Contains(err.Error(), "no active or expired acknowledged Funnel TTL") {
					t.Fatal(err)
				}
			}
			after, err := os.ReadFile(path)
			if err != nil || string(before) != string(after) {
				t.Fatal("private service changed", err)
			}
		})
	}
}

func TestAddFinalLifetimePolicyLoadFailure(t *testing.T) {
	path := recipeTestRegistry(t)
	configPath, err := config.ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`{"durations":{"public_max":"never"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	svc := registry.Service{Name: "preview", Type: registry.TypeProxy, Target: "http://localhost:3000"}
	_, _, err = executeAdd(context.Background(), svc, path, filepath.Join(filepath.Dir(path), "tslink.pid"), "", true, 0, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
	if err == nil || !strings.Contains(err.Error(), "config.json") || !strings.Contains(err.Error(), "invalid relative duration") {
		t.Fatal("invalid final policy ignored", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("failed policy persisted registry", err)
	}
}

func TestPublicLifetimeDryRunPreservesOnlyPublicChoices(t *testing.T) {
	for _, choice := range []string{"private", "public_deadline", "public_never"} {
		t.Run(choice, func(t *testing.T) {
			path := recipeTestRegistry(t)
			now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
			oldClock := durationNowFn
			durationNowFn = func() time.Time { return now }
			t.Cleanup(func() { durationNowFn = oldClock })
			oldDeadline := now.Add(72 * time.Hour)
			svc := registry.Service{Name: "preview", Type: registry.TypeProxy, Target: "http://localhost:3000"}
			if choice != "private" {
				svc.Funnel, svc.PublicAck = true, true
			}
			if choice == "public_deadline" {
				svc.FunnelExpiresAt = &oldDeadline
			}
			if _, err := registry.Add(path, svc); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := runRecipeRoot(t, "add", "preview", "--proxy", "localhost:3000", "--funnel", "--public", "--dry-run", "--json")
			if err != nil {
				t.Fatal(err)
			}
			var frame struct {
				OK   bool            `json:"ok"`
				Data AddDryRunResult `json:"data"`
			}
			if err := json.Unmarshal([]byte(raw), &frame); err != nil {
				t.Fatal(err)
			}
			got := frame.Data.Service.FunnelExpiresAt
			if !frame.OK || !frame.Data.DryRun {
				t.Fatal(raw)
			}
			switch choice {
			case "public_never":
				if got != nil {
					t.Fatal("legacy never changed", got)
				}
			case "public_deadline":
				if got == nil || !got.Equal(oldDeadline) {
					t.Fatal("public deadline changed", got)
				}
			case "private":
				if got == nil || !got.Equal(now.Add(24*time.Hour)) {
					t.Fatal("private expiry carried to public", got)
				}
			}
			after, err := os.ReadFile(path)
			if err != nil || string(before) != string(after) {
				t.Fatal("dry run changed registry", err)
			}
		})
	}
}
