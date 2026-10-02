package server

import "testing"

func TestReview3InvalidUTF8LegacyWhoIsDeniesTags(t *testing.T) {
	if !isAllowed("alice\ufffd@example.com", []string{"tag:trusted"}, []string{"tag:trusted"}) {
		t.Fatal("tag positive control failed")
	}
	if isAllowed("alice\xff@example.com", []string{"tag:trusted"}, []string{"tag:trusted"}) {
		t.Fatal("malformed WhoIs fell through to a legacy tag grant")
	}
}
