# utpnet/connection_cap_test.go

Every comment in `utpnet/connection_cap_test.go` at e56c9aa, verbatim.

## Line 10 — func TestMaxConnectionsRefusesPastTheCap

```
// Options.MaxConnections reaches the socket, over real UDP.
//
// A cap of 1 admits two connections -- the check is libutp's "more than", see
// utp.WithMaxConnections -- and refuses the third, silently: its dial times
// out, nothing is accepted, and no RESET is sent.
```
