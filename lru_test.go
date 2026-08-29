package lru

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// newTestCache 创建带可注入虚拟时钟的缓存,便于确定性测试 TTL 语义。
// 返回的 clock 指针可在测试中推进;所有条目按该虚拟时钟判定过期。
func newTestCache[K comparable, V any](capacity int) (*Cache[K, V], *time.Time) {
	c := New[K, V](capacity)
	clock := new(time.Time)
	*clock = time.Unix(1_000_000, 0)
	c.now = func() time.Time { return *clock }
	return c, clock
}

func advance(clock *time.Time, d time.Duration) { *clock = (*clock).Add(d) }

func TestNewPanicsOnNonPositiveCapacity(t *testing.T) {
	for _, cap := range []int{0, -1, -100} {
		func() {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatalf("New(%d) 应 panic,但没有", cap)
				}
				msg, ok := r.(string)
				if !ok || !strings.Contains(msg, "capacity") {
					t.Fatalf("New(%d) panic 消息 = %v,应包含 capacity", cap, r)
				}
			}()
			_ = New[int, int](cap)
		}()
	}
}

func TestZeroValueBehaviors(t *testing.T) {
	var c Cache[int, int] // 零值:读取按空缓存处理,写入 panic
	if v, ok := c.Get(1); ok || v != 0 {
		t.Fatalf("零值 Get 应返回 miss,got (%v,%v)", v, ok)
	}
	if c.Contains(1) {
		t.Fatal("零值 Contains 应为 false")
	}
	if c.Len() != 0 {
		t.Fatal("零值 Len 应为 0")
	}
	if c.Capacity() != 0 {
		t.Fatal("零值 Capacity 应为 0")
	}
	if c.Delete(1) {
		t.Fatal("零值 Delete 应为 false")
	}
	if ks := c.Keys(); ks != nil {
		t.Fatalf("零值 Keys 应为 nil,got %v", ks)
	}
	func() {
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("零值 Set 应 panic,但没有")
			}
			msg, ok := r.(string)
			if !ok || !strings.Contains(msg, "lru.New") {
				t.Fatalf("零值 Set panic 消息 = %v,应提示使用 lru.New", r)
			}
		}()
		c.Set(1, 1)
	}()
}

func TestSetGetBasic(t *testing.T) {
	c, _ := newTestCache[int, string](4)
	c.Set(1, "a")
	c.Set(2, "b")

	v, ok := c.Get(1)
	if !ok || v != "a" {
		t.Fatalf("Get(1) = (%q,%v), want (a,true)", v, ok)
	}
	v, ok = c.Get(3)
	if ok {
		t.Fatalf("Get(3) 应 miss,got (%q,%v)", v, ok)
	}
	if c.Len() != 2 {
		t.Fatalf("Len = %d, want 2", c.Len())
	}
}

func TestEvictionLRUOrder(t *testing.T) {
	c, _ := newTestCache[int, int](2)
	c.Set(1, 10)
	c.Set(2, 20)
	c.Set(3, 30) // 淘汰最久未用的 1

	if _, ok := c.Get(1); ok {
		t.Fatal("Get(1) 应 miss(已被淘汰)")
	}
	if _, ok := c.Get(2); !ok {
		t.Fatal("Get(2) 应命中")
	}
	if _, ok := c.Get(3); !ok {
		t.Fatal("Get(3) 应命中")
	}
	if c.Len() != 2 {
		t.Fatalf("Len = %d, want 2", c.Len())
	}
}

func TestGetMovesToFront(t *testing.T) {
	c, _ := newTestCache[int, int](3)
	c.Set(1, 10)
	c.Set(2, 20)
	c.Set(3, 30)

	// 顺序:最近使用在前
	if got := c.Keys(); !equalKeys(got, []int{3, 2, 1}) {
		t.Fatalf("Keys = %v, want [3 2 1]", got)
	}

	c.Get(1) // 1 变为最近使用
	if got := c.Keys(); !equalKeys(got, []int{1, 3, 2}) {
		t.Fatalf("Get 后 Keys = %v, want [1 3 2]", got)
	}

	c.Set(4, 40) // 淘汰最久未用的 2
	if _, ok := c.Get(2); ok {
		t.Fatal("Get(2) 应 miss(最久未用被淘汰)")
	}
	if c.Len() != 3 {
		t.Fatalf("Len = %d, want 3", c.Len())
	}
}

