package cmd

// The scheduler wire format is platform independent so XML and status fixtures
// run on every CI host. The COM transport lives in supervision_windows.go.
import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"strings"
	"unicode/utf16"
)

const windowsTaskNamespace = "http://schemas.microsoft.com/windows/2004/02/mit/task"

type windowsTaskSpec struct {
	SID, Executable, ConfigDir, PowerShell string
	NoAutoProvision                        bool
}

func powershellLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func powershellEncoded(s string) string {
	words := utf16.Encode([]rune(s))
	data := make([]byte, len(words)*2)
	for i, word := range words {
		binary.LittleEndian.PutUint16(data[i*2:], word)
	}
	return base64.StdEncoding.EncodeToString(data)
}

func base64UTF8(data []byte) string { return base64.StdEncoding.EncodeToString(data) }

func decodePowerShell(encoded string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(data)%2 != 0 {
		return "", fmt.Errorf("invalid encoded PowerShell")
	}
	words := make([]uint16, len(data)/2)
	for i := range words {
		words[i] = binary.LittleEndian.Uint16(data[i*2:])
	}
	return string(utf16.Decode(words)), nil
}

func (s windowsTaskSpec) arguments() string {
	// Wait for the foreground daemon and propagate its exit code. Detaching here
	// would make a crash look like a successful task and defeat restart-on-failure.
	script := "$ErrorActionPreference='Stop'; $env:TSLINK_CONFIG_DIR=" + powershellLiteral(s.ConfigDir) +
		"; $env:TSLINK_MANAGED_LOGS='1'; & " + powershellLiteral(s.Executable) + " serve --no-browser"
	if s.NoAutoProvision {
		script += " --no-auto-provision"
	}
	script += "; exit $LASTEXITCODE"
	return "-NoLogo -NoProfile -NonInteractive -WindowStyle Hidden -EncodedCommand " + powershellEncoded(script)
}

type windowsTaskXML struct {
	XMLName     xml.Name `xml:"http://schemas.microsoft.com/windows/2004/02/mit/task Task"`
	Version     string   `xml:"version,attr"`
	Description string   `xml:"RegistrationInfo>Description"`
	Triggers    struct {
		Items []struct {
			XMLName xml.Name
			Enabled *bool  `xml:"Enabled"`
			UserID  string `xml:"UserId"`
		} `xml:",any"`
	} `xml:"Triggers"`
	Principals []struct {
		ID        string `xml:"id,attr"`
		UserID    string `xml:"UserId"`
		LogonType string `xml:"LogonType"`
		RunLevel  string `xml:"RunLevel"`
	} `xml:"Principals>Principal"`
	Settings struct {
		MultipleInstances string `xml:"MultipleInstancesPolicy"`
		DisallowBattery   *bool  `xml:"DisallowStartIfOnBatteries"`
		StopBattery       *bool  `xml:"StopIfGoingOnBatteries"`
		HardTerminate     *bool  `xml:"AllowHardTerminate"`
		StartAvailable    *bool  `xml:"StartWhenAvailable"`
		AllowDemand       *bool  `xml:"AllowStartOnDemand"`
		OnlyIdle          *bool  `xml:"RunOnlyIfIdle"`
		OnlyNetwork       *bool  `xml:"RunOnlyIfNetworkAvailable"`
		Enabled           *bool  `xml:"Enabled"`
		TimeLimit         string `xml:"ExecutionTimeLimit"`
		Restart           struct {
			Interval string `xml:"Interval"`
			Count    int    `xml:"Count"`
		} `xml:"RestartOnFailure"`
	} `xml:"Settings"`
	Actions struct {
		Context string `xml:"Context,attr"`
		Exec    []struct {
			XMLName          xml.Name
			Command          string `xml:"Command"`
			Arguments        string `xml:"Arguments"`
			WorkingDirectory string `xml:"WorkingDirectory"`
		} `xml:",any"`
	} `xml:"Actions"`
}

func renderWindowsTask(s windowsTaskSpec) ([]byte, error) {
	if s.SID == "" || s.Executable == "" || s.ConfigDir == "" || s.PowerShell == "" {
		return nil, fmt.Errorf("task requires user SID, executable, config and PowerShell paths")
	}
	esc := func(s string) string {
		var b strings.Builder
		_ = xml.EscapeText(&b, []byte(s))
		return b.String()
	}
	// No execution deadline (the scheduler default is three days), no battery
	// stop, one instance, least privilege and only this user's logon trigger.
	return []byte(xml.Header + fmt.Sprintf(`<Task version="1.2" xmlns="%s">
  <RegistrationInfo><Description>TSLink per-user daemon; config=%s</Description></RegistrationInfo>
  <Triggers><LogonTrigger><Enabled>true</Enabled><UserId>%s</UserId></LogonTrigger></Triggers>
  <Principals><Principal id="User"><UserId>%s</UserId><LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel></Principal></Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries><StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>false</AllowHardTerminate><StartWhenAvailable>true</StartWhenAvailable>
    <AllowStartOnDemand>true</AllowStartOnDemand><RunOnlyIfIdle>false</RunOnlyIfIdle><RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>
    <Enabled>true</Enabled><ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <RestartOnFailure><Interval>PT1M</Interval><Count>255</Count></RestartOnFailure>
  </Settings>
  <Actions Context="User"><Exec><Command>%s</Command><Arguments>%s</Arguments><WorkingDirectory>%s</WorkingDirectory></Exec></Actions>
</Task>
`, windowsTaskNamespace, esc(s.ConfigDir), esc(s.SID), esc(s.SID), esc(s.PowerShell), esc(s.arguments()), esc(s.ConfigDir))), nil
}

