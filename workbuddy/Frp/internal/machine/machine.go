// Package machine 生成跨平台的稳定机器码。
//
// 不依赖 WMI / /etc/machine-id 等平台特定机制，仅用 Go 标准库的
// hostname + 网卡 MAC 组合计算 SHA256，Windows / Linux / macOS 通用。
package machine

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"os"
	"sort"
	"strings"
)

// ID 返回稳定的机器码（32 位十六进制），跨平台且同一台机器保持不变。
func ID() string {
	hostname, _ := os.Hostname()

	var macs []string
	if ifaces, err := net.Interfaces(); err == nil {
		for _, iface := range ifaces {
			// 跳过回环、未启用、虚拟/临时网卡
			if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
				continue
			}
			mac := iface.HardwareAddr.String()
			if mac == "" || strings.HasPrefix(mac, "00:00:00") {
				continue
			}
			macs = append(macs, mac)
		}
	}
	sort.Strings(macs)

	raw := hostname + "|" + strings.Join(macs, "|")
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:16])
}
