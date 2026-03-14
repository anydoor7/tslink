package middleware

import (
	"crypto/subtle"
	"net/http"
)

// BasicAuth returns a Middleware that enforces HTTP Basic Authentication.
// It returns 401 Unauthorized with a WWW-Authenticate header on failure.
func BasicAuth(username, password string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u, p, ok := r.BasicAuth()
			if !ok {
				w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			usernameMatch := subtle.ConstantTimeCompare([]byte(u), []byte(username)) == 1
			passwordMatch := subtle.ConstantTimeCompare([]byte(p), []byte(password)) == 1

			if !usernameMatch || !passwordMatch {
				w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
