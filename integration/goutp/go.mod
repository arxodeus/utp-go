module github.com/zen-eth/utp-go/integration/goutp

go 1.24.2

require (
	github.com/anacrolix/go-utp v0.0.0-20260908033909-9f1664acf866
	github.com/ethereum/go-ethereum v1.14.11
	github.com/zen-eth/utp-go v0.0.0
)

require (
	github.com/google/btree v1.1.3 // indirect
	github.com/holiman/uint256 v1.3.1 // indirect
	github.com/valyala/fastrand v1.1.0 // indirect
	golang.org/x/exp v0.0.0-20240823005443-9b4947da3948 // indirect
	golang.org/x/sys v0.28.0 // indirect
)

replace github.com/zen-eth/utp-go => ../..
