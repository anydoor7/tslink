package middleware

import "net/http"

// Middleware is a function that wraps an HTTP handler.
type Middleware func(http.Handler) http.Handler

// Pipeline chains multiple middleware in order.
// The first middleware in the slice is the outermost (executed first).
func Pipeline(middlewares ...Middleware) Middleware {
	return func(final http.Handler) http.Handler {
		for i := len(middlewares) - 1; i >= 0; i-- {
			final = middlewares[i](final)
		}
		return final
	}
}
