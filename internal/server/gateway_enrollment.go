package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/anydoor7/tslink/internal/atomicfile"
	"github.com/anydoor7/tslink/internal/registry"
)

// The marker is evidence of a completed persistent enrollment, not a desired
// config bit. A changed auth identity needs a fresh state directory because
// tsnet ignores new auth keys when it finds an enrolled node in that directory.
type gatewayEnrollment struct {
	NodeID       string   `json:"node_id"`
	Credentialed bool     `json:"credentialed"`
	Tags         []string `json:"tags"`
	ControlURL   string   `json:"control_url"`
}

const gatewayEnrollmentFile = "persistent-enrollment.json"

func gatewayEnrollmentIdentity(credentialed bool, tags []string, controlURL string) gatewayEnrollment {
	tags = append([]string(nil), tags...)
	sort.Strings(tags)
	if controlURL == "" {
		controlURL = os.Getenv("TS_CONTROL_URL")
	}
	return gatewayEnrollment{Credentialed: credentialed, Tags: tags, ControlURL: controlURL}
}

func readGatewayEnrollment(dir string) (gatewayEnrollment, error) {
	path := filepath.Join(dir, gatewayEnrollmentFile)
	if err := atomicfile.ConvergePrivateFile(path); err != nil {
		return gatewayEnrollment{}, err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return gatewayEnrollment{}, nil
	}
	if err != nil {
		return gatewayEnrollment{}, err
	}
	var marker gatewayEnrollment
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&marker); err != nil {
		return marker, err
	}
	if marker.NodeID == "" || len(marker.NodeID) > 256 {
		return marker, fmt.Errorf("invalid persistent enrollment marker")
	}
	// Reject extra JSON rather than trusting a valid prefix.
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return marker, fmt.Errorf("invalid persistent enrollment marker suffix")
	}
	return marker, nil
}

func sameGatewayEnrollment(a, b gatewayEnrollment) bool {
	a.NodeID, b.NodeID = "", ""
	return reflect.DeepEqual(a, b)
}

func writeGatewayEnrollment(dir string, marker gatewayEnrollment) error {
	b, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(filepath.Join(dir, gatewayEnrollmentFile), append(b, '\n'))
}

// Validate the scope before removing any tsnet state. Keep the old directory
// under a recovery name until the separate gateway ownership ledger has been
// durably copied. A crash at any step resumes this copy before removing backup;
// accepted shares' historical NodeIDs must never disappear with tsnet state.
func resetGatewayNodeState(configDir, name string) error {
	if strings.TrimSpace(configDir) == "" {
		return fmt.Errorf("gateway state requires a config directory")
	}
	if err := registry.ValidateName(name); err != nil {
		return err
	}
	parent := filepath.Join(configDir, "portal-nodes")
	if err := atomicfile.EnsurePrivateDir(parent); err != nil {
		return err
	}
	dir := filepath.Join(parent, name)
	backup := filepath.Join(parent, name+".reenroll")
	recover := func() error {
		if _, err := os.Lstat(backup); errors.Is(err, os.ErrNotExist) {
			return nil
		} else if err != nil {
			return err
		}
		if err := atomicfile.EnsurePrivateDir(backup); err != nil {
			return err
		}
		if err := atomicfile.EnsurePrivateDir(dir); err != nil {
			return err
		}
		source := filepath.Join(backup, "ownership.json")
		if err := atomicfile.ConvergePrivateFile(source); err != nil {
			return err
		}
		data, err := os.ReadFile(source)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil {
			target := filepath.Join(dir, "ownership.json")
			if err := atomicfile.ConvergePrivateFile(target); err != nil {
				return err
			}
			current, readErr := os.ReadFile(target)
			if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
				return readErr
			}
			if readErr == nil && !bytes.Equal(data, current) {
				return fmt.Errorf("gateway ownership recovery conflicts")
			}
			if err := atomicfile.WriteFile(target, data); err != nil {
				return err
			}
		}
		return os.RemoveAll(backup)
	}
	if err := recover(); err != nil {
		return err
	}
	if err := atomicfile.EnsurePrivateDir(dir); err != nil {
		return err
	}
	if err := os.Rename(dir, backup); err != nil {
		return err
	}
	return recover()
}

func prepareGatewayEnrollment(configDir, name string, identity gatewayEnrollment) (gatewayEnrollment, error) {
	if strings.TrimSpace(configDir) == "" {
		return gatewayEnrollment{}, fmt.Errorf("gateway state requires a config directory")
	}
	if err := registry.ValidateName(name); err != nil {
		return gatewayEnrollment{}, err
	}
	parent := filepath.Join(configDir, "portal-nodes")
	if err := atomicfile.EnsurePrivateDir(parent); err != nil {
		return gatewayEnrollment{}, err
	}
	dir := filepath.Join(parent, name)
	if err := atomicfile.EnsurePrivateDir(dir); err != nil {
		return gatewayEnrollment{}, err
	}
	marker, err := readGatewayEnrollment(dir)
	if err != nil {
		return marker, err
	}
	if marker.NodeID != "" && sameGatewayEnrollment(marker, identity) {
		return marker, nil
	}
	if identity.Credentialed {
		if err := resetGatewayNodeState(configDir, name); err != nil {
			return marker, err
		}
	}
	return gatewayEnrollment{}, nil
}
