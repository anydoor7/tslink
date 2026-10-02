package server

import (
	"net/http"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
)

var peopleExpiryFn = registry.ExpirePeople

// peopleMiddleware re-reads the atomic registry for each request; an accepted
// network share cannot bypass it. It deliberately does not cache WhoIs.
// now is captured by startNodeLocked before any handler goroutine exists.
func peopleMiddleware(path string, initial registry.Service, provide func() (*LocalClient, error), now func() time.Time) func(http.Handler) http.Handler {
	expire := peopleExpiryFn
	unavailable := func(err error, issues []registry.ServiceIssue) bool {
		if err != nil {
			return true
		}
		for _, issue := range issues {
			if issue.Name == initial.Name {
				return true
			}
		}
		return false
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reg, issues, err := registry.Preflight(path)
			if unavailable(err, issues) {
				writeAccessDenied(w, "access denied: people registry unavailable")
				return
			}
			svc := initial
			for _, current := range reg.Services {
				if current.Name == initial.Name {
					svc = current
					break
				}
			}
			if len(reg.People) == 0 && !svc.PeopleScoped && reg.Portal == nil {
				if len(svc.AllowedUsers) == 0 {
					next.ServeHTTP(w, r)
					return
				}
				lc, _ := provide()
				ACLMiddleware(svc.AllowedUsers, lc)(next).ServeHTTP(w, r)
				return
			}
			t := now()
			needsLatch := false
			for _, p := range reg.People {
				for _, g := range p.Grants {
					if !g.Expired && g.ExpiresAt != nil && !t.Before(*g.ExpiresAt) {
						needsLatch = true
					}
				}
			}
			if needsLatch {
				if _, err := expire(path, t); err != nil {
					writeAccessDenied(w, "access denied: cannot persist expiry")
					return
				}
				// Re-read after taking the mutation lock: another process could
				// have removed or changed the person while we were waiting.
				reg, issues, err = registry.Preflight(path)
				if unavailable(err, issues) {
					writeAccessDenied(w, "access denied: people registry unavailable")
					return
				}
			}
			lc, err := provide()
			if err != nil || lc == nil {
				writeAccessDenied(w, "access denied: unable to identify caller")
				return
			}
			who, err := lc.WhoIs(r.Context(), r.RemoteAddr)
			if err != nil || who == nil || who.UserProfile == nil {
				writeAccessDenied(w, "access denied: unable to identify caller")
				return
			}
			var tags []string
			if who.Node != nil {
				tags = who.Node.Tags
			}
			allowed, _ := AppAccessAt(reg, svc, who.UserProfile.LoginName, tags, t)
			if !allowed {
				writeAccessDenied(w, "access denied")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
