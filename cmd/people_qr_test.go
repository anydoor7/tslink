package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
	"github.com/makiuchi-d/gozxing"
	decoder "github.com/makiuchi-d/gozxing/qrcode"
)

func independentQRDecode(t *testing.T, img image.Image, want string) {
	t.Helper()
	bitmap, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		t.Fatal(err)
	}
	result, err := decoder.NewQRCodeReader().Decode(bitmap, nil)
	if err != nil || result.GetText() != want {
		t.Fatalf("independent decoder: got=%v err=%v want=%q", result, err, want)
	}
}

func TestPhoneQRPNGAndTerminalIndependentDecode(t *testing.T) {
	for _, payload := range []string{"https://home.tailnet.ts.net", "https://photos.tailnet.ts.net", "https://login.tailscale.com/admin/invite/token-fixture", "https://home.tailnet.ts.net/path?name=%E4%B8%AD"} {
		t.Run(payload, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "qr.png")
			if err := qrPNG(payload, file); err != nil {
				t.Fatal(err)
			}
			f, err := os.Open(file)
			if err != nil {
				t.Fatal(err)
			}
			img, err := png.Decode(f)
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
			independentQRDecode(t, img, payload)
			text, err := terminalQR(payload)
			if err != nil || strings.Contains(text, "\x1b") {
				t.Fatal(err, text)
			}
			lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
			width := len([]rune(lines[0]))
			img2 := image.NewGray(image.Rect(0, 0, width*8, len(lines)*16))
			for y, line := range lines {
				for x, r := range []rune(line) {
					for dy := 0; dy < 16; dy++ {
						for dx := 0; dx < 8; dx++ {
							shade := uint8(255)
							if r == '█' || (r == '▀' && dy < 8) || (r == '▄' && dy >= 8) {
								shade = 0
							}
							img2.SetGray(x*8+dx, y*16+dy, color.Gray{Y: shade})
						}
					}
				}
			}
			independentQRDecode(t, img2, payload)
		})
	}
}

func TestPeopleQRPayloadGuideAndBearerDiscipline(t *testing.T) {
	base := PeopleResult{Person: PeopleView{Grants: []PeopleGrantView{{URL: "https://photos.tailnet.ts.net", Active: true}}}, Portal: tsruntime.PortalState{Enabled: true, URL: "https://home.tailnet.ts.net"}, Invites: []PeopleInviteView{{App: "photos", InviteURL: "https://login.tailscale.com/admin/invite/fixture"}}}
	for _, tc := range []struct {
		name   string
		portal bool
		args   peopleArguments
		want   string
		fail   bool
	}{
		{"portal", true, peopleArguments{QR: true}, "https://home.tailnet.ts.net", false},
		{"app", false, peopleArguments{QR: true}, "https://photos.tailnet.ts.net", false},
		{"no qr", true, peopleArguments{}, "", false},
		{"invite", true, peopleArguments{QR: true, QRInvite: "photos", PrintLinks: true}, "https://login.tailscale.com/admin/invite/fixture", false},
		{"no credential consent", true, peopleArguments{QR: true, QRInvite: "photos"}, "", true},
		{"missing invite", true, peopleArguments{QR: true, QRInvite: "missing", PrintLinks: true}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := base
			r.Portal.Enabled = tc.portal
			err := preparePeopleQR(&r, tc.args)
			if (err != nil) != tc.fail {
				t.Fatal(r, err)
			}
			if !tc.fail && r.QRPayload != tc.want {
				t.Fatal(r)
			}
			if len(r.Guide) > 5 || len(r.GuideZH) > 5 || len(r.Guide) != 4 {
				t.Fatal(r.Guide, r.GuideZH)
			}
			if tc.name == "invite" && !strings.Contains(r.QRWarning, "credential") {
				t.Fatal(r)
			}
		})
	}
	r := base
	r.Portal.URL = ""
	if err := preparePeopleQR(&r, peopleArguments{QR: true}); err == nil {
		t.Fatal("pending portal fell back to an app URL")
	}
	var out bytes.Buffer
	r = base
	if err := preparePeopleQR(&r, peopleArguments{QR: true}); err != nil {
		t.Fatal(err)
	}
	writePeopleResult(&out, "people add", r, true)
	var wire output.Result
	if err := json.Unmarshal(out.Bytes(), &wire); err != nil || !wire.OK || wire.SchemaVersion != 1 || bytes.Contains(out.Bytes(), []byte("█")) || bytes.Contains(out.Bytes(), []byte("PNG")) {
		t.Fatal(out.String(), err)
	}
	out.Reset()
	writePeopleResult(&out, "people add", r, false)
	if !strings.Contains(out.String(), "▀") || !strings.Contains(out.String(), "Install Tailscale on your phone") {
		t.Fatal(out.String())
	}
}

