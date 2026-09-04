package tailapi

import (
	"strings"
	"testing"
)

// parsePolicyDocument is the gate in front of every surgical policy edit, and the
// edits are written back to the tailnet. Two things matter: a document that is
// not a policy object must be refused before any code assumes it is one, and a
// document that is accepted must survive pack() without losing the operator's
// comments or formatting.

const funnelTestTarget = "tag:tslink-funnel"

func TestParsePolicyDocumentRejectsMalformedHuJSON(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"truncated object", `{"acls":`},
		{"unclosed array", `{"nodeAttrs":[{"target":["a"]}`},
		{"empty input", ``},
		{"bare word", `notjson`},
		{"unterminated string", `{"acls":"`},
		{"trailing garbage", `{} extra`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := parsePolicyDocument(tc.raw)
			if err == nil {
				t.Fatalf("parsePolicyDocument(%q) error = nil, want a parse refusal", tc.raw)
			}
			if doc != nil {
				t.Fatalf("parsePolicyDocument(%q) returned a document alongside an error", tc.raw)
			}
		})
	}
}

func TestParsePolicyDocumentRejectsNonObjectRoot(t *testing.T) {
	// Every downstream edit does root.Value.(*hujson.Object). Without this guard
	// a policy whose root is an array or scalar would panic mid-edit instead of
	// being refused before anything is written back.
	cases := []struct {
		name string
		raw  string
	}{
		{"array root", `[]`},
		{"populated array root", `[{"target":["a"]}]`},
		{"string root", `"policy"`},
		{"number root", `12`},
		{"null root", `null`},
		{"bool root", `true`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := parsePolicyDocument(tc.raw)
			if err == nil {
				t.Fatalf("parsePolicyDocument(%q) error = nil, want a non-object refusal", tc.raw)
			}
			if !strings.Contains(err.Error(), "policy root must be an object") {
				t.Fatalf("parsePolicyDocument(%q) error = %v, want the non-object reason", tc.raw, err)
			}
			if doc != nil {
				t.Fatalf("parsePolicyDocument(%q) returned a document alongside an error", tc.raw)
			}
		})
	}
}

const commentedPolicy = `{
	// Owners of every TSLink tag.
	"tagOwners": {
		"tag:tsmain": ["autogroup:admin"], // inline note
	},
	/* block comment kept verbatim */
	"acls": [
		{"action": "accept", "src": ["*"], "dst": ["*:*"]},
	],
}`

func TestParsePolicyDocumentPackIsLosslessForCommentsAndTrailingCommas(t *testing.T) {
	doc, err := parsePolicyDocument(commentedPolicy)
	if err != nil {
		t.Fatalf("parsePolicyDocument() error = %v", err)
	}
	if got := doc.pack(); got != commentedPolicy {
		t.Fatalf("pack() changed an unedited policy.\n got: %s\nwant: %s", got, commentedPolicy)
	}
}

func TestEnsureFunnelAttrEditKeepsSurroundingCommentsAndUnrelatedContent(t *testing.T) {
	doc, err := parsePolicyDocument(commentedPolicy)
	if err != nil {
		t.Fatalf("parsePolicyDocument() error = %v", err)
	}
	changed, err := doc.ensureFunnelAttr(funnelTestTarget)
	if err != nil {
		t.Fatalf("ensureFunnelAttr() error = %v", err)
	}
	if !changed {
		t.Fatal("ensureFunnelAttr() changed = false, want the missing nodeAttrs grant added")
	}
	packed := doc.pack()
	for _, keep := range []string{
		"// Owners of every TSLink tag.",
		"// inline note",
		"/* block comment kept verbatim */",
		`"tag:tsmain": ["autogroup:admin"]`,
		`"action": "accept"`,
	} {
		if !strings.Contains(packed, keep) {
			t.Fatalf("surgical Funnel edit dropped %q from the policy:\n%s", keep, packed)
		}
	}
	if !strings.Contains(packed, funnelTestTarget) || !strings.Contains(packed, FunnelNodeAttr) {
		t.Fatalf("surgical Funnel edit did not add the grant:\n%s", packed)
	}
	// Re-running must be a no-op: a second write would churn the tailnet policy.
	again, err := doc.ensureFunnelAttr(funnelTestTarget)
	if err != nil {
		t.Fatalf("second ensureFunnelAttr() error = %v", err)
	}
	if again {
		t.Fatal("second ensureFunnelAttr() reported a change; the grant it just wrote was not recognized")
	}
	if got := doc.pack(); got != packed {
		t.Fatalf("idempotent ensureFunnelAttr() still rewrote the document:\n%s", got)
	}
}

