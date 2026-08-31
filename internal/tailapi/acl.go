package tailapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/monody0007/tslink/internal/credentials"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/tailscale/hujson"
	tailscale "tailscale.com/client/tailscale/v2"
)

// DefaultTag is the default ACL tag applied to tslink services.
const DefaultTag = "tag:tsmain"

// FunnelNodeAttr is the policy-file attribute that authorizes Funnel nodes.
const FunnelNodeAttr = "funnel"

const (
	PolicyWriteNotAttempted = "not_attempted"
	PolicyWriteUnchanged    = "unchanged"
	PolicyWriteChanged      = "changed"
	PolicyWriteRejected     = "rejected"
	PolicyWriteUnknown      = "unknown"
)

// ErrPolicyConflict reports that the policy ETag changed between read and
// write. Callers may retry the entire read-modify-write operation.
var ErrPolicyConflict = errors.New("tailnet policy changed concurrently; retry the operation")

// ErrPolicyAccessDenied reports that the configured API credential can reach
// the tailnet policy endpoint but lacks permission to read or update it.
var ErrPolicyAccessDenied = errors.New("tailnet policy access forbidden")

// ErrTailnetHTTPSDisabled reports the non-policy Funnel prerequisite without
// issuing an ACL write. Enabling it requires the tailnet settings API and the
// networking_settings OAuth scope; nodeAttrs cannot express this setting.
var ErrTailnetHTTPSDisabled = errors.New("tailnet HTTPS is disabled")

// ErrTailnetSettingsUnavailable means TSLink refused to guess whether HTTPS
// is enabled. An ACL POST is unsafe until the non-policy prerequisite can be
// read with the networking_settings OAuth scope.
var ErrTailnetSettingsUnavailable = errors.New("tailnet settings unavailable")

// aclClientFn is a testable seam for creating the Tailscale API client.
var aclClientFn = credentials.NewTailscaleClient

// policyMutationMu serializes process-local policy read-modify-write cycles.
// The ETag remains the cross-process concurrency guard.
var policyMutationMu sync.Mutex

// FunnelPolicyRequest describes one fused Funnel policy transaction. Tags are
// ordinary tagOwners entries that may be created with the legacy admin owner;
// Target is the shared Funnel tag and Owners are existing tag identities held
// by the OAuth clients that must be able to mint keys for Target.
type FunnelPolicyRequest struct {
	Tags   []string
	Target string
	Owners []string
}

// PolicyMutationResult distinguishes a confirmed change from an idempotent
// no-op and from a write whose server-side outcome could not be observed.
type PolicyMutationResult struct {
	Changed      bool
	WriteOutcome string
}

// ReadTags returns user-managed tag names from the tailnet ACL tagOwners. The
// shared Funnel plumbing tag is deliberately hidden from `tslink tags pull` so
// it is not presented as a user tag.
// Returns ErrNoAPIClient if no API client is available.
func ReadTags(ctx context.Context) ([]string, error) {
	client, err := aclClientFn()
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, ErrNoAPIClient
	}

	acl, err := client.PolicyFile().Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("read ACL: %w", err)
	}

	var tags []string
	for tag := range acl.TagOwners {
		if tag == registry.FunnelTag {
			continue
		}
		tags = append(tags, tag)
	}
	return tags, nil
}

// EnsureTags checks that the given tags exist in the ACL tagOwners. Missing
// tags are created with owner ["autogroup:admin"]. The Raw/Set path and
// surgical HuJSON AST edit preserve comments, formatting, and unknown keys.
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

	policyMutationMu.Lock()
	defer policyMutationMu.Unlock()

	client, err := policyClient()
	if err != nil {
		return err
	}
	raw, err := client.PolicyFile().Raw(ctx)
	if err != nil {
		if policyAccessDenied(err) {
			return fmt.Errorf("%w: read ACL: %v", ErrPolicyAccessDenied, err)
		}
		return fmt.Errorf("read ACL: %w", err)
	}

	doc, err := parsePolicyDocument(raw.HuJSON)
	if err != nil {
		return fmt.Errorf("parse HuJSON ACL without rewriting it: %w", err)
	}
	changed, err := doc.ensureTagOwners(tags, []string{"autogroup:admin"}, false)
	if err != nil {
		return fmt.Errorf("patch tagOwners without rewriting unrelated policy: %w", err)
	}
	if !changed {
		return nil
	}

	_, err = setRawPolicy(ctx, client, doc.pack(), raw.ETag, "tagOwners")
	return err
}

