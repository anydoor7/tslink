package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/registry"
)

// RemoveServiceNodeState deletes the tsnet state directory a service kept inside
// configDir.
//
// configDir is a parameter rather than something this function reads from the
// environment, and the callers derive it from the registry path they just
// wrote. That is what keeps the deletion inside the same config directory as
// the decision that authorised it: a caller operating on one registry while
// config.Dir() resolves to another would otherwise delete a live service's node
// identity out of an unrelated installation, and the deletion would look
// entirely normal from inside that caller.
//
// The name is validated as a registry service name before anything is removed.
// Callers reach this with a name that came out of the ownership ledger or off a
// command line, and a registry service name is a DNS label -- no separator, no
// dot segment, no empty string -- so the validation is what keeps a malformed
// or hostile name from turning a RemoveAll of one directory into a RemoveAll of
// the nodes directory itself, or of something beside it.
//
// It is deliberately not a decision about *whether* removing the state is safe.
// That decision needs facts this package does not have (was the remote node
// actually deleted, is a tsnet server still holding the directory), so it stays
// with the callers that have them.
func RemoveServiceNodeState(configDir, name string) error {
	if err := registry.ValidateName(name); err != nil {
		return fmt.Errorf("refusing to remove node state for an invalid service name: %w", err)
	}
	if strings.TrimSpace(configDir) == "" {
		return fmt.Errorf("refusing to remove node state for %q without a config directory", name)
	}
	return os.RemoveAll(filepath.Join(config.NodesDirIn(configDir), name))
}

// ServiceNodeStateConfigDir maps a registry path to the config directory that
// owns it. Registry and node state are siblings by construction, so this is the
// one derivation both callers use.
func ServiceNodeStateConfigDir(registryPath string) string {
	if strings.TrimSpace(registryPath) == "" {
		return ""
	}
	return filepath.Dir(registryPath)
}
