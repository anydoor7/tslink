package daemon

import "testing"

func TestServeIdentityMatchesCobraGrammar(t *testing.T) {
	previous := processArguments
	t.Cleanup(func() { processArguments = previous })
	for _, tc := range []struct {
		name string
		args []string
		want bool
	}{
		{"canonical", []string{"serve", "--no-browser"}, true},
		{"leading_global_false", []string{"--json=false", "serve"}, true},
		{"leading_local_bool_equals", []string{"--no-browser=true", "serve"}, true},
		{"leading_local_string", []string{"--control-url=http://localhost:3000", "serve"}, true},
		{"leading_false_local", []string{"--no-browser=false", "serve"}, true},
		{"value_contains_serve", []string{"--control-url=serve", "serve"}, true},
		{"trailing_value_contains_serve", []string{"serve", "--control-url=serve"}, true},
		{"local_bool_separate", []string{"--no-browser", "serve"}, false},
		{"invalid_bool", []string{"serve", "--json=serve"}, false},
		{"invalid_flag", []string{"serve", "--bogus"}, false},
		{"invalid_positionals", []string{"serve", "status"}, false},
		{"value_is_serve", []string{"--control-url", "serve", "status"}, false},
		{"help", []string{"serve", "--help"}, false},
		{"short_help", []string{"serve", "-h"}, false},
		{"version", []string{"serve", "--version"}, false},
		{"leading_version", []string{"--version", "serve"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			processArguments = func(int) ([]string, error) { return append([]string{"tslink"}, tc.args...), nil }
			got := verifyProcessServeCommand(1234) == nil
			if got != tc.want {
				t.Fatalf("argv %q: got=%t want=%t", tc.args, got, tc.want)
			}
		})
	}
}