// EnsureFunnelAttr performs the complete Funnel policy provisioning in one
// Raw -> surgical patch -> Set transaction: ordinary requested tags, the
// shared tagOwners rule with caller-derived owner tags, and the exact-target
// nodeAttrs grant. It never modifies a grant whose target is not exactly the
// requested target. Existing wildcard or broader Funnel grants are recognized
// as coverage and left byte-for-byte untouched.
func EnsureFunnelAttr(ctx context.Context, request FunnelPolicyRequest) (PolicyMutationResult, error) {
	if request.Target == "" {
		return PolicyMutationResult{WriteOutcome: PolicyWriteNotAttempted}, fmt.Errorf("Funnel node attribute target is empty")
	}
	if err := registry.ValidateTag(request.Target); err != nil {
		return PolicyMutationResult{WriteOutcome: PolicyWriteNotAttempted}, err
	}
	for _, tag := range request.Tags {
		if err := registry.ValidateTag(tag); err != nil {
			return PolicyMutationResult{WriteOutcome: PolicyWriteNotAttempted}, err
		}
	}
	owners := dedupeStrings(request.Owners)
	if len(owners) == 0 {
		return PolicyMutationResult{WriteOutcome: PolicyWriteNotAttempted}, fmt.Errorf("Funnel tag %q has no usable existing tag owner", request.Target)
	}
	for _, owner := range owners {
		if err := registry.ValidateTag(owner); err != nil {
			return PolicyMutationResult{WriteOutcome: PolicyWriteNotAttempted}, fmt.Errorf("invalid Funnel tag owner %q: %w", owner, err)
		}
		if owner == request.Target {
			return PolicyMutationResult{WriteOutcome: PolicyWriteNotAttempted}, fmt.Errorf("Funnel tag %q cannot own itself", request.Target)
		}
	}

	policyMutationMu.Lock()
	defer policyMutationMu.Unlock()

	client, err := policyClient()
	if err != nil {
		return PolicyMutationResult{WriteOutcome: PolicyWriteNotAttempted}, err
	}
	settings, err := client.TailnetSettings().Get(ctx)
	if err != nil {
		return PolicyMutationResult{WriteOutcome: PolicyWriteNotAttempted}, fmt.Errorf("read tailnet settings before Funnel policy mutation; OAuth scope networking_settings is required: %w", errors.Join(ErrTailnetSettingsUnavailable, err))
	}
	if !settings.HTTPSEnabled {
		return PolicyMutationResult{WriteOutcome: PolicyWriteNotAttempted}, fmt.Errorf("%w; enable it with PATCH /api/v2/tailnet/{tailnet}/settings and OAuth scope networking_settings", ErrTailnetHTTPSDisabled)
	}
	raw, err := client.PolicyFile().Raw(ctx)
	if err != nil {
		return PolicyMutationResult{WriteOutcome: PolicyWriteNotAttempted}, fmt.Errorf("read HuJSON ACL for Funnel provisioning: %w", err)
	}

	doc, err := parsePolicyDocument(raw.HuJSON)
	if err != nil {
		return PolicyMutationResult{WriteOutcome: PolicyWriteNotAttempted}, fmt.Errorf("parse HuJSON ACL without rewriting it: %w", err)
	}
	changed := false
	ordinaryTags := make([]string, 0, len(request.Tags))
	for _, tag := range request.Tags {
		if tag != request.Target {
			ordinaryTags = append(ordinaryTags, tag)
		}
	}
	if len(ordinaryTags) > 0 {
		tagsChanged, patchErr := doc.ensureTagOwners(ordinaryTags, []string{"autogroup:admin"}, false)
		if patchErr != nil {
			return PolicyMutationResult{WriteOutcome: PolicyWriteNotAttempted}, fmt.Errorf("patch ordinary tagOwners without rewriting unrelated policy: %w", patchErr)
		}
		changed = changed || tagsChanged
	}
	funnelOwnerChanged, err := doc.ensureTagOwners([]string{request.Target}, owners, true)
	if err != nil {
		return PolicyMutationResult{WriteOutcome: PolicyWriteNotAttempted}, fmt.Errorf("patch Funnel tagOwners without rewriting unrelated policy: %w", err)
	}
	changed = changed || funnelOwnerChanged
	funnelAttrChanged, err := doc.ensureFunnelAttr(request.Target)
	if err != nil {
		return PolicyMutationResult{WriteOutcome: PolicyWriteNotAttempted}, fmt.Errorf("patch exact Funnel nodeAttrs grant without rewriting unrelated policy: %w", err)
	}
	changed = changed || funnelAttrChanged
	if !changed {
		return PolicyMutationResult{WriteOutcome: PolicyWriteUnchanged}, nil
	}

	result, err := setRawPolicy(ctx, client, doc.pack(), raw.ETag, "Funnel tagOwners and nodeAttrs")
	if err != nil {
		return result, err
	}
	slog.Info("updated tailnet policy for Funnel", "target", request.Target, "owners", owners, "attribute", FunnelNodeAttr)
	return result, nil
}