func TestSetUpdatesAndMovesToFront(t *testing.T) {
	c, _ := newTestCache[int, int](3)
	c.Set(1, 10)
	c.Set(2, 20)
	c.Set(3, 30)

	c.Set(1, 100) // 更新 + 移动到最近使用
	if v, _ := c.Get(1); v != 100 {
		t.Fatalf("Get(1) = %d, want 100", v)
	}
	if got := c.Keys(); !equalKeys(got, []int{1, 3, 2}) {
		t.Fatalf("Keys = %v, want [1 3 2]", got)
	}

	c.Set(4, 40) // 淘汰最久未用的 2
	if _, ok := c.Get(2); ok {
		t.Fatal("Get(2) 应 miss")
	}
	if c.Len() != 3 {
		t.Fatalf("Len = %d, want 3", c.Len())
	}
}

func TestPeekDoesNotMove(t *testing.T) {
	c, _ := newTestCache[int, int](3)
	c.Set(1, 10)
	c.Set(2, 20)
	c.Set(3, 30)

	v, ok := c.Peek(1)
	if !ok || v != 10 {
		t.Fatalf("Peek(1) = (%d,%v), want (10,true)", v, ok)
	}
	if got := c.Keys(); !equalKeys(got, []int{3, 2, 1}) {
		t.Fatalf("Peek 后 Keys = %v,want [3 2 1](顺序不应变化)", got)
	}

	c.Set(4, 40) // 最久未用的仍是 1(Peek 不刷新),应淘汰 1
	if _, ok := c.Get(1); ok {
		t.Fatal("Get(1) 应 miss(Peek 不改变顺序)")
	}
}

func TestDelete(t *testing.T) {
	c, _ := newTestCache[int, int](3)
	c.Set(1, 10)
	c.Set(2, 20)
	c.Set(3, 30)

	if !c.Delete(2) {
		t.Fatal("Delete(2) 应返回 true")
	}
	if c.Len() != 2 {
		t.Fatalf("Len = %d, want 2", c.Len())
	}
	if c.Delete(2) {
		t.Fatal("再次 Delete(2) 应返回 false")
	}
	if got := c.Keys(); !equalKeys(got, []int{3, 1}) {
		t.Fatalf("Keys = %v, want [3 1]", got)
	}
}

func TestContains(t *testing.T) {
	c, _ := newTestCache[int, int](3)
	c.Set(1, 10)
	if !c.Contains(1) {
		t.Fatal("Contains(1) 应为 true")
	}
	if c.Contains(2) {
		t.Fatal("Contains(2) 应为 false")
	}
}

func TestClear(t *testing.T) {
	c, _ := newTestCache[int, int](3)
	c.Set(1, 10)
	c.Set(2, 20)
	c.Clear()

	if c.Len() != 0 {
		t.Fatalf("Clear 后 Len = %d, want 0", c.Len())
	}
	if ks := c.Keys(); len(ks) != 0 {
		t.Fatalf("Clear 后 Keys = %v, want 空", ks)
	}
	if c.Capacity() != 3 {
		t.Fatalf("Clear 后 Capacity = %d, want 3", c.Capacity())
	}
	// 清空后仍可正常使用
	c.Set(5, 50)
	if v, _ := c.Get(5); v != 50 {
		t.Fatalf("Clear 后 Get(5) = %d, want 50", v)
	}
}

func TestCapacity(t *testing.T) {
	c, _ := newTestCache[int, int](7)
	if c.Capacity() != 7 {
		t.Fatalf("Capacity = %d, want 7", c.Capacity())
	}
}

func TestLen(t *testing.T) {
	c, _ := newTestCache[int, int](4)
	if c.Len() != 0 {
		t.Fatalf("初始 Len = %d, want 0", c.Len())
	}
	for i := 0; i < 5; i++ {
		c.Set(i, i)
	}
	if c.Len() != 4 {
		t.Fatalf("Len = %d, want 4(受容量限制)", c.Len())
	}
}

func TestStringKeys(t *testing.T) {
	c, _ := newTestCache[string, int](2)
	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3) // 淘汰 "a"
	if _, ok := c.Get("a"); ok {
		t.Fatal("Get(a) 应 miss")
	}
	if v, _ := c.Get("c"); v != 3 {
		t.Fatalf("Get(c) = %d, want 3", v)
	}
}

func TestStructValues(t *testing.T) {
	type point struct{ x, y int }
	c, _ := newTestCache[int, point](2)
	c.Set(1, point{1, 2})
	v, ok := c.Get(1)
	if !ok || v != (point{1, 2}) {
		t.Fatalf("Get(1) = (%v,%v), want ({1 2},true)", v, ok)
	}
}

