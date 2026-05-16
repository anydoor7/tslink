package tailapi

import (
	"context"
	"fmt"

	"github.com/monody0007/tslink/internal/credentials"
	"github.com/monody0007/tslink/internal/registry"
)

// DefaultTag is the default ACL tag applied to tslink services.
const DefaultTag = "tag:tsmain"

// aclClientFn is a testable seam for creating the Tailscale API client.
var aclClientFn = credentials.NewTailscaleClient

// ReadTags returns all tag names from the tailnet ACL tagOwners.
// Returns (nil, nil) if no API client is available.
func ReadTags(ctx context.Context) ([]string, error) {
	client, err := aclClientFn()
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, nil
	}

	acl, err := client.PolicyFile().Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("read ACL: %w", err)
	}

	var tags []string
	for tag := range acl.TagOwners {
		tags = append(tags, tag)
	}
	return tags, nil
}

// EnsureTags checks that the given tags exist in the ACL tagOwners.
// Missing tags are created with owner ["autogroup:admin"].
// Returns ErrNoAPIClient for non-empty tag input if no API client is available.
func EnsureTags(ctx context.Context, tags []string) error {
	for _, tag := range tags {
		if err := registry.ValidateTag(tag); err != nil {
			return err
		}
	}
	if len(tags) == 0 {
		return nil
	}

	client, err := aclClientFn()
	if err != nil {
		return err
	}
	if client == nil {
		return ErrNoAPIClient
	}

	acl, err := client.PolicyFile().Get(ctx)
	if err != nil {
		return fmt.Errorf("read ACL: %w", err)
	}

	if acl.TagOwners == nil {
		acl.TagOwners = make(map[string][]string)
	}

	changed := false
	for _, tag := range tags {
		if _, exists := acl.TagOwners[tag]; !exists {
			acl.TagOwners[tag] = []string{"autogroup:admin"}
			changed = true
		}
	}

	if !changed {
		return nil
	}

	if err := client.PolicyFile().Set(ctx, *acl, acl.ETag); err != nil {
		return fmt.Errorf("update ACL: %w", err)
	}
	return nil
}

// DeleteTag removes a tag from the ACL tagOwners.
// Returns an error if no API client is available or the tag does not exist.
func DeleteTag(ctx context.Context, tag string) error {
	if err := registry.ValidateTag(tag); err != nil {
		return err
	}

	client, err := aclClientFn()
	if err != nil {
		return err
	}
	if client == nil {
		return fmt.Errorf("no API client available")
	}

	acl, err := client.PolicyFile().Get(ctx)
	if err != nil {
		return fmt.Errorf("read ACL: %w", err)
	}

	if _, exists := acl.TagOwners[tag]; !exists {
		return fmt.Errorf("tag %q not found in tailnet ACL", tag)
	}

	delete(acl.TagOwners, tag)

	if err := client.PolicyFile().Set(ctx, *acl, acl.ETag); err != nil {
		return fmt.Errorf("update ACL: %w", err)
	}
	return nil
}
