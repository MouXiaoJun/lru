// Package lru 提供基于 Go 泛型实现的线程安全 LRU 缓存,支持可选的逐条目 TTL 过期。
//
// 零依赖(仅 Go 标准库),面向 Go 1.21+,风格参考 github.com/MouXiaoJun/set。
// 底层为 map + 自实现双向链表(未使用 container/list),除 Keys 为 O(n) 外,操作平均 O(1):
//
//   - Cache[K comparable, V any]   线程安全 LRU 缓存(sync.Mutex 包裹,-race 验证)
//   - Get/Peek/Contains/Keys 等读取路径会惰性清理已过期的 TTL 条目
//   - SetWithTTL 按条目设置绝对过期时间;命中不会延长过期(非滑动窗口)
//
// 缓存必须通过 New 创建;零值 Cache 的读取方法按空缓存处理,Set/SetWithTTL 会 panic。
package lru

import (
	"sync"
	"time"
)

// entry 是双向链表的节点,同时保存键、值以及可选的过期信息。
type entry[K comparable, V any] struct {
	key   K
	value V

	hasTTL    bool      // 是否设置了过期时间
	expiresAt time.Time // hasTTL 为 true 时,now >= expiresAt 视为已过期

	prev, next *entry[K, V]
}

// Cache 是线程安全的泛型 LRU 缓存。
//
// 内部维护 map[K]*entry 用于 O(1) 定位,以及带头尾哨兵的双向链表维护最近使用序:
// head.next 为最近使用的条目,tail.prev 为最久未使用(淘汰候选)。
//
// 所有方法都是并发安全的,可被任意多个 goroutine 同时使用。
// Cache 首次使用后不得复制;复制会共享节点和 map,但不会共享锁。
// 键必须可比较且自等(key == key);Set/SetWithTTL 拒绝 NaN 及含 NaN 的非自等键。
type Cache[K comparable, V any] struct {
	mu   sync.Mutex
	cap  int
	m    map[K]*entry[K, V]
	head *entry[K, V] // 哨兵:head.next = 最近使用
	tail *entry[K, V] // 哨兵:tail.prev = 最久未使用

	now func() time.Time // 可注入时钟,便于确定性测试;默认 time.Now
}

// New 创建容量为 capacity 的 LRU 缓存。
//
// capacity <= 0 会 panic(消息为 "lru: capacity must be positive")。
// 后续写入还要求键可比较且自等,见 SetWithTTL。
func New[K comparable, V any](capacity int) *Cache[K, V] {
	if capacity <= 0 {
		panic("lru: capacity must be positive")
	}
	c := &Cache[K, V]{
		cap:  capacity,
		m:    make(map[K]*entry[K, V]),
		head: &entry[K, V]{},
		tail: &entry[K, V]{},
		now:  time.Now,
	}
	c.head.next = c.tail
	c.tail.prev = c.head
	return c
}

// expired 报告节点在 now 时刻是否已过期。没有 TTL 的条目永不过期。
func (e *entry[K, V]) expired(now time.Time) bool {
	return e.hasTTL && !now.Before(e.expiresAt)
}

// remove 把节点从链表中摘下,并清空指针便于 GC。
func (c *Cache[K, V]) remove(e *entry[K, V]) {
	e.prev.next = e.next
	e.next.prev = e.prev
	e.prev, e.next = nil, nil
}

// pushFront 把节点插入链表头部(最近使用端)。
func (c *Cache[K, V]) pushFront(e *entry[K, V]) {
	e.prev = c.head
	e.next = c.head.next
	c.head.next.prev = e
	c.head.next = e
}

// moveToFront 在命中时刷新最近使用序。
func (c *Cache[K, V]) moveToFront(e *entry[K, V]) {
	c.remove(e)
	c.pushFront(e)
}

// back 返回最久未使用的节点(不摘除);仅当缓存非空时调用。
func (c *Cache[K, V]) back() *entry[K, V] {
	return c.tail.prev
}

// drop 从缓存中彻底移除一个节点:摘链表并删除 map 项。
func (c *Cache[K, V]) drop(e *entry[K, V]) {
	delete(c.m, e.key)
	c.remove(e)
}

// Get 返回 key 对应的值;命中时把该条目移动到最近使用位置。
// 已过期的条目视为未命中,并在此处被惰性删除。未命中返回 V 的零值与 false。
func (c *Cache[K, V]) Get(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if e, ok := c.m[key]; ok {
		if e.hasTTL && e.expired(c.now()) {
			c.drop(e)
			var zero V
			return zero, false
		}
		c.moveToFront(e)
		return e.value, true
	}
	var zero V
	return zero, false
}

