package cmd

import (
	"testing"
	"time"
)

func TestCLIWaitFlagsReadDurationGrammar(t *testing.T) {
	for _, name := range []string{"add", "share", "url"} {
		t.Run(name, func(t *testing.T) {
			command, _, err := rootCmd.Find([]string{name})
			if err != nil {
				t.Fatal(err)
			}
			flag := command.Flags().Lookup("wait")
			original, changed := flag.Value.String(), flag.Changed
			t.Cleanup(func() { _ = flag.Value.Set(original); flag.Changed = changed })
			if flag.Value.Type() != "duration" {
				t.Fatalf("wait type = %s", flag.Value.Type())
			}
			wantDefault := "30s"
			if name == "url" {
				wantDefault = "0s"
			}
			if flag.DefValue != wantDefault {
				t.Errorf("default = %s, want %s", flag.DefValue, wantDefault)
			}
			for input, want := range map[string]time.Duration{"0": 0, "0d": 0, "1d": 24 * time.Hour, "1.5d": 36 * time.Hour, "30s": 30 * time.Second, "1h30m": 90 * time.Minute} {
				t.Run(input, func(t *testing.T) {
					if err := command.ParseFlags([]string{"--wait=" + input}); err != nil {
						t.Fatalf("--wait=%s rejected: %v", input, err)
					}
					if got, err := command.Flags().GetDuration("wait"); err != nil || got != want {
						t.Errorf("wait = %v, %v; want %v", got, err, want)
					}
				})
			}
			if err := command.ParseFlags([]string{"--wait=3w"}); err == nil {
				t.Error("invalid unit accepted")
			}
			if name == "url" {
				if err := command.ParseFlags([]string{"--wait"}); err != nil {
					t.Fatal(err)
				}
				if got, err := command.Flags().GetDuration("wait"); err != nil || got != 30*time.Second {
					t.Errorf("bare --wait = %v, %v", got, err)
				}
			}
		})
	}
}
