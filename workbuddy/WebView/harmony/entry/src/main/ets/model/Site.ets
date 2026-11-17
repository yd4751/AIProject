// 站点坞 · 数据契约（与 Godot 版 SiteStore.gd 保持一致）

export interface Site {
  id: string;
  name: string;
  url: string;
  group: string;
  icon: string;
  pinned: boolean;
  createdAt: number;
  updatedAt: number;
}

export interface SiteDraft {
  name: string;
  url: string;
  group: string;
  icon: string;
  pinned: boolean;
}

// state: unknown | checking | online | offline
// 健康检测结果不落盘，重启后重新检测（与站点数据分离存储）
export interface SiteStatus {
  state: string;
  code: number;
  latencyMs: number;
  checkedAt: number;
  message: string;
}

export interface SiteStatusEntry {
  id: string;
  status: SiteStatus;
}

export function emptyStatus(): SiteStatus {
  return {
    state: 'unknown',
    code: 0,
    latencyMs: 0,
    checkedAt: 0,
    message: ''
  };
}

export function emptyDraft(): SiteDraft {
  return {
    name: '',
    url: '',
    group: '',
    icon: '',
    pinned: false
  };
}
