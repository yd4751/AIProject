package core

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

type mockComp struct {
	name     string
	mu       *sync.Mutex
	events   *[]string
	startErr error
}

func (m *mockComp) Name() string { return m.name }

func (m *mockComp) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	*m.events = append(*m.events, "start:"+m.name)
	return m.startErr
}

func (m *mockComp) Stop(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	*m.events = append(*m.events, "stop:"+m.name)
	return nil
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// 顺序必须是：正序启动，倒序停止。
// 网关要最后起、最先关，这样关停期间不会有新连接进来。
func TestComponentsStartForwardStopReverse(t *testing.T) {
	var mu sync.Mutex
	var events []string

	app := New("test", quietLogger())
	for _, name := range []string{"scene", "gate", "admin"} {
		app.Register(&mockComp{name: name, mu: &mu, events: &events})
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(80 * time.Millisecond)
		cancel()
	}()

	if err := app.Run(ctx); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	mu.Lock()
	got := strings.Join(events, ",")
	mu.Unlock()

	want := "start:scene,start:gate,start:admin,stop:admin,stop:gate,stop:scene"
	if got != want {
		t.Fatalf("lifecycle order mismatch\n got: %s\nwant: %s", got, want)
	}
}

// 中途启动失败时，已经起来的组件必须被回滚停止，不能留下半启动状态。
func TestStartFailureRollsBackStartedComponents(t *testing.T) {
	var mu sync.Mutex
	var events []string

	app := New("test", quietLogger())
	app.Register(&mockComp{name: "a", mu: &mu, events: &events})
	app.Register(&mockComp{name: "b", mu: &mu, events: &events, startErr: errors.New("boom")})
	app.Register(&mockComp{name: "c", mu: &mu, events: &events})

	err := app.Run(context.Background())
	if err == nil {
		t.Fatal("expected Run to return the start error")
	}

	mu.Lock()
	got := strings.Join(events, ",")
	mu.Unlock()

	want := "start:a,start:b,stop:a"
	if got != want {
		t.Fatalf("rollback mismatch\n got: %s\nwant: %s", got, want)
	}
	if app.Ready() {
		t.Fatal("app must not be ready when a component failed to start")
	}
}
