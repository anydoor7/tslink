package server

import (
	"net/http"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
	"tailscale.com/client/tailscale/apitype"
)

// The responses TSLink itself generates for an app, as opposed to the app's
// own: an access refusal and the closing of a route or node. The gateway and
// an app's own node share them, so the same request gets the same status,
// headers and body whichever way it arrived, and the access log's bytes_out
// for a person and app cannot depend on the entry path. The access-log reason
// (acl, people, no_grant, route_closed, node_closed, ...) stays the path's own;
// the visitor-facing code of a refusal or closing both paths can produce is the
// same on both. Some refusals exist on one path only (cross_origin_blocked and
// route_changed on the gateway, the legacy allow list on an app node) and use
// the same page.

// appResponseCommand labels the JSON error envelope of a generated response.
// It names neither a CLI command nor an entry path.
const appResponseCommand = "app request"

// accessDenial is one refusal as the visitor sees it.
type accessDenial struct {
	// Visitor is the visitor-facing code, which picks the message. The access
	// log keeps its own reason for the same refusal. (Not named Code: the
	// errcode guard treats a Code field as an emitted stable error code.)
	Visitor string
	// App is named only to a caller who can see the app or request access to
	// it, and only for the refusals that are about that grant.
	App         string
	Visible     bool
	Requestable bool
	// Dashboard is where the caller can see their apps; empty when the caller
	// cannot be sent there.
	Dashboard string
}

// message is the visitor text for the denial.
func (d accessDenial) message() string {
	message := denialMessage(d.Visitor)
	if (d.Visible || d.Requestable) && (d.Visitor == "no_grant" || d.Visitor == "expired" || d.Visitor == "revoked") {
		message += ": " + d.App
	}
	return message
}

// links reports whether the page may send the caller to the dashboard. A
// caller TSLink could not identify, or a policy it could not read, gets none:
// nothing says the caller may open it.
func (d accessDenial) links() bool {
	return d.Visitor != "whois_failed" && d.Visitor != "registry_unavailable"
}

// writeAccessDenial answers an access refusal with 403: an HTML page for a
// browser navigation, the JSON error envelope otherwise.
func writeAccessDenial(w http.ResponseWriter, r *http.Request, d accessDenial) {
	dashboard := d.Dashboard
	if !d.links() {
		dashboard = ""
	}
	gatewayErrorPage(w, r, http.StatusForbidden, d.Visitor, d.message(), dashboard, d.Requestable)
}

func denialMessage(code string) string {
	switch code {
	case "whois_failed":
		return "Unable to identify this connection"
	case "registry_unavailable":
		return "Access policy is temporarily unavailable"
	case "cross_origin_blocked":
		return "Cross-origin request blocked"
	case "route_changed":
		return "This route is being reconfigured"
	case "expired":
		return "Your access has expired"
	case "revoked":
		return "Your access has been revoked"
	default:
		return "You do not have access to this app"
	}
}

// appClosingCode is the visitor code of a request TSLink cut because it is
// closing a gateway route or an app node. The access log tells them apart
// (route_closed, node_closed); the visitor is told the same thing.
const appClosingCode = "app_restarting"

// appClosingPage answers a request that TSLink itself ended with 503.
func appClosingPage(w http.ResponseWriter, r *http.Request, app string) {
	gatewayErrorPage(w, r, http.StatusServiceUnavailable, appClosingCode, appLabel(app)+" is being reconfigured; try again", "", false)
}

// appLabel names the app in a generated message.
func appLabel(app string) string {
	if app == "" {
		return "The app"
	}
	return app
}

// dashboardFunc reports the portal dashboard's address, or "" while there is
// none. It is called per request and must not take Server.mu: stopping a node
// holds that lock while it waits for the node's requests.
type dashboardFunc func() string

func (d dashboardFunc) address() string {
	if d == nil {
		return ""
	}
	return d()
}

// denialDashboard is the dashboard link for the page refusing an identified
// caller, on the gateway and on an app's own node alike: the address is given
// only while the dashboard is up and only to a caller who could open it right
// now. A refused caller learns nothing about the portal that they could not
// already reach, and the same person and app get the same page on either path.
func denialDashboard(address string, reg *registry.Registry, who *apitype.WhoIsResponse, app string, now time.Time) string {
	if address == "" || !gatewayAdmits(reg, who, app, true, now) {
		return ""
	}
	return address
}
