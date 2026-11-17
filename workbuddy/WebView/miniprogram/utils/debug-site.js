// 调试期站点：域名与 HTTPS 反代已就位，ICP 备案仍在进行中。
// 集中管理地址、状态与上线待办，避免散落在各页面里。

const DEBUG_SITE = {
    url: 'https://bighat.icu:7000',
    name: '大礼帽',
    group: '调试',
    note: '实时可访问 · 免费 CA 证书已部署，等备案',
    host: 'bighat.icu',
    legalDomain: 'https://bighat.icu:7000',
    stage: '备案中',
    // 实测结论：HTTPS 200 / CORS 正常 / TrustAsia DV 证书已生效，预览与开发者工具可直连
    // 剩余阻塞只有 ICP 备案：真机与正式版必须先登记 request 合法域名
    blocker: 'ICP 备案（真机 / 正式版）',
};

// 只做一次初始化：用户手动删掉内置站点后不再自动重建
const BUILTIN_MARKER_KEY = 'sitedock-builtin-site-v2';

function getDebugSite() {
    return {
        url: DEBUG_SITE.url,
        name: DEBUG_SITE.name,
        group: DEBUG_SITE.group,
        note: DEBUG_SITE.note,
        host: DEBUG_SITE.host,
        legalDomain: DEBUG_SITE.legalDomain,
        stage: DEBUG_SITE.stage,
        blocker: DEBUG_SITE.blocker,
    };
}

function isDebugSite(url) {
    const match = /^https?:\/\/([^/:?#]+)/i.exec(url || '');
    return !!match && match[1].toLowerCase() === DEBUG_SITE.host;
}

function getFilingChecklist() {
    return [
        '证书已部署：腾讯云免费 TrustAsia DV 已替换自签证书，HTTPS 200 / CORS 正常，预览与开发者工具可直连',
        '到期提醒：免费证书 90 天有效，2027-02-04 前需重新申请；或改用 acme.sh DNS-01 自动续期',
        '浏览方式：站点自带 JSON 接口，浏览页自动切到原生应用视图（曲库 / 搜索 / 站点 / 任务 + 播放控件）',
        '通用浏览：任意其它站点走卡片 / 网页原貌 / 阅读三层视图，纯小程序端渲染，不需要额外服务端',
        '小程序不能执行网页 JS，脚本驱动站点无法百分百还原；完整原样渲染需 web-view（企业主体 + 备案 443 域名）或服务端浏览器',
        'nginx 7000~7010 → 6000~6010 是 TLS 卸载反向代理，属于服务架构，不能关闭',
        '抓取代理（proxy-app）不必部署；它只作为 http / 裸 IP / 自签站点的兜底',
        '当前剩余阻塞：ICP 备案通过后（进行中），后台登记 ' + DEBUG_SITE.legalDomain,
        '请求 URL 必须与登记值完全一致，不能省略 :7000',
        '注意：境内服务器未备案域名的 80/443 常被拦截，这里用 7000 非标端口的 HTTPS 才通',
    ];
}

module.exports = {
    BUILTIN_MARKER_KEY,
    getDebugSite,
    isDebugSite,
    getFilingChecklist,
};
