// 站点坞 · 站点仓库
// 与 Godot 版约定一致：站点数据与健康检测状态分离；所有 list 返回副本；上限 500；
// 显式 http / 裸 IP / 非标端口原样保留，仅对「未写协议」的地址补 https。
import { preferences } from '@kit.ArkData';
import { common } from '@kit.AbilityKit';
import { hilog } from '@kit.PerformanceAnalysisKit';
import { Site, SiteDraft } from './Site';
import { SiteDockConst } from '../common/Constants';

const DOMAIN: number = 0x5D0C;
const TAG: string = 'SiteStore';

export class SiteStore {
  private static _instance: SiteStore = new SiteStore();

  private sites: Site[] = [];
  private pref: preferences.Preferences | null = null;
  private seq: number = 0;

  static getInstance(): SiteStore {
    return SiteStore._instance;
  }

  async init(context: common.Context): Promise<number> {
    try {
      this.pref = await preferences.getPreferences(context, SiteDockConst.STORE_NAME);
      const raw: string = await this.pref.get(SiteDockConst.STORE_KEY, '[]') as string;
      const parsed: Site[] = JSON.parse(raw) as Site[];
      this.sites = [];
      if (Array.isArray(parsed)) {
        for (let i = 0; i < parsed.length && this.sites.length < SiteDockConst.MAX_SITES; i++) {
          const item: Site = parsed[i];
          if (item !== null && item !== undefined && typeof item.id === 'string' &&
            typeof item.url === 'string') {
            this.sites.push(this.normalizeSite(item));
          }
        }
      }
    } catch (err) {
      hilog.warn(DOMAIN, TAG, 'load failed: %{public}s', String(err));
      this.sites = [];
    }
    if (this.sites.length === 0) {
      this.seedDefaults();
      await this.persist();
    }
    return this.sites.length;
  }

  list(): Site[] {
    const out: Site[] = [];
    for (const s of this.sites) {
      out.push({
        id: s.id,
        name: s.name,
        url: s.url,
        group: s.group,
        icon: s.icon,
        pinned: s.pinned,
        createdAt: s.createdAt,
        updatedAt: s.updatedAt
      });
    }
    // 置顶优先 → 分组 → 创建时间，保证 UI 顺序稳定可复算
    out.sort((a: Site, b: Site): number => {
      if (a.pinned !== b.pinned) {
        return a.pinned ? -1 : 1;
      }
      if (a.group !== b.group) {
        return a.group.localeCompare(b.group);
      }
      return a.createdAt - b.createdAt;
    });
    return out;
  }

  count(): number {
    return this.sites.length;
  }

  async add(draft: SiteDraft): Promise<boolean> {
    if (this.sites.length >= SiteDockConst.MAX_SITES) {
      return false;
    }
    const now: number = Date.now();
    const site: Site = {
      id: this.nextId(),
      name: draft.name,
      url: draft.url,
      group: draft.group,
      icon: draft.icon,
      pinned: draft.pinned,
      createdAt: now,
      updatedAt: now
    };
    this.sites.push(site);
    await this.persist();
    return true;
  }

  async update(id: string, draft: SiteDraft): Promise<boolean> {
    for (let i = 0; i < this.sites.length; i++) {
      if (this.sites[i].id === id) {
        const old: Site = this.sites[i];
        this.sites[i] = {
          id: old.id,
          name: draft.name,
          url: draft.url,
          group: draft.group,
          icon: draft.icon,
          pinned: draft.pinned,
          createdAt: old.createdAt,
          updatedAt: Date.now()
        };
        await this.persist();
        return true;
      }
    }
    return false;
  }

  async remove(id: string): Promise<boolean> {
    const next: Site[] = [];
    let removed: boolean = false;
    for (const s of this.sites) {
      if (s.id === id) {
        removed = true;
      } else {
        next.push(s);
      }
    }
    this.sites = next;
    if (removed) {
      await this.persist();
    }
    return removed;
  }

  async togglePin(id: string): Promise<boolean> {
    for (const s of this.sites) {
      if (s.id === id) {
        s.pinned = !s.pinned;
        s.updatedAt = Date.now();
        await this.persist();
        return true;
      }
    }
    return false;
  }

  normalizeUrl(raw: string): string {
    const s: string = raw.trim();
    if (s.length === 0) {
      return '';
    }
    if (s.startsWith('http://') || s.startsWith('https://')) {
      return s;
    }
    return 'https://' + s;
  }

  async persist(): Promise<boolean> {
    if (this.pref === null) {
      return false;
    }
    try {
      await this.pref.put(SiteDockConst.STORE_KEY, JSON.stringify(this.sites));
      await this.pref.flush();
      return true;
    } catch (err) {
      hilog.error(DOMAIN, TAG, 'persist failed: %{public}s', String(err));
      return false;
    }
  }

  private normalizeSite(item: Site): Site {
    return {
      id: item.id,
      name: typeof item.name === 'string' ? item.name : '未命名站点',
      url: item.url,
      group: typeof item.group === 'string' ? item.group : '',
      icon: typeof item.icon === 'string' ? item.icon : '',
      pinned: item.pinned === true,
      createdAt: typeof item.createdAt === 'number' ? item.createdAt : Date.now(),
      updatedAt: typeof item.updatedAt === 'number' ? item.updatedAt : Date.now()
    };
  }

  private nextId(): string {
    this.seq += 1;
    return 's' + Date.now().toString(36) + '_' + this.seq.toString(36);
  }

  private seedDefaults(): void {
    const now: number = Date.now();
    this.sites.push({
      id: this.nextId(),
      name: '大礼帽',
      url: 'https://bighat.icu:7000',
      group: '自有',
      icon: '🎩',
      pinned: true,
      createdAt: now,
      updatedAt: now
    });
    this.sites.push({
      id: this.nextId(),
      name: '示例站点',
      url: 'https://www.example.com',
      group: '示例',
      icon: '🌐',
      pinned: false,
      createdAt: now + 1,
      updatedAt: now + 1
    });
  }
}
