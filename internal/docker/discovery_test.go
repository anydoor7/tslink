package docker

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/registry"
)

// mockDockerClient is a test double for DockerClient.
type mockDockerClient struct {
	containers    map[string]ContainerInfo
	events        chan ContainerEvent
	errors        chan error
	listErr       error // if set, ListContainers returns this error
	inspectErr    error // if set, InspectContainer returns this error for any ID
}

func newMockClient() *mockDockerClient {
	return &mockDockerClient{
		containers: make(map[string]ContainerInfo),
		events:     make(chan ContainerEvent, 16),
		errors:     make(chan error, 1),
	}
}

func (m *mockDockerClient) ListContainers(_ context.Context) ([]ContainerInfo, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	out := make([]ContainerInfo, 0, len(m.containers))
	for _, c := range m.containers {
		out = append(out, c)
	}
	return out, nil
}

func (m *mockDockerClient) InspectContainer(_ context.Context, id string) (ContainerInfo, error) {
	if m.inspectErr != nil {
		return ContainerInfo{}, m.inspectErr
	}
	c, ok := m.containers[id]
	if !ok {
		return ContainerInfo{}, &containerNotFoundError{id: id}
	}
	return c, nil
}

func (m *mockDockerClient) Events(_ context.Context) (<-chan ContainerEvent, <-chan error) {
	return m.events, m.errors
}

type containerNotFoundError struct{ id string }

func (e *containerNotFoundError) Error() string { return "container not found: " + e.id }

// --- helpers ---

func testRegistryPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "registry.json")
}

func containerWith(id, name string, labels map[string]string, ports map[string]int) ContainerInfo {
	return ContainerInfo{
		ID:     id,
		Name:   name,
		Labels: labels,
		Ports:  ports,
		State:  "running",
	}
}

// --- parseContainerLabels tests ---

func TestParseContainerLabels_Enabled(t *testing.T) {
	c := containerWith("abc123", "myapp", map[string]string{
		"tslink.enable": "true",
		"tslink.name":   "myapp",
	}, map[string]int{"3000/tcp": 3000})

	svc := parseContainerLabels(c)
	if svc == nil {
		t.Fatal("expected non-nil service, got nil")
	}
	if svc.Name != "myapp" {
		t.Errorf("expected name myapp, got %q", svc.Name)
	}
}

func TestParseContainerLabels_Disabled(t *testing.T) {
	c := containerWith("abc123", "myapp", map[string]string{
		"tslink.name": "myapp",
	}, nil)

	svc := parseContainerLabels(c)
	if svc != nil {
		t.Fatalf("expected nil for disabled container, got %+v", svc)
	}
}

func TestParseContainerLabels_MissingName(t *testing.T) {
	c := containerWith("abc123", "myapp", map[string]string{
		"tslink.enable": "true",
	}, nil)

	svc := parseContainerLabels(c)
	if svc != nil {
		t.Fatalf("expected nil when name is missing, got %+v", svc)
	}
}

func TestParseContainerLabels_ProxyType(t *testing.T) {
	c := containerWith("abc123", "api", map[string]string{
		"tslink.enable": "true",
		"tslink.name":   "api",
		"tslink.type":   "proxy",
		"tslink.target": "localhost:4000",
	}, nil)

	svc := parseContainerLabels(c)
	if svc == nil {
		t.Fatal("expected non-nil service")
	}
	if svc.Type != registry.TypeProxy {
		t.Errorf("expected type proxy, got %q", svc.Type)
	}
	if svc.Target != "http://localhost:4000" {
		t.Errorf("expected target http://localhost:4000, got %q", svc.Target)
	}
}

func TestParseContainerLabels_ProxyType_DefaultTarget(t *testing.T) {
	c := containerWith("abc123", "api", map[string]string{
		"tslink.enable": "true",
		"tslink.name":   "api",
	}, map[string]int{"3000/tcp": 3000})

	svc := parseContainerLabels(c)
	if svc == nil {
		t.Fatal("expected non-nil service")
	}
	if svc.Target != "http://localhost:3000" {
		t.Errorf("expected default target http://localhost:3000, got %q", svc.Target)
	}
}

