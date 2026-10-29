# compat/golibutp/conn_publish_test.go

Every comment in `compat/golibutp/conn_publish_test.go` at e56c9aa, verbatim.

## Line 9 — func TestCloseWhileAConnIsBeingPublished

```
// A Socket.Close that lands between a connection's publication and the end
// of newConn must not leave newConn reading the stream Close took away: it
// read c.us outside the lock, after Close had set it to nil, and panicked.
```