func policyClient() (*tailscale.Client, error) {
	client, err := aclClientFn()
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, ErrNoAPIClient
	}
	return client, nil
}

func setRawPolicy(ctx context.Context, client *tailscale.Client, policy, etag, operation string) (PolicyMutationResult, error) {
	err := client.PolicyFile().Set(ctx, policy, etag)
	if err == nil {
		return PolicyMutationResult{Changed: true, WriteOutcome: PolicyWriteChanged}, nil
	}

	var apiErr tailscale.APIError
	if errors.As(err, &apiErr) {
		if apiErr.Status == http.StatusPreconditionFailed {
			return PolicyMutationResult{WriteOutcome: PolicyWriteRejected}, fmt.Errorf("%w: policy update rejected with HTTP %d", ErrPolicyConflict, apiErr.Status)
		}
		if apiErr.Status == http.StatusForbidden {
			return PolicyMutationResult{WriteOutcome: PolicyWriteRejected}, fmt.Errorf("%w: update ACL for %s rejected with HTTP %d: %v", ErrPolicyAccessDenied, operation, apiErr.Status, err)
		}
		return PolicyMutationResult{WriteOutcome: PolicyWriteRejected}, fmt.Errorf("update ACL for %s rejected with HTTP %d: %w", operation, apiErr.Status, err)
	}

	// Once Set has sent the POST, a context or transport failure cannot prove
	// whether the server committed the write before the response was lost.
	return PolicyMutationResult{WriteOutcome: PolicyWriteUnknown}, fmt.Errorf("update ACL for %s; server-side write outcome is unknown: %w", operation, err)
}

func policyAccessDenied(err error) bool {
	var apiErr tailscale.APIError
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusForbidden
}

type policyDocument struct {
	root hujson.Value
}

func parsePolicyDocument(raw string) (*policyDocument, error) {
	root, err := hujson.Parse([]byte(raw))
	if err != nil {
		return nil, err
	}
	if _, ok := root.Value.(*hujson.Object); !ok {
		return nil, fmt.Errorf("policy root must be an object")
	}
	return &policyDocument{root: root}, nil
}

func (d *policyDocument) pack() string {
	return string(d.root.Pack())
}

func (d *policyDocument) rootObject() *hujson.Object {
	return d.root.Value.(*hujson.Object)
}

func (d *policyDocument) ensureTagOwners(tags, owners []string, appendOwners bool) (bool, error) {
	root := d.rootObject()
	_, tagOwnersValue, found, err := findObjectMember(root, "tagOwners")
	if err != nil {
		return false, err
	}
	var tagOwners *hujson.Object
	if !found {
		tagOwners = &hujson.Object{}
		appendObjectMember(root, "tagOwners", tagOwners)
		tagOwnersValue = &root.Members[len(root.Members)-1].Value
	} else {
		var ok bool
		tagOwners, ok = tagOwnersValue.Value.(*hujson.Object)
		if !ok {
			return false, fmt.Errorf("tagOwners must be an object")
		}
	}

	changed := !found
	for _, tag := range dedupeStrings(tags) {
		_, ownerValue, exists, findErr := findObjectMember(tagOwners, tag)
		if findErr != nil {
			return false, findErr
		}
		if !exists {
			value, valueErr := newJSONValue(dedupeStrings(owners))
			if valueErr != nil {
				return false, valueErr
			}
			appendObjectMember(tagOwners, tag, value)
			changed = true
			continue
		}
		ownerArray, ok := ownerValue.Value.(*hujson.Array)
		if !ok {
			return false, fmt.Errorf("tagOwners entry %q must be an array", tag)
		}
		existing, err := stringArray(ownerArray)
		if err != nil {
			return false, fmt.Errorf("tagOwners entry %q: %w", tag, err)
		}
		if !appendOwners {
			continue
		}
		for _, owner := range owners {
			if containsString(existing, owner) {
				continue
			}
			appendArrayString(ownerArray, owner)
			existing = append(existing, owner)
			changed = true
		}
	}
	_ = tagOwnersValue
	return changed, nil
}

