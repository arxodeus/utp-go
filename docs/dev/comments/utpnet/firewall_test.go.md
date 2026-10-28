# utpnet/firewall_test.go

Every comment in `utpnet/firewall_test.go` at e56c9aa, verbatim.

## Line 12 — func TestFirewallRefusesBeforeAnyStateExists

```
// The firewall callback: refuse a peer before any state exists for it.
//
// libutp asks its embedder about every SYN for a connection it does not
// already have, after the duplicate check and before it creates the socket:
//
//	// true means yes, block connection.  false means no, don't block.
//	if (utp_call_on_firewall(ctx, to, tolen)) {
//	    ...
//	    return 1;
//	}
//	                                        (utp_internal.cpp:2975-2982)
//
// This library accepted a callback at the anacrolix adapter and ignored it,
// which is the worst of the three options available: a caller that passed a
// blocklist got no blocking, and no sign that it was not happening.
//
// Two properties matter beyond "the connection does not happen". A refusal
// must create no connection state, or a blocklist becomes a way to make a node
// allocate; and it must be silent, because answering -- with a RESET or
// anything else -- confirms to a refused peer that something is listening
// here, which is the opposite of what a blocklist is for. libutp's refusal is
// a bare `return 1`.
```

## Line 47 — func TestFirewallRefusesBeforeAnyStateExists

```
// Refuse everyone, so the test does not depend on which port the
			// kernel handed the dialler.
```

## Line 99 — func TestFirewallRefusesBeforeAnyStateExists

```
// No state, and no answer.
```

## Line 120 — func TestFirewallAdmitsWhatItDoesNotRefuse

```
// A firewall that admits a peer must not get in its way.
```