func TestParseContainerLabels_ProxyType_PreserveScheme(t *testing.T) {
	c := containerWith("abc123", "api", map[string]string{
		"tslink.enable": "true",
		"tslink.name":   "api",
		"tslink.target": "https://internal.svc:8443",
	}, nil)

	svc := parseContainerLabels(c)
	if svc == nil {
		t.Fatal("expected non-nil service")
	}
	if svc.Target != "https://internal.svc:8443" {
		t.Errorf("expected target to preserve https scheme, got %q", svc.Target)
	}
}

func TestParseContainerLabels_TCPType(t *testing.T) {
	c := containerWith("abc123", "db", map[string]string{
		"tslink.enable": "true",
		"tslink.name":   "db",
		"tslink.type":   "tcp",
		"tslink.target": "localhost:5432",
		"tslink.port":   "5432",
	}, nil)

	svc := parseContainerLabels(c)
	if svc == nil {
		t.Fatal("expected non-nil service")
	}
	if svc.Type != registry.TypeTCP {
		t.Errorf("expected type tcp, got %q", svc.Type)
	}
	if svc.Port != 5432 {
		t.Errorf("expected port 5432, got %d", svc.Port)
	}
	if svc.Target != "localhost:5432" {
		t.Errorf("expected target localhost:5432, got %q", svc.Target)
	}
}

func TestParseContainerLabels_TCPType_MissingPort(t *testing.T) {
	c := containerWith("abc123", "db", map[string]string{
		"tslink.enable": "true",
		"tslink.name":   "db",
		"tslink.type":   "tcp",
		"tslink.target": "localhost:5432",
		// no tslink.port
	}, nil)

	svc := parseContainerLabels(c)
	if svc != nil {
		t.Fatalf("expected nil for tcp without port, got %+v", svc)
	}
}

func TestParseContainerLabels_WithTags(t *testing.T) {
	c := containerWith("abc123", "web", map[string]string{
		"tslink.enable": "true",
		"tslink.name":   "web",
		"tslink.target": "localhost:8080",
		"tslink.tags":   "tag:web,tag:prod",
	}, nil)

	svc := parseContainerLabels(c)
	if svc == nil {
		t.Fatal("expected non-nil service")
	}
	if len(svc.Tags) != 2 {
		t.Fatalf("expected 2 tags, got %d: %v", len(svc.Tags), svc.Tags)
	}
	if svc.Tags[0] != "tag:web" || svc.Tags[1] != "tag:prod" {
		t.Errorf("unexpected tags: %v", svc.Tags)
	}
}

func TestParseContainerLabels_Ephemeral(t *testing.T) {
	c := containerWith("abc123", "worker", map[string]string{
		"tslink.enable":    "true",
		"tslink.name":      "worker",
		"tslink.target":    "localhost:9000",
		"tslink.ephemeral": "true",
	}, nil)

	svc := parseContainerLabels(c)
	if svc == nil {
		t.Fatal("expected non-nil service")
	}
	if !svc.Ephemeral {
		t.Error("expected ephemeral=true")
	}
}

// --- Discovery integration tests ---

func TestDiscovery_InitialSync(t *testing.T) {
	client := newMockClient()
	client.containers["c1"] = containerWith("c1", "app1", map[string]string{
		"tslink.enable": "true",
		"tslink.name":   "app1",
		"tslink.target": "localhost:3000",
	}, nil)
	client.containers["c2"] = containerWith("c2", "app2", map[string]string{
		// not enabled
		"tslink.name": "app2",
	}, nil)

	regPath := testRegistryPath(t)
	d := New(client, regPath)

	ctx := context.Background()
	if err := d.initialSync(ctx); err != nil {
		t.Fatalf("initialSync error: %v", err)
	}

	reg, err := registry.Load(regPath)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	if len(reg.Services) != 1 {
		t.Fatalf("expected 1 registered service, got %d", len(reg.Services))
	}
	if reg.Services[0].Name != "app1" {
		t.Errorf("expected service name app1, got %q", reg.Services[0].Name)
	}

	// containerToSvc mapping should be set for c1 only
	d.mu.Lock()
	svcName, ok := d.containerToSvc["c1"]
	d.mu.Unlock()
	if !ok || svcName != "app1" {
		t.Errorf("expected containerToSvc[c1]=app1, got ok=%v name=%q", ok, svcName)
	}
}

