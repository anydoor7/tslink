// Package admin provides the embedded web dashboard and REST API for TSLink.
//
// StartAdminNode is the integration point for starting the admin dashboard
// as a tsnet node named "tslink-admin". The actual tsnet integration is
// enabled when the user configures the dashboard feature. This file
// documents the expected wiring so the server package can call it.
package admin

// StartAdminNode is called by the server to start the admin dashboard.
// It blocks until ctx is cancelled, serving the Handler over HTTPS via
// a dedicated tsnet node whose hostname is "tslink-admin".
//
// This is a placeholder — actual tsnet wiring is added when the user
// enables the dashboard. For now it documents the integration point.
//
// Usage (server.go):
//
//	go admin.StartAdminNode(ctx, cfg.RegistryPath(), cfg.PIDPath())
func StartAdminNode(regPath, pidPath string) *Handler {
	return New(regPath, pidPath)
}
