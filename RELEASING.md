# Releasing

How a version of `github.com/arxodeus/utp` and of its go-libutp drop-in,
`github.com/arxodeus/utp/compat/golibutp`, is published, and what has to pass
first. Two modules, released together, the root first.

## Before tagging

On the commit to be tagged, all of these pass. The quick ones run on every
push in CI (`.github/workflows/ci.yml`); the long ones run nightly in the same
workflow, and are run by hand on the release commit and their logs kept.

| Gate | Command | CI |
| --- | --- | --- |
| Formatting, vet, staticcheck | `gofmt -l .`, `go vet ./...`, `staticcheck ./...` | every push |
| Cross-build | `GOOS=… GOARCH=… go vet ./...` for linux (amd64, arm64, 386), darwin, freebsd, windows | every push |
| Root tests, cgo and not | `go test -short ./...` with `CGO_ENABLED=1` and `=0` | every push |
| Race | `go test -race -short ./...` | every push |
| Each nested module | `go test ./...` and `go test -race ./...` in `compat/golibutp`, `integration/nettest`, `integration/goutp`, `integration/anacrolix` -- neither at the root enters them | every push |
| Vulnerabilities | `govulncheck ./...` in the root and each nested module, on the current Go release | every push |
| Transport tests on macOS and Windows | `go test -short ./...` | every push |
| Full root suite | `go test -timeout 120m ./...` | nightly |
| Full race suite | `UTP_TEST_TRANSFERS=50 go test -race -timeout 120m ./...` | nightly |
| Pinned libutp reference | `scripts/check-libutp-reference.sh` | nightly |
| Fuzzing | each target in [FUZZING.md](FUZZING.md) for 5 minutes, revision and toolchain recorded | nightly |

A platform is claimed when its tests have run on it, not when it builds.

## Tagging

The compat module requires the root at the version being released (its
go.mod names it; the `replace` there serves development only and is ignored
by anyone depending on it), so the root's tag has to exist first.

1. The repository is `github.com/arxodeus/utp`, which is the module path. A
   module served from a repository of another name is not installable under
   its path.
2. `compat/golibutp/go.mod` requires `github.com/arxodeus/utp vX.Y.Z`, the
   version about to be tagged.
3. Tag the root, `vX.Y.Z`, and the compat module, `compat/golibutp/vX.Y.Z` --
   it lives in a subdirectory, so its tags carry the directory -- on that
   commit, and push both.
4. `scripts/check-release.sh vX.Y.Z`. It builds a fresh module outside any
   checkout, with no `go.work` and no filesystem replacements, against the
   published tags: a use of the root package, of `utpnet`, and of the go-libutp
   drop-in through the one `replace` line its README tells an application to
   add. A tag that is missing, a compat module that requires a version that
   does not exist, or anything advertised that does not compile, fails it.

## After

The Go version in each go.mod is the minimum, and CI builds with it and with
the current release. It is not a recommendation: the standard library of
older releases has vulnerabilities this code reaches -- GO-2026-4971 (`net`
on Windows, fixed in Go 1.25.10) from the root module, and four in
`net/http` (fixed in Go 1.26.9) that compat/golibutp reaches through
go-libutp's own dependencies. govulncheck finds none in any module on Go
1.26.9. Build with a current release. `go list -m all` in each module is the dependency
inventory; `govulncheck ./...` in each is the vulnerability scan.
