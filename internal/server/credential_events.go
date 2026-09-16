package server

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/monody0007/tslink/internal/config"
)

// credentialStateDebounce collapses the burst of filesystem events one
// credential write produces.
//
// An atomic save arrives as a Create plus a Rename, a delete can arrive as
// Remove plus Chmod, and `tslink login` touches the value file and the metadata
// file as one transaction. Answering each event separately would push several
// frames for one logical change; waiting a beat and then asking what the state
// is now answers it once. The delay is started by an event and never by a
// clock, so nothing here polls.
const credentialStateDebounce = 200 * time.Millisecond

// credentialStatePaths are the config-directory files whose contents decide the
// credential half of the control-plane status payload: whether a credential is
// stored, and whether an interactive enrollment is pending.
//
// The daemon already watches this whole directory for registry.json, so these
// events are being delivered today and discarded by the path filter. Watching
// files rather than hooking the writers is what makes cross-process changes
// visible: `tslink login` and `tslink logout` run in their own process and have
// no channel to the daemon, while the daemon's own auth-handoff writes land in
// the same directory and are picked up by the same rule.
//
// runtime.json is deliberately not here. It already publishes from
// writeRuntimeSnapshotLocked, and adding it would push two frames per change.
//
// credential-upgrade-pending.json is also not here, and that exclusion is a
// contract rather than an oversight: it is the one other file the login
// transaction writes into this directory, and it is absent only because no
// field of the event payload reads it today (status does not; the daemon
// consumes it on node rebuild). If the payload ever gains an upgrade-pending
// field, this list must gain the file in the same change -- otherwise login
// stops producing a frame for a state the client can now see, which is exactly
// the shape of the gap this file was added to close, and it fails silently.
func (s *Server) credentialStatePaths() map[string]struct{} {
	names := []string{
		config.AuthHandoffFileName,
		config.APIKeyFileName,
		config.ClientSecretFileName,
		config.CredentialMetaFileName,
	}
	paths := make(map[string]struct{}, len(names))
	for _, name := range names {
		paths[filepath.Join(filepath.Clean(s.cfgDir), name)] = struct{}{}
	}
	return paths
}

// credentialStateDigest hashes the credential files so an event that changed
// nothing can be told from one that did.
//
// Absence is part of the state and is hashed as such: logout removes files, and
// a digest that could not distinguish "gone" from "unchanged" would drop the
// one change a client most needs to see. The digest of a credential value never
// leaves this process — it is only ever compared with the previous digest — and
// the credential metadata file already stores a fingerprint of the same kind.
func credentialStateDigest(paths map[string]struct{}) string {
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)

	digest := sha256.New()
	for _, path := range ordered {
		fmt.Fprintf(digest, "%s\x00", filepath.Base(path))
		content, err := os.ReadFile(path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			digest.Write([]byte("absent\x00"))
		case err != nil:
			// A file that is present but unreadable is its own state, and a
			// distinct one: it is not absence, and pretending it is would make
			// a permissions regression look like a logout.
			digest.Write([]byte("unreadable\x00"))
		default:
			sum := sha256.Sum256(content)
			digest.Write(sum[:])
			digest.Write([]byte{0})
		}
	}
	return hex.EncodeToString(digest.Sum(nil))
}

// primeCredentialState records the current digest without publishing. It runs
// before the watcher starts so the first real change is the first frame, and a
// spurious event at startup is not.
func (s *Server) primeCredentialState() {
	digest := credentialStateDigest(s.credentialStatePaths())
	s.credentialStateMu.Lock()
	s.credentialStateDigest = digest
	s.credentialStateKnown = true
	s.credentialStateMu.Unlock()
}

// notifyCredentialStateChanged publishes only when the credential files now
// hash differently than they did the last time this was asked.
//
// Returns whether it published, which is what the tests assert on: "no frame"
// and "a frame nobody can distinguish from the last one" look the same from
// outside.
func (s *Server) notifyCredentialStateChanged() bool {
	digest := credentialStateDigest(s.credentialStatePaths())
	s.credentialStateMu.Lock()
	unchanged := s.credentialStateKnown && s.credentialStateDigest == digest
	s.credentialStateDigest = digest
	s.credentialStateKnown = true
	s.credentialStateMu.Unlock()
	if unchanged {
		return false
	}
	s.events.publish()
	return true
}
