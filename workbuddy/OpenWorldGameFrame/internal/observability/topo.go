// 拓扑热力图：把 Chunk 归属和负载渲染成 SVG。
//
// 数据来自 world.Chunks()，经 any 传入（避免 observability 依赖 world）。
// 这里只负责"把格子画出来"，不关心数据怎么来——换数据源只改 OnTopo 的回调。
package observability

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// topoCell 是热力图的一个格子，字段名与 world.ChunkInfo 的 JSON 标签对齐。
//
// 为什么不用 world.ChunkInfo 直接接收：observability 被 world import
// （world.NewScene 接收 *observability.Metrics），反方向 import 会成环。
// 用 JSON 标签对齐的本地结构体，让两个包在"字段名"这个契约上解耦。
type topoCell struct {
	Chunk    struct{ X, Z int32 } `json:"chunk"`
	SceneID  uint32               `json:"sceneId"`
	Node     string               `json:"node"`
	Local    bool                 `json:"local"`
	Alive    bool                 `json:"alive"`
	Entities int64                `json:"entities"`
	Load     int64                `json:"load"`
}

// handleTopo 渲染拓扑热力图页面。
func (a *Admin) handleTopo(w http.ResponseWriter, r *http.Request) {
	if a.topoFn == nil {
		http.Error(w, "本进程不支持拓扑热力图", http.StatusNotImplemented)
		return
	}
	cells, err := decodeTopoCells(a.topoFn())
	if err != nil {
		http.Error(w, "拓扑数据解析失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(renderTopoPage(cells)))
}

// decodeTopoCells 把 any（实际是 []ChunkInfo 的 JSON）转成 []topoCell。
func decodeTopoCells(v any) ([]topoCell, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var cells []topoCell
	if err := json.Unmarshal(b, &cells); err != nil {
		return nil, err
	}
	return cells, nil
}

// renderTopoPage 生成完整的 HTML 页面，内含 SVG 热力图。
//
// 着色规则：
//   - Alive=false 的格子画灰（主人挂了/不可达）
//   - 活格子按 Load（0-100）从绿到黄到红渐变，负载越高越红
//   - 本进程承载的格子描实线，远端格子描虚线
//
// 负载的颜色不是线性插值，而是分档：0-60 绿、60-85 黄、85-100 红。
// 分档比连续渐变更好读——运维一眼能看出"哪个格子该拆"，而连续渐变
// 需要对着色条比数值。
func renderTopoPage(cells []topoCell) string {
	if len(cells) == 0 {
		return "<html><body>拓扑为空（无场景登记）</body></html>"
	}

	// 先扫一遍确定网格范围，再定 cell 像素尺寸。
	var minX, minZ, maxX, maxZ int32
	first := true
	for _, c := range cells {
		if first {
			minX, maxX, minZ, maxZ = c.Chunk.X, c.Chunk.X, c.Chunk.Z, c.Chunk.Z
			first = false
			continue
		}
		if c.Chunk.X < minX {
			minX = c.Chunk.X
		}
		if c.Chunk.X > maxX {
			maxX = c.Chunk.X
		}
		if c.Chunk.Z < minZ {
			minZ = c.Chunk.Z
		}
		if c.Chunk.Z > maxZ {
			maxZ = c.Chunk.Z
		}
	}

	const (
		cell    = 48 // 每格像素
		gap     = 3  // 格子间距
		padding = 20 // 画布内边距
		labelW  = 160
		headerH = 20
	)

	cols := int(maxX - minX + 1)
	rows := int(maxZ - minZ + 1)
	svgW := padding*2 + cols*cell + (cols-1)*gap + labelW
	svgH := padding*2 + rows*cell + (rows-1)*gap

	var sb strings.Builder
	sb.Grow(4096)

	// 按 (X,Z) 定位每个格子。
	byKey := make(map[[2]int32]topoCell, len(cells))
	for _, c := range cells {
		byKey[[2]int32{c.Chunk.X, c.Chunk.Z}] = c
	}

	// 图例 + 标题。
	sb.WriteString(`<!doctype html><html><head><meta charset="utf-8"><title>拓扑热力图</title>`)
	sb.WriteString(`<style>body{font-family:system-ui,sans-serif;background:#111;color:#ddd;margin:0;padding:20px}`)
	sb.WriteString(`h1{font-size:18px;font-weight:600}`)
	sb.WriteString(`.legend{display:flex;gap:16px;align-items:center;margin:12px 0;font-size:13px}`)
	sb.WriteString(`.swatch{display:inline-block;width:14px;height:14px;border-radius:3px;vertical-align:middle;margin-right:4px}`)
	sb.WriteString(`svg{max-width:100%;height:auto}</style></head><body>`)
	sb.WriteString(`<h1>世界拓扑热力图</h1>`)
	sb.WriteString(`<div class="legend">`)
	sb.WriteString(`<span><span class="swatch" style="background:#2e7d32"></span>低负载</span>`)
	sb.WriteString(`<span><span class="swatch" style="background:#f9a825"></span>中负载</span>`)
	sb.WriteString(`<span><span class="swatch" style="background:#c62828"></span>高负载</span>`)
	sb.WriteString(`<span><span class="swatch" style="background:#555"></span>不可达</span>`)
	sb.WriteString(`<span>实线=本进程 &nbsp; 虚线=远端</span>`)
	sb.WriteString(`</div>`)

	fmt.Fprintf(&sb, `<svg width="%d" height="%d" xmlns="http://www.w3.org/2000/svg">`, svgW, svgH)
	sb.WriteString(`<rect width="100%" height="100%" fill="#1a1a1a"/>`)

	for cz := minZ; cz <= maxZ; cz++ {
		for cx := minX; cx <= maxX; cx++ {
			key := [2]int32{cx, cz}
			c, ok := byKey[key]
			x := padding + int(cx-minX)*(cell+gap)
			y := padding + int(cz-minZ)*(cell+gap)

			fill := "#333" // 默认：未登记
			stroke := "none"
			strokeDash := ""
			var label string

			if ok {
				label = fmt.Sprintf("S%d", c.SceneID)
				switch {
				case !c.Alive:
					fill = "#555"
				default:
					fill = loadColor(c.Load)
				}
				if c.Local {
					stroke = "#fff"
				} else {
					stroke = "#999"
					strokeDash = ` stroke-dasharray="4 3"`
				}
			}

			fmt.Fprintf(&sb, `<rect x="%d" y="%d" width="%d" height="%d" rx="3" fill="%s" stroke="%s" stroke-width="1"%s/>`,
				x, y, cell, cell, fill, stroke, strokeDash)

			if label != "" {
				// 场景号居中，字体随格子尺寸缩放。
				fs := cell / 3
				fmt.Fprintf(&sb, `<text x="%d" y="%d" font-size="%d" fill="#fff" text-anchor="middle" dominant-baseline="middle" font-family="system-ui">%s</text>`,
					x+cell/2, y+cell/2, fs, label)
				if c.Entities > 0 {
					fmt.Fprintf(&sb, `<text x="%d" y="%d" font-size="10" fill="#bbb" text-anchor="middle" font-family="system-ui">%d</text>`,
						x+cell/2, y+cell-6, c.Entities)
				}
			}
		}
	}

	// 坐标轴标签（X 轴 Chunk 号，Z 轴 Chunk 号）。
	for cx := minX; cx <= maxX; cx++ {
		x := padding + int(cx-minX)*(cell+gap) + cell/2
		fmt.Fprintf(&sb, `<text x="%d" y="%d" font-size="10" fill="#888" text-anchor="middle" font-family="system-ui">x%d</text>`,
			x, svgH-padding+14, cx)
	}
	for cz := minZ; cz <= maxZ; cz++ {
		y := padding + int(cz-minZ)*(cell+gap) + cell/2
		fmt.Fprintf(&sb, `<text x="%d" y="%d" font-size="10" fill="#888" text-anchor="middle" font-family="system-ui">z%d</text>`,
			padding-24, y, cz)
	}

	sb.WriteString(`</svg></body></html>`)
	return sb.String()
}

// loadColor 把负载 0-100 映射到颜色，分三档。
func loadColor(load int64) string {
	switch {
	case load >= 85:
		return "#c62828" // 红：该拆了
	case load >= 60:
		return "#f9a825" // 黄：注意
	default:
		return "#2e7d32" // 绿：健康
	}
}