func TestPeopleQRFailurePathsAndCommand(t *testing.T) {
	if _, err := terminalQR(strings.Repeat("x", 5000)); err == nil {
		t.Fatal("oversize terminal payload accepted")
	}
	if err := qrPNG(strings.Repeat("x", 5000), filepath.Join(t.TempDir(), "bad.png")); err == nil {
		t.Fatal("oversize PNG payload accepted")
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "directory")
	if err := os.Mkdir(file, 0700); err != nil {
		t.Fatal(err)
	}
	if err := qrPNG("https://home.tailnet.ts.net", file); err == nil {
		t.Fatal("directory overwritten")
	}
	if err := qrPNG("https://home.tailnet.ts.net", filepath.Join(dir, "missing", "q.png")); err == nil {
		t.Fatal("missing parent silently created")
	}
	paths := peopleTestPaths(t)
	_, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Apps: []string{"photos"}, QR: true, QRInvite: "photos"}, false)
	if err == nil {
		t.Fatal("missing print consent accepted")
	}
	regBefore, _ := os.ReadFile(paths.Registry)
	restoreInviteCommandSeams(t)
	inviteRegistryPathFn = func() (string, error) { return paths.Registry, nil }
	invitePIDPathFn = func() (string, error) { return paths.PID, nil }
	inviteRuntimeSnapshotPathFn = func() (string, error) { return paths.Snapshot, nil }
	group := newPeopleCmd()
	group.SetArgs([]string{"add", "alice", "--apps", "photos", "--qr-png", ""})
	group.SetOut(&bytes.Buffer{})
	if err := group.Execute(); err == nil {
		t.Fatal("empty PNG flag accepted")
	}
	after, _ := os.ReadFile(paths.Registry)
	if !bytes.Equal(regBefore, after) {
		t.Fatal("invalid flags changed grants")
	}
}

func TestPeopleQRCommandReadyPortal(t *testing.T) {
	paths := peopleTestPaths(t)
	if _, err := changePortal(paths, portalArguments{Owner: "owner"}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Apps: []string{"photos"}}, false); err != nil {
		t.Fatal(err)
	}
	reg, _, err := registry.Preflight(paths.Registry)
	if err != nil {
		t.Fatal(err)
	}
	fp, err := tsruntime.RegistryFingerprint(reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now().Add(-time.Minute)
	if err := os.WriteFile(paths.PID, []byte("42"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(paths.PID, started, started); err != nil {
		t.Fatal(err)
	}
	snapshot := tsruntime.NewSnapshot(42, started, fp, time.Now(), nil)
	snapshot.Portal = tsruntime.PortalState{Enabled: true, Hostname: "home", State: "running", URL: "https://home.tailnet.ts.net"}
	if err := tsruntime.Save(paths.Snapshot, snapshot); err != nil {
		t.Fatal(err)
	}
	restoreInviteCommandSeams(t)
	inviteRegistryPathFn = func() (string, error) { return paths.Registry, nil }
	invitePIDPathFn = func() (string, error) { return paths.PID, nil }
	inviteRuntimeSnapshotPathFn = func() (string, error) { return paths.Snapshot, nil }
	inviteIsRunningFn = func(string) bool { return true }
	inviteReadPIDFn = func(string) (int, error) { return 42, nil }
	before, _ := os.ReadFile(paths.Registry)
	for _, asJSON := range []bool{false, true} {
		group := newPeopleCmd()
		group.PersistentFlags().Bool("json", asJSON, "JSON result")
		var out bytes.Buffer
		group.SetOut(&out)
		file := filepath.Join(t.TempDir(), "phone.png")
		group.SetArgs([]string{"update", "alice", "--qr", "--qr-png", file})
		if err := group.Execute(); err != nil {
			t.Fatal(out.String(), err)
		}
		if !strings.Contains(out.String(), snapshot.Portal.URL) || (asJSON && strings.Contains(out.String(), "▀")) || (!asJSON && !strings.Contains(out.String(), "▀")) {
			t.Fatal(out.String())
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		independentQRDecode(t, img, snapshot.Portal.URL)
	}
	after, _ := os.ReadFile(paths.Registry)
	if !bytes.Equal(before, after) {
		t.Fatal("QR-only update changed the person grant")
	}
}
