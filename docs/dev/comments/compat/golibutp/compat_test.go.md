# compat/golibutp/compat_test.go

Every comment in `compat/golibutp/compat_test.go` at e56c9aa, verbatim.

## Line 16 — func udpPair

```
// udpPair is two sockets on loopback, b accepting.
```

## Line 48 — func TestSyncFirewallRefusesWithoutAnswer

```
// The synchronous firewall callback is asked about each connection request,
// with the requester's UDP address, and a refusal gets no answer: the dial
// runs into its own deadline.
```

## Line 67 — func TestAsyncFirewallRefuses

```
// The asynchronous callback is used when there is no synchronous one.
```

## Line 75 — func TestOptionsReachNewConnections

```
// Context options are libutp's: set and read back, refused outside what
// utp_context_set_option takes, and given to connections made afterwards --
// including one accepted by an Accept that was already waiting when the
// option changed, since libutp copies the options into a socket when its
// SYN arrives.
```

## Line 84 — func TestOptionsReachNewConnections

```
// One connection first, so that b's accept loop is surely waiting in a
	// fresh Accept, called with the defaults, when the options change.
```

## Line 114 — func TestConnSetWriteBufferLen

```
// SetWriteBufferLen changes a live connection's window ceiling.
```

## Line 124 — func TestUnsendableDialFailsAtOnce

```
// A send the operating system says can never succeed fails a dial at once,
// as go-libutp's sendto callback makes it, rather than after libutp's
// 21 seconds of retransmitted SYNs.
```

## Line 138 — func TestNonUtpDatagramsShareThePort

```
// The Socket is a PacketConn for everything that is not uTP, alongside its
// connections, with working deadlines where go-libutp panics.
```

## Line 172 — func TestConnClosedErrors

```
// Read and Write after Close return ErrConnClosed, as go-libutp's do.
```

## Line 183 — func TestTransferAndTeardown

```
// A connection's data goes both ways, and the connection leaves the socket's
// books once both ends have closed.
```

## Line 205 — func TestWriteStatus

```
// WriteStatus lists each open socket in go-libutp's format.
```

## Line 214 — func TestLogOptions

```
// Log options open the logger up as libutp's do.
```

## Line 220 — func TestLogOptions

```
// debug
```

## Line 227 — func TestDialCompletesBeforeAccept

```
// A dial completes whether or not anything is waiting in Accept, as it does
// against libutp, and Accept then returns the connection: the engine answers
// a SYN as it arrives on a socket with a configuration of its own, which
// utpnet's always has (utp.WithAcceptConfig).
```
