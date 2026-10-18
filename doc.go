// Package coopdock embeds guest WebSocket sessions, game-scoped rooms, and
// server-authoritative movement in an existing HTTP application.
//
// Start with DefaultConfig, adjust its public settings, call New, and mount the
// returned Server.Handler in a host-owned HTTP server. Each Server owns its rooms,
// sessions, connections, and optional operations collector independently.
//
// The package reads no environment variables, installs no signal handlers, and
// opens no listeners. The host owns HTTP timeouts, TLS, and process lifecycle.
// Call Server.Shutdown explicitly: http.Server.Shutdown does not close hijacked
// WebSocket connections. When stopping the whole application, request both
// shutdowns and wait for both results, even if the first reports a deadline.
//
// Admin is disabled by default. If enabled, dispatch raw /admin and /admin/
// paths to Handler before an outer ServeMux can normalize them. Prefix mounts
// require StripPrefix; the dashboard still uses absolute /admin assets and APIs,
// so it additionally needs a root /admin alias or a dedicated origin.
package coopdock
