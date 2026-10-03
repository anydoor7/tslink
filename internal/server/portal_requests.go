package server

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/anydoor7/tslink/internal/health"
	"github.com/anydoor7/tslink/internal/registry"
	"tailscale.com/client/tailscale/apitype"
)

type PortalRequests struct {
	key           [32]byte
	Notifier      health.NotifierConfig
	NotifierError string
	Publish       func()
	Record        func(registry.RequestEvent)
}

// Like F11's post-save duration hook: synchronous, no I/O by default, and no
// dependency on F4. Integration can record the typed, secret-free event.
var accessRequestRecordedFn = func(registry.RequestEvent) {}

func newPortalRequests(c health.NotifierConfig, configErr error, publish func()) (*PortalRequests, error) {
	p := &PortalRequests{Notifier: c, Publish: publish, Record: accessRequestRecordedFn}
	if configErr != nil {
		p.NotifierError = "alert_config_invalid"
	}
	if _, err := rand.Read(p.key[:]); err != nil {
		return nil, fmt.Errorf("portal request protection unavailable")
	}
	return p, nil
}

// Stateless CSRF token bound to canonical host and WhoIs identity. It expires
// in two hours and rotates on portal restart. No cookies or user session DB.
func (p *PortalRequests) token(login, host string, expiry int64) string {
	mac := hmac.New(sha256.New, p.key[:])
	fmt.Fprintf(mac, "%s\x00%s\x00%d", login, strings.ToLower(host), expiry)
	return strconv.FormatInt(expiry, 10) + "." + hex.EncodeToString(mac.Sum(nil))
}

func (p *PortalRequests) validToken(value, login, host string, now time.Time) bool {
	stamp, _, ok := strings.Cut(value, ".")
	expiry, err := strconv.ParseInt(stamp, 10, 64)
	if !ok || err != nil || expiry <= now.Unix() || expiry > now.Add(2*time.Hour).Unix() {
		return false
	}
	return hmac.Equal([]byte(value), []byte(p.token(login, host, expiry)))
}

func requestVisitor(reg *registry.Registry, who *apitype.WhoIsResponse) (string, bool) {
	// A tagged or shared-in peer is not a human tailnet member. WhoIs owns
	// these fields; request headers and visitor form fields never supply identity.
	if who == nil || who.UserProfile == nil || who.Node == nil || len(who.Node.Tags) > 0 ||
		who.Node.Sharer != 0 || !who.Node.Hostinfo.Valid() || who.Node.Hostinfo.ShareeNode() {
		return "", false
	}
	login, err := registry.NormalizePerson(who.UserProfile.LoginName)
	if err != nil || strings.HasPrefix(login, "tag:") {
		return "", false
	}
	for _, person := range reg.People {
		if person.Login == login && (person.Revoked || registry.PersonIsGuest(person)) {
			return "", false
		}
	}
	return login, true
}

func (p *PortalRequests) post(w http.ResponseWriter, r *http.Request, path, login, host string, now time.Time) {
	if len(r.Header.Values("Origin")) != 1 {
		http.Error(w, "Invalid Origin", http.StatusForbidden)
		return
	}
	if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
		http.Error(w, "Use the request form", http.StatusUnsupportedMediaType)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Request form is too large or incomplete", http.StatusBadRequest)
		return
	}
	for key, values := range r.PostForm {
		if len(values) != 1 || (key != "csrf" && key != "app" && key != "duration" && key != "note") {
			http.Error(w, "Invalid request form", http.StatusBadRequest)
			return
		}
	}
	if !p.validToken(r.PostForm.Get("csrf"), login, host, now) {
		http.Error(w, "This form expired. Reload the page and try again.", http.StatusForbidden)
		return
	}
	result, err := registry.SubmitAccessRequest(path, login, r.PostForm.Get("app"), r.PostForm.Get("duration"), r.PostForm.Get("note"), now)
	if err != nil {
		status := http.StatusBadRequest
		code, _ := registry.ErrorCode(err)
		switch code {
		case "access_request_unavailable":
			status = http.StatusForbidden
		case "access_request_duplicate":
			status = http.StatusConflict
		case "access_request_rate_limited":
			status = http.StatusTooManyRequests
			w.Header().Set("Retry-After", "3600")
		case "access_request_busy", "access_request_capacity":
			status = http.StatusServiceUnavailable
		}
		// Stable code only: error text never echoes untrusted form data or paths.
		http.Error(w, "Request could not be sent: "+code, status)
		return
	}
	event := result.Event()
	if p.Record != nil {
		p.Record(event)
	}
	if p.Publish != nil {
		p.Publish()
	}
	// Existing F2 delivery: no shell, bounded command/process/webhook handling.
	// The request is already durable. Delivery failure never undoes it or
	// causes a duplicate notification on a visitor retry.
	notification := "none"
	if p.NotifierError != "" {
		notification = "failed"
	} else if p.Notifier.Kind() != "none" {
		notification = "sent"
		if health.Notify(r.Context(), p.Notifier, health.Event{Kind: "access_requested", Service: result.App, Subject: result.Who, At: now.UTC(), Request: &event}) != nil {
			notification = "failed"
		}
	}
	w.Header().Set("X-TSLink-Notification", notification)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (p *PortalRequests) page(reg *registry.Registry, path, login, host string, now time.Time) ([]string, []registry.AccessRequest, string, error) {
	apps := []string{}
	for _, svc := range reg.Services {
		if svc.Requestable && registry.PeopleServiceSupported(svc) {
			apps = append(apps, svc.Name)
		}
	}
	requests, err := registry.ListAccessRequests(path, now)
	if err != nil {
		return nil, nil, "", err
	}
	visible := []registry.AccessRequest{}
	for _, r := range requests {
		if r.Who != login {
			continue
		}
		// A service made non-requestable is hidden immediately, including its
		// old request history; it does not leak names through this side channel.
		for _, app := range apps {
			if r.App == app {
				visible = append(visible, r)
				break
			}
		}
	}
	return apps, visible, p.token(login, host, now.Add(2*time.Hour).Unix()), nil
}

func requestStatusText(status string) string {
	switch status {
	case registry.RequestPending:
		return "Waiting for the owner."
	case registry.RequestApproved:
		return "Approved. Your app is ready above when its address is available."
	case registry.RequestDenied:
		return "The owner declined this request."
	default:
		return "This request timed out. You can ask again."
	}
}

// Caller identity for owner-only MCP request tools. Local stdio has no remote
// caller and retains the existing trusted owner process contract.
type mcpCallerKey struct{}
type MCPCaller struct {
	Login string
	Tags  []string
}

func MCPCallerFromContext(ctx context.Context) (MCPCaller, bool) {
	c, ok := ctx.Value(mcpCallerKey{}).(MCPCaller)
	return c, ok
}