// --- TTL 语义 ---

func TestSetWithTTLExpiryAndLazyDelete(t *testing.T) {
	c, clock := newTestCache[int, int](4)
	c.SetWithTTL(1, 10, 10*time.Millisecond)

	advance(clock, 5*time.Millisecond)
	if v, ok := c.Get(1); !ok || v != 10 {
		t.Fatalf("TTL 内 Get(1) = (%d,%v), want (10,true)", v, ok)
	}

	advance(clock, 5*time.Millisecond) // 累计 10ms,到期时刻视为过期
	if _, ok := c.Get(1); ok {
		t.Fatal("到期后 Get(1) 应 miss")
	}
	// 惰性删除:Get 触发清理后 Len 归零
	if c.Len() != 0 {
		t.Fatalf("到期且 Get 后 Len = %d, want 0", c.Len())
	}
}

func TestPeekContainsSeeExpired(t *testing.T) {
	c, clock := newTestCache[int, int](4)
	c.SetWithTTL(1, 10, 10*time.Millisecond)
	c.SetWithTTL(2, 20, 10*time.Millisecond)

	advance(clock, 11*time.Millisecond)
	if _, ok := c.Peek(1); ok {
		t.Fatal("到期后 Peek(1) 应 miss")
	}
	if c.Contains(2) {
		t.Fatal("到期后 Contains(2) 应为 false")
	}
	if c.Len() != 0 {
		t.Fatalf("Peek/Contains 清理后 Len = %d, want 0", c.Len())
	}
}

func TestTTLIsNotExtendedByGet(t *testing.T) {
	c, clock := newTestCache[int, int](4)
	c.SetWithTTL(1, 10, 10*time.Millisecond)

	advance(clock, 4*time.Millisecond)
	if _, ok := c.Get(1); !ok {
		t.Fatal("Get(1) 应命中")
	}
	advance(clock, 4*time.Millisecond) // 累计 8ms
	if _, ok := c.Get(1); !ok {
		t.Fatal("Get(1) 应命中(尚未到期)")
	}
	advance(clock, 3*time.Millisecond) // 累计 11ms
	if _, ok := c.Get(1); ok {
		t.Fatal("Get(1) 应 miss:命中不延长过期时间(绝对过期)")
	}
}

func TestSetOnTTLEntryClearsTTL(t *testing.T) {
	c, clock := newTestCache[int, int](4)
	c.SetWithTTL(1, 10, 10*time.Millisecond)

	advance(clock, 5*time.Millisecond)
	c.Set(1, 99) // 变为永久条目

	advance(clock, 1*time.Hour)
	if v, ok := c.Get(1); !ok || v != 99 {
		t.Fatalf("Set 后长时间 Get(1) = (%d,%v), want (99,true):Set 应清除 TTL", v, ok)
	}
}

func TestSetWithTTLZeroMeansPermanent(t *testing.T) {
	c, clock := newTestCache[int, int](4)
	c.SetWithTTL(1, 10, 0)
	c.SetWithTTL(2, 20, -5*time.Millisecond) // ttl <= 0 同样表示永久

	advance(clock, 1*time.Hour)
	if v, ok := c.Get(1); !ok || v != 10 {
		t.Fatalf("Get(1) = (%d,%v), want (10,true)", v, ok)
	}
	if v, ok := c.Get(2); !ok || v != 20 {
		t.Fatalf("Get(2) = (%d,%v), want (20,true)", v, ok)
	}
}

func TestSetWithTTLUpdatesExistingEntry(t *testing.T) {
	c, clock := newTestCache[int, int](4)
	c.SetWithTTL(1, 10, 10*time.Millisecond)

	advance(clock, 5*time.Millisecond)
	c.SetWithTTL(1, 99, 20*time.Millisecond) // 重置过期时间

	if v, _ := c.Get(1); v != 99 {
		t.Fatalf("Get(1) = %d, want 99", v)
	}
	advance(clock, 15*time.Millisecond) // 距第二次写入 15ms < 20ms
	if _, ok := c.Get(1); !ok {
		t.Fatal("Get(1) 应命中(TTL 从最近一次写入重新计时)")
	}
	advance(clock, 6*time.Millisecond) // 距第二次写入 21ms > 20ms
	if _, ok := c.Get(1); ok {
		t.Fatal("Get(1) 应 miss(绝对过期)")
	}
}