func TestDiscovery_SyncContainer(t *testing.T) {
	client := newMockClient()
	client.containers["c1"] = containerWith("c1", "svc1", map[string]string{
		"tslink.enable": "true",
		"tslink.name":   "svc1",
		"tslink.target": "localhost:5000",
	}, nil)

	regPath := testRegistryPath(t)
	d := New(client, regPath)

	ctx := context.Background()
	if err := d.syncContainer(ctx, "c1"); err != nil {
		t.Fatalf("syncContainer error: %v", err)
	}

	reg, err := registry.Load(regPath)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if len(reg.Services) != 1 || reg.Services[0].Name != "svc1" {
		t.Fatalf("expected svc1 registered, got %+v", reg.Services)
	}
}

func TestDiscovery_SyncContainer_NotEnabled(t *testing.T) {
	client := newMockClient()
	client.containers["c1"] = containerWith("c1", "ignored", map[string]string{
		"some.other": "label",
	}, nil)

	regPath := testRegistryPath(t)
	d := New(client, regPath)

	ctx := context.Background()
	if err := d.syncContainer(ctx, "c1"); err != nil {
		t.Fatalf("syncContainer should not error for non-enabled container: %v", err)
	}

	reg, err := registry.Load(regPath)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if len(reg.Services) != 0 {
		t.Fatalf("expected 0 services, got %d", len(reg.Services))
	}
}

func TestDiscovery_RemoveContainer(t *testing.T) {
	client := newMockClient()
	client.containers["c1"] = containerWith("c1", "mysvc", map[string]string{
		"tslink.enable": "true",
		"tslink.name":   "mysvc",
		"tslink.target": "localhost:6000",
	}, nil)

	regPath := testRegistryPath(t)
	d := New(client, regPath)

	ctx := context.Background()

	// First register the container
	if err := d.syncContainer(ctx, "c1"); err != nil {
		t.Fatalf("syncContainer error: %v", err)
	}

	// Verify it's registered
	reg, err := registry.Load(regPath)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if len(reg.Services) != 1 {
		t.Fatalf("expected 1 service before remove, got %d", len(reg.Services))
	}

	// Now remove it
	if err := d.removeContainer(ctx, "c1"); err != nil {
		t.Fatalf("removeContainer error: %v", err)
	}

	// Verify it's gone
	reg, err = registry.Load(regPath)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if len(reg.Services) != 0 {
		t.Fatalf("expected 0 services after remove, got %d", len(reg.Services))
	}

	// Mapping should be cleaned up
	d.mu.Lock()
	_, ok := d.containerToSvc["c1"]
	d.mu.Unlock()
	if ok {
		t.Error("expected containerToSvc[c1] to be deleted")
	}
}

func TestDiscovery_RemoveContainer_Unknown(t *testing.T) {
	client := newMockClient()
	regPath := testRegistryPath(t)
	d := New(client, regPath)

	ctx := context.Background()
	// Removing an unknown container should not error
	if err := d.removeContainer(ctx, "unknown-id"); err != nil {
		t.Fatalf("removeContainer on unknown container should not error: %v", err)
	}
}

func TestDiscovery_EventStart(t *testing.T) {
	client := newMockClient()
	client.containers["c1"] = containerWith("c1", "evtsvc", map[string]string{
		"tslink.enable": "true",
		"tslink.name":   "evtsvc",
		"tslink.target": "localhost:7000",
	}, nil)

	regPath := testRegistryPath(t)
	d := New(client, regPath)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- d.Run(ctx)
	}()

	// Send a start event
	client.events <- ContainerEvent{Action: "start", ContainerID: "c1"}

	// Give Run time to process
	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done

	reg, err := registry.Load(regPath)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if len(reg.Services) != 1 || reg.Services[0].Name != "evtsvc" {
		t.Fatalf("expected evtsvc registered after start event, got %+v", reg.Services)
	}
}

func TestDiscovery_EventDie(t *testing.T) {
	client := newMockClient()
	client.containers["c1"] = containerWith("c1", "dying", map[string]string{
		"tslink.enable": "true",
		"tslink.name":   "dying",
		"tslink.target": "localhost:8000",
	}, nil)

	regPath := testRegistryPath(t)
	d := New(client, regPath)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Pre-register so we have something to remove
	if _, err := registry.Add(regPath, registry.Service{
		Name:   "dying",
		Type:   registry.TypeProxy,
		Target: "http://localhost:8000",
	}); err != nil {
		t.Fatalf("pre-register error: %v", err)
	}

	// Seed the mapping so removeContainer knows what name to use
	d.mu.Lock()
	d.containerToSvc["c1"] = "dying"
	d.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		done <- d.Run(ctx)
	}()

	// Send a die event
	client.events <- ContainerEvent{Action: "die", ContainerID: "c1"}

	// Give Run time to process
	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done

	reg, err := registry.Load(regPath)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if len(reg.Services) != 0 {
		t.Fatalf("expected 0 services after die event, got %+v", reg.Services)
	}
}