func (d *policyDocument) ensureFunnelAttr(target string) (bool, error) {
	root := d.rootObject()
	_, nodeAttrsValue, found, err := findObjectMember(root, "nodeAttrs")
	if err != nil {
		return false, err
	}
	if !found {
		grant, valueErr := newJSONValue(map[string]any{
			"target": []string{target},
			"attr":   []string{FunnelNodeAttr},
		})
		if valueErr != nil {
			return false, valueErr
		}
		nodeAttrs := &hujson.Array{Elements: []hujson.ArrayElement{{Value: grant}}}
		appendObjectMember(root, "nodeAttrs", nodeAttrs)
		return true, nil
	}
	nodeAttrs, ok := nodeAttrsValue.Value.(*hujson.Array)
	if !ok {
		return false, fmt.Errorf("nodeAttrs must be an array")
	}

	exactGrant := -1
	for i := range nodeAttrs.Elements {
		grant, ok := nodeAttrs.Elements[i].Value.(*hujson.Object)
		if !ok {
			return false, fmt.Errorf("nodeAttrs[%d] must be an object", i)
		}
		_, targetValue, targetFound, findErr := findObjectMember(grant, "target")
		if findErr != nil {
			return false, fmt.Errorf("nodeAttrs[%d]: %w", i, findErr)
		}
		if !targetFound {
			return false, fmt.Errorf("nodeAttrs[%d] has no target array", i)
		}
		targetArray, ok := targetValue.Value.(*hujson.Array)
		if !ok {
			return false, fmt.Errorf("nodeAttrs[%d].target must be an array", i)
		}
		targets, parseErr := stringArray(targetArray)
		if parseErr != nil {
			return false, fmt.Errorf("nodeAttrs[%d].target: %w", i, parseErr)
		}

		_, attrValue, attrFound, findErr := findObjectMember(grant, "attr")
		if findErr != nil {
			return false, fmt.Errorf("nodeAttrs[%d]: %w", i, findErr)
		}
		var attrs []string
		if attrFound {
			attrArray, ok := attrValue.Value.(*hujson.Array)
			if !ok {
				return false, fmt.Errorf("nodeAttrs[%d].attr must be an array", i)
			}
			attrs, parseErr = stringArray(attrArray)
			if parseErr != nil {
				return false, fmt.Errorf("nodeAttrs[%d].attr: %w", i, parseErr)
			}
		}

		coveredTarget := containsString(targets, target) || containsString(targets, "*")
		if coveredTarget && containsString(attrs, FunnelNodeAttr) {
			return false, nil
		}
		if len(targets) == 1 && targets[0] == target && exactGrant == -1 {
			exactGrant = i
		}
	}

	if exactGrant >= 0 {
		grant := nodeAttrs.Elements[exactGrant].Value.(*hujson.Object)
		_, attrValue, attrFound, err := findObjectMember(grant, "attr")
		if err != nil {
			return false, err
		}
		if !attrFound {
			attrArray := &hujson.Array{Elements: []hujson.ArrayElement{{Value: hujson.String(FunnelNodeAttr)}}}
			appendObjectMember(grant, "attr", attrArray)
			return true, nil
		}
		attrArray := attrValue.Value.(*hujson.Array)
		appendArrayString(attrArray, FunnelNodeAttr)
		return true, nil
	}

	grant, err := newJSONValue(map[string]any{
		"target": []string{target},
		"attr":   []string{FunnelNodeAttr},
	})
	if err != nil {
		return false, err
	}
	appendArrayValue(nodeAttrs, grant)
	return true, nil
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func dedupeStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func findObjectMember(obj *hujson.Object, name string) (int, *hujson.Value, bool, error) {
	found := -1
	for i := range obj.Members {
		memberName, err := stringLiteral(obj.Members[i].Name)
		if err != nil {
			return -1, nil, false, fmt.Errorf("object member %d name: %w", i, err)
		}
		if !strings.EqualFold(memberName, name) {
			continue
		}
		if found >= 0 {
			return -1, nil, false, fmt.Errorf("policy has duplicate %q members; refusing ambiguous surgical edit", name)
		}
		found = i
	}
	if found < 0 {
		return -1, nil, false, nil
	}
	return found, &obj.Members[found].Value, true, nil
}

func stringLiteral(value hujson.Value) (string, error) {
	literal, ok := value.Value.(hujson.Literal)
	if !ok || literal.Kind() != '"' {
		return "", fmt.Errorf("must be a string")
	}
	return literal.String(), nil
}

func stringArray(array *hujson.Array) ([]string, error) {
	result := make([]string, 0, len(array.Elements))
	for i := range array.Elements {
		value, err := stringLiteral(array.Elements[i])
		if err != nil {
			return nil, fmt.Errorf("element %d %w", i, err)
		}
		result = append(result, value)
	}
	return result, nil
}

func newJSONValue(value any) (hujson.ValueTrimmed, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	parsed, err := hujson.Parse(data)
	if err != nil {
		return nil, err
	}
	return parsed.Value, nil
}

func appendObjectMember(obj *hujson.Object, name string, value hujson.ValueTrimmed) {
	trailingComma := false
	var before hujson.Extra
	if len(obj.Members) > 0 {
		last := &obj.Members[len(obj.Members)-1]
		trailingComma = last.Value.AfterExtra != nil
		before = inferredSiblingExtra(last.Name.BeforeExtra)
	}
	member := hujson.ObjectMember{
		Name:  hujson.Value{BeforeExtra: before, Value: hujson.String(name)},
		Value: hujson.Value{Value: value},
	}
	if trailingComma {
		member.Value.AfterExtra = hujson.Extra{}
	}
	obj.Members = append(obj.Members, member)
}

func appendArrayString(array *hujson.Array, value string) {
	appendArrayValue(array, hujson.String(value))
}

func appendArrayValue(array *hujson.Array, value hujson.ValueTrimmed) {
	trailingComma := false
	var before hujson.Extra
	if len(array.Elements) > 0 {
		last := &array.Elements[len(array.Elements)-1]
		trailingComma = last.AfterExtra != nil
		before = inferredSiblingExtra(last.BeforeExtra)
	}
	element := hujson.ArrayElement{BeforeExtra: before, Value: value}
	if trailingComma {
		element.AfterExtra = hujson.Extra{}
	}
	array.Elements = append(array.Elements, element)
}

func inferredSiblingExtra(previous hujson.Extra) hujson.Extra {
	lastNewline := -1
	for i, b := range previous {
		if b == '\n' {
			lastNewline = i
		}
	}
	if lastNewline >= 0 {
		suffix := previous[lastNewline+1:]
		if onlyHorizontalSpace(suffix) {
			result := make([]byte, 1+len(suffix))
			result[0] = '\n'
			copy(result[1:], suffix)
			return result
		}
	}
	if onlyHorizontalSpace(previous) {
		return append(hujson.Extra(nil), previous...)
	}
	return nil
}

func onlyHorizontalSpace(value []byte) bool {
	for _, b := range value {
		if b != ' ' && b != '\t' && b != '\r' {
			return false
		}
	}
	return true
}

// DeleteTag removes a tag from tagOwners using the lossless HuJSON path. A
// referenced tag is refused, except for TSLink's exact canonical Funnel grant,
// which is removed atomically with the plumbing tag so teardown cannot leave a
// dangling nodeAttrs reference.
// Returns ErrNoAPIClient if no API client is available.
func DeleteTag(ctx context.Context, tag string) error {
	if err := registry.ValidateTag(tag); err != nil {
		return err
	}

	policyMutationMu.Lock()
	defer policyMutationMu.Unlock()

	client, err := policyClient()
	if err != nil {
		return err
	}
	raw, err := client.PolicyFile().Raw(ctx)
	if err != nil {
		return fmt.Errorf("read ACL: %w", err)
	}
	doc, err := parsePolicyDocument(raw.HuJSON)
	if err != nil {
		return fmt.Errorf("parse HuJSON ACL without rewriting it: %w", err)
	}
	root := doc.rootObject()
	_, tagOwnersValue, found, err := findObjectMember(root, "tagOwners")
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("tag %q not found in tailnet ACL", tag)
	}
	tagOwners, ok := tagOwnersValue.Value.(*hujson.Object)
	if !ok {
		return fmt.Errorf("tagOwners must be an object")
	}
	tagIndex, _, found, err := findObjectMember(tagOwners, tag)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("tag %q not found in tailnet ACL", tag)
	}

	removeGrantIndexes, err := doc.deletableFunnelGrantIndexes(tag)
	if err != nil {
		return err
	}
	removeObjectMember(tagOwners, tagIndex)
	if len(removeGrantIndexes) > 0 {
		_, nodeAttrsValue, _, _ := findObjectMember(root, "nodeAttrs")
		nodeAttrs := nodeAttrsValue.Value.(*hujson.Array)
		for i := len(removeGrantIndexes) - 1; i >= 0; i-- {
			removeArrayElement(nodeAttrs, removeGrantIndexes[i])
		}
	}

	_, err = setRawPolicy(ctx, client, doc.pack(), raw.ETag, fmt.Sprintf("delete tagOwner %q", tag))
	return err
}

