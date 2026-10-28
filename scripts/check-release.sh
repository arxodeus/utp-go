#!/usr/bin/env bash
# check-release.sh builds what a consumer builds, from the published tags and
# nothing else: a fresh module in a temporary directory, outside any checkout,
# with no go.work and no filesystem replacements. It compiles a use of the
# root package, of utpnet, and of the go-libutp drop-in through the one
# `replace` line compat/golibutp/README.md tells an application to add.
#
#   scripts/check-release.sh v0.1.0
#
# Run it after pushing the tags (docs/dev/RELEASING.md). It fails if either tag is
# missing, if compat/golibutp requires a root version that does not exist,
# or if anything advertised does not compile.
set -euo pipefail

version=${1:?usage: $0 vX.Y.Z}
dir=$(mktemp -d)
trap 'rm -rf "$dir"' EXIT
cd "$dir"

export GOWORK=off GOFLAGS=-mod=mod

go mod init example.com/consumer >/dev/null
go mod edit -replace "github.com/anacrolix/go-libutp=github.com/arxodeus/utp/compat/golibutp@$version"

cat > main.go <<'GO'
package main

import (
	"context"
	"log/slog"
	"net"

	libutp "github.com/anacrolix/go-libutp"
	utp "github.com/arxodeus/utp"
	"github.com/arxodeus/utp/utpnet"
)

func main() {
	ctx := context.Background()
	sock, err := utp.Bind(ctx, "udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}, slog.Default())
	if err != nil {
		panic(err)
	}
	sock.Close()

	s, err := utpnet.Listen(ctx, "udp", "127.0.0.1:0", nil)
	if err != nil {
		panic(err)
	}
	s.Close()

	l, err := libutp.NewSocket("udp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	l.Close()
}
GO

go get "github.com/arxodeus/utp@$version" "github.com/anacrolix/go-libutp@v1.5.1"
go mod tidy
CGO_ENABLED=0 go build -o /dev/null .
CGO_ENABLED=0 go run .
echo "release $version: a clean consumer builds and runs the root package, utpnet and the go-libutp drop-in"
