//go:build !cgo

// Package libutp wraps the vendored C libutp for interoperability testing.
//
// This build has cgo disabled, so the package is empty. Its whole purpose is
// linking a C library; there is nothing to provide without cgo. The file
// exists so `CGO_ENABLED=0 go build ./...` succeeds for the module rather
// than failing with "build constraints exclude all Go files" -- building the
// library without cgo is the point of this fork.
package libutp
