// 请求策略与访问诊断：
// 1) 默认直连站点（换 CA 证书后的主路径）；配置抓取代理后才改由服务端中转，仅作 http / IP / 自签站点兜底。
// 2) 依据微信小程序的网络规则，指出某个 URL 为什么在小程序里请求不通。

const { isDebugSite, getDebugSite } = require('./debug-site');

const PROXY_STORAGE_KEY = 'sitedock-proxy-v1';

function getProxyConfig() {
    const value = wx.getStorageSync(PROXY_STORAGE_KEY);
    if (!value || typeof value !== 'object' || Array.isArray(value)) {
        return { template: '', token: '' };
    }
    return {
        template: typeof value.template === 'string' ? value.template.trim() : '',
        token: typeof value.token === 'string' ? value.token.trim() : '',
    };
}

function saveProxyConfig(patch) {
    const next = {
        template: typeof patch.template === 'string' ? patch.template.trim() : '',
        token: typeof patch.token === 'string' ? patch.token.trim() : '',
    };
    wx.setStorageSync(PROXY_STORAGE_KEY, next);
    return next;
}

function clearProxyConfig() {
    wx.removeStorageSync(PROXY_STORAGE_KEY);
}

function isUsingProxy() {
    return getProxyConfig().template !== '';
}

// 代理模板示例：https://proxy.example.com/fetch?url={url}
// 未写 {url} 时自动追加 url 参数；填写了 token 则一并追加，供服务端校验。
function buildRequestUrl(targetUrl) {
    const config = getProxyConfig();
    if (!config.template) {
        return targetUrl;
    }
    const separator = config.template.indexOf('?') >= 0 ? '&' : '?';
    let requestUrl = config.template.indexOf('{url}') >= 0
        ? config.template.replace('{url}', encodeURIComponent(targetUrl))
        : config.template + separator + 'url=' + encodeURIComponent(targetUrl);
    if (config.token) {
        requestUrl += (requestUrl.indexOf('?') >= 0 ? '&' : '?') + 'token=' + encodeURIComponent(config.token);
    }
    return requestUrl;
}

// 返回该地址在小程序环境里请求不通的具体原因（未配置代理时按直连规则判断）
// 未配置代理时，该地址是否满足小程序直连的最低条件（https + 域名）
// 注：官方允许按「域名:端口」登记合法域名，因此端口不作为硬性阻断条件。
function isDirectlyReachable(url) {
    if (isUsingProxy()) {
        return true;
    }
    const match = /^(https?):\/\/([^/:?#]+)(?::(\d+))?/i.exec(url || '');
    if (!match) {
        return false;
    }
    const isIp = /^\d{1,3}(\.\d{1,3}){3}$/.test(match[2]);
    return match[1].toLowerCase() === 'https' && !isIp;
}

function diagnoseUrl(url) {
    // 备案调试期站点：给出针对性结论，避免把问题笼统归到「格式不合规」
    if (isDebugSite(url)) {
        const site = getDebugSite();
        const lines = [
            '服务端已就绪：https 返回 200、CORS 头正常、自签证书已换成腾讯云免费 TrustAsia DV，域名 ' + site.host,
            '实时请求现在可以直接连通；若仍然失败，优先检查网络、证书是否到期（2027-02-04）或 nginx 是否已 reload',
            '浏览页会自动探测站点 JSON 接口并切到原生应用视图；其它站点走通用卡片 / 网页原貌 / 阅读三层渲染',
            '脚本驱动的网页无法在小程序里原样执行：这是平台限制，不是证书或跨域问题',
            '真机 / 体验版还需：ICP 备案通过（当前进行中）→ 后台按 ' + site.legalDomain + ' 原样登记 request 合法域名 → 重新发布',
        ];
        if (isUsingProxy()) {
            lines.push('已启用抓取代理：本站点不需要它，留空直连更直接；抓取代理只是 http / 裸 IP / 自签站点的兜底');
        } else {
            lines.push('保持直连即可；抓取代理只在目标站点是 http / 裸 IP / 自签证书时才需要');
        }
        lines.push('WorkBuddy 预览面板属于浏览器环境：nginx 需返回 Access-Control-Allow-* 并正确处理 OPTIONS 预检');
        return lines;
    }
    const reasons = [];
    const proxyOn = isUsingProxy();
    const match = /^(https?):\/\/([^/:?#]+)(?::(\d+))?/i.exec(url || '');
    if (!match) {
        return ['站点地址格式不正确，请检查是否以 http(s):// 开头'];
    }
    const scheme = match[1].toLowerCase();
    const host = match[2];
    const port = match[3] || '';
    const isIp = /^\d{1,3}(\.\d{1,3}){3}$/.test(host);

    if (!proxyOn) {
        if (scheme !== 'https') {
            reasons.push('使用了 http 明文：微信小程序只允许 https 请求，真机与体验版一律拒绝');
        }
        if (isIp) {
            reasons.push('直接使用 IP 地址：小程序合法域名必须是域名，不支持 IP 直连');
        }
        if (port !== '' && port !== '443') {
            reasons.push('使用了 ' + port + ' 端口：后台须按「域名:端口」原样登记，请求 URL 的端口必须与登记完全一致');
        }
        if (reasons.length > 0) {
            reasons.push('浏览器能打开不等于小程序能请求：小程序走的是独立的合法域名校验，且不受站点 CORS 设置影响');
            reasons.push('解决方式一：给站点绑定已备案域名 + HTTPS 证书，并在「开发管理 → 开发设置 → 服务器域名」加入 request 合法域名');
            reasons.push('解决方式二：在「设置 → 抓取代理」填一个 https 中转服务地址，由服务端抓取后回传');
        } else {
            reasons.push('请确认该域名已加入小程序后台的 request 合法域名');
            reasons.push('本地调试可在微信开发者工具勾选「不校验合法域名、TLS 版本以及 HTTPS 证书」');
        }
    } else {
        if (scheme !== 'https') {
            reasons.push('目标站点是 http 明文：需由抓取代理在服务端完成中转，代理地址本身必须是 https');
        }
        if (isIp) {
            reasons.push('目标站点是 IP 直连：需由抓取代理在服务端访问，小程序侧只请求代理域名');
        }
        reasons.push('请确认代理域名已加入小程序后台的 request 合法域名，且代理服务可访问目标站点');
    }
    reasons.push('在 WorkBuddy 预览面板（浏览器环境）中，跨域请求还需要代理返回 Access-Control-Allow-Origin');
    return reasons;
}

module.exports = {
    getProxyConfig,
    saveProxyConfig,
    clearProxyConfig,
    isUsingProxy,
    isDirectlyReachable,
    buildRequestUrl,
    diagnoseUrl,
};
