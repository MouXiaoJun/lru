# go-lru

[English](README.md)

[![Go Reference](https://pkg.go.dev/badge/github.com/MouXiaoJun/lru.svg)](https://pkg.go.dev/github.com/MouXiaoJun/lru)
[![Go Version](https://img.shields.io/badge/go-1.21+-00ADD8?style=flat-square&logo=go)](https://golang.org)
[![License](https://img.shields.io/badge/license-MIT-green.svg?style=flat-square)](LICENSE)

基于 Go 泛型的**零依赖**线程安全 LRU 缓存,支持可选的**逐条目 TTL** 过期。面向 Go 1.21+,仅使用标准库。

- ✅ **`Cache[K comparable, V any]`** — 泛型,可比较且自等的键与任意值类型
- ✅ **纯 LRU** — map + 自实现双向链表(带头尾哨兵),淘汰最久未使用条目
- ✅ **线程安全** — `sync.Mutex` 包裹,`go test -race` 验证
- ✅ **可选 TTL** — `SetWithTTL` 按条目设置绝对过期时间,`Get`/`Peek`/`Contains`/`Keys` 惰性清理
- ✅ **零依赖** — 仅 Go 标准库(`sync` / `time`)
- ✅ **质量** — 模型对照模糊测试 `FuzzCacheModel` + 基准测试

## 安装

```bash
go get github.com/MouXiaoJun/lru
```

需要 **Go 1.21+**。

## 快速开始

```go
package main

import (
	"fmt"
	"time"

	"github.com/MouXiaoJun/lru"
)

func main() {
	c := lru.New[string, int](100) // 容量 100 条,容量 <= 0 会 panic

	c.Set("a", 1)
	c.Set("b", 2)

	if v, ok := c.Get("a"); ok {
		fmt.Println(v) // 1
	}
	fmt.Println(c.Len())       // 2
	fmt.Println(c.Keys())      // [a b] 最近使用在前(Get 后 a 排最前)
	fmt.Println(c.Peek("b"))   // 2 true(查询不改变顺序)
	c.Delete("a")              // true
	c.Clear()

	// TTL:5 秒后自动过期
	c.SetWithTTL("session", 42, 5*time.Second)
}
```

## API

| 方法 | 说明 | 复杂度 |
| --- | --- | --- |
| `New[K, V](capacity int) *Cache[K, V]` | 创建缓存;**capacity <= 0 会 panic**(消息 `lru: capacity must be positive`);写入还需满足下述键要求 | O(1) |
| `Get(key K) (V, bool)` | 取值;命中移动到最近使用位置;过期视为未命中并惰性删除 | O(1) |
| `Set(key K, value V)` | 写入;已存在则更新并移动到最近使用;新增超容量时淘汰最久未使用;写入的条目**永久有效** | O(1) |
| `SetWithTTL(key K, value V, ttl time.Duration)` | 同 Set,但设置 ttl 过期;**ttl <= 0 等价于 Set**(永久) | O(1) |
| `Delete(key K) bool` | 删除;返回 true 当且仅当删除了一个存活条目 | O(1) |
| `Contains(key K) bool` | 是否存在且未过期;过期视为不存在并惰性删除 | O(1) |
| `Peek(key K) (V, bool)` | 查询,不改变最近使用顺序 | O(1) |
| `Keys() []K` | 所有存活键,最近使用序(最近在前);顺带清理过期条目 | O(n) |
| `Len() int` | 当前存储条目数(见下方 TTL 语义说明) | O(1) |
| `Clear()` | 清空,容量保持不变,之后可继续使用 | O(1) |
| `Capacity() int` | 返回容量 | O(1) |

> 所有方法并发安全;除 `Set`/`SetWithTTL` 外,零值 `Cache` 按空缓存处理。缓存必须通过 `New` 创建。

首次使用后不得复制 `Cache`。键必须可比较且自等(`key == key`):NaN 或含 NaN 的非自等复合键在 Set/SetWithTTL 修改缓存前会 panic,消息为 `lru: key must equal itself`。接口键的动态值不可比较时,与 Go map 一样 panic。对零值调用 Clear 是 no-op,不会代替 New 初始化。

## 淘汰策略

标准 **LRU(Least Recently Used,最近最少使用)**:每次命中/写入把条目移动到链表头部(最近使用端),新增条目超出容量时,从链表尾部淘汰**最久未使用**的条目。

- 底层为 `map[K]*entry`(O(1) 定位)+ 带头尾哨兵的双向链表(O(1) 移动/淘汰),未使用 `container/list`,哨兵节点省去全部空指针分支。
- 复杂度:Get/Set/Delete/Contains/Peek 平均 O(1);Keys O(n)。
- 与 FIFO 的区别:LRU 按**访问时间**而非**插入时间**淘汰;与 LFU 的区别:LRU 只记最近一次访问,不统计频率。
- 淘汰发生在写入路径:即使条目已过期但尚未被访问,只要排在链表尾部,也会被正常淘汰。

## TTL 语义

- **绝对过期**:条目在写入时刻起 ttl 后过期(`expiresAt = now + ttl`);后续 `Get`/`Peek` 命中**不会延长**过期时间(非滑动窗口)。
- **惰性删除**:过期的条目仍占用容量,直到被 `Get`/`Peek`/`Contains`/`Delete`/`Keys`/`Set` 触达才被清理;因此 `Len()` 在 TTL 场景下**可能包含尚未清理的过期条目**(无 TTL 时 Len 恒等于存活条目数)。需要精确计数时先调 `Keys()` 或读取触发清理。
- **ttl <= 0 表示永久**:`SetWithTTL(k, v, 0)` 与 `Set(k, v)` 等价;`Set` 语义即"永久写入"。
- **最近一次写入决定属性**:对存活条目 `Set`(或 `SetWithTTL` 传 0)会**清除其 TTL** 使其永久;对已过期条目写入视同**新增**(旧条目先清理,不占容量)。
- **过期判定边界**:`now >= expiresAt` 视为过期(到期时刻含)。
- 单位是**条目数**的容量,与字节无关。

## 并发安全

单个 `Cache` 实例可被任意多个 goroutine 并发使用;内部用 `sync.Mutex` 串行化全部操作,读写同锁(实现简单、避免读时被写者穿插导致顺序判定复杂)。压力测试与 `-race` 见 [lru_test.go](lru_test.go) 的 `TestConcurrentMixedOps` / `TestConcurrentTTLOps`。

> 高并发读多写少且需极致吞吐时,可考虑分片方案(如 freecache/bigcache),见 FAQ。

## 与 freecache / bigcache / golang-lru 对比

| 维度 | **本库 (go-lru)** | **golang-lru** | **freecache** | **bigcache** |
| --- | --- | --- | --- | --- |
| 泛型 | ✅ `Cache[K, V]` | ✅ v2 `Cache[K, V]` | ❌ `[]byte` | ❌ `[]byte` |
| 淘汰策略 | 严格 LRU | 严格 LRU(另有 2Q 变体) | 近似 LRU(环形缓冲) | 非 LRU,近似 FIFO(按写入序淘汰,命中不重排) |
| TTL | ✅ 逐条目,绝对过期 | v2 `ExpirableLRU` 提供(缓存级 TTL) | ✅ 逐条目(`SetWithExpire`) | ✅ 逐条目(后台 janitor 批量清理) |
| 并发模型 | 单 Mutex,实现简单 | 单 Mutex | 256 分片,各带锁,低竞争 | 默认 1024 分片,RWMutex |
| 值类型 | 任意 `V` | 任意 `V`(v2) | `[]byte`(大小受限) | `[]byte` |
| 依赖 | 零依赖 | 零依赖 | 零依赖 | 零依赖 |
| 容量语义 | 条目数 | 条目数 | 字节 | 字节 |

### FAQ

**Q: 和 golang-lru 比,选哪个?**
A: [HashiCorp golang-lru v2](https://github.com/hashicorp/golang-lru) 同样支持泛型;需要 Resize、淘汰回调或 2Q 等现成能力时可评估它。本库保留较小 API、逐条目 TTL 与无后台清理 goroutine 的行为,不以泛型作为独有差异。

**Q: 什么时候该用 freecache / bigcache?**
A: 当缓存的是**大体积字节流**(如序列化结果)、容量按**字节**计、且需要分片低竞争时,这两个库更合适——它们把存储放在连续内存/环形缓冲里,减少 GC 压力。代价是:**bigcache 不是 LRU**(命中不重排,按写入序淘汰),freecache 只是近似 LRU;且键值都必须是 `[]byte`。

**Q: 为什么自己写链表,不用 `container/list`?**
A: 两者复杂度相同。自实现哨兵链表省去每次操作的容器间接层、可直接持有键值、可显式清空节点指针帮助 GC,代码也更直观;同时保持零依赖的"自包含"风格(参考 [go-set](https://github.com/MouXiaoJun/set))。

**Q: TTL 是滑动窗口吗?命中会续期吗?**
A: 不是。TTL 是绝对过期(从写入时刻计时),命中不续期。需要滑动窗口语义请自行在写入路径调用 `SetWithTTL` 重置。

**Q: 为什么 `Len()` 可能比实际存活条目多?**
A: 惰性删除策略:过期条目在被访问前不主动扫描清理(无后台 goroutine,避免额外开销)。读取路径触达即清理。无 TTL 时不受影响。

**Q: `New` 为什么用 panic 而不是返回 error?**
A: `capacity <= 0` 是调用方编码错误,而非运行时可恢复条件;panic 让 `New` 保持无 error 的简洁签名,错误立即暴露在调用点。文档与测试均覆盖该行为。

## 质量保证

本地门禁(gofmt / vet / build / test / race / fuzz 全部通过):

```bash
go vet ./...
go test -race ./...
go test -run xxx -fuzz FuzzCacheModel -fuzztime 5s ./
go test -run xxx -bench . -benchmem ./
```

基准(Apple M5,`-benchtime` 默认,见 [benchmark_test.go](benchmark_test.go)):

| Benchmark | 耗时/op | 分配 |
| --- | --- | --- |
| `BenchmarkSet` | ~544 ns | 64 B,1 alloc |
| `BenchmarkGetHit` | ~205 ns | 0 B,0 alloc |
| `BenchmarkGetMiss` | ~205 ns | 0 B,0 alloc |
| `BenchmarkSetWithTTL` | ~907 ns | 64 B,1 alloc |
| `BenchmarkPeek` | ~237 ns | 0 B,0 alloc |

模糊测试 `FuzzCacheModel` 以参考模型(独立实现的 map+切片 LRU)对照全部公开操作,校验返回值与顺序一致性,并全程禁止 panic。

## 许可证

[MIT](LICENSE)
