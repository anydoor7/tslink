package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"html/template"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/anydoor7/tslink/internal/health"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
)

// PortalRequestPath accepts CSRF-protected requests from human tailnet members.
const PortalRequestPath = "/access-requests"

type PortalApp struct {
	Name       string     `json:"name"`
	URL        string     `json:"url"`
	Health     string     `json:"health"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	ExpiryText string     `json:"expiry"`
	AccessNote string     `json:"access_note,omitempty"`
}

// BrowserLink keeps TCP connection addresses as text, matching tslink url.
func (app PortalApp) BrowserLink() bool { return strings.HasPrefix(app.URL, "https://") }

type portalPage struct {
	Apps         []PortalApp              `json:"apps"`
	Requestable  []string                 `json:"requestable,omitempty"`
	Requests     []registry.AccessRequest `json:"requests,omitempty"`
	RequestError string                   `json:"request_error,omitempty"`
	CSRF         string                   `json:"-"`
}

// PortalHandler uses live, uncached WhoIs and the same decision as app nodes.
// Apps must return runtime observations only; authorization stays here.
type PortalHandler struct {
	RegistryPath  string
	LocalClient   *LocalClient
	CanonicalHost func() string
	Apps          func(*registry.Registry, time.Time) map[string]PortalApp
	Now           func() time.Time
	Requests      *PortalRequests
}

const portalCSS = `:root{color-scheme:light dark;--bg:#f6f7fb;--card:#fff;--text:#192336;--muted:#526078;--line:#dbe1eb;--accent:#2556b8}*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--text);font:16px/1.55 system-ui,sans-serif}main{max-width:1000px;margin:auto;padding:48px 24px}header{margin-bottom:32px}.eyebrow{color:var(--accent);font-weight:700;letter-spacing:.08em;font-size:.8rem}h1{font-size:clamp(2rem,6vw,3rem);line-height:1.15;margin:12px 0}p{margin:8px 0;color:var(--muted)}.apps{display:grid;grid-template-columns:repeat(auto-fit,minmax(min(100%,280px),1fr));gap:16px;list-style:none;padding:0}.app,.help,.empty{border:1px solid var(--line);border-radius:16px;background:var(--card);padding:24px}.app .address{overflow-wrap:anywhere}.app h2{font-size:1.25rem;margin:0 0 12px;overflow-wrap:anywhere}.app a{color:var(--accent);display:block;font-weight:650;overflow-wrap:anywhere;padding:8px 0;margin-top:12px}.app a:focus-visible{outline:3px solid var(--accent);outline-offset:4px}.status{font-size:.9rem;font-weight:650}.expiry{font-size:.9rem;margin-top:16px}form input,form select,form textarea,form button{font:inherit;max-width:100%;border:1px solid var(--line);border-radius:8px;padding:8px;background:var(--bg);color:var(--text)}form textarea{width:100%}form button{cursor:pointer;color:var(--accent);font-weight:650}form :focus-visible{outline:3px solid var(--accent);outline-offset:3px}.help{margin-top:32px}.help p{overflow-wrap:anywhere}.help h2,.empty h2{font-size:1.15rem;margin:0 0 8px}footer{font-size:.8rem;margin-top:28px;color:var(--muted)}@media(max-width:480px){main{padding:28px 16px}.app,.help,.empty{padding:20px}}@media(prefers-color-scheme:dark){:root{--bg:#101723;--card:#192333;--text:#edf1fa;--muted:#b2bfd3;--line:#334259;--accent:#9bbcff}}`

var portalTemplate = template.Must(template.New("portal").Funcs(template.FuncMap{"requestStatus": requestStatusText}).Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Your apps · TSLink</title><style>` + portalCSS + `</style></head>
<body><main><header><div class="eyebrow">HOME · PRIVATE APPS</div><h1>Your apps, in one place.</h1><p>Bookmark this page. Keep Tailscale connected, then choose an app below.</p></header>
{{if .Apps}}<ul class="apps">{{range .Apps}}<li class="app"><h2>{{.Name}}</h2><div class="status">Status: {{.Health}}</div>{{if .URL}}{{if .BrowserLink}}<a href="{{.URL}}">Open {{.Name}} <span aria-hidden="true">↗</span></a><p class="address">{{.URL}}</p>{{else}}<p>Connect using your app:</p><p class="address">{{.URL}}</p>{{end}}{{else}}<p>Address not ready. Ask the owner for help.</p>{{end}}{{if .AccessNote}}<p>{{.AccessNote}}</p>{{end}}<p class="expiry">{{.ExpiryText}}</p></li>{{end}}</ul>{{else}}<section class="empty" aria-labelledby="empty-title"><h2 id="empty-title">No apps available yet</h2><p>Ask the owner to share an app with the account you use in Tailscale. If access ended, the owner can renew it.</p></section>{{end}}
{{if .RequestError}}<section class="help"><p>{{.RequestError}}</p></section>{{end}}{{if .Requestable}}<section class="help" aria-labelledby="request-title"><h2 id="request-title">Ask for an app or more time</h2><form method="post" action="/access-requests"><input type="hidden" name="csrf" value="{{.CSRF}}"><p><label>App <select name="app" required>{{range .Requestable}}<option value="{{.}}">{{.}}</option>{{end}}</select></label></p><p><label>How long? (optional) <input name="duration" maxlength="128" placeholder="24h, 3d or 7d"></label></p><p><label>Note for the owner (optional, 500 characters)<br><textarea name="note" maxlength="500" rows="3"></textarea></label></p><button type="submit">Send request</button></form><p>The owner chooses when access ends. Please keep private information out of your note.</p></section>{{end}}{{if .Requests}}<section class="help" aria-labelledby="requests-title"><h2 id="requests-title">Your requests</h2>{{range .Requests}}<article><h3>{{.App}}</h3><p>{{requestStatus .Status}}</p>{{if .Note}}<p>Your note: {{.Note}}</p>{{end}}{{if .Reason}}<p>Owner's reason: {{.Reason}}</p>{{end}}</article>{{end}}</section>{{end}}<aside class="help" aria-labelledby="help-title"><h2 id="help-title">Need a hand?</h2><p>Open Tailscale on your phone or computer and make sure it says connected. Sign in with the account the owner invited.</p><p>Healthy means the app responded to its last check. Degraded means it had a recent problem. Down means repeated checks failed. Unknown means there is no recent check.</p><p>If an app is missing or will not open, keep Tailscale connected and ask the owner for help.</p></aside><footer>TSLink works with Tailscale, independent project.</footer></main></body></html>
`))

func portalCSP() string {
	hash := sha256.Sum256([]byte(portalCSS))
	return "default-src 'none'; style-src 'sha256-" + base64.StdEncoding.EncodeToString(hash[:]) + "'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'; object-src 'none'"
}

func (h PortalHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	portalSecurityHeaders(w)
	host := registry.CanonicalProxyHost(nil, h.CanonicalHost())
	if host == "" || !strings.EqualFold(r.Host, host) || (r.URL.Host != "" && !strings.EqualFold(r.URL.Host, host)) {
		http.Error(w, "Portal address unavailable or incorrect. Use the address from the owner.", http.StatusForbidden)
		return
	}
	origins := r.Header.Values("Origin")
	if len(origins) > 1 || (len(origins) == 1 && !mcpOriginMatchesHost(origins[0], host)) {
		http.Error(w, "Invalid Origin", http.StatusForbidden)
		return
	}
	post := h.Requests != nil && r.Method == http.MethodPost && r.URL.Path == PortalRequestPath
	if r.Method != http.MethodGet && r.Method != http.MethodHead && !post {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "This page is read-only.", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path != "/" && r.URL.Path != "/api/apps" && !post {
		http.NotFound(w, r)
		return
	}
	if h.LocalClient == nil {
		writeAccessDenied(w, "access denied: unable to identify caller")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	who, err := h.LocalClient.WhoIs(ctx, r.RemoteAddr)
	if err != nil || who == nil || who.UserProfile == nil {
		writeAccessDenied(w, "access denied: unable to identify caller")
		return
	}
	reg, _, err := registry.PortalPreflight(h.RegistryPath)
	if err != nil || reg.Portal == nil || !reg.Portal.Enabled || reg.Portal.Funnel {
		http.Error(w, "Apps are temporarily unavailable. Ask the owner for help.", http.StatusServiceUnavailable)
		return
	}
	var tags []string
	if who.Node != nil {
		tags = who.Node.Tags
	}
	now := h.Now()
	login, canRequest := requestVisitor(reg, who)
	if post {
		if !canRequest {
			writeAccessDenied(w, "Requests are available to tailnet members only.")
			return
		}
		h.Requests.post(w, r, h.RegistryPath, login, host, now)
		return
	}
	visible := *reg
	visible.Services = nil
	decisions := make(map[string]AppAccessDecision)
	for _, svc := range reg.Services {
		decision := AppAccessDecisionAt(reg, svc, who.UserProfile.LoginName, tags, now)
		if decision.DirectoryVisible {
			visible.Services = append(visible.Services, svc)
			decisions[svc.Name] = decision
		}
	}
	// Only observe authorized directory entries; hidden nodes are never probed.
	observed := h.Apps(&visible, now)
	page := portalPage{Apps: []PortalApp{}}
	if h.Requests != nil && canRequest {
		page.Requestable, page.Requests, page.CSRF, err = h.Requests.page(reg, h.RegistryPath, login, host, now)
		if err != nil {
			page.Requestable, page.Requests, page.CSRF = nil, nil, ""
			page.RequestError = "Requests are temporarily unavailable. Ask the owner for help."
		}
		sort.Strings(page.Requestable)
	}
	for _, svc := range visible.Services {
		decision := decisions[svc.Name]
		expiry := decision.ExpiresAt
		app, ok := observed[svc.Name]
		if !ok {
			app = PortalApp{Health: health.Unknown}
		}
		app.Name, app.ExpiresAt = svc.Name, expiry
		app.AccessNote = ""
		if !decision.IdentityEnforced {
			app.AccessNote = "Anyone who can reach this device can connect; TSLink can't limit it per person."
		}
		app.ExpiryText = "No scheduled expiry."
		if expiry != nil {
			app.ExpiryText = "Access ends " + expiry.UTC().Format("January 2, 2006 at 15:04 UTC") + "."
		}
		page.Apps = append(page.Apps, app)
	}
	sort.Slice(page.Apps, func(i, j int) bool { return page.Apps[i].Name < page.Apps[j].Name })
	if r.URL.Path == "/api/apps" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if r.Method != http.MethodHead {
			_ = json.NewEncoder(w).Encode(output.NewSuccess("portal apps", page))
		}
		return
	}
	var body bytes.Buffer
	if err := portalTemplate.Execute(&body, page); err != nil {
		http.Error(w, "Page unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method != http.MethodHead {
		_, _ = w.Write(body.Bytes())
	}
}

func portalSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", portalCSP())
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
}

func portalSecurityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		portalSecurityHeaders(w)
		next.ServeHTTP(w, r)
	})
}
