package server

import (
	"context"
	"github.com/anydoor7/tslink/internal/health"
	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReviewRefusedRegistryTargetIsNotProbed(t *testing.T) {
	s, dir := healthTestServer(t)
	path := filepath.Join(dir, "registry.json")
	// Parsing the real invalid registry path, not manufacturing its failure code.
	raw := `{"services":[{"name":"metadata","type":"proxy","target":"http://169.254.169.254/latest/meta-data/"}]}`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	_, issues, err := registry.LoadForRuntime(path)
	if err != nil || len(issues) != 1 {
		t.Fatalf("fixture: issues=%+v err=%v", issues, err)
	}
	f := serviceIssueFailure(issues[0])
	if f.Error.Code != registry.CodeLinkLocalTargetRefused {
		t.Fatal("wrong refusal", f.Error)
	}
	s.serviceFailures["metadata"] = f
	s.serviceFailures["ordinary"] = tsruntime.ServiceState{Service: registry.Service{Name: "ordinary", Type: registry.TypeProxy, Target: "http://localhost:1234"}, RuntimeState: tsruntime.ServiceRuntimeFailed}
	recorder := health.NewRecorder(filepath.Join(dir, health.StateFile), health.NotifierConfig{})
	calls := make(chan string, 2)
	// This spy deliberately never dials the forbidden host or any other network.
	s.healthCycle(context.Background(), recorder, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), func(_ context.Context, svc registry.Service) string { calls <- svc.Name; return "" })
	close(calls)
	counts := map[string]int{}
	for name := range calls {
		counts[name]++
	}
	if counts["ordinary"] != 1 {
		t.Fatal("positive control did not probe", counts)
	}
	t.Logf("runtime refusal=%s; probes=%v", f.Error.Code, counts)
	if counts["metadata"] != 0 {
		t.Errorf("healthCycle schedules an HTTP probe of a target strict registry loading refused")
	}
}

func TestHealthSchedulerRefusesTCPAndRetainsEnrollmentPolicyFailures(t *testing.T) {
	s, dir := healthTestServer(t)
	for _, name := range []string{"enrollment", "policy"} {
		s.serviceFailures[name] = tsruntime.ServiceState{Service: registry.Service{Name: name, Type: registry.TypeTCP, Target: "localhost:1234"}, RuntimeState: tsruntime.ServiceRuntimeFailed}
	}
	for _, name := range []string{"refused-running", "refused-failed"} {
		svc := registry.Service{Name: name, Type: registry.TypeTCP, Target: "169.254.169.254:80"}
		if name == "refused-running" {
			s.nodes[name] = &ServiceNode{service: svc}
		} else {
			s.serviceFailures[name] = tsruntime.ServiceState{Service: svc, RuntimeState: tsruntime.ServiceRuntimeFailed}
		}
	}
	r := health.NewRecorder(filepath.Join(dir, health.StateFile), health.NotifierConfig{})
	calls := make(chan string, 4)
	s.healthCycle(context.Background(), r, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), func(_ context.Context, svc registry.Service) string { calls <- svc.Name; return "" })
	close(calls)
	counts := map[string]int{}
	for name := range calls {
		counts[name]++
	}
	if counts["enrollment"] != 1 || counts["policy"] != 1 {
		t.Fatal("positive controls", counts)
	}
	if len(counts) != 2 {
		t.Fatal("refused TCP target scheduled", counts)
	}
}
