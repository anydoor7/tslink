package inspect

import "testing"

func TestEndpointExactURLPredicates(t *testing.T) {
	const host = "https://photos.tail-guest.ts.net"
	cases := []struct {
		name      string
		endpoint  EndpointView
		exact     bool
		exactHTTP bool
	}{
		{"tailnet https", EndpointView{Kind: EndpointKindHTTPS, State: EndpointStateExact, Display: host}, true, true},
		{"funnel public_https keeps the node URL", EndpointView{Kind: EndpointKindPublicHTTPS, State: EndpointStateExact, Display: host}, true, true},
		{"tcp is exact but not HTTP", EndpointView{Kind: EndpointKindTCP, State: EndpointStateExact, Display: "db.tail-guest.ts.net:5432"}, true, false},
		{"unknown kind", EndpointView{Kind: EndpointKindUnknown, State: EndpointStateExact, Display: host}, true, false},
		{"expected state with a display", EndpointView{Kind: EndpointKindPublicHTTPS, State: EndpointStateExpected, Display: host}, false, false},
		{"exact state without a display", EndpointView{Kind: EndpointKindHTTPS, State: EndpointStateExact}, false, false},
		{"template host", EndpointView{Kind: EndpointKindHTTPS, State: EndpointStateExact, Display: "https://photos.<tailnet>.ts.net"}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u, ok := tc.endpoint.ExactURL()
			if ok != tc.exact || (ok && u != tc.endpoint.Display) || (!ok && u != "") {
				t.Fatalf("ExactURL = (%q, %v), want ok=%v", u, ok, tc.exact)
			}
			u, ok = tc.endpoint.ExactHTTPSURL()
			if ok != tc.exactHTTP || (ok && u != tc.endpoint.Display) || (!ok && u != "") {
				t.Fatalf("ExactHTTPSURL = (%q, %v), want ok=%v", u, ok, tc.exactHTTP)
			}
		})
	}
}
