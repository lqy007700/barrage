---
name: barrage-go-danmaku
description: Use when continuing work on the barrage Go service in this repo. Covers the current gnet + ants + websocket + protobuf + Kafka architecture, loop-sharded room model, current implementation status, known constraints, and the next recommended optimization steps.
---

# Barrage Go Danmaku

Use this skill when working in `/Users/zxnl/Data/code/go-application/barrage`.

## Project Goal

This repo is a high-performance barrage service with these design goals:

- Language: Go
- Network framework: `gnet`
- Worker pool: `ants`
- Protocol: `WebSocket` + `Protobuf`
- Architecture emphasis:
  - gnet only handles network IO, connection lifecycle, and handing work off
  - business logic runs in worker goroutines
  - room connections are sharded by loop
  - local broadcast should go back to the target loop
  - distributed broadcast goes through Kafka

## Current Architecture

### Network and message path

- `internal/server/event_handler.go`
  - `OnOpen`: creates `ConnContext` and allocates `ConnID`
  - `OnTraffic`: handles websocket handshake, frame parsing, and dispatches inbound protobuf bytes to worker pool
  - `OnClose`: removes the connection from room shard using `RemoveConnInLoop`
- `internal/server/websocket.go`
  - minimal websocket handshake
  - frame parsing
  - base support for half-packets / sticky packets using per-connection buffers

### Connection context

- `internal/connctx/context.go`
- Important fields:
  - `ConnID`
  - `UserID`
  - `RoomID`
  - `LoopIdx`
  - `IsPremium`
  - `HandshakeDone`
  - `HandshakeBuffer`
  - `ReadBuffer`

### Room model

- `internal/room/manager.go`
- `Room.Shards[loopIdx]` stores room-local connections by loop shard
- `ActiveLoops` and `ActiveLoopIndexes(roomID)` are used to find active loop shards quickly
- interface names were intentionally tightened:
  - `AddConnInLoop`
  - `RemoveConnInLoop`
  - `RangeShardInLoop`
- Important constraint:
  - shard-level operations are intended to happen in the owning loop context

### Loop routing

- `internal/app/loop_registry.go`
  - maps `gnet.EventLoop -> loopIdx`
- `internal/app/loop_dispatcher.go`
  - maps `loopIdx -> gnet.EventLoop`
  - dispatches tasks back into target event loops with `EventLoop.Execute`
- `internal/app/app.go`
  - `ResolveLoopIdx(c)` uses `Conn.EventLoop()` to assign stable loop indices

### Broadcasting

- `internal/broadcast/broadcaster.go`
- Current rule:
  - broadcast finds active loop indexes for a room
  - each loop gets one dispatched task
  - `broadcastInLoop` runs inside the target loop and:
    - iterates `RangeShardInLoop`
    - skips sender connection by `SenderConnId`
    - applies outbound buffer downgrade:
      - if `OutboundBuffered() > 512KB` and receiver is not premium, skip sending
- No direct current-goroutine broadcast fallback is intended anymore

### Business handling

- `internal/app/app.go`
- Current operations:
  - `OP_JOIN_ROOM`
  - `OP_CHAT`
  - `OP_HEARTBEAT`
- `handleChat` flow:
  - decode `ChatMsg`
  - run sensitive-word check
  - if hit, log and reject
  - else wrap as `OP_BROADCAST`
  - encode broadcast payload once
  - local broadcast or Kafka publish

### Sensitive-word rule

- `internal/filter/filter.go`
- `internal/filter/simple_filter.go`
- Current policy:
  - detect only
  - do not replace
  - log hit and reject message
  - do not broadcast
  - do not publish to Kafka

### Kafka

- `internal/mq/producer.go`
- `internal/mq/consumer.go`
- `internal/mq/model.go`
- Current design:
  - fixed topic, not one topic per room
  - use `room_id` as Kafka key so the same room hashes to the same partition
  - local dev can run with Kafka disabled
