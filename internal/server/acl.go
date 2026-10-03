package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/anydoor7/tslink/internal/registry"
)

// isAllowed checks whether a caller (identified by login and node tags)
// matches any entry in the allowedUsers list.
// If allowedUsers is empty, all callers are allowed (no restriction).
func isAllowed(login string, nodeTags []string, allowedUsers []string) bool {
	if !utf8.ValidString(login) {
		return false
	}
	if len(allowedUsers) == 0 {
		return true
	}

	normalizedLogin, loginErr := registry.NormalizePerson(login)
	for _, entry := range allowedUsers {
		entry = strings.TrimSpace(entry)
		// Check tag match: entry is "tag:xxx" and caller's node has that tag
		if strings.HasPrefix(entry, "tag:") {
			for _, t := range nodeTags {
				if t == entry {
					return true
				}
			}
			continue
		}

		// Check email/login match
		canonical, err := registry.NormalizePerson(entry)
		if loginErr == nil && err == nil && normalizedLogin == canonical {
			return true
		}
	}

	return false
}

// ACLMiddleware returns an HTTP middleware that enforces access control.
// allowedUsers is a list of email addresses and/or "tag:xxx" entries.
// If allowedUsers is empty, all requests are allowed.
// It uses the Tailscale LocalClient WhoIs to identify the caller.
func ACLMiddleware(allowedUsers []string, localClient *LocalClient) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(allowedUsers) == 0 {
				next.ServeHTTP(w, r)
				return
			}

			if localClient == nil {
				slog.Warn("acl: no local client available")
				accessDeny(r, "acl")
				writeAccessDenied(w, "access denied: unable to identify caller")
				return
			}

			whois, err := localClient.WhoIs(r.Context(), r.RemoteAddr)
			if err != nil {
				slog.Warn("acl: failed to identify caller", "error", err)
				accessDeny(r, "acl")
				writeAccessDenied(w, "access denied: unable to identify caller")
				return
			}
			if whois == nil || whois.UserProfile == nil {
				slog.Warn("acl: caller identity missing user profile")
				accessDeny(r, "acl")
				writeAccessDenied(w, "access denied: unable to identify caller")
				return
			}

			login := whois.UserProfile.LoginName
			var nodeTags []string
			if whois.Node != nil {
				nodeTags = whois.Node.Tags
			}

			accessAttest(r, whois, matchedLegacy(login, nodeTags, allowedUsers))
			if !isAllowed(login, nodeTags, allowedUsers) {
				slog.Info("acl: access denied", "login", login)
				accessDeny(r, "acl")
				writeAccessDenied(w, "access denied")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func writeAccessDenied(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	json.NewEncoder(w).Encode(map[string]string{
		"error": msg,
	})
}