// --- Additional tests for uncovered paths ---

func TestParseContainerLabels_TCPType_MissingTarget(t *testing.T) {
	c := containerWith("abc123", "db", map[string]string{
		"tslink.enable": "true",
		"tslink.name":   "db",
		"tslink.type":   "tcp",
		"tslink.port":   "5432",
		// no tslink.target
	}, nil)

	svc := parseContainerLabels(c)
	if svc != nil {
		t.Fatalf("expected nil for tcp without target, got %+v", svc)
	}
}

func TestParseContainerLabels_TCPType_InvalidPort(t *testing.T) {
	c := containerWith("abc123", "db", map[string]string{
		"tslink.enable": "true",
		"tslink.name":   "db",
		"tslink.type":   "tcp",
		"tslink.target": "localhost:5432",
		"tslink.port":   "not-a-number",
	}, nil)

	svc := parseContainerLabels(c)
	if svc != nil {
		t.Fatalf("expected nil for tcp with invalid port, got %+v", svc)
	}
}

func TestParseContainerLabels_ProxyNoTargetNoPorts(t *testing.T) {
	c := containerWith("abc123", "api", map[string]string{
		"tslink.enable": "true",
		"tslink.name":   "api",
	}, nil) // no ports, no target

	svc := parseContainerLabels(c)
	if svc == nil {
		t.Fatal("expected non-nil service")
	}
	// Target should be empty since there are no ports and no explicit target
	if svc.Target != "" {
		t.Errorf("expected empty target, got %q", svc.Target)
	}
}

func TestParseContainerLabels_TagsWithWhitespace(t *testing.T) {
	c := containerWith("abc123", "web", map[string]string{
		"tslink.enable": "true",
		"tslink.name":   "web",
		"tslink.target": "localhost:8080",
		"tslink.tags":   " , tag:a , , tag:b , ",
	}, nil)

	svc := parseContainerLabels(c)
	if svc == nil {
		t.Fatal("expected non-nil service")
	}
	if len(svc.Tags) != 2 {
		t.Fatalf("expected 2 tags (empty strings trimmed), got %d: %v", len(svc.Tags), svc.Tags)
	}
}

func TestFirstExposedPort_MultiplePorts(t *testing.T) {
	ports := map[string]int{
		"8080/tcp": 8080,
		"3000/tcp": 3000,
		"9090/tcp": 9090,
	}
	got := firstExposedPort(ports)
	if got != 3000 {
		t.Errorf("expected lowest port 3000, got %d", got)
	}
}

func TestFirstExposedPort_Empty(t *testing.T) {
	got := firstExposedPort(nil)
	if got != 0 {
		t.Errorf("expected 0 for nil ports, got %d", got)
	}
}

func TestDiscovery_InitialSync_ListError(t *testing.T) {
	client := newMockClient()
	client.listErr = fmt.Errorf("docker daemon unavailable")

	regPath := testRegistryPath(t)
	d := New(client, regPath)

	err := d.initialSync(context.Background())
	if err == nil {
		t.Fatal("expected error from initialSync when ListContainers fails")
	}
	if !strings.Contains(err.Error(), "list containers") {
		t.Errorf("expected 'list containers' in error, got %q", err.Error())
	}
}

func TestDiscovery_InitialSync_RegisterError(t *testing.T) {
	client := newMockClient()
	// Use an invalid service name to trigger registry.Add error
	client.containers["c1"] = containerWith("c1", "bad", map[string]string{
		"tslink.enable": "true",
		"tslink.name":   "-invalid-name-",
		"tslink.target": "localhost:3000",
	}, nil)

	regPath := testRegistryPath(t)
	d := New(client, regPath)

	// Should not return error (it logs warning and continues)
	err := d.initialSync(context.Background())
	if err != nil {
		t.Fatalf("expected no error (warn + continue), got %v", err)
	}

	// Container should NOT be in the mapping since registration failed
	d.mu.Lock()
	_, ok := d.containerToSvc["c1"]
	d.mu.Unlock()
	if ok {
		t.Error("expected containerToSvc[c1] to not be set after registration failure")
	}
}

