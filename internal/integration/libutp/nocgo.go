//go:build !cgo

// Package libutp wraps the vendored C libutp for interoperability testing.
//
// This build has cgo disabled, so the package is empty. The file exists so
// that `CGO_ENABLED=0 go build ./...` succeeds for the module.
package libutp
