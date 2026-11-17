// 站点坞 · 真实健康检测（Godot 预览版此处是演示值）
// 只读取响应码，不缓存网页内容；超时与并发口径由 Constants 统一定义。
import { http } from '@kit.NetworkKit';
import { SiteStatus } from './Site';

export class SiteChecker {
  static async check(url: string, timeoutMs: number): Promise<SiteStatus> {
    const started: number = Date.now();
    const req = http.createHttp();
    try {
      const headers: Record<string, string> = {};
      headers['User-Agent'] = 'SiteDock/1.0';
      const options: http.HttpRequestOptions = {
        method: http.RequestMethod.GET,
        connectTimeout: timeoutMs,
        readTimeout: timeoutMs,
        header: headers
      };
      const resp: http.HttpResponse = await req.request(url, options);
      const code: number = resp.responseCode;
      const ok: boolean = code >= 200 && code < 400;
      return {
        state: ok ? 'online' : 'offline',
        code: code,
        latencyMs: Date.now() - started,
        checkedAt: Date.now(),
        message: ok ? '' : ('HTTP ' + code.toString())
      };
    } catch (err) {
      return {
        state: 'offline',
        code: 0,
        latencyMs: Date.now() - started,
        checkedAt: Date.now(),
        message: String(err)
      };
    } finally {
      req.destroy();
    }
  }
}