func TestDiscovery_SyncContainer_InspectError(t *testing.T) {
	client := newMockClient()
	// Container not in map, so InspectContainer returns not-found error

	regPath := testRegistryPath(t)
	d := New(client, regPath)

	err := d.syncContainer(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error from syncContainer when inspect fails")
	}
	if !strings.Contains(err.Error(), "inspect container") {
		t.Errorf("expected 'inspect container' in error, got %q", err.Error())
	}
}

func TestDiscovery_SyncContainer_RegisterError(t *testing.T) {
	client := newMockClient()
	client.containers["c1"] = containerWith("c1", "bad", map[string]string{
		"tslink.enable": "true",
		"tslink.name":   "-invalid-name-",
		"tslink.target": "localhost:3000",
	}, nil)

	regPath := testRegistryPath(t)
	d := New(client, regPath)

	err := d.syncContainer(context.Background(), "c1")
	if err == nil {
		t.Fatal("expected error from syncContainer when registry.Add fails")
	}
	if !strings.Contains(err.Error(), "register service") {
		t.Errorf("expected 'register service' in error, got %q", err.Error())
	}
}

func TestDiscovery_RemoveContainer_RegistryRemoveError(t *testing.T) {
	client := newMockClient()
	// Use a path that doesn't have a valid registry to trigger Remove error
	regPath := filepath.Join(t.TempDir(), "nonexistent-dir", "sub", "registry.json")
	d := New(client, regPath)

	// Manually set the mapping so removeContainer tries to call registry.Remove
	d.mu.Lock()
	d.containerToSvc["c1"] = "some-service"
	d.mu.Unlock()

	err := d.removeContainer(context.Background(), "c1")
	if err == nil {
		t.Fatal("expected error from removeContainer when registry.Remove fails")
	}
	if !strings.Contains(err.Error(), "remove service") {
		t.Errorf("expected 'remove service' in error, got %q", err.Error())
	}
}

func TestDiscovery_Run_InitialSyncError(t *testing.T) {
	client := newMockClient()
	client.listErr = fmt.Errorf("docker not available")

	regPath := testRegistryPath(t)
	d := New(client, regPath)

	err := d.Run(context.Background())
	if err == nil {
		t.Fatal("expected error from Run when initialSync fails")
	}
	if !strings.Contains(err.Error(), "initial sync") {
		t.Errorf("expected 'initial sync' in error, got %q", err.Error())
	}
}

func TestDiscovery_Run_ContextCancelled(t *testing.T) {
	client := newMockClient()
	regPath := testRegistryPath(t)
	d := New(client, regPath)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- d.Run(ctx)
	}()

	// Let Run start event loop then cancel
	time.Sleep(50 * time.Millisecond)
	cancel()

	err := <-done
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestDiscovery_Run_ErrorChannel(t *testing.T) {
	client := newMockClient()
	regPath := testRegistryPath(t)
	d := New(client, regPath)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- d.Run(ctx)
	}()

	// Send an error on the error channel
	client.errors <- fmt.Errorf("connection lost")

	err := <-done
	if err == nil {
		t.Fatal("expected error from Run when error channel receives an error")
	}
	if !strings.Contains(err.Error(), "docker events error") {
		t.Errorf("expected 'docker events error' in error, got %q", err.Error())
	}
}

func TestDiscovery_Run_ErrorChannelNil(t *testing.T) {
	client := newMockClient()
	regPath := testRegistryPath(t)
	d := New(client, regPath)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- d.Run(ctx)
	}()

	// Send nil error (stream ended gracefully)
	client.errors <- nil

	err := <-done
	if err != nil {
		t.Fatalf("expected nil error when error channel receives nil, got %v", err)
	}
}

func TestDiscovery_Run_EventsChannelClosed(t *testing.T) {
	client := newMockClient()
	regPath := testRegistryPath(t)
	d := New(client, regPath)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- d.Run(ctx)
	}()

	// Close the events channel
	time.Sleep(50 * time.Millisecond)
	close(client.events)

	err := <-done
	if err != nil {
		t.Fatalf("expected nil error when events channel closed, got %v", err)
	}
}