func (d *policyDocument) deletableFunnelGrantIndexes(tag string) ([]int, error) {
	root := d.rootObject()
	_, nodeAttrsValue, found, err := findObjectMember(root, "nodeAttrs")
	if err != nil || !found {
		return nil, err
	}
	nodeAttrs, ok := nodeAttrsValue.Value.(*hujson.Array)
	if !ok {
		return nil, fmt.Errorf("nodeAttrs must be an array")
	}
	var removable []int
	for i := range nodeAttrs.Elements {
		grant, ok := nodeAttrs.Elements[i].Value.(*hujson.Object)
		if !ok {
			return nil, fmt.Errorf("nodeAttrs[%d] must be an object", i)
		}
		_, targetValue, found, err := findObjectMember(grant, "target")
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		targetArray, ok := targetValue.Value.(*hujson.Array)
		if !ok {
			return nil, fmt.Errorf("nodeAttrs[%d].target must be an array", i)
		}
		targets, err := stringArray(targetArray)
		if err != nil {
			return nil, err
		}
		if !containsString(targets, tag) {
			continue
		}
		_, attrValue, found, err := findObjectMember(grant, "attr")
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("refusing to delete tag %q because nodeAttrs[%d] references it without an attr array", tag, i)
		}
		attrArray, ok := attrValue.Value.(*hujson.Array)
		if !ok {
			return nil, fmt.Errorf("nodeAttrs[%d].attr must be an array", i)
		}
		attrs, err := stringArray(attrArray)
		if err != nil {
			return nil, err
		}
		canonical := tag == registry.FunnelTag && len(targets) == 1 && targets[0] == tag && len(attrs) == 1 && attrs[0] == FunnelNodeAttr
		if !canonical {
			return nil, fmt.Errorf("refusing to delete tag %q because nodeAttrs[%d] still references it; remove or retarget that grant first", tag, i)
		}
		removable = append(removable, i)
	}
	return removable, nil
}

func removeObjectMember(object *hujson.Object, index int) {
	trailingComma := len(object.Members) > 0 && object.Members[len(object.Members)-1].Value.AfterExtra != nil
	copy(object.Members[index:], object.Members[index+1:])
	object.Members = object.Members[:len(object.Members)-1]
	if trailingComma && len(object.Members) > 0 && object.Members[len(object.Members)-1].Value.AfterExtra == nil {
		object.Members[len(object.Members)-1].Value.AfterExtra = hujson.Extra{}
	}
}

func removeArrayElement(array *hujson.Array, index int) {
	trailingComma := len(array.Elements) > 0 && array.Elements[len(array.Elements)-1].AfterExtra != nil
	copy(array.Elements[index:], array.Elements[index+1:])
	array.Elements = array.Elements[:len(array.Elements)-1]
	if trailingComma && len(array.Elements) > 0 && array.Elements[len(array.Elements)-1].AfterExtra == nil {
		array.Elements[len(array.Elements)-1].AfterExtra = hujson.Extra{}
	}
}
