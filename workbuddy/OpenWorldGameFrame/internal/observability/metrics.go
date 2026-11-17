// Package observability 提供指标采集和运维接口。
//
// 这里没有引 prometheus/client_golang，因为它会把依赖树拉得很长，
// 而我们只需要最基础的 counter 和 gauge。输出格式保持与 Prometheus 兼容，
// 后续要换官方 SDK 只需要替换 Render 的实现。
package observability

import (
	"fmt"
	"io"
	"sort"
	"sync"
	"sync/atomic"
)

// Metrics 是一个极简的指标注册表。
//
// 指标按「名字 + 标签」定位。没有标签时名字就是唯一键，行为和以前完全一样；
// 有标签时同一个 family 下会有多条序列，Render 保证 HELP/TYPE 只输出一次。
//
// 为什么需要标签：多场景部署时每个场景都要报自己的 tick 滞后和人数。
// 早期实现按名字去重，结果是**所有场景共用一个计数器、只保留第一个
// 注册的 gauge**——控制台上三个场景的广播量永远相等，看着像 bug 其实是
// 指标系统在骗人。这类"数字对得上但其实是同一个数"的问题极难排查，
// 所以宁可在注册表里加一层标签，也不要用场景 ID 拼进名字
// （拼名字会让 Prometheus 的 family 数量随场景数爆炸）。
type Metrics struct {
	mu       sync.RWMutex
	counters map[string]*counterEntry
	gauges   map[string]*gaugeEntry
	// order 记录"序列键"的注册顺序，键 = family + labels。
	order []string
	// family 把序列键映射回指标名，Render 靠它分组输出 HELP/TYPE。
	family map[string]string
}

type counterEntry struct {
	help string
	val  atomic.Int64
}

type gaugeEntry struct {
	help string
	fn   func() float64
}

// NewMetrics 创建空注册表。
func NewMetrics() *Metrics {
	return &Metrics{
		counters: make(map[string]*counterEntry),
		gauges:   make(map[string]*gaugeEntry),
		family:   make(map[string]string),
	}
}

// Counter 注册（或复用）一个无标签的单调递增计数器。
func (m *Metrics) Counter(name, help string) *atomic.Int64 {
	return m.CounterNamed(name, "", help)
}

// CounterNamed 注册带标签的计数器。labels 已经是格式化好的
// `{k="v",k2="v2"}` 片段，空字符串表示无标签。
func (m *Metrics) CounterNamed(name, labels, help string) *atomic.Int64 {
	key := name + labels
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.counters[key]; ok {
		return &e.val
	}
	e := &counterEntry{help: help}
	m.counters[key] = e
	m.order = append(m.order, key)
	m.family[key] = name
	return &e.val
}

// Gauge 注册一个无标签的按需计算仪表。fn 会在每次抓取时被调用，
// 因此必须足够轻量，不能在里面做 IO。
func (m *Metrics) Gauge(name, help string, fn func() float64) {
	m.GaugeNamed(name, "", help, fn)
}

// GaugeNamed 注册带标签的仪表。
func (m *Metrics) GaugeNamed(name, labels, help string, fn func() float64) {
	key := name + labels
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.gauges[key]; ok {
		return
	}
	m.gauges[key] = &gaugeEntry{help: help, fn: fn}
	m.order = append(m.order, key)
	m.family[key] = name
}

// Render 输出 Prometheus 文本格式。
func (m *Metrics) Render(w io.Writer) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// 按 family 分组：Prometheus 要求同一个指标名的 HELP/TYPE 只出现一次，
	// 但同一 family 下的不同标签序列要挨着输出。
	byFamily := make(map[string][]string, len(m.family))
	for key, fam := range m.family {
		byFamily[fam] = append(byFamily[fam], key)
	}
	fams := make([]string, 0, len(byFamily))
	for f := range byFamily {
		fams = append(fams, f)
	}
	sort.Strings(fams)

	for _, fam := range fams {
		keys := byFamily[fam]
		sort.Strings(keys)

		// 同一 family 下所有序列的 help 和类型是一致的，取任意一条即可。
		var help, kind string
		if e, ok := m.counters[keys[0]]; ok {
			help, kind = e.help, "counter"
		} else if e, ok := m.gauges[keys[0]]; ok {
			help, kind = e.help, "gauge"
		} else {
			continue
		}
		fmt.Fprintf(w, "# HELP %s %s\n", fam, help)
		fmt.Fprintf(w, "# TYPE %s %s\n", fam, kind)

		for _, key := range keys {
			if e, ok := m.counters[key]; ok {
				fmt.Fprintf(w, "%s %d\n", key, e.val.Load())
				continue
			}
			if e, ok := m.gauges[key]; ok {
				fmt.Fprintf(w, "%s %g\n", key, e.fn())
			}
		}
	}
}
