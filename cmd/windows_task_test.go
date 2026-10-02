package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func taskFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/task-scheduler/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func fixtureTaskSpec() windowsTaskSpec {
	return windowsTaskSpec{SID: "S-1-5-21-100-200-300-1001", Executable: `C:\Program Files\TSLink\tslink.exe`,
		ConfigDir: `C:\Users\Alice Example\AppData\Roaming\tslink`, PowerShell: `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`}
}

func TestWindowsTaskXMLFixture(t *testing.T) {
	spec := fixtureTaskSpec()
	data, err := renderWindowsTask(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, taskFixture(t, "task.xml")) {
		t.Fatal("generated task differs from independent XML fixture")
	}
	if !windowsTaskMatches(data, spec) {
		t.Fatal("positive control task policy not verified")
	}
	for _, field := range []string{"sid", "executable", "config", "powershell"} {
		t.Run(field, func(t *testing.T) {
			s := spec
			switch field {
			case "sid":
				s.SID = ""
			case "executable":
				s.Executable = ""
			case "config":
				s.ConfigDir = ""
			case "powershell":
				s.PowerShell = ""
			}
			if _, err := renderWindowsTask(s); err == nil {
				t.Fatal("missing required field accepted")
			}
		})
	}
}

func TestWindowsTaskEscapingAndForegroundExit(t *testing.T) {
	s := fixtureTaskSpec()
	s.Executable = `C:\O'Brien & 家人\tslink.exe`
	s.ConfigDir = `C:\Users\A < B & 家人\`
	s.NoAutoProvision = true
	data, err := renderWindowsTask(s)
	if err != nil {
		t.Fatal(err)
	}
	if !windowsTaskMatches(data, s) {
		t.Fatal("escaped task did not round trip")
	}
	task, err := parseWindowsTask(data)
	if err != nil {
		t.Fatal(err)
	}
	script, err := decodePowerShell(strings.TrimPrefix(task.Actions.Exec[0].Arguments, "-NoLogo -NoProfile -NonInteractive -WindowStyle Hidden -EncodedCommand "))
	if err != nil {
		t.Fatal(err)
	}
	want := "$ErrorActionPreference='Stop'; $env:TSLINK_CONFIG_DIR='C:\\Users\\A < B & 家人\\'; $env:TSLINK_MANAGED_LOGS='1'; & 'C:\\O''Brien & 家人\\tslink.exe' supervise --no-auto-provision; exit $LASTEXITCODE"
	if script != want {
		t.Fatalf("foreground launcher = %q, want %q", script, want)
	}
	for _, encoded := range []string{"bad!", "YQ=="} {
		if _, err := decodePowerShell(encoded); err == nil {
			t.Fatalf("invalid UTF16 input %q accepted", encoded)
		}
	}
}

func TestWindowsTaskPolicyMutations(t *testing.T) {
	data := taskFixture(t, "task.xml")
	if !windowsTaskMatches(data, fixtureTaskSpec()) {
		t.Fatal("mutation positive control rejected")
	}
	for _, tc := range []struct{ name, from, to string }{
		{"namespace", windowsTaskNamespace, "invalid"},
		{"version", `version="1.2"`, `version="1.1"`},
		{"principal", "<LogonType>InteractiveToken</LogonType>", "<LogonType>S4U</LogonType>"},
		{"elevated", "LeastPrivilege", "HighestAvailable"},
		{"user", "S-1-5-21-100-200-300-1001", "S-1-5-21-other"},
		{"triggerDisabled", "<Enabled>true</Enabled>", "<Enabled>false</Enabled>"},
		{"otherTrigger", "</Triggers>", "<BootTrigger/></Triggers>"},
		{"otherAction", "</Actions>", "<ComHandler/></Actions>"},
		{"instances", "IgnoreNew", "Parallel"},
		{"battery", "<StopIfGoingOnBatteries>false", "<StopIfGoingOnBatteries>true"},
		{"batteryStart", "<DisallowStartIfOnBatteries>false", "<DisallowStartIfOnBatteries>true"},
		{"hardKill", "<AllowHardTerminate>false", "<AllowHardTerminate>true"},
		{"timeLimit", "<ExecutionTimeLimit>PT0S", "<ExecutionTimeLimit>P3D"},
		{"restartInterval", "<Interval>PT1M", "<Interval>PT1S"},
		{"restartCount", "<Count>255", "<Count>0"},
		{"removedRestart", "<RestartOnFailure>", "<UnrecognizedRestart>"},
		{"actionContext", `Context="User"`, `Context="Other"`},
		{"workingDirectory", "<WorkingDirectory>C:", "<WorkingDirectory>D:"},
		{"launcher", "-NoProfile", "-Profile"},
		{"idle", "<RunOnlyIfIdle>false", "<RunOnlyIfIdle>true"},
		{"network", "<RunOnlyIfNetworkAvailable>false", "<RunOnlyIfNetworkAvailable>true"},
		{"demand", "<AllowStartOnDemand>true", "<AllowStartOnDemand>false"},
		{"available", "<StartWhenAvailable>true", "<StartWhenAvailable>false"},
		{"missingBatteryStart", "<DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>", ""},
		{"missingBatteryStop", "<StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>", ""},
		{"missingHardTerminate", "<AllowHardTerminate>false</AllowHardTerminate>", ""},
		{"missingStartAvailable", "<StartWhenAvailable>true</StartWhenAvailable>", ""},
		{"missingTimeLimit", "<ExecutionTimeLimit>PT0S</ExecutionTimeLimit>", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := bytes.Replace(data, []byte(tc.from), []byte(tc.to), 1)
			if bytes.Equal(changed, data) {
				t.Fatal("mutation anchor absent")
			}
			if windowsTaskMatches(changed, fixtureTaskSpec()) {
				t.Fatal("changed task incorrectly earns supervision")
			}
		})
	}
	other := fixtureTaskSpec()
	other.ConfigDir += "other"
	if windowsTaskConfigMatches(data, other.ConfigDir) {
		t.Fatal("foreign config accepted")
	}
}

func TestWindowsSchedulerStatusFixtures(t *testing.T) {
	for _, tc := range []struct {
		name            string
		exists, enabled bool
		state, engines  int
	}{
		{"running", true, true, 4, 1}, {"ready", true, true, 3, 0}, {"disabled", true, false, 1, 0}, {"absent", false, false, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := parseWindowsSchedulerStatus(taskFixture(t, tc.name+".json"))
			if err != nil {
				t.Fatal(err)
			}
			if s.Exists != tc.exists || s.Enabled != tc.enabled || s.State != tc.state || len(s.Engines) != tc.engines {
				t.Fatalf("status = %+v", s)
			}
			if s.Exists && (!windowsTaskOwned([]byte(s.XML), fixtureTaskSpec()) || windowsTaskMatches([]byte(s.XML), fixtureTaskSpec()) != tc.enabled) {
				t.Fatal("loaded XML ownership/health does not match enabled state")
			}
		})
	}
}

func TestWindowsSchedulerStatusRejectsUncertainOutput(t *testing.T) {
	fixture := taskFixture(t, "running.json")
	if _, err := parseWindowsSchedulerStatus(fixture); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"exists", "xml", "enabled", "state", "engines", "last_result"} {
		t.Run("missing-"+field, func(t *testing.T) {
			var obj map[string]any
			if err := json.Unmarshal(fixture, &obj); err != nil {
				t.Fatal(err)
			}
			delete(obj, field)
			data, _ := json.Marshal(obj)
			if _, err := parseWindowsSchedulerStatus(data); err == nil {
				t.Fatalf("missing %s accepted", field)
			}
		})
	}
	for _, tc := range []struct {
		field string
		value any
	}{{"state", 5}, {"engines", []int{0}}, {"xml", "garbage"}, {"enabled", "true"}} {
		t.Run("invalid-"+tc.field, func(t *testing.T) {
			var obj map[string]any
			_ = json.Unmarshal(fixture, &obj)
			obj[tc.field] = tc.value
			data, _ := json.Marshal(obj)
			if _, err := parseWindowsSchedulerStatus(data); err == nil {
				t.Fatal("invalid status accepted")
			}
		})
	}
	if _, err := parseWindowsSchedulerStatus([]byte("Access is denied.")); err == nil {
		t.Fatal("localized failure accepted as absent")
	}
}