// Peek 返回 key 对应的值,但不改变最近使用顺序(纯查询)。
// 已过期的条目视为未命中,并在此处被惰性删除。
func (c *Cache[K, V]) Peek(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if e, ok := c.m[key]; ok {
		if e.hasTTL && e.expired(c.now()) {
			c.drop(e)
			var zero V
			return zero, false
		}
		return e.value, true
	}
	var zero V
	return zero, false
}

// Contains 报告 key 是否存在且未过期。已过期的条目视为不存在并惰性删除。
func (c *Cache[K, V]) Contains(key K) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if e, ok := c.m[key]; ok {
		if e.hasTTL && e.expired(c.now()) {
			c.drop(e)
			return false
		}
		return true
	}
	return false
}

// Set 写入 key→value。
//
//   - key 已存在:更新值并移动到最近使用位置;若该条目带 TTL,本次写入使其变为永久(无过期)。
//   - key 不存在:新增;若容量已满,淘汰最久未使用的条目。
//
// 等价于 SetWithTTL(key, value, 0)。
// key 不自等时 panic;例如 NaN 或含 NaN 的数组、结构体、复数。
func (c *Cache[K, V]) Set(key K, value V) {
	c.SetWithTTL(key, value, 0)
}

// SetWithTTL 与 Set 相同,但为该条目设置 ttl 的过期时间。
// key 必须可比较且自等;非自等键会在修改缓存前 panic("lru: key must equal itself")。
// 接口键的动态值不可比较时,与 Go map 一样 panic。
//
// TTL 语义:
//   - ttl > 0:条目在写入时刻起 ttl 后过期(绝对过期时间);之后的 Get/Peek 命中不会延长它。
//   - ttl <= 0:不设置过期时间,等价于 Set(条目永久有效)。
//   - key 已有条目:仍存活则更新值并重置过期时间;已过期则视同新增(旧条目被清理,不占容量)。
func (c *Cache[K, V]) SetWithTTL(key K, value V, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cap <= 0 {
		// 零值 Cache 未经 New 初始化,直接写入会 panic 在 nil map 上;提前给出清晰错误。
		panic("lru: uninitialized cache (capacity 0); create it with lru.New")
	}
	if key != key {
		panic("lru: key must equal itself")
	}

	var (
		hasTTL    bool
		expiresAt time.Time
	)
	if ttl > 0 {
		hasTTL = true
		expiresAt = c.now().Add(ttl)
	}

	if e, ok := c.m[key]; ok {
		if !e.hasTTL || !e.expired(c.now()) {
			// 存活:更新值、重置过期属性,并移动到最近使用位置。
			e.value = value
			e.hasTTL = hasTTL
			e.expiresAt = expiresAt
			c.moveToFront(e)
			return
		}
		// 已过期:视同新增,先清理旧条目,避免其继续占用容量。
		c.drop(e)
	}

	e := &entry[K, V]{key: key, value: value, hasTTL: hasTTL, expiresAt: expiresAt}
	c.m[key] = e
	c.pushFront(e)
	if len(c.m) > c.cap {
		c.drop(c.back())
	}
}

// Delete 删除 key 对应的条目。
//
// 返回 true 当且仅当删除了一个存活(未过期)的条目;若 key 不存在、
// 或条目已过期(会被顺带清理,但不算删除成功),返回 false。
func (c *Cache[K, V]) Delete(key K) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if e, ok := c.m[key]; ok {
		if e.hasTTL && e.expired(c.now()) {
			c.drop(e)
			return false
		}
		c.drop(e)
		return true
	}
	return false
}

// Len 返回当前存储的条目数。
//
// 注意:启用 TTL 后,该值可能包含已过期但尚未被访问清理的条目
// (Get/Peek/Contains/Keys/Delete/Set 会惰性清理它们,清理后 Len 随之变小)。
// 纯无 TTL 使用时,Len 恒等于存活条目数,且为 O(1)。
func (c *Cache[K, V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.m)
}

// Keys 返回所有存活(未过期)条目的键,按最近使用序排列(最近使用在前)。
// 遍历过程中发现的过期条目会被惰性删除,因此调用后 Len 会收缩。
func (c *Cache[K, V]) Keys() []K {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.head == nil {
		return nil
	}
	now := c.now()
	keys := make([]K, 0, len(c.m))
	for e := c.head.next; e != c.tail; {
		next := e.next // drop 会清空 e 的前后指针,先保存后继
		if e.expired(now) {
			c.drop(e)
		} else {
			keys = append(keys, e.key)
		}
		e = next
	}
	return keys
}

// Clear 清空缓存,容量保持不变;已构造的缓存之后可继续使用,零值保持零值。
func (c *Cache[K, V]) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.head == nil {
		return
	}
	c.m = make(map[K]*entry[K, V])
	c.head = &entry[K, V]{}
	c.tail = &entry[K, V]{}
	c.head.next = c.tail
	c.tail.prev = c.head
}

// Capacity 返回缓存容量。
func (c *Cache[K, V]) Capacity() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cap
}
