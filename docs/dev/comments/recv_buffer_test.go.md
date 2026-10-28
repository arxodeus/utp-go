# recv_buffer_test.go

Every comment in `recv_buffer_test.go` at e56c9aa, verbatim.

## Line 28 — func TestRecvBufferAvailable

```
// Write out-of-order packet.
```

## Line 36 — func TestRecvBufferAvailable

```
// Write in-order packet.
```

## Line 44 — func TestRecvBufferAvailable

```
// Read all data.
```

## Line 82 — func TestRecvBufferWrite

```
// Write out-of-order packet.
```

## Line 96 — func TestRecvBufferWrite

```
// Write in-order packet.
```

## Line 112 — func TestRecvBufferWriteExceedsAvailable

```
// Refused while the buffer holds something to read: the reader will free the
// room. With nothing readable, the next packet is taken whatever its size (see
// receiveBuffer.Admits), so this holds one byte first.
```

## Line 136 — func TestRecvBufferRead

```
// Write out-of-order packet.
```

## Line 151 — func TestRecvBufferRead

```
// Write in-order packet.
```

## Line 191 — func TestAckNum

```
// Write out-of-order packet.
```

## Line 200 — func TestAckNum

```
// Write in-order packet.
```

## Line 225 — func TestSelectiveAck

```
// Write out-of-order packet.
```

## Line 235 — func TestSelectiveAck

```
// Write in-order packet.
```

## Line 250 — func TestSelectiveACKOverflow

```
// 初始时无选择性ACK
```

## Line 259 — func TestSelectiveACKOverflow

```
// 写入乱序包（注意Go中uint16自动处理溢出）
```

## Line 260 — func TestSelectiveACKOverflow

```
// 65533 + 2 = 65535 (overflow)
```

## Line 263 — func TestSelectiveACKOverflow

```
// 65533 + 3 = 0 (overflow)
```

## Line 266 — func TestSelectiveACKOverflow

```
// 65534 + 5 = 2 (overflow)
```

## Line 269 — func TestSelectiveACKOverflow

```
// 验证选择性ACK
```

## Line 275 — func TestSelectiveACKOverflow

```
// 构造期望的确认位图
```

## Line 277 — func TestSelectiveACKOverflow

```
// 对应序列号0（起始0+0）
```

## Line 278 — func TestSelectiveACKOverflow

```
// 对应序列号1（起始0+1）
```

## Line 279 — func TestSelectiveACKOverflow

```
// 对应序列号3（起始0+3）
```

## Line 285 — func TestSelectiveAckWindowIsBounded

```
// The selective-ack bitfield is a fixed 30-entry window starting at
// `ack_nr + 2`, and encodes to exactly four bytes, whatever is pending.
// libutp: utp_internal.cpp:797 (`ext_len = 4`), :805 (`min(14+16, ...)`).
```

## Line 291 — func TestSelectiveAckWindowIsBounded

```
// A gap at 101, one packet just past it, and one far past the window.
```

## Line 310 — func TestSelectiveAckWindowIsBounded

```
// Entry i reports on ack_nr + 2 + i, so 102 is entry 0.
```

## Line 321 — func TestSelectiveAckIgnoresPacketsPastTheWindow

```
// A packet pending beyond the window produces an all-zero bitfield rather
// than a wider one. libutp does the same: it scans a fixed window and emits
// four bytes regardless of what lies past it.
```
