package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/registry"
)

// An app's own node answers a caller who is not let through with the refusal,
// never with the request limit, whatever the size of the request: the 413
// names the app and the configured limit, which only a caller who may use the
// app is told. The same request through the gateway gets the same answer.
func TestRequestLimitsAfterAccessAnswersOversizeOnlyToAnAdmittedCaller(t *testing.T) {
	refusing := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			accessDeny(r, "people")
			writeAccessDenial(w, r, accessDenial{Visitor: "no_grant", App: "photos"})
		})
	}
	admitting := func(next http.Handler) http.Handler { return next }
	type result struct {
		status  int
		body    string
		reached bool
		wrapped bool // the access check saw the request's body inside the limits wrapper
	}
	serve := func(service registry.Service, access func(http.Handler) http.Handler, body string) (result, []inspect.WarningView) {
		var res result
		var warnings serviceLimitWarnings
		backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			res.reached = true
			w.WriteHeader(http.StatusOK)
		})
		probe := func(next http.Handler) http.Handler {
			inner := access(next)
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, res.wrapped = r.Body.(*progressBody)
				inner.ServeHTTP(w, r)
			})
		}
		h := RequestLimitsAfterAccess(service, func(w inspect.WarningView) { warnings.add(w) }, probe, backend)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
		res.status, res.body = w.Code, w.Body.String()
		return res, warnings.snapshot()
	}
	limited := registry.Service{Name: "photos", Type: registry.TypeProxy, RequestLimits: &registry.RequestLimits{MaxBody: "8B"}}
	const small, big = "tiny", "much-too-large"

	refusedBig, warned := serve(limited, refusing, big)
	refusedSmall, _ := serve(limited, refusing, small)
	if refusedBig.status != http.StatusForbidden || refusedBig.reached || refusedBig != refusedSmall {
		t.Fatalf("a refused caller is told something that depends on the size: big=%+v small=%+v", refusedBig, refusedSmall)
	}
	for _, leak := range []string{"photos", "exceeds", "8 bytes", "max-request-body"} {
		if strings.Contains(refusedBig.body, leak) {
			t.Fatalf("a refused caller learns %q: %q", leak, refusedBig.body)
		}
	}
	if len(warned) != 0 {
		t.Fatalf("a refused caller's oversize raised the owner's warning: %+v", warned)
	}
	// The limits still wrap the access check (so the body is bounded and disposed
	// of whatever the answer), not the other way round.
	if !refusedBig.wrapped || !refusedSmall.wrapped {
		t.Fatalf("the access check ran outside the limits wrapper: %+v %+v", refusedBig, refusedSmall)
	}

	admittedBig, warned := serve(limited, admitting, big)
	if admittedBig.status != http.StatusRequestEntityTooLarge || admittedBig.reached || !strings.Contains(admittedBig.body, "service photos: request body exceeds 8 bytes") {
		t.Fatalf("an admitted caller is not told the limit: %+v", admittedBig)
	}
	if len(warned) != 1 || warned[0].Code != registry.CodeRequestBodyLimit {
		t.Fatalf("an admitted caller's oversize raised no single owner warning: %+v", warned)
	}
	if admittedSmall, _ := serve(limited, admitting, small); admittedSmall.status != http.StatusOK || !admittedSmall.reached {
		t.Fatalf("an admitted caller within the limit is refused: %+v", admittedSmall)
	}

	// A limit setting the node cannot honor fails closed with the same order:
	// its message names a flag, so only an admitted caller reads it.
	invalid := registry.Service{Name: "photos", Type: registry.TypeProxy, RequestLimits: &registry.RequestLimits{MaxBody: "unlimited"}}
	if got, _ := serve(invalid, refusing, small); got.status != http.StatusForbidden || got.reached || strings.Contains(got.body, "ack-unlimited-request-body") {
		t.Fatalf("a refused caller learns the node's limit settings are invalid: %+v", got)
	}
	if got, _ := serve(invalid, admitting, small); got.status != http.StatusServiceUnavailable || got.reached || !strings.Contains(got.body, "ack-unlimited-request-body") {
		t.Fatalf("an admitted caller is not told the settings are invalid: %+v", got)
	}

	// The gateway-style order (RequestLimitsMiddleware inside the access check)
	// and the immediate wrapper keep their behavior: a 413 at once.
	immediate := httptest.NewRecorder()
	RequestLimitsMiddleware(limited, nil, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("reached the backend") })).
		ServeHTTP(immediate, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(big)))
	if immediate.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("RequestLimitsMiddleware no longer refuses an oversized request at once: %d", immediate.Code)
	}
}
