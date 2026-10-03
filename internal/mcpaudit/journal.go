// Package mcpaudit stores bounded mutation receipts consumed by the access log.
package mcpaudit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/anydoor7/tslink/internal/atomicfile"
	"github.com/anydoor7/tslink/internal/filelock"
	"github.com/anydoor7/tslink/internal/mcpscope"
)

const MaxEntries = 1024
const MaxBytes = 1024 * 1024

// Entry contains identifiers and stable results only: never raw arguments,
// errors, invitation links, targets, tokens, or response bodies.
type Entry struct {
	Kind           string            `json:"kind"`
	ID             string            `json:"id"`
	Time           time.Time         `json:"time"`
	Identity       mcpscope.Identity `json:"identity"`
	Principal      string            `json:"principal"`
	Role           string            `json:"role"`
	Phase          string            `json:"phase"`
	Capabilities   mcpscope.Scope    `json:"capabilities"`
	ScopeExpiresAt *time.Time        `json:"scope_expires_at,omitempty"`
	Tool           string            `json:"tool"`
	Apps           []string          `json:"apps"`
	Result         string            `json:"result"`
	Surface        string            `json:"surface,omitempty"`
	Changes        []Change          `json:"changes,omitempty"`
}

type Journal struct{ Path string }

// UnmarshalJSON preserves attribution from the development journal format.
// Historical principal tags do not establish a missing caller identity.
func (e *Entry) UnmarshalJSON(data []byte) error {
	type entry Entry
	var decoded struct {
		entry
		Who   string `json:"who"`
		Scope string `json:"scope"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	if decoded.Principal == "" {
		decoded.Principal = decoded.Who
	}
	if decoded.Role == "" {
		decoded.Role = decoded.Scope
	}
	*e = Entry(decoded.entry)
	return nil
}

func (j Journal) Read() ([]Entry, error) {
	info, err := os.Lstat(j.Path)
	if os.IsNotExist(err) {
		// Windows also reports not-exist for a path below a regular file.
		for dir := filepath.Dir(j.Path); ; dir = filepath.Dir(dir) {
			parent, parentErr := os.Stat(dir)
			if parentErr == nil {
				if !parent.IsDir() {
					return nil, fmt.Errorf("MCP journal parent is not a directory")
				}
				break
			}
			if runtime.GOOS != "windows" || !os.IsNotExist(parentErr) || filepath.Dir(dir) == dir {
				break
			}
		}
		return []Entry{}, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > MaxBytes {
		return nil, fmt.Errorf("unsafe or oversized MCP journal")
	}
	f, err := os.Open(j.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxBytes {
		return nil, fmt.Errorf("oversized MCP journal")
	}
	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	if entries == nil {
		return nil, fmt.Errorf("invalid MCP journal: expected array")
	}
	if len(entries) > MaxEntries {
		return nil, fmt.Errorf("oversized MCP journal")
	}
	return entries, nil
}

// Record serializes writers across stdio processes and the daemon, with a
// bounded lock wait. Invalid/partial state is preserved and fails explicitly.
func (j Journal) Record(ctx context.Context, entry Entry) error {
	encoded, err := json.Marshal(entry)
	if err != nil || len(encoded) > 16384 {
		return fmt.Errorf("invalid MCP audit entry")
	}
	if err := atomicfile.EnsurePrivateDir(filepath.Dir(j.Path)); err != nil {
		return err
	}
	lockPath := j.Path + ".lock"
	if err := atomicfile.ConvergePrivateFile(lockPath); err != nil {
		return err
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for {
		locked, err := filelock.TryLock(f)
		if err != nil {
			return err
		}
		if locked {
			break
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	defer filelock.Unlock(f)
	if err := ctx.Err(); err != nil {
		return err
	}
	entries, err := j.Read()
	if err != nil {
		return err
	}
	entries = append(entries, entry)
	if len(entries) > MaxEntries {
		entries = entries[len(entries)-MaxEntries:]
	}
	for {
		data, err := json.Marshal(entries)
		if err != nil {
			return err
		}
		if len(data) <= MaxBytes {
			return atomicfile.WriteFile(j.Path, data)
		}
		if len(entries) <= 1 {
			return errors.New("MCP audit entry exceeds journal bound")
		}
		entries = entries[1:]
	}
}
