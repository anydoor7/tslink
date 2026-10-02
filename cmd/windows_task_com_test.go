package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWindowsSchedulerCOMUnicodeCapture(t *testing.T) {
	data := taskFixture(t, "com-unicode.json")
	var wire windowsSchedulerStatus
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(wire.XML, `<?xml version="1.0" encoding="UTF-16"?>`) {
		t.Fatal("capture does not exercise the COM UTF-16 declaration")
	}
	// A file parser must still honor byte encodings. Only the decoded COM
	// string boundary may discard its obsolete byte-encoding declaration.
	if _, err := parseWindowsTask([]byte(wire.XML)); err == nil {
		t.Fatal("file parser silently ignored the UTF-16 byte encoding")
	}
	s, err := parseWindowsSchedulerStatus(data)
	if err != nil {
		t.Fatal(err)
	}
	spec := fixtureTaskSpec()
	spec.ConfigDir = `C:\Windows\Temp\tslink-f9-fix1\家人 & O'Brien`
	spec.Executable = spec.ConfigDir + `\tslink.exe`
	if !s.Exists || !s.Enabled || !windowsTaskOwned([]byte(s.XML), spec) {
		t.Fatal("real COM definition lost ownership or Unicode paths")
	}
	if windowsTaskMatches([]byte(s.XML), spec) {
		t.Fatal("legacy direct-daemon capture incorrectly promises crash recovery")
	}
	definition, err := parseWindowsTask([]byte(s.XML))
	if err != nil || definition.Actions.Exec[0].WorkingDirectory != spec.ConfigDir {
		t.Fatalf("Unicode working directory changed: %v", err)
	}
	// The renderer remains a UTF-8 file producer.
	rendered, err := renderWindowsTask(spec)
	if err != nil || !strings.HasPrefix(string(rendered), `<?xml version="1.0" encoding="UTF-8"?>`) {
		t.Fatalf("renderer encoding changed: %v", err)
	}
}

func TestWindowsSchedulerCOMDeclarationVariants(t *testing.T) {
	var wire windowsSchedulerStatus
	if err := json.Unmarshal(taskFixture(t, "running.json"), &wire); err != nil {
		t.Fatal(err)
	}
	xmlBody := strings.TrimPrefix(wire.XML, "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	for _, header := range []string{"", `<?xml version="1.0" encoding="UTF-16"?>`, "\ufeff<?xml version='1.0' encoding='utf-16'?>", "<?xml\tversion='1.0' encoding='UTF-16'?>"} {
		t.Run(header, func(t *testing.T) {
			wire.XML = header + xmlBody
			data, _ := json.Marshal(wire)
			got, err := parseWindowsSchedulerStatus(data)
			if err != nil || !windowsTaskMatches([]byte(got.XML), fixtureTaskSpec()) {
				t.Fatalf("decoded COM declaration rejected: %v", err)
			}
		})
	}
	for _, malformed := range []string{"<?xml version='1.0' encoding='UTF-16'", "<?xml version='1.0' encoding='UTF-16'?><Task>"} {
		wire.XML = malformed
		data, _ := json.Marshal(wire)
		if _, err := parseWindowsSchedulerStatus(data); err == nil {
			t.Fatal("malformed COM XML accepted")
		}
	}
}

func TestWindowsTaskSchemaDefaultProjection(t *testing.T) {
	data, err := renderWindowsTask(fixtureTaskSpec())
	if err != nil {
		t.Fatal(err)
	}
	// These are the same default-valued fields omitted by the real capture.
	for _, element := range []string{
		"<Enabled>true</Enabled>", "<RunLevel>LeastPrivilege</RunLevel>",
		"<AllowStartOnDemand>true</AllowStartOnDemand>", "<RunOnlyIfIdle>false</RunOnlyIfIdle>",
		"<RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>",
	} {
		data = []byte(strings.ReplaceAll(string(data), element, ""))
	}
	if !windowsTaskMatches(data, fixtureTaskSpec()) {
		t.Fatal("default-valued COM projection rejected")
	}
	// A disabled trigger remains unhealthy even when the other defaults are
	// omitted. Its explicit value must take precedence over the default.
	disabled := strings.Replace(string(data), "<LogonTrigger>", "<LogonTrigger><Enabled>false</Enabled>", 1)
	if windowsTaskMatches([]byte(disabled), fixtureTaskSpec()) {
		t.Fatal("explicit disabled trigger lost to schema default")
	}
}
