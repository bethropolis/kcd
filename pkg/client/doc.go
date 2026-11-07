// Package client provides the official Go client for communicating with the
// kcd daemon via its local Unix IPC socket.
//
// The client supports single-request calls, persistent keep-alive sessions,
// bidirectional event watching with resume offsets, and typed error handling.
//
// # Stability
//
// The kcd IPC wire protocol and pkg/client Go API follow semantic versioning.
// New methods, request fields, and event types are additive and backwards-compatible.
// Callers can negotiate and verify server capabilities dynamically using Hello.
package client
