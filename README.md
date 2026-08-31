# go-lru

Maintenance scope: preserve the published API; focus on bug fixes, security and Go compatibility, with no planned API expansion.

[中文](README_zh.md)

[![Go Reference](https://pkg.go.dev/badge/github.com/MouXiaoJun/lru.svg)](https://pkg.go.dev/github.com/MouXiaoJun/lru)
[![Go Version](https://img.shields.io/badge/go-1.21+-00ADD8?style=flat-square&logo=go)](https://golang.org)
[![License](https://img.shields.io/badge/license-MIT-green.svg?style=flat-square)](LICENSE)

A **zero-dependency**, thread-safe, **generic LRU cache** with optional **per-entry TTL**, for Go 1.21+ (standard library only).

## Features

- ✅ **`Cache[K comparable, V any]`** — comparable, self-equal keys; any value type
- ✅ **True LRU** — `map` + hand-written doubly linked list with head/tail sentinels; evicts the least recently used entry
- ✅ **Thread-safe** — guarded by `sync.Mutex`, verified with `go test -race`
- ✅ **Optional TTL** — `SetWithTTL` per entry (absolute expiry, not sliding); expired entries are **lazily removed** on access
- ✅ **Zero dependencies** — only `sync` / `time` from the standard library
- ✅ **Quality** — model-based fuzzing (`FuzzCacheModel`) + benchmarks

## Install

```bash
go get github.com/MouXiaoJun/lru
```

Requires **Go 1.21+**.

## Quick start

```go
package main

import (
	"fmt"
	"time"

	"github.com/MouXiaoJun/lru"
)

func main() {
	c := lru.New[string, int](100) // panics if capacity <= 0

	c.Set("a", 1)
	c.Set("b", 2)

	if v, ok := c.Get("a"); ok { // Get moves "a" to most-recent
		fmt.Println(v) // 1
	}
	fmt.Println(c.Keys())    // [a b] — most-recent first
	fmt.Println(c.Peek("b")) // 2 true — peek does not reorder
	c.Delete("a")
	c.Clear()

	c.SetWithTTL("session", 42, 5*time.Second) // expires in 5s
}
```

## API

| Method | Description | Complexity |
| --- | --- | --- |
| `New[K, V](capacity int) *Cache[K, V]` | Create cache; **panics if capacity <= 0** (`lru: capacity must be positive`) | O(1) |
| `Get(key K) (V, bool)` | Fetch; on hit move to most-recent; expired treated as miss and removed lazily | O(1) |
| `Set(key K, value V)` | Upsert; evict least-recently-used when over capacity; written entry is permanent | O(1) |
| `SetWithTTL(key K, value V, ttl time.Duration)` | Like Set with a TTL; **ttl <= 0 means permanent** (same as Set) | O(1) |
| `Delete(key K) bool` | Delete; returns true iff a live entry was removed | O(1) |
| `Contains(key K) bool` | Membership, ignoring expired entries (removes them lazily) | O(1) |
| `Peek(key K) (V, bool)` | Query without changing recency order | O(1) |
| `Keys() []K` | Live keys, most-recent first; purges expired entries on the way | O(n) |
| `Len() int` | Entries currently stored (see TTL semantics below) | O(1) |
| `Clear()` | Empty the cache; capacity is preserved | O(1) |
| `Capacity() int` | The cache capacity | O(1) |

All methods are safe for concurrent use. Create caches with `New`; a zero-value `Cache` behaves as empty for reads, and `Set`/`SetWithTTL` panic with a hint.

Do not copy a `Cache` after first use. Keys must compare equal to themselves (`key == key`): `Set`/`SetWithTTL` panic with `lru: key must equal itself` before mutation for NaN or composite keys containing NaN. Interface keys with non-comparable dynamic values panic as they do with a Go map. `Clear` on a zero-value cache is a no-op, not initialization.

## Eviction policy

Classic **LRU**: every hit/write moves the entry to the head (most-recent); when a new entry exceeds capacity, the tail (least-recently-used) is evicted. `map[K]*entry` gives O(1) lookup; the sentinel doubly linked list gives O(1) move/evict. `Keys` is O(n).

## TTL semantics

- **Absolute expiry**: an entry expires at `now + ttl` from write time; hits do **not** extend it (not a sliding window).
- **Lazy removal**: expired entries still occupy capacity until touched by `Get`/`Peek`/`Contains`/`Delete`/`Keys`/`Set`; `Len()` may therefore include not-yet-collected expired entries when TTL is in use. Without TTL, `Len` is always the live count.
- **ttl <= 0 ⇒ permanent**: `SetWithTTL(k, v, 0)` ≡ `Set(k, v)`; `Set` clears any TTL on an existing entry.
- **Last write wins**: writing to an expired key counts as a fresh insert (the stale entry is cleaned first, no capacity wasted).
- Expiry boundary: `now >= expiresAt` is expired.

## vs. freecache / bigcache / golang-lru

| | **go-lru** | **golang-lru** | **freecache** | **bigcache** |
| --- | --- | --- | --- | --- |
| Generic | ✅ `Cache[K, V]` | ✅ v2 `Cache[K, V]` | ❌ `[]byte` | ❌ `[]byte` |
| Policy | true LRU | true LRU (+2Q variant) | approximate LRU (ring buffer) | **not LRU** — FIFO-ish (no reorder on hit) |
| TTL | ✅ per-entry, absolute | v2 `ExpirableLRU` (cache-wide) | ✅ per-entry | ✅ per-entry (janitor) |
| Concurrency | single Mutex | single Mutex | 256 shards, low contention | ~1024 shards, RWMutex |
| Values | any `V` | any `V` (v2) | `[]byte` (size-limited) | `[]byte` |
| Capacity | entry count | entry count | bytes | bytes |

**FAQ:** [HashiCorp golang-lru v2](https://github.com/hashicorp/golang-lru) is generic too; evaluate it when you need APIs such as Resize, eviction callbacks or 2Q. This library keeps a smaller API with per-entry TTL and no background cleanup goroutine. TTL is not sliding-window — reset it yourself in the write path if needed. `New` panics on non-positive capacity; writes also enforce the key requirements above.

## Quality gates

```bash
go vet ./...
go test -race ./...
go test -run xxx -fuzz FuzzCacheModel -fuzztime 5s ./
go test -run xxx -bench . -benchmem ./
```

Benchmarks (Apple M5): `BenchmarkSet` ~544 ns/op (64 B, 1 alloc); `BenchmarkGetHit` ~205 ns/op (0 alloc); `BenchmarkGetMiss` ~205 ns/op (0 alloc); `BenchmarkSetWithTTL` ~907 ns/op; `BenchmarkPeek` ~237 ns/op. Fuzzing cross-checks every public operation against an independent reference model (no panics, identical results and order).

## License

[MIT](LICENSE)
