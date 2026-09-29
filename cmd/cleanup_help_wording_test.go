package cmd

import (
	"strings"
	"testing"
)

// TestCleanupHelpDescribesLocalACLReportOnly pins R4-15. --manage-acl on
// cleanup makes no tailnet API request (see internal/lifecycle
// shared_acl_test.go): it reports whether this host still uses the shared
// Funnel grant, so its help must not promise an ACL status. --dry-run can
// no longer withhold an ACL deletion, because cleanup never deletes the grant.
func TestCleanupHelpDescribesLocalACLReportOnly(t *testing.T) {
	command, _, err := rootCmd.Find([]string{"cleanup"})
	if err != nil {
		t.Fatal(err)
	}
	manageACL := command.Flags().Lookup("manage-acl")
	dryRun := command.Flags().Lookup("dry-run")
	if manageACL == nil || dryRun == nil {
		t.Fatalf("cleanup flags missing: manage-acl=%v dry-run=%v", manageACL, dryRun)
	}
	for _, want := range []string{"whether this host still uses the shared Funnel grant", "does not query the tailnet ACL"} {
		if !strings.Contains(manageACL.Usage, want) {
			t.Errorf("--manage-acl usage = %q, want it to say %q", manageACL.Usage, want)
		}
	}
	// The same flag set names the ACL on --manage-acl, so this check can see
	// the word when it is there.
	if strings.Contains(dryRun.Usage, "ACL") {
		t.Errorf("--dry-run usage = %q, still offers to withhold an ACL deletion cleanup never performs", dryRun.Usage)
	}
	if !strings.Contains(command.Long, "does not query the tailnet ACL") {
		t.Errorf("cleanup help does not say --manage-acl stays local:\n%s", command.Long)
	}
	// The manifest describes the dry_run result field to agents, so it must
	// not offer an ACL deletion either (CV-2).
	dryRunField, ok := commandJSONResultFields("tslink cleanup")["dry_run"]
	if !ok {
		t.Fatal("tslink cleanup manifest has no dry_run result field")
	}
	if strings.Contains(dryRunField.Description, "ACL") {
		t.Errorf("manifest dry_run description = %q, still mentions an ACL deletion cleanup never performs", dryRunField.Description)
	}
}
