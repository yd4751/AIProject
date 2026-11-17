package control

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// persistFile 是控制面落盘的最小状态。
//
// 只存"重启后还需要知道的东西"：谁应该是活的、PID 是多少、上次是什么状态。
// 不存运行指标（在线数、tick 滞后那些）——它们重启后会重新采到，
// 存下来反而会让控制台显示过期数据。
type persistFile struct {
	SavedAt   time.Time           `json:"savedAt"`
	Zones     []persistedZone     `json:"zones"`
	Instances []persistedInstance `json:"instances"`
}

type persistedZone struct {
	ID      string `json:"id"`
	Desired bool   `json:"desired"`
}

type persistedInstance struct {
	ID    string    `json:"id"`
	State State     `json:"state"`
	PID   int       `json:"pid"`
	Since time.Time `json:"since"`
}

func (r *Registry) statePath() string {
	if r.cfg.LogDir == "" {
		return ""
	}
	return filepath.Join(r.cfg.LogDir, "control-state.json")
}

// Save 把状态原子地写到磁盘。
//
// 先写临时文件再 rename：控制面可能在任意时刻被 kill，
// 直接覆盖会导致文件半截、下次启动解析失败，然后所有实例都被当成不认识。
func (r *Registry) Save() error {
	path := r.statePath()
	if path == "" {
		return nil
	}

	r.mu.RLock()
	pf := persistFile{SavedAt: time.Now()}
	for _, z := range r.zoneList {
		pf.Zones = append(pf.Zones, persistedZone{ID: z.cfg.ID, Desired: z.desiredRunning})
		for _, inst := range z.instances {
			pf.Instances = append(pf.Instances, persistedInstance{
				ID:    inst.ID,
				State: inst.State,
				PID:   inst.PID,
				Since: inst.Since,
			})
		}
	}
	r.mu.RUnlock()

	b, err := json.MarshalIndent(pf, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Load 读取上次的状态并尝试接管还活着的实例。
//
// 这一步是"控制面重启不等于全区停服"的关键：控制面是单点，
// 它重启时底下几十个 World 不能跟着重来一遍。
//
// 接管判断只看一个事实：运维接口还应答吗？
// 应答就接过来继续管；不应答就标异常，让人决定要不要拉起来。
func (r *Registry) Load() (adopted, lost int) {
	path := r.statePath()
	if path == "" {
		return 0, 0
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, 0
	}
	// 去掉 UTF-8 BOM。Windows 上的编辑器、PowerShell 的 Set-Content -Encoding utf8
	// 都会在文件头写 BOM，而 encoding/json 不接受它，会报
	// "invalid character 'ï' looking for beginning of value"。
	// 这类问题只会在"人手工改过配置文件"时出现，恰恰是最需要能启动的时候。
	b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
	var pf persistFile
	if err := json.Unmarshal(b, &pf); err != nil {
		r.log.Warn("控制面状态文件损坏，忽略", "path", path, "err", err)
		return 0, 0
	}

	desired := make(map[string]bool, len(pf.Zones))
	for _, z := range pf.Zones {
		desired[z.ID] = z.Desired
	}

	for _, pi := range pf.Instances {
		inst, ok := r.Get(pi.ID)
		if !ok || !pi.State.Active() {
			continue
		}
		if code, err := r.getStatus(inst.ProbeURL("/readyz")); err == nil && code < 500 {
			// 还在应答（排水中的实例会返回 503，也算活着）。
			r.Update(pi.ID, func(i *Instance) {
				i.State = StateRunning
				i.PID = pi.PID
				i.Since = pi.Since
				i.LastError = ""
			})
			adopted++
		} else {
			r.Update(pi.ID, func(i *Instance) {
				i.State = StateFailed
				i.LastError = "控制面重启后未探活，进程可能已退出"
				i.Since = time.Now()
			})
			lost++
		}
	}

	r.mu.Lock()
	for _, z := range r.zoneList {
		if v, ok := desired[z.cfg.ID]; ok {
			z.desiredRunning = v
		}
	}
	r.mu.Unlock()

	return adopted, lost
}

// RunAutoSave 周期落盘。
//
// 为什么用定时保存而不是"每次状态变化就保存"：探针每 2 秒更新一次快照，
// 逐次触发写盘会让磁盘 IO 随着实例数线性增长，而这个文件的时效性
// 只需要秒级——控制面真挂了，人也要几秒后才会发现。
func (r *Registry) RunAutoSave(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = r.Save()
			return
		case <-t.C:
			if err := r.Save(); err != nil {
				r.log.Warn("控制面状态落盘失败", "err", err)
			}
		}
	}
}
