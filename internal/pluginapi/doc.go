// Package pluginapi is the administration API of the Ervisio core plugin
// (PLAN 9.2). It is served on the unix socket server.plugin_socket and never
// on the public listener.
//
// Every request needs the header x-ervisio-plugin carrying a 32-byte secret
// generated at each process start, compared in constant time; without it the
// answer is 404, as for any unknown route. The socket listener adds the
// header itself (like the socket proxy of ErvisioDrive), so the plugin sends
// no secret: the boundary is the permission of the socket file (0660).
//
// Routes, all under /api/plugin/:
//
//	GET  status      version, public URL, upstream for a reverse proxy
//	GET  settings    safe subset of the instance configuration
//	PUT  settings    partial update, persisted in the TOML file
//	GET  storage     targets, policies, caches, warnings
//	GET  runners     instance runners
//	PUT  public-url  set server.public_url (after the Caddy setup)
//
// A change of the configuration file takes effect at the next start of the
// server: the answers say restartRequired.
package pluginapi
