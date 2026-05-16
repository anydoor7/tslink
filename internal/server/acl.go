package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
)

// isAllowed checks whether a caller (identified by login and node tags)
// matches any entry in the allowedUsers list.
// If allowedUsers is empty, all callers are allowed (no restriction).
func isAllowed(login string, nodeTags []string, allowedUsers []string) bool {
	if len(allowedUsers) == 0 {
		return true
	}

	for _, entry := range allowedUsers {
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
		if login == entry {
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
				slog.Warn("acl: no local client available", "remote_addr", r.RemoteAddr)
				writeAccessDenied(w, "access denied: unable to identify caller")
				return
			}

			whois, err := localClient.WhoIs(r.Context(), r.RemoteAddr)
			if err != nil {
				slog.Warn("acl: failed to identify caller", "remote_addr", r.RemoteAddr, "error", err)
				writeAccessDenied(w, "access denied: unable to identify caller")
				return
			}
			if whois == nil || whois.UserProfile == nil {
				slog.Warn("acl: caller identity missing user profile", "remote_addr", r.RemoteAddr)
				writeAccessDenied(w, "access denied: unable to identify caller")
				return
			}

			login := whois.UserProfile.LoginName
			var nodeTags []string
			if whois.Node != nil {
				nodeTags = whois.Node.Tags
			}

			if !isAllowed(login, nodeTags, allowedUsers) {
				slog.Info("acl: access denied", "login", login, "remote_addr", r.RemoteAddr)
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
