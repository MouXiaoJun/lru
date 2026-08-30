# Changelog

本项目遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/),版本号遵循 [语义化版本](https://semver.org/lang/zh-CN/)。

## [1.0.1] - 2026-08-30

### Fixed / Changed

- Reject non-reflexive keys such as NaN before mutating the cache.
- Make Clear safe for a zero-value cache; add lifecycle regression tests.
- Align LICENSE and current documentation with MIT, as confirmed by the maintainer; preserve existing copyright notices.

## [v1.0.0] - 2026-08-29

首个正式版本。

### Added

- 泛型线程安全 LRU 缓存 `Cache[K comparable, V any]`,零依赖(仅标准库),Go 1.21+。
- API:`New` / `Get` / `Set` / `SetWithTTL` / `Delete` / `Contains` / `Peek` / `Keys` / `Len` / `Clear` / `Capacity`。
- 底层实现:map + 自实现双向链表(带头尾哨兵),Get/Set/Delete/Peek O(1),Keys O(n)。
- 逐条目 TTL:`SetWithTTL` 绝对过期,命中不续期;过期条目由读取路径惰性清理。
- 并发安全:`sync.Mutex` 包裹,`-race` 通过。
- 测试:单元测试(容量淘汰/更新/顺序/删除/Peek/TTL 过期/并发读写)、模型对照模糊测试 `FuzzCacheModel`、基准测试。
- CI:GitHub Actions(Test / Bench / Release 三件套)。
- 文档:README_zh.md / README.md / CHANGELOG.md。

### 行为约定(文档化)

- `New(capacity)` 对 `capacity <= 0` panic(消息 `lru: capacity must be positive`)。
- `Set` 写入的条目永久有效;`SetWithTTL` 传 `ttl <= 0` 等价于 `Set`。
- `Len()` 在启用 TTL 后可能包含尚未清理的过期条目。
- 零值 `Cache` 读取按空缓存处理;`Set` / `SetWithTTL` panic 并提示使用 `lru.New`。
