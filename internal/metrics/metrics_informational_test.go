package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// A 1xx response is not the final status. The middleware must pass it
// through and keep its one final-status slot for the response that follows,
// or net/http sends an implicit 200 in place of the backend's error.
func TestMetricsMiddlewareInformationalThenFinalStatus(t *testing.T) {
	for _, informational := range []int{http.StatusContinue, http.StatusEarlyHints} {
		t.Run(strconv.Itoa(informational), func(t *testing.T) {
			m := New()
			const svc = "probe"
			handler := m.Middleware(svc, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(informational)
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = io.WriteString(w, "backend unavailable")
			}))
			front := httptest.NewServer(handler)
			t.Cleanup(front.Close)

			resp, err := front.Client().Get(front.URL + "/")
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != http.StatusServiceUnavailable || string(body) != "backend unavailable" {
				t.Fatalf("client response = %d %q, want 503 %q", resp.StatusCode, body, "backend unavailable")
			}
			if got := getCounterValue(t, m.RequestsTotal, svc, http.MethodGet, "503"); got != 1 {
				t.Fatalf("requests counted as 503 = %v, want 1", got)
			}
			if got := getCounterValue(t, m.RequestsTotal, svc, http.MethodGet, strconv.Itoa(informational)); got != 0 {
				t.Fatalf("requests counted as %d = %v, want 0", informational, got)
			}
		})
	}
}