func parseWindowsTask(data []byte) (windowsTaskXML, error) {
	var task windowsTaskXML
	err := xml.Unmarshal(data, &task)
	return task, err
}

func windowsTaskConfigMatches(data []byte, dir string) bool {
	task, err := parseWindowsTask(data)
	return err == nil && task.Description == "TSLink per-user daemon; config="+dir
}

func boolIs(p *bool, v bool) bool { return p != nil && *p == v }

func windowsTaskMatches(data []byte, s windowsTaskSpec) bool {
	t, err := parseWindowsTask(data)
	if err != nil || t.Version != "1.2" || !windowsTaskConfigMatches(data, s.ConfigDir) ||
		len(t.Triggers.Items) != 1 || t.Triggers.Items[0].XMLName.Local != "LogonTrigger" || t.Triggers.Items[0].UserID != s.SID || !boolIs(t.Triggers.Items[0].Enabled, true) ||
		len(t.Principals) != 1 || t.Principals[0].ID != "User" || t.Principals[0].UserID != s.SID ||
		t.Principals[0].LogonType != "InteractiveToken" || t.Principals[0].RunLevel != "LeastPrivilege" ||
		t.Actions.Context != "User" || len(t.Actions.Exec) != 1 || t.Actions.Exec[0].XMLName.Local != "Exec" {
		return false
	}
	a := t.Actions.Exec[0]
	p := t.Settings
	return strings.EqualFold(a.Command, s.PowerShell) && a.Arguments == s.arguments() && a.WorkingDirectory == s.ConfigDir &&
		p.MultipleInstances == "IgnoreNew" && boolIs(p.DisallowBattery, false) && boolIs(p.StopBattery, false) &&
		boolIs(p.HardTerminate, false) && boolIs(p.StartAvailable, true) && boolIs(p.Enabled, true) &&
		boolIs(p.AllowDemand, true) && boolIs(p.OnlyIdle, false) && boolIs(p.OnlyNetwork, false) &&
		p.TimeLimit == "PT0S" && p.Restart.Interval == "PT1M" && p.Restart.Count == 255
}

type windowsSchedulerStatus struct {
	Exists     bool   `json:"exists"`
	XML        string `json:"xml"`
	Enabled    bool   `json:"enabled"`
	State      int    `json:"state"`
	Engines    []int  `json:"engines"`
	LastResult int64  `json:"last_result"`
}

func parseWindowsSchedulerStatus(data []byte) (windowsSchedulerStatus, error) {
	var wire struct {
		Exists     *bool   `json:"exists"`
		XML        *string `json:"xml"`
		Enabled    *bool   `json:"enabled"`
		State      *int    `json:"state"`
		Engines    *[]int  `json:"engines"`
		LastResult *int64  `json:"last_result"`
	}
	data = []byte(strings.TrimPrefix(strings.TrimSpace(string(data)), "\ufeff"))
	if err := json.Unmarshal(data, &wire); err != nil {
		return windowsSchedulerStatus{}, fmt.Errorf("parse scheduler status: %w", err)
	}
	if wire.Exists == nil {
		return windowsSchedulerStatus{}, fmt.Errorf("scheduler status missing exists")
	}
	if !*wire.Exists {
		return windowsSchedulerStatus{}, nil
	}
	if wire.XML == nil || wire.Enabled == nil || wire.State == nil || wire.Engines == nil || wire.LastResult == nil || *wire.State < 0 || *wire.State > 4 {
		return windowsSchedulerStatus{}, fmt.Errorf("scheduler status missing/invalid task fields")
	}
	if _, err := parseWindowsTask([]byte(*wire.XML)); err != nil {
		return windowsSchedulerStatus{}, fmt.Errorf("scheduler task XML: %w", err)
	}
	for _, pid := range *wire.Engines {
		if pid <= 0 {
			return windowsSchedulerStatus{}, fmt.Errorf("scheduler status invalid engine PID")
		}
	}
	return windowsSchedulerStatus{true, *wire.XML, *wire.Enabled, *wire.State, *wire.Engines, *wire.LastResult}, nil
}
