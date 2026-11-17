package observability

import (
	"bytes"
	"strings"
	"testing"
)

// TestMetricsLabeledSeries 盯的是多场景部署时最容易踩的一个坑：
// 同名指标按名字去重，结果所有场景共用一个计数器、只保留第一个 gauge。
func TestMetricsLabeledSeries(t *testing.T) {
	m := NewMetrics()
	a := m.CounterNamed("scene_broadcast_total", `{scene="1"}`, "entity state packets broadcast")
	b := m.CounterNamed("scene_broadcast_total", `{scene="2"}`, "entity state packets broadcast")
	a.Add(10)
	b.Add(3)

	if a == b {
		t.Fatal("不同标签的计数器必须是不同的对象，否则各场景的数字会互相覆盖")
	}

	m.GaugeNamed("scene_entity_count", `{scene="1"}`, "current entity count", func() float64 { return 100 })
	m.GaugeNamed("scene_entity_count", `{scene="2"}`, "current entity count", func() float64 { return 7 })

	var buf bytes.Buffer
	m.Render(&buf)
	out := buf.String()

	// HELP / TYPE 每个 family 只能出现一次，否则 Prometheus 抓取会报错。
	if n := strings.Count(out, "# HELP scene_entity_count "); n != 1 {
		t.Fatalf("HELP 应只出现 1 次，实际 %d 次\n%s", n, out)
	}
	if n := strings.Count(out, "# TYPE scene_entity_count gauge"); n != 1 {
		t.Fatalf("TYPE 应只出现 1 次，实际 %d 次\n%s", n, out)
	}
	for _, want := range []string{
		`scene_entity_count{scene="1"} 100`,
		`scene_entity_count{scene="2"} 7`,
		`scene_broadcast_total{scene="1"} 10`,
		`scene_broadcast_total{scene="2"} 3`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("输出缺少 %q\n%s", want, out)
		}
	}
}

// TestMetricsUnlabeledUnchanged 保证没有标签时输出格式和以前一模一样，
// 老看板和抓取规则不需要改。
func TestMetricsUnlabeledUnchanged(t *testing.T) {
	m := NewMetrics()
	m.Counter("gate_messages_in_total", "messages received").Add(5)
	m.Gauge("gate_online", "online sessions", func() float64 { return 3 })

	var buf bytes.Buffer
	m.Render(&buf)
	out := buf.String()

	for _, want := range []string{
		"# HELP gate_messages_in_total messages received\n",
		"# TYPE gate_messages_in_total counter\n",
		"gate_messages_in_total 5\n",
		"# TYPE gate_online gauge\n",
		"gate_online 3\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("输出缺少 %q\n%s", want, out)
		}
	}
}