func TestSetOnExpiredEntryTreatedAsNew(t *testing.T) {
	c, clock := newTestCache[int, int](2)
	c.SetWithTTL(1, 10, 5*time.Millisecond)
	c.Set(2, 20)

	advance(clock, 10*time.Millisecond) // 1 已过期,但仍占容量
	if c.Len() != 2 {
		t.Fatalf("过期未清理前 Len = %d, want 2(条目仍在存储中)", c.Len())
	}

	c.Set(1, 100) // 对过期键 Set 视同新增,不重复占容量
	if c.Len() != 2 {
		t.Fatalf("Set 后 Len = %d, want 2", c.Len())
	}
	if v, _ := c.Get(1); v != 100 {
		t.Fatalf("Get(1) = %d, want 100", v)
	}
	if _, ok := c.Get(2); !ok {
		t.Fatal("Get(2) 应仍命中")
	}
}

func TestDeleteExpiredReturnsFalse(t *testing.T) {
	c, clock := newTestCache[int, int](4)
	c.SetWithTTL(1, 10, 5*time.Millisecond)
	advance(clock, 10*time.Millisecond)

	if c.Delete(1) {
		t.Fatal("Delete(1) 对已过期条目应返回 false(并顺带清理)")
	}
	if c.Len() != 0 {
		t.Fatalf("Delete 清理后 Len = %d, want 0", c.Len())
	}
}

func TestKeysSkipsExpiredAndPurges(t *testing.T) {
	c, clock := newTestCache[int, int](4)
	c.Set(1, 10) // 永久
	c.SetWithTTL(2, 20, 5*time.Millisecond)
	c.SetWithTTL(3, 30, 5*time.Millisecond)

	advance(clock, 10*time.Millisecond)
	got := c.Keys()
	if !equalKeys(got, []int{1}) {
		t.Fatalf("Keys = %v, want [1](跳过过期条目)", got)
	}
	if c.Len() != 1 {
		t.Fatalf("Keys 清理后 Len = %d, want 1", c.Len())
	}
}

func TestEvictionWithTTL(t *testing.T) {
	c, clock := newTestCache[int, int](2)
	c.SetWithTTL(1, 10, 1*time.Hour)
	c.SetWithTTL(2, 20, 1*time.Hour)
	c.SetWithTTL(3, 30, 1*time.Hour) // 淘汰最久未用的 1
	if _, ok := c.Get(1); ok {
		t.Fatal("Get(1) 应 miss(被淘汰)")
	}
	if c.Len() != 2 {
		t.Fatalf("Len = %d, want 2", c.Len())
	}
	advance(clock, 2*time.Hour) // 全部过期
	if c.Len() != 2 {
		t.Fatalf("过期后 Len = %d, want 2(惰性清理前仍占容量)", c.Len())
	}
	if ks := c.Keys(); len(ks) != 0 {
		t.Fatalf("Keys = %v, want 空", ks)
	}
	if c.Len() != 0 {
		t.Fatalf("Keys 清理后 Len = %d, want 0", c.Len())
	}
}

// --- 并发安全 ---

func TestConcurrentMixedOps(t *testing.T) {
	c, _ := newTestCache[int, int](64)

	const workers, per = 8, 5000
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < per; i++ {
				k := (w*per + i) % 256
				switch i % 4 {
				case 0:
					c.Set(k, i)
				case 1:
					c.Get(k)
				case 2:
					c.Contains(k)
				case 3:
					c.Delete(k)
				}
				if i%100 == 0 {
					c.Peek(k)
					c.Keys()
					c.Len()
					c.Capacity()
				}
			}
		}(w)
	}
	wg.Wait()

	if n := c.Len(); n > 64 {
		t.Fatalf("并发结束后 Len = %d,超过容量 64", n)
	}
}

func TestConcurrentTTLOps(t *testing.T) {
	c := New[int, int](128) // 使用真实时钟

	const workers, per = 4, 3000
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < per; i++ {
				k := w*per + i
				switch i % 3 {
				case 0:
					c.SetWithTTL(k, i, 50*time.Millisecond)
				case 1:
					c.Get(k)
				default:
					c.Delete(k)
				}
			}
		}(w)
	}
	wg.Wait()
	c.Clear() // 收尾清理,不应 panic
}

// --- 辅助 ---

// equalKeys 比较两个 int 切片(顺序敏感)。
func equalKeys(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