func TestEnsureFunnelAttrRefusesStructurallyInvalidPolicyWithoutMutatingIt(t *testing.T) {
	cases := []struct {
		name   string
		raw    string
		reason string
	}{
		{
			name:   "duplicate nodeAttrs members",
			raw:    `{"nodeAttrs":[],"nodeattrs":[]}`,
			reason: `duplicate "nodeAttrs" members`,
		},
		{
			name:   "grant is not an object",
			raw:    `{"nodeAttrs":["not-a-grant"]}`,
			reason: "nodeAttrs[0] must be an object",
		},
		{
			name:   "grant has duplicate target members",
			raw:    `{"nodeAttrs":[{"target":["a"],"TARGET":["b"],"attr":["funnel"]}]}`,
			reason: `duplicate "target" members`,
		},
		{
			name:   "grant has no target",
			raw:    `{"nodeAttrs":[{"attr":["funnel"]}]}`,
			reason: "nodeAttrs[0] has no target array",
		},
		{
			name:   "grant target is not an array",
			raw:    `{"nodeAttrs":[{"target":"a","attr":["funnel"]}]}`,
			reason: "nodeAttrs[0].target must be an array",
		},
		{
			name:   "grant target holds a non-string",
			raw:    `{"nodeAttrs":[{"target":[7],"attr":["funnel"]}]}`,
			reason: "nodeAttrs[0].target:",
		},
		{
			name:   "grant has duplicate attr members",
			raw:    `{"nodeAttrs":[{"target":["a"],"attr":["funnel"],"ATTR":["funnel"]}]}`,
			reason: `duplicate "attr" members`,
		},
		{
			name:   "grant attr is not an array",
			raw:    `{"nodeAttrs":[{"target":["a"],"attr":"funnel"}]}`,
			reason: "nodeAttrs[0].attr must be an array",
		},
		{
			name:   "grant attr holds a non-string",
			raw:    `{"nodeAttrs":[{"target":["a"],"attr":[7]}]}`,
			reason: "nodeAttrs[0].attr:",
		},
		{
			name:   "invalid grant sits after a valid one",
			raw:    `{"nodeAttrs":[{"target":["other"],"attr":["drive:share"]},{"target":["a"]," attr ":1,"attr":"funnel"}]}`,
			reason: "nodeAttrs[1].attr must be an array",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := parsePolicyDocument(tc.raw)
			if err != nil {
				t.Fatalf("parsePolicyDocument(%q) error = %v, want a parseable document", tc.raw, err)
			}
			before := doc.pack()

			changed, err := doc.ensureFunnelAttr(funnelTestTarget)
			if err == nil {
				t.Fatalf("ensureFunnelAttr() error = nil for %q, want a refusal", tc.raw)
			}
			if !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("ensureFunnelAttr() error = %v, want it to mention %q", err, tc.reason)
			}
			if changed {
				t.Fatal("ensureFunnelAttr() reported a change while refusing; a partial edit could be written back")
			}
			if after := doc.pack(); after != before {
				t.Fatalf("refused ensureFunnelAttr() still mutated the policy.\nbefore: %s\nafter:  %s", before, after)
			}
		})
	}
}

func TestEnsureFunnelAttrCompletesAnExactTargetGrantThatHasNoAttrMember(t *testing.T) {
	// A hand-written grant that names exactly this target but carries no attr
	// list must be completed in place rather than duplicated.
	doc, err := parsePolicyDocument(`{"nodeAttrs":[{"target":["` + funnelTestTarget + `"]}]}`)
	if err != nil {
		t.Fatalf("parsePolicyDocument() error = %v", err)
	}
	changed, err := doc.ensureFunnelAttr(funnelTestTarget)
	if err != nil {
		t.Fatalf("ensureFunnelAttr() error = %v", err)
	}
	if !changed {
		t.Fatal("ensureFunnelAttr() changed = false, want the attr member added")
	}
	packed := doc.pack()
	if strings.Count(packed, funnelTestTarget) != 1 {
		t.Fatalf("ensureFunnelAttr() duplicated the grant instead of completing it:\n%s", packed)
	}
	if !strings.Contains(packed, `"attr"`) || !strings.Contains(packed, FunnelNodeAttr) {
		t.Fatalf("ensureFunnelAttr() did not add the funnel attribute:\n%s", packed)
	}

	again, err := doc.ensureFunnelAttr(funnelTestTarget)
	if err != nil {
		t.Fatalf("second ensureFunnelAttr() error = %v", err)
	}
	if again {
		t.Fatal("second ensureFunnelAttr() reported a change; the completed grant was not recognized")
	}
}

func TestEnsureFunnelAttrDoesNotWidenAGrantThatCoversOtherTargets(t *testing.T) {
	doc, err := parsePolicyDocument(`{"nodeAttrs":[{"target":["tag:other","tag:another"],"attr":["drive:share"]}]}`)
	if err != nil {
		t.Fatalf("parsePolicyDocument() error = %v", err)
	}
	changed, err := doc.ensureFunnelAttr(funnelTestTarget)
	if err != nil {
		t.Fatalf("ensureFunnelAttr() error = %v", err)
	}
	if !changed {
		t.Fatal("ensureFunnelAttr() changed = false, want a new grant appended")
	}
	packed := doc.pack()
	if strings.Contains(packed, `"tag:other","tag:another","`+funnelTestTarget) {
		t.Fatalf("ensureFunnelAttr() widened a multi-target grant:\n%s", packed)
	}
	if !strings.Contains(packed, `"drive:share"`) {
		t.Fatalf("ensureFunnelAttr() dropped an unrelated attribute:\n%s", packed)
	}
	if !strings.Contains(packed, funnelTestTarget) {
		t.Fatalf("ensureFunnelAttr() did not add the funnel grant:\n%s", packed)
	}
}
