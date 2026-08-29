package lru

import (
	"testing"
	"time"
)

// refEntry 是参考模型中的条目。
type refEntry struct {
	val       int
	hasTTL    bool
	expiresAt time.Time
}

// refModel 是 Cache 的参考模型:map + 切片(order[0] 为最近使用),语义与 Cache 逐条对应。
// 它与 Cache 使用同一虚拟时钟,用于模糊测试对照校验。
type refModel struct {
	cap   int
	m     map[int]refEntry
	order []int
}

func newRefModel(capacity int) *refModel {
	return &refModel{cap: capacity, m: make(map[int]refEntry)}
}

func (r *refModel) expired(k int, now time.Time) bool {
	e, ok := r.m[k]
	return ok && e.hasTTL && !now.Before(e.expiresAt)
}

// removeFromOrder 仅把 k 从 order 中移除,不动 map。
func (r *refModel) removeFromOrder(k int) {
	for i, x := range r.order {
		if x == k {
			r.order = append(r.order[:i], r.order[i+1:]...)
			return
		}
	}
}

// moveFront 把 k 移到 order 最前(最近使用),不动 map。
func (r *refModel) moveFront(k int) {
	r.removeFromOrder(k)
	r.order = append([]int{k}, r.order...)
}

// removeKey 从模型彻底移除 k:摘出 order 并删除 map 项。
func (r *refModel) removeKey(k int) {
	r.removeFromOrder(k)
	delete(r.m, k)
}

func (r *refModel) get(k int, now time.Time) (int, bool) {
	if r.expired(k, now) {
		r.removeKey(k)
		return 0, false
	}
	e, ok := r.m[k]
	if !ok {
		return 0, false
	}
	r.moveFront(k)
	return e.val, true
}

func (r *refModel) peek(k int, now time.Time) (int, bool) {
	if r.expired(k, now) {
		r.removeKey(k)
		return 0, false
	}
	e, ok := r.m[k]
	if !ok {
		return 0, false
	}
	return e.val, true
}

func (r *refModel) contains(k int, now time.Time) bool {
	if r.expired(k, now) {
		r.removeKey(k)
		return false
	}
	_, ok := r.m[k]
	return ok
}

func (r *refModel) set(k, v int, ttl time.Duration, now time.Time) {
	var (
		hasTTL    bool
		expiresAt time.Time
	)
	if ttl > 0 {
		hasTTL = true
		expiresAt = now.Add(ttl)
	}
	if e, ok := r.m[k]; ok {
		if !e.hasTTL || !r.expired(k, now) {
			// 存活:更新并移动到最近使用。
			e.val = v
			e.hasTTL = hasTTL
			e.expiresAt = expiresAt
			r.m[k] = e
			r.moveFront(k)
			return
		}
		// 已过期:视同新增,先清理。
		r.removeKey(k)
	}
	r.m[k] = refEntry{val: v, hasTTL: hasTTL, expiresAt: expiresAt}
	r.moveFront(k)
	if len(r.m) > r.cap {
		r.removeKey(r.order[len(r.order)-1])
	}
}

func (r *refModel) delete(k int, now time.Time) bool {
	if r.expired(k, now) {
		r.removeKey(k)
		return false
	}
	if _, ok := r.m[k]; !ok {
		return false
	}
	r.removeKey(k)
	return true
}

// keys 返回按最近使用序排列的存活键,并顺带清理过期条目(与 Cache.Keys 一致)。
func (r *refModel) keys(now time.Time) []int {
	out := make([]int, 0, len(r.order))
	keep := r.order[:0]
	for _, k := range r.order {
		if r.expired(k, now) {
			delete(r.m, k)
			continue
		}
		keep = append(keep, k)
		out = append(out, k)
	}
	r.order = keep
	return out
}

func (r *refModel) len() int { return len(r.m) }

// FuzzCacheModel 用参考模型对照校验 Cache 的全部公开操作。
//
// 输入按字节解码:data[0] 决定容量(1..16),后续每个字节决定一步操作,
// 操作码取 i%8。TTL 语义通过可注入虚拟时钟保证确定、可复现:
// 每步前时钟前进 0..6ms(每 13 步大步前进 100ms,制造批量过期)。
//
// 断言:每步操作结果与参考模型一致;收尾时 Len 一致;全程不 panic。
func FuzzCacheModel(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{3, 1, 2, 3, 4, 5, 6})
	f.Add([]byte{2, 10, 1, 2, 10, 3})
	f.Add([]byte{1, 9, 9, 9, 9, 9, 9, 9})
	f.Add([]byte{4, 0, 0, 0, 0, 0, 0, 0, 0})
	f.Add([]byte{255, 1, 2, 3, 4, 5, 6, 7, 8})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) == 0 {
			return
		}
		capacity := int(data[0])%16 + 1 // 1..16

		c := New[int, int](capacity)
		ref := newRefModel(capacity)

		var clock time.Time // 从零时刻起虚拟推进
		c.now = func() time.Time { return clock }

		for i, b := range data[1:] {
			if i%13 == 0 {
				clock = clock.Add(100 * time.Millisecond) // 大步前进,批量过期
			} else {
				clock = clock.Add(time.Duration(i%7) * time.Millisecond)
			}

			key, val := int(b), int(b)*2
			switch i % 8 {
			case 0: // Set(永久)
				c.Set(key, val)
				ref.set(key, val, 0, clock)
			case 1: // SetWithTTL(1..5ms)
				ttl := time.Duration(1+i%5) * time.Millisecond
				c.SetWithTTL(key, val, ttl)
				ref.set(key, val, ttl, clock)
			case 2: // Get
				gv, gok := c.Get(key)
				wv, wok := ref.get(key, clock)
				if gok != wok || (gok && gv != wv) {
					t.Fatalf("Get(%d) = (%d,%v), want (%d,%v)", key, gv, gok, wv, wok)
				}
			case 3: // Delete
				if c.Delete(key) != ref.delete(key, clock) {
					t.Fatalf("Delete(%d) 结果不一致", key)
				}
			case 4: // Contains
				if c.Contains(key) != ref.contains(key, clock) {
					t.Fatalf("Contains(%d) 结果不一致", key)
				}
			case 5: // Peek
				pv, pok := c.Peek(key)
				wv, wok := ref.peek(key, clock)
				if pok != wok || (pok && pv != wv) {
					t.Fatalf("Peek(%d) = (%d,%v), want (%d,%v)", key, pv, pok, wv, wok)
				}
			case 6: // Len
				if c.Len() != ref.len() {
					t.Fatalf("Len = %d, want %d", c.Len(), ref.len())
				}
			case 7: // Keys(顺序敏感)
				got := c.Keys()
				want := ref.keys(clock)
				if len(got) != len(want) {
					t.Fatalf("Keys 长度 = %d, want %d", len(got), len(want))
				}
				for j := range got {
					if got[j] != want[j] {
						t.Fatalf("Keys[%d] = %d, want %d(顺序不一致)", j, got[j], want[j])
					}
				}
			}
		}

		if c.Len() != ref.len() {
			t.Fatalf("最终 Len = %d, want %d", c.Len(), ref.len())
		}
	})
}