- Recent improvement:
  - Kafka envelope was moved from JSON wrapper to protobuf `BroadcastEnvelope`
- Important:
  - after changing `internal/pb/barrage.proto`, regenerate `internal/pb/barrage.pb.go`

## Protobuf

- `internal/pb/barrage.proto`
- Messages include:
  - `Frame`
  - `JoinRoomReq`
  - `ChatMsg`
  - `BroadcastMsg`
  - `BroadcastEnvelope`
  - `Heartbeat`

Regenerate with:

```bash
protoc --go_out=. --go_opt=paths=source_relative internal/pb/barrage.proto
```

If needed first:

```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
```

## Current Testing Setup

### Server

Run from repo root:

```bash
go run cmd/barrage/main.go
```

### Node test client

- `test/test-client.js`
- used for websocket + protobuf smoke tests

Examples from repo root:

```bash
node test/test-client.js listen 1 1001
node test/test-client.js send 1 1002 "你好，这是第一条弹幕"
```

## Important Decisions Already Made

- Do not broadcast back to the sending connection
- Keep an independent `LoopDispatcher`
  - this is expected to be reused for:
    - broadcast
    - kick user
    - force close connection
    - loop-local cleanup tasks
    - other local connection operations
- Do not rely on direct current-goroutine broadcast fallback
- Keep `ChatMsg` decoding in `handleChat`
  - it is now needed for sensitive-word filtering
- Kafka topic should stay fixed
  - room is routed by key, not by topic name per room

## Recommended Next Steps

Work in roughly this order unless the user redirects:

1. Regenerate protobuf after recent `barrage.proto` changes if not already done
2. Clear any compile errors caused by the new protobuf message
3. Reduce `WorkerPoolSize`
   - current config is still large
   - recommended first step: `runtime.NumCPU() * 64` or `* 128`
4. Move sensitive words from hardcoded list in `app.go` to file loading
5. Optionally return a client-visible rejection message when sensitive words hit
6. Add tests for:
   - sensitive-word rejection
   - no self-echo on broadcast
   - loop dispatcher binding and dispatch behavior
7. Improve websocket protocol edge handling
8. Add metrics / counters

## High-Priority Risks Still Open

- `Room.Shards` uses native maps and still relies on correct loop ownership semantics
- websocket support is usable but not yet production-complete
- worker pool size is likely too large
- observability is still weak
- room empty cleanup can be improved later
- Kafka production behavior still needs more lifecycle and error-handling polish

## Capacity Planning Notes From Prior Discussion

- Topic should be fixed, for example `barrage-broadcast`
- room ID should be Kafka message key
- same room should hash to the same partition
- partitions are not one-per-room
- for the discussed scale:
  - 3 million online
  - 2k rooms daily, 4k peak
  - single hot room up to 100k online
- earlier rough planning guidance was:
  - barrage access layer: around 24 machines as a safe starting point
  - per machine: about `32 vCPU / 64GB RAM / 20-25Gbps NIC`
- earlier single-machine planning estimate for current architecture:
  - safe production planning target: `100k ~ 120k` long connections per machine
  - more aggressive target after optimization and validation: `120k ~ 150k+`

## If You Continue Work In A New Window

Read these files first:

- `/Users/zxnl/Data/code/go-application/barrage/internal/app/app.go`
- `/Users/zxnl/Data/code/go-application/barrage/internal/broadcast/broadcaster.go`
- `/Users/zxnl/Data/code/go-application/barrage/internal/room/manager.go`
- `/Users/zxnl/Data/code/go-application/barrage/internal/app/loop_dispatcher.go`
- `/Users/zxnl/Data/code/go-application/barrage/internal/server/event_handler.go`
- `/Users/zxnl/Data/code/go-application/barrage/internal/pb/barrage.proto`

Then check whether `internal/pb/barrage.pb.go` has already been regenerated after the latest proto changes.
