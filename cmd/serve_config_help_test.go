package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestServeHelpDocumentsMCPInviteOptInAndDurationGrammar(t *testing.T) {
	command, _, err := rootCmd.Find([]string{"serve"})
	if err != nil {
		t.Fatal(err)
	}
	old := command.OutOrStdout()
	t.Cleanup(func() { command.SetOut(old) })
	var help bytes.Buffer
	command.SetOut(&help)
	if err := command.Help(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"allow_elevated_invites", "bool, default false", "other than member", "exit-node", "CLI", "Go syntax", "d for days"} {
		if !strings.Contains(help.String(), want) {
			t.Errorf("serve help missing %q", want)
		}
	}
}
