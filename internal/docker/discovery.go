package docker

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"

	"github.com/monody0007/tslink/internal/registry"
)

const (
	labelEnable    = "tslink.enable"
	labelName      = "tslink.name"
	labelType      = "tslink.type"
	labelTarget    = "tslink.target"
	labelPort      = "tslink.port"
	labelEphemeral = "tslink.ephemeral"
	labelTags      = "tslink.tags"
)

// ContainerInfo holds the relevant info extracted from a Docker container.
type ContainerInfo struct {
	ID     string
	Name   string
	Labels map[string]string
	// Ports maps container port spec to host port (e.g., "3000/tcp" -> 8080)
	Ports map[string]int
	State string // "running", "exited", etc.
}

// ContainerEvent represents a Docker container lifecycle event.
type ContainerEvent struct {
	Action      string // "start", "stop", "die", "destroy"
	ContainerID string
}

// DockerClient is the interface for Docker operations.
type DockerClient interface {
	// ListContainers returns all running containers.
	ListContainers(ctx context.Context) ([]ContainerInfo, error)
	// InspectContainer returns info about a specific container.
	InspectContainer(ctx context.Context, id string) (ContainerInfo, error)
	// Events returns a channel of container events.
	Events(ctx context.Context) (<-chan ContainerEvent, <-chan error)
}

// Discovery watches Docker containers and auto-registers/unregisters services.
type Discovery struct {
	client           DockerClient
	regPath          string
	mu               sync.Mutex
	containerToSvc   map[string]string // containerID -> service name
}

// New creates a new Discovery instance.
func New(client DockerClient, regPath string) *Discovery {
	return &Discovery{
		client:         client,
		regPath:        regPath,
		containerToSvc: make(map[string]string),
	}
}

// parseContainerLabels extracts TSLink config from container labels.
// Returns nil if tslink.enable is not "true" or if required labels are missing.
func parseContainerLabels(c ContainerInfo) *registry.Service {
	if c.Labels[labelEnable] != "true" {
		return nil
	}

	name := c.Labels[labelName]
	if name == "" {
		return nil
	}

	svcType := c.Labels[labelType]
	if svcType == "" {
		svcType = registry.TypeProxy
	}

	var target string
	var port int

	switch svcType {
	case registry.TypeProxy:
		target = c.Labels[labelTarget]
		if target == "" {
			// Default to first exposed port
			firstPort := firstExposedPort(c.Ports)
			if firstPort > 0 {
				target = fmt.Sprintf("localhost:%d", firstPort)
			}
		}
		// Prepend http:// if no scheme present
		if target != "" && !strings.Contains(target, "://") {
			target = "http://" + target
		}

	case registry.TypeTCP:
		target = c.Labels[labelTarget]
		portStr := c.Labels[labelPort]
		if portStr != "" {
			p, err := strconv.Atoi(portStr)
			if err == nil {
				port = p
			}
		}
		// For TCP type both target and port are required
		if target == "" || port == 0 {
			return nil
		}
	}

	ephemeral := c.Labels[labelEphemeral] == "true"

	var tags []string
	if tagsRaw := c.Labels[labelTags]; tagsRaw != "" {
		for _, t := range strings.Split(tagsRaw, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				tags = append(tags, t)
			}
		}
	}

	svc := &registry.Service{
		Name:      name,
		Type:      svcType,
		Target:    target,
		Port:      port,
		Ephemeral: ephemeral,
		Tags:      tags,
	}

	return svc
}

// firstExposedPort returns the lowest host port from the container's port map.
// Returns 0 if no ports are mapped.
func firstExposedPort(ports map[string]int) int {
	lowest := 0
	for _, hostPort := range ports {
		if lowest == 0 || hostPort < lowest {
			lowest = hostPort
		}
	}
	return lowest
}

// initialSync lists all running containers and registers enabled ones.
func (d *Discovery) initialSync(ctx context.Context) error {
	containers, err := d.client.ListContainers(ctx)
	if err != nil {
		return fmt.Errorf("list containers: %w", err)
	}

	for _, c := range containers {
		svc := parseContainerLabels(c)
		if svc == nil {
			continue
		}

		if _, err := registry.Add(d.regPath, *svc); err != nil {
			slog.Warn("docker discovery: failed to register container",
				"container_id", c.ID,
				"service", svc.Name,
				"error", err,
			)
			continue
		}

		d.mu.Lock()
		d.containerToSvc[c.ID] = svc.Name
		d.mu.Unlock()

		slog.Info("docker discovery: registered container",
			"container_id", c.ID,
			"service", svc.Name,
		)
	}

	return nil
}

// syncContainer registers or updates a container's service.
func (d *Discovery) syncContainer(ctx context.Context, containerID string) error {
	info, err := d.client.InspectContainer(ctx, containerID)
	if err != nil {
		return fmt.Errorf("inspect container %s: %w", containerID, err)
	}

	svc := parseContainerLabels(info)
	if svc == nil {
		// Not enabled, skip silently
		return nil
	}

	if _, err := registry.Add(d.regPath, *svc); err != nil {
		return fmt.Errorf("register service %s: %w", svc.Name, err)
	}

	d.mu.Lock()
	d.containerToSvc[containerID] = svc.Name
	d.mu.Unlock()

	slog.Info("docker discovery: synced container",
		"container_id", containerID,
		"service", svc.Name,
	)

	return nil
}

// removeContainer unregisters a container's service.
func (d *Discovery) removeContainer(ctx context.Context, containerID string) error {
	d.mu.Lock()
	name, ok := d.containerToSvc[containerID]
	if ok {
		delete(d.containerToSvc, containerID)
	}
	d.mu.Unlock()

	if !ok {
		// Unknown container, nothing to do
		return nil
	}

	if err := registry.Remove(d.regPath, name); err != nil {
		return fmt.Errorf("remove service %s: %w", name, err)
	}

	slog.Info("docker discovery: removed container",
		"container_id", containerID,
		"service", name,
	)

	return nil
}

// Run starts watching Docker events and syncing services.
// It first does an initial sync, then watches for container start/stop events.
// Run blocks until ctx is cancelled or a fatal error occurs.
func (d *Discovery) Run(ctx context.Context) error {
	if err := d.initialSync(ctx); err != nil {
		return fmt.Errorf("initial sync: %w", err)
	}

	events, errs := d.client.Events(ctx)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case err := <-errs:
			if err != nil {
				return fmt.Errorf("docker events error: %w", err)
			}
			// nil error on the channel means the stream ended
			return nil

		case ev, ok := <-events:
			if !ok {
				// Channel closed, done
				return nil
			}

			switch ev.Action {
			case "start":
				if err := d.syncContainer(ctx, ev.ContainerID); err != nil {
					slog.Warn("docker discovery: sync failed",
						"container_id", ev.ContainerID,
						"error", err,
					)
				}

			case "die", "stop", "destroy":
				if err := d.removeContainer(ctx, ev.ContainerID); err != nil {
					slog.Warn("docker discovery: remove failed",
						"container_id", ev.ContainerID,
						"error", err,
					)
				}
			}
		}
	}
}
