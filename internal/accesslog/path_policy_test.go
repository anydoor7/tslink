package accesslog

import (
	"strings"
	"testing"
)

func TestPathPolicy(t *testing.T) {
	for _, tc := range []struct{ path, prefix, full string }{
		{"/album/item?secret=query", "/album", "/album/item"},
		{"/guest%2Fshortbearer", "/guest", "/guest/[redacted]"},
		{"/invite%2fshortbearer", "/invite", "/invite/[redacted]"},
		{"/guest%5Cshortbearer", "/guest", "/guest/[redacted]"},
		{`/guest\shortbearer`, "/guest", "/guest/[redacted]"},
		{"/guest%252Fshortbearer", "/guest", "/guest/[redacted]"},
		{"/_tslink/guest/shortbearer", "/_tslink", "/_tslink/guest/[redacted]"},
		{"/g/shortbearer", "/g", "/g/[redacted]"},
		{"/Ab7qP9k2Lm4vR8x6/item", "/[redacted]", "/[redacted]/item"},
		{"/album%2Fitem", "/album", "/album/item"},
		{"/album%5citem", "/album", "/album/item"},
		{"/", "/", "/"}, {"/bad%zz", "/[redacted]", "/[redacted]"},
		{"relative/path", "", ""}, {"//host/path", "", ""},
		{"/" + strings.Repeat("%25", 10), "/[redacted]", "/[redacted]"},
		{"/" + strings.Repeat("%25", 1) + "25252525252525252Ftoken", "/[redacted]", "/[redacted]"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			if got := PathForMode(tc.path, "prefix"); got != tc.prefix {
				t.Errorf("prefix=%q want %q", got, tc.prefix)
			}
			if got := PathForMode(tc.path, "full"); got != tc.full {
				t.Errorf("full=%q want %q", got, tc.full)
			}
			if got := PathForMode(tc.path, "off"); got != "" {
				t.Errorf("off=%q", got)
			}
		})
	}
}
func TestPathModeMigration(t *testing.T) {
	on, off := true, false
	for _, tc := range []struct {
		o      Options
		mode   string
		legacy *bool
		want   string
	}{
		{Options{}, "", nil, "prefix"}, {Options{RecordPath: &on}, "", nil, "prefix"},
		{Options{}, "", &on, "prefix"}, {Options{RecordPath: &off}, "full", nil, "off"},
		{Options{PathMode: "full"}, "prefix", nil, "prefix"}, {Options{PathMode: "off"}, "full", nil, "off"},
		{Options{}, "full", nil, "full"}, {Options{PathMode: "full"}, "", &off, "off"},
		{Options{}, "off", nil, "off"},
	} {
		if got := tc.o.ModeFor(tc.mode, tc.legacy); got != tc.want {
			t.Errorf("mode %q want %q", got, tc.want)
		}
	}
	if err := (Options{PathMode: "unsafe"}).Validate(); err == nil {
		t.Fatal("invalid mode accepted")
	}
}