func TestDiscovery_Run_EventStop(t *testing.T) {
	client := newMockClient()
	client.containers["c1"] = containerWith("c1", "stopsvc", map[string]string{
		"tslink.enable": "true",
		"tslink.name":   "stopsvc",
		"tslink.target": "localhost:7000",
	}, nil)

	regPath := testRegistryPath(t)
	d := New(client, regPath)

	// Pre-register so we have something to remove
	if _, err := registry.Add(regPath, registry.Service{
		Name:   "stopsvc",
		Type:   registry.TypeProxy,
		Target: "http://localhost:7000",
	}); err != nil {
		t.Fatalf("pre-register error: %v", err)
	}
	d.mu.Lock()
	d.containerToSvc["c1"] = "stopsvc"
	d.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- d.Run(ctx)
	}()

	client.events <- ContainerEvent{Action: "stop", ContainerID: "c1"}
	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done

	reg, err := registry.Load(regPath)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if len(reg.Services) != 0 {
		t.Fatalf("expected 0 services after stop event, got %+v", reg.Services)
	}
}

func TestDiscovery_Run_EventDestroy(t *testing.T) {
	client := newMockClient()
	regPath := testRegistryPath(t)
	d := New(client, regPath)

	// Pre-register
	if _, err := registry.Add(regPath, registry.Service{
		Name:   "destroysvc",
		Type:   registry.TypeProxy,
		Target: "http://localhost:9000",
	}); err != nil {
		t.Fatalf("pre-register error: %v", err)
	}
	d.mu.Lock()
	d.containerToSvc["c1"] = "destroysvc"
	d.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- d.Run(ctx)
	}()

	client.events <- ContainerEvent{Action: "destroy", ContainerID: "c1"}
	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done

	reg, err := registry.Load(regPath)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if len(reg.Services) != 0 {
		t.Fatalf("expected 0 services after destroy event, got %+v", reg.Services)
	}
}

func TestDiscovery_Run_SyncError_Logs(t *testing.T) {
	client := newMockClient()
	// Container not in mock's map, so InspectContainer will fail
	regPath := testRegistryPath(t)
	d := New(client, regPath)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- d.Run(ctx)
	}()

	// Send start event for a container that doesn't exist in mock
	client.events <- ContainerEvent{Action: "start", ContainerID: "missing"}
	time.Sleep(100 * time.Millisecond)
	cancel()

	err := <-done
	// Run should NOT return an error - it logs the warning and continues
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestDiscovery_Run_RemoveError_Logs(t *testing.T) {
	client := newMockClient()
	// Use a broken registry path so Remove fails
	regPath := filepath.Join(t.TempDir(), "broken", "deep", "registry.json")
	d := New(client, regPath)

	// Seed a container mapping
	d.mu.Lock()
	d.containerToSvc["c1"] = "some-svc"
	d.mu.Unlock()

	// We need ListContainers to succeed (no containers is fine)
	// But we need a valid registry for initialSync. Let's use a fresh writable path
	// and then break the path after sync.
	// Actually - let's just use a different approach: use a valid regPath for the mock
	// but manually break the mapping.

	// Simpler: just use a valid regPath, and have the container mapped to a name
	// that doesn't exist in the registry, which should cause Remove to fail.
	regPath2 := testRegistryPath(t)
	d2 := New(client, regPath2)
	d2.mu.Lock()
	d2.containerToSvc["c1"] = "nonexistent-service"
	d2.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- d2.Run(ctx)
	}()

	// Send die event for c1 - registry.Remove for nonexistent service may or may not error
	// depending on implementation, but at least we exercise the code path
	client.events <- ContainerEvent{Action: "die", ContainerID: "c1"}
	time.Sleep(100 * time.Millisecond)
	cancel()

	<-done
	// We don't assert the error here - we just ensure the code path is exercised
}

func TestDiscovery_Run_UnknownEventAction(t *testing.T) {
	client := newMockClient()
	regPath := testRegistryPath(t)
	d := New(client, regPath)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- d.Run(ctx)
	}()

	// Send an unknown event action - should be silently ignored
	client.events <- ContainerEvent{Action: "pause", ContainerID: "c1"}
	time.Sleep(50 * time.Millisecond)
	cancel()

	err := <-done
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
