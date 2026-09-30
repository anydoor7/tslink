package cmd

import "testing"

func TestServeCommandAcceptsGlobalJSONFlagOnEitherSide(t *testing.T) {
	flag := rootCmd.PersistentFlags().Lookup("json")
	previous, changed := flag.Value.String(), flag.Changed
	t.Cleanup(func() { _ = flag.Value.Set(previous); flag.Changed = changed })
	for _, args := range [][]string{
		{"serve", "--json=false"},
		{"--json=false", "serve"},
		{"--json", "serve"},
	} {
		command, remaining, err := rootCmd.Find(args)
		if err != nil || command.Name() != "serve" {
			t.Fatalf("Find(%q) command=%v err=%v", args, command, err)
		}
		if err := command.ParseFlags(remaining); err != nil {
			t.Fatalf("ParseFlags(%q): %v", args, err)
		}
	}
}
