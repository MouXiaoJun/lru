package lru

import (
	"testing"
	"time"
)

var sink any // 防止编译器优化掉被测操作

const benchCap = 1024

// BenchmarkSet 衡量 Set 的吞吐:在容量边缘反复写入,包含更新与淘汰路径。
func BenchmarkSet(b *testing.B) {
	c := New[int, int](benchCap)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		c.Set(i%(benchCap*2), i)
	}
	sink = c.Len()
}

// BenchmarkGetHit 衡量命中路径(含移动到最近使用)的吞吐。
func BenchmarkGetHit(b *testing.B) {
	c := New[int, int](benchCap)
	for i := 0; i < benchCap; i++ {
		c.Set(i, i)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := c.Get(i % benchCap); ok {
			sink = ok
		}
	}
}

// BenchmarkGetMiss 衡量未命中路径(纯 map 查找失败)的吞吐。
func BenchmarkGetMiss(b *testing.B) {
	c := New[int, int](benchCap)
	for i := 0; i < benchCap; i++ {
		c.Set(i, i)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := c.Get(i + benchCap*2); ok {
			sink = ok
		}
	}
}

// BenchmarkSetWithTTL 衡量带 TTL 的写入路径(额外一次时钟调用)。
func BenchmarkSetWithTTL(b *testing.B) {
	c := New[int, int](benchCap)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		c.SetWithTTL(i%(benchCap*2), i, time.Minute)
	}
	sink = c.Len()
}

// BenchmarkPeek 衡量纯查询(不移动顺序)路径。
func BenchmarkPeek(b *testing.B) {
	c := New[int, int](benchCap)
	for i := 0; i < benchCap; i++ {
		c.Set(i, i)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := c.Peek(i % benchCap); ok {
			sink = ok
		}
	}
}
