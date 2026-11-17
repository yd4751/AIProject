package discovery

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// waitView 等到视图满足条件，超时即失败。
//
// 成员视图是"最终一致"的：注册之后要等一个轮询周期才可见。
// 测试里所有断言都必须经过这个等待，不能假设"写完立刻可见"——
// 那正是实现里刻意不保证的东西。
func waitView(t *testing.T, ch <-chan View, timeout time.Duration, ok func(View) bool) View {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case v, open := <-ch:
			if !open {
				t.Fatalf("视图通道提前关闭")
			}
			if ok(v) {
				return v
			}
		case <-deadline:
			t.Fatalf("%v 内没等到满足条件的视图", timeout)
		}
	}
}

func TestStaticView(t *testing.T) {
	r := NewStatic(Node{Name: "n1", Addr: "a"}, Node{Name: "n2", Addr: "b"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	v := waitView(t, r.Watch(ctx), time.Second, func(View) bool { return true })
	if got := v.Names(); len(got) != 2 || got[0] != "n1" || got[1] != "n2" {
		t.Fatalf("成员不对: %v", got)
	}
	if addr, ok := v.Addr("n2"); !ok || addr != "b" {
		t.Fatalf("地址不对: %q %v", addr, ok)
	}
	if v.Alive("n3") {
		t.Fatal("n3 不该活着")
	}
	if info := r.Info(); info.Kind != "static" || len(info.Members) != 2 {
		t.Fatalf("Info 不对: %+v", info)
	}
}

func TestFileJoinAndLeave(t *testing.T) {
	dir := t.TempDir()
	const interval = 10 * time.Millisecond

	a, err := NewFile(dir, WithInterval(interval), WithTTL(60*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewFile(dir, WithInterval(interval), WithTTL(60*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if _, err := a.Register(ctx, Node{Name: "n1", Addr: "127.0.0.1:7003"}); err != nil {
		t.Fatal(err)
	}
	ch := a.Watch(ctx)
	waitView(t, ch, time.Second, func(v View) bool { return v.Alive("n1") })

	// n2 后加入：n1 的视图必须自己发现它，不需要重启、不需要通知。
	unreg, err := b.Register(ctx, Node{Name: "n2", Addr: "127.0.0.1:7103"})
	if err != nil {
		t.Fatal(err)
	}
	v := waitView(t, ch, time.Second, func(v View) bool { return v.Alive("n2") })
	if addr, _ := v.Addr("n2"); addr != "127.0.0.1:7103" {
		t.Fatalf("n2 地址不对: %q", addr)
	}

	// 优雅下线：撤销记录后应该在一个轮询周期内从视图里消失。
	unreg()
	waitView(t, ch, time.Second, func(v View) bool { return !v.Alive("n2") })

	// 撤销函数必须可重复调用（defer 里再调一次是常见写法）。
	unreg()
	unreg()
}

func TestFileStaleRecordIgnored(t *testing.T) {
	dir := t.TempDir()
	f, err := NewFile(dir, WithInterval(10*time.Millisecond), WithTTL(50*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	// 一条早就过期的心跳：模拟进程被 kill -9，来不及删自己的记录。
	old := Node{Name: "ghost", Addr: "127.0.0.1:7999", TS: time.Now().Add(-time.Hour)}
	b, _ := json.Marshal(old)
	p := filepath.Join(dir, "ghost"+recordSuffix)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := f.Watch(ctx)
	v := waitView(t, ch, time.Second, func(View) bool { return true })
	if v.Alive("ghost") {
		t.Fatal("过期记录不该算活着")
	}

	// 文件还在（不删别人的文件），但主人一续心跳就立刻活过来。
	old.TS = time.Now()
	b, _ = json.Marshal(old)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	waitView(t, ch, time.Second, func(v View) bool { return v.Alive("ghost") })
}

func TestFileAddrChange(t *testing.T) {
	dir := t.TempDir()
	f, _ := NewFile(dir, WithInterval(10*time.Millisecond), WithTTL(100*time.Millisecond))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := f.Watch(ctx)
	unreg, err := f.Register(ctx, Node{Name: "n1", Addr: "10.0.0.1:7003"})
	if err != nil {
		t.Fatal(err)
	}
	waitView(t, ch, time.Second, func(v View) bool {
		a, _ := v.Addr("n1")
		return a == "10.0.0.1:7003"
	})
	// 停掉旧心跳：否则它每 10ms 把地址改回去，测到的是两个写者的抢写节奏。
	unreg()

	// 节点换地址重启：同一个名字、新地址。直接改写记录来模拟"新进程
	// 接管了同一个节点名"——不能在本进程里再 Register 一次，那会留下
	// 两个心跳协程互相覆盖 addr，测出来的是它们抢写的节奏。
	restarted := Node{Name: "n1", Addr: "10.0.0.2:7003", TS: time.Now()}
	b, _ := json.Marshal(restarted)
	if err := os.WriteFile(filepath.Join(dir, "n1"+recordSuffix), b, 0o644); err != nil {
		t.Fatal(err)
	}
	waitView(t, ch, time.Second, func(v View) bool {
		a, _ := v.Addr("n1")
		return a == "10.0.0.2:7003"
	})
}

func TestViewEqualIgnoresHeartbeat(t *testing.T) {
	now := time.Now()
	a := View{Nodes: []Node{{Name: "n1", Addr: "x", TS: now}, {Name: "n2", Addr: "y", TS: now}}}
	b := View{Nodes: []Node{{Name: "n2", Addr: "y", TS: now.Add(time.Hour)}, {Name: "n1", Addr: "x", TS: now.Add(time.Hour)}}}
	if !a.Equal(b) {
		t.Fatal("心跳时间不同不该算成员变更")
	}
	c := View{Nodes: []Node{{Name: "n1", Addr: "x"}, {Name: "n2", Addr: "z"}}}
	if a.Equal(c) {
		t.Fatal("地址变了必须算变更")
	}
	if a.Equal(View{}) {
		t.Fatal("成员数不同必须算变更")
	}
}

func TestFileBadNodeName(t *testing.T) {
	f, _ := NewFile(t.TempDir())
	if _, err := f.Register(context.Background(), Node{Name: ""}); err == nil {
		t.Fatal("空节点名应该被拒绝")
	}
	// 节点名会变成文件名，带分隔符就能写到目录外面去。
	if _, err := f.Register(context.Background(), Node{Name: "../escape"}); err == nil {
		t.Fatal("带路径分隔符的节点名应该被拒绝")
	}
}
