package recipes

import (
	"encoding/hex"
	"net"
	"strconv"
	"strings"
)

func parseLsof(raw string) []Listener {
	var result []Listener
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "n") {
			address := strings.TrimSuffix(strings.TrimPrefix(line, "n"), " (LISTEN)")
			result = append(result, loopbackListeners(address)...)
		}
	}
	return result
}
func parseProcTCP(raw string) []Listener {
	var result []Listener
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[3] != "0A" {
			continue
		}
		ipHex, portHex, ok := strings.Cut(fields[1], ":")
		if !ok {
			continue
		}
		b, err := hex.DecodeString(ipHex)
		if err != nil || (len(b) != 4 && len(b) != 16) {
			continue
		}
		// Linux procfs encodes each address word in host byte order on our supported
		// little-endian builds (amd64/arm64). Reverse bytes inside every 32-bit word.
		for i := 0; i < len(b); i += 4 {
			b[i], b[i+3] = b[i+3], b[i]
			b[i+1], b[i+2] = b[i+2], b[i+1]
		}
		port, err := strconv.ParseUint(portHex, 16, 16)
		if err != nil {
			continue
		}
		result = append(result, loopbackListeners(net.JoinHostPort(net.IP(b).String(), strconv.Itoa(int(port))))...)
	}
	return result
}
