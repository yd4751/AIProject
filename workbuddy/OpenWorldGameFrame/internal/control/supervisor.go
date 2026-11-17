package control

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// Supervisor 负责子进程的拉起、等待和强制回收。
//
// 它只做"进程级"的事：起、等、杀。至于这个进程该不该起、起完健不健康，
// 由编排器和探针决定。把两件事混在一起会写出"重启逻辑散落在三个地方"
// 的代码，很难排查。
type Supervisor struct {
	binary   string
	workDir  string
	logDir   string
	tickMs   int
	logLevel string

	mu    sync.Mutex
	procs map[string]*procHandle

	// onExit 在子进程退出后回调，把真实退出码写回注册表。
	// 这是发现"进程自己崩了"的唯一可靠信号——HTTP 探针只能告诉你
	// 连不上，不能告诉你为什么连不上。
	onExit func(instanceID string, code int)
}

type procHandle struct {
	cmd  *exec.Cmd
	done chan struct{}

	// 以下字段只在 done 关闭后可读（close 建立了 happens-before）。
	code int
	err  error
}

// NewSupervisor 创建进程管理器。
func NewSupervisor(cfg Config) (*Supervisor, error) {
	abs, err := filepath.Abs(cfg.Binary)
	if err != nil {
		return nil, err
	}
	dir := cfg.LogDir
	if dir == "" {
		dir = "logs"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}
	return &Supervisor{
		binary:   abs,
		workDir:  cfg.WorkDir,
		logDir:   dir,
		tickMs:   cfg.TickMs,
		logLevel: cfg.LogLevel,
		procs:    make(map[string]*procHandle),
	}, nil
}

// SetExitHook 注册退出回调。
func (s *Supervisor) SetExitHook(fn func(instanceID string, code int)) { s.onExit = fn }

// Start 拉起一个实例，返回 PID。
func (s *Supervisor) Start(inst Instance) (int, error) {
	s.mu.Lock()
	_, exists := s.procs[inst.ID]
	s.mu.Unlock()
	if exists {
		return 0, fmt.Errorf("实例 %s 已有托管进程在运行", inst.ID)
	}

	logPath := filepath.Join(s.logDir, inst.ID+".log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, fmt.Errorf("打开日志文件: %w", err)
	}

	args := []string{
		"-gate-addr", inst.GateAddr,
		"-admin-addr", inst.AdminAddr,
		"-instance-id", inst.ID,
		"-zone", inst.ZoneID,
	}
	if s.tickMs > 0 {
		args = append(args, "-tick-ms", strconv.Itoa(s.tickMs))
	}
	if s.logLevel != "" {
		args = append(args, "-log-level", s.logLevel)
	}

	cmd := exec.Command(s.binary, args...)
	cmd.Dir = s.workDir
	// stdout/stderr 直接落文件，不经过控制面的管道：
	// 走管道的话，子进程日志量大时会被我们自己的读取速度反压，
	// 表现为"服务端莫名其妙变慢"。
	cmd.Stdout = f
	cmd.Stderr = f

	if err := cmd.Start(); err != nil {
		_ = f.Close()
		return 0, fmt.Errorf("启动进程: %w", err)
	}

	h := &procHandle{cmd: cmd, done: make(chan struct{})}
	s.mu.Lock()
	s.procs[inst.ID] = h
	s.mu.Unlock()

	go func() {
		werr := cmd.Wait()
		_ = f.Close()

		code := 0
		if werr != nil {
			var ee *exec.ExitError
			if errors.As(werr, &ee) {
				code = ee.ExitCode()
			} else {
				code = -1
			}
		}
		h.code = code
		h.err = werr
		close(h.done)

		s.mu.Lock()
		delete(s.procs, inst.ID)
		s.mu.Unlock()

		if s.onExit != nil {
			s.onExit(inst.ID, code)
		}
	}()

	return cmd.Process.Pid, nil
}

// Wait 等待进程退出。返回 (退出码, 是否在超时前退出)。
func (s *Supervisor) Wait(id string, timeout time.Duration) (int, bool) {
	s.mu.Lock()
	h := s.procs[id]
	s.mu.Unlock()
	if h == nil {
		return 0, true // 已经没有托管进程了，视为已退出
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-h.done:
		return h.code, true
	case <-timer.C:
		return 0, false
	}
}

// Kill 强制结束进程。返回是否真的发出了 kill。
func (s *Supervisor) Kill(id string) bool {
	s.mu.Lock()
	h := s.procs[id]
	s.mu.Unlock()
	if h == nil || h.cmd.Process == nil {
		return false
	}
	// 进程可能刚好在这时自己退了，Kill 返回错误是正常的。
	_ = h.cmd.Process.Kill()
	return true
}

// Managed 返回是否有托管进程。
func (s *Supervisor) Managed(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.procs[id]
	return ok
}
