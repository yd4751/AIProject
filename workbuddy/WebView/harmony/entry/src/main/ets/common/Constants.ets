// 站点坞 · 全局常量
// 数值口径沿用 Godot 版数据层约定，便于两端对照复算。
export class SiteDockConst {
  static readonly MAX_SITES: number = 500;
  static readonly CHECK_TOP_N: number = 8;
  static readonly CHECK_CONCURRENCY: number = 4;
  static readonly CHECK_TIMEOUT_MS: number = 1500;
  static readonly STORE_NAME: string = 'sitedock_sites';
  static readonly STORE_KEY: string = 'sites_json';
  // 调试期开启 ArkWeb 远程调试（hdc fport + chrome://inspect）；发布前改为 false
  static readonly WEB_DEBUGGING: boolean = true;
}
