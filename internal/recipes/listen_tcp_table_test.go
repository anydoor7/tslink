package recipes

import (
	"context"
	"encoding/binary"
	"net"
	"reflect"
	"strings"
	"testing"
)

// The local/remote ports occupy DWORDs, but only their first two bytes are
// meaningful. Nonzero high bytes catch a decoder that swaps the entire DWORD.
func windowsTableFixture(family uint32, addresses []string, states []uint32, ports []uint16) []byte {
	rowSize, stateOffset, addrOffset, portOffset, pidOffset := 24, 0, 4, 8, 20
	if family == windowsAFInet6 {
		rowSize, stateOffset, addrOffset, portOffset, pidOffset = 56, 48, 0, 20, 52
	}
	b := make([]byte, 4+len(addresses)*rowSize)
	binary.LittleEndian.PutUint32(b[:4], uint32(len(addresses)))
	for i, address := range addresses {
		row := b[4+i*rowSize : 4+(i+1)*rowSize]
		ip := net.ParseIP(address)
		if family == windowsAFInet {
			ip = ip.To4()
		} else {
			ip = ip.To16()
		}
		copy(row[addrOffset:], ip)
		binary.LittleEndian.PutUint32(row[stateOffset:], states[i])
		binary.BigEndian.PutUint16(row[portOffset:], ports[i])
		row[portOffset+2], row[portOffset+3] = 0xaa, 0xbb
		binary.LittleEndian.PutUint32(row[pidOffset:], 42)
	}
	return b
}

func TestWindowsTCPTableDecode(t *testing.T) {
	for _, tc := range []struct {
		name      string
		family    uint32
		addresses []string
		want      []Listener
	}{
		{"IPv4", windowsAFInet, []string{"0.0.0.0", "127.0.0.2", "192.0.2.1", "127.0.0.1", "127.0.0.1"}, []Listener{{"127.0.0.1", 135}, {"127.0.0.2", 49669}}},
		{"IPv6", windowsAFInet6, []string{"::", "::1", "2001:db8::1", "::1", "::1"}, []Listener{{"::1", 135}, {"127.0.0.1", 135}, {"::1", 49669}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			table := windowsTableFixture(tc.family, tc.addresses, []uint32{2, 2, 2, 5, 2}, []uint16{135, 49669, 8123, 8888, 0})
			got, err := decodeWindowsTCPTable(table, tc.family)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("listeners=%v want=%v err=%v", got, tc.want, err)
			}
		})
	}
}

// Port of the reviewer's three localized netstat address cases. Language is
// deliberately confined to fixture labels: production consumes numeric state 2.
func TestReviewWindowsLocalizedListeners(t *testing.T) {
	for _, localized := range []string{"LISTENING", "ABHÖREN"} {
		for _, tc := range []struct {
			address string
			family  uint32
			ip      string
			port    uint16
			want    []Listener
		}{
			{"0.0.0.0:135", windowsAFInet, "0.0.0.0", 135, []Listener{{"127.0.0.1", 135}}},
			{"[::]:135", windowsAFInet6, "::", 135, []Listener{{"::1", 135}, {"127.0.0.1", 135}}},
			{"[::1]:49669", windowsAFInet6, "::1", 49669, []Listener{{"::1", 49669}}},
		} {
			t.Run(localized+"/"+tc.address, func(t *testing.T) {
				got, err := decodeWindowsTCPTable(windowsTableFixture(tc.family, []string{tc.ip}, []uint32{2}, []uint16{tc.port}), tc.family)
				if err != nil || !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("localized listener lost: %v %v", got, err)
				}
			})
		}
	}
}

func TestWindowsTCPTableInvalid(t *testing.T) {
	for _, tc := range []struct {
		name   string
		b      []byte
		family uint32
		cause  string
	}{
		{"family", make([]byte, 4), 99, "unsupported"},
		{"header", []byte{0, 0, 0}, windowsAFInet, "entry count"},
		{"rows", []byte{1, 0, 0, 0}, windowsAFInet, "rows"},
		{"overflow", []byte{255, 255, 255, 255}, windowsAFInet6, "rows"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeWindowsTCPTable(tc.b, tc.family)
			if err == nil || !strings.Contains(err.Error(), tc.cause) {
				t.Fatalf("refusal cause: %v", err)
			}
		})
	}
	for _, f := range []uint32{windowsAFInet, windowsAFInet6} {
		if got, err := decodeWindowsTCPTable(make([]byte, 4), f); err != nil || len(got) != 0 {
			t.Fatalf("empty control: %v %v", got, err)
		}
	}
}

func TestWindowsTCPTableRead(t *testing.T) {
	fixture := windowsTableFixture(windowsAFInet, []string{"127.0.0.1"}, []uint32{2}, []uint16{8123})
	calls := 0
	got, err := readWindowsTCPTable(context.Background(), windowsAFInet, func(b []byte, size *uint32, family uint32) uint32 {
		calls++
		if family != 2 {
			t.Fatal("wrong family")
		}
		*size = uint32(len(fixture))
		if len(b) < len(fixture) {
			return 122
		}
		copy(b, fixture)
		return 0
	})
	if err != nil || !reflect.DeepEqual(got, fixture) || calls != 2 {
		t.Fatalf("table=%x calls=%d err=%v", got, calls, err)
	}
	for _, tc := range []struct {
		name         string
		status, size uint32
		cause        string
	}{
		{"API error", 5, 0, "Windows error 5"},
		{"too small", 122, 3, "invalid TCP table buffer size"},
		{"too large", 122, 16777217, "invalid TCP table buffer size"},
		{"bad success size", 0, 4, "exceeds buffer"},
		{"growing table", 122, 28, "changed during enumeration"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			_, err := readWindowsTCPTable(context.Background(), windowsAFInet, func(_ []byte, size *uint32, _ uint32) uint32 { calls++; *size = tc.size; return tc.status })
			if err == nil || !strings.Contains(err.Error(), tc.cause) {
				t.Fatalf("read refusal cause: %v", err)
			}
			if tc.name == "growing table" && calls != 4 {
				t.Fatalf("unbounded sizing retries: %d calls; want 4", calls)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = readWindowsTCPTable(ctx, windowsAFInet, func([]byte, *uint32, uint32) uint32 { t.Fatal("called API after cancellation"); return 0 })
	if err != context.Canceled {
		t.Fatalf("cancellation: %v", err)
	}
}
