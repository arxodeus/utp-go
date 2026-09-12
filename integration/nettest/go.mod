module github.com/zen-eth/utp-go/integration/nettest

go 1.24.2

require (
	github.com/ethereum/go-ethereum v1.14.11
	github.com/zen-eth/utp-go v0.0.0
	golang.org/x/net v0.47.0
)

require (
	github.com/google/btree v1.1.3 // indirect
	github.com/holiman/uint256 v1.3.1 // indirect
	github.com/valyala/fastrand v1.1.0 // indirect
	golang.org/x/crypto v0.44.0 // indirect
	golang.org/x/exp v0.0.0-20231110203233-9a3e6036ecaa // indirect
	golang.org/x/sys v0.38.0 // indirect
)

replace github.com/zen-eth/utp-go => ../..
