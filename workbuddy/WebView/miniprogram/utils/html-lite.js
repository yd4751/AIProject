// 轻量 HTML 抓取与解析：把站点首页解析成结构化内容，
// 供原生阅读页渲染（标题、文本块、图片、站内链接），不依赖 DOM。
const { buildRequestUrl, diagnoseUrl, isUsingProxy } = require('./request-proxy');

function fetchPage(url, timeout) {
    return new Promise((resolve, reject) => {
        wx.request({
            url: buildRequestUrl(url),
            method: 'GET',
            timeout: timeout || 10000,
            header: {
                Accept: 'text/html,application/xhtml+xml',
            },
            success: response => {
                if (response.statusCode >= 500) {
                    reject(withDiagnosis(new Error('请求返回 ' + response.statusCode + (isUsingProxy() ? '（抓取代理）' : '（站点）')), url));
                    return;
                }
                if (response.statusCode >= 400) {
                    reject(withDiagnosis(new Error('请求返回 ' + response.statusCode + '，页面不存在或代理拒绝访问'), url));
                    return;
                }
                let html = response.data;
                if (html && typeof html === 'object' && typeof html.html === 'string') {
                    html = html.html;
                }
                if (typeof html !== 'string') {
                    reject(withDiagnosis(new Error('没有拿到网页内容（代理应返回 HTML 文本或 { html: "..." }）'), url));
                    return;
                }
                resolve(html);
            },
            fail: error => {
                const message = error && error.errMsg ? error.errMsg : '';
                let reason = '无法连接到站点';
                if (message.indexOf('timeout') >= 0) {
                    reason = '请求超时';
                } else if (message.indexOf('domain') >= 0 || message.indexOf('ssl') >= 0 || message.indexOf('SSL') >= 0) {
                    reason = '请求被拦截：域名未加入合法域名，或 HTTPS 证书不被信任';
                } else if (isUsingProxy()) {
                    reason = '无法连接到抓取代理';
                }
                reject(withDiagnosis(new Error(reason), url));
            },
        });
    });
}

function withDiagnosis(error, url) {
    error.reasons = diagnoseUrl(url);
    return error;
}

function parseHtml(html, baseUrl) {
    const title = decodeEntities(firstMatch(/<title[^>]*>([\s\S]*?)<\/title>/i, html));
    const description = decodeEntities(
        firstMatch(/<meta[^>]+name=["']description["'][^>]+content=["']([^"']*)["']/i, html)
        || firstMatch(/<meta[^>]+content=["']([^"']*)["'][^>]+name=["']description["']/i, html)
    );

    const body = stripNoise(html);
    const blocks = extractBlocks(body, baseUrl);
    const links = extractLinks(body, baseUrl);

    return {
        title: cleanText(title),
        description: cleanText(description),
        blocks: blocks,
        links: links,
        host: hostOf(baseUrl),
    };
}

function stripNoise(html) {
    return html
        .replace(/<!--[\s\S]*?-->/g, '')
        .replace(/<(script|style|noscript|template|svg|iframe|canvas)[^>]*>[\s\S]*?<\/\1>/gi, '')
        .replace(/<(script|style|noscript|template|svg|iframe|canvas)[^>]*\/>/gi, '');
}

function extractBlocks(body, baseUrl) {
    const blocks = [];
    const pattern = /<(h[1-6]|p|li|blockquote|figcaption)([^>]*)>([\s\S]*?)<\/\1>|<img([^>]*)>/gi;
    let match = pattern.exec(body);
    while (match !== null) {
        if (match[4] !== undefined) {
            const rawSrc = imageSrc(match[4]);
            const src = rawSrc.indexOf('data:') === 0 ? rawSrc : resolveUrl(baseUrl, rawSrc);
            if (src) {
                blocks.push({ type: 'image', src: src });
            }
        } else {
            const tag = match[1].toLowerCase();
            const text = cleanText(decodeEntities(stripTags(match[3])));
            if (text.length < 4) {
                match = pattern.exec(body);
                continue;
            }
            if (tag.charAt(0) === 'h') {
                blocks.push({ type: 'heading', level: Number(tag.charAt(1)), text: text });
            } else {
                blocks.push({ type: 'text', text: text });
            }
        }
        if (blocks.length >= 120) {
            break;
        }
        match = pattern.exec(body);
    }

    const deduped = [];
    let previous = '';
    for (let i = 0; i < blocks.length; i += 1) {
        const block = blocks[i];
        if (block.type !== 'image' && block.text === previous) {
            continue;
        }
        if (block.type !== 'image') {
            previous = block.text;
        }
        deduped.push(block);
    }
    return deduped;
}

function imageSrc(attrText) {
    const pairs = [
        /(?:^|\s)src=["']([^"']+)["']/i,
        /(?:^|\s)data-src=["']([^"']+)["']/i,
        /(?:^|\s)data-original=["']([^"']+)["']/i,
        /(?:^|\s)data-lazy-src=["']([^"']+)["']/i,
    ];
    for (let i = 0; i < pairs.length; i += 1) {
        const value = firstMatch(pairs[i], attrText);
        if (value && !/^data:image\/svg/i.test(value)) {
            return value.trim();
        }
    }
    return '';
}

function extractLinks(body, baseUrl) {
    const baseHost = hostOf(baseUrl);
    const seen = {};
    const links = [];
    const pattern = /<a\s[^>]*href=["']([^"']+)["'][^>]*>([\s\S]*?)<\/a>/gi;
    let match = pattern.exec(body);
    while (match !== null && links.length < 20) {
        const href = match[1].trim();
        const text = cleanText(decodeEntities(stripTags(match[2])));
        if (href === '#' || href.indexOf('javascript:') === 0 || href.indexOf('mailto:') === 0 || href.indexOf('tel:') === 0) {
            match = pattern.exec(body);
            continue;
        }
        const resolved = resolveUrl(baseUrl, href);
        if (resolved === '' || hostOf(resolved) !== baseHost || seen[resolved]) {
            match = pattern.exec(body);
            continue;
        }
        seen[resolved] = true;
        links.push({
            href: resolved,
            text: text.length > 1 ? text : pathOf(resolved),
        });
        match = pattern.exec(body);
    }
    return links;
}

function resolveUrl(baseUrl, href) {
    const baseMatch = /^(https?:\/\/[^/?#]+)([^?#]*)/i.exec(baseUrl);
    if (!baseMatch) {
        return '';
    }
    if (/^https?:\/\//i.test(href)) {
        return href;
    }
    if (href.indexOf('//') === 0) {
        return 'https:' + href;
    }
    const origin = baseMatch[1];
    const cut = href.search(/[?#]/);
    const pathPart = cut === -1 ? href : href.slice(0, cut);
    const suffix = cut === -1 ? '' : href.slice(cut);
    const basePath = baseMatch[2] || '/';
    const combined = pathPart.charAt(0) === '/'
        ? pathPart
        : basePath.slice(0, basePath.lastIndexOf('/') + 1) + pathPart;
    const segments = [];
    const parts = combined.split('/');
    for (let i = 0; i < parts.length; i += 1) {
        if (parts[i] === '' || parts[i] === '.') {
            continue;
        }
        if (parts[i] === '..') {
            segments.pop();
        } else {
            segments.push(parts[i]);
        }
    }
    return origin + '/' + segments.join('/') + suffix;
}

function hostOf(url) {
    const match = /^https?:\/\/([^/:?#]+)/i.exec(url);
    return match ? match[1].toLowerCase() : '';
}

function pathOf(url) {
    const match = /^https?:\/\/[^/]+(\/[^?#]*)/i.exec(url);
    if (!match) {
        return url;
    }
    const path = match[1];
    return path === '/' ? '首页' : path;
}

function stripTags(text) {
    return String(text).replace(/<[^>]+>/g, ' ');
}

function cleanText(text) {
    return String(text).replace(/\s+/g, ' ').trim();
}

function firstMatch(pattern, source) {
    const match = pattern.exec(source);
    return match ? match[1] : '';
}

// APP 化视图：从链接里提取「可点击的卡片 / 条目 / 顶部标签」，
// 让普通站点也能以原生列表与卡片网格浏览，而不是一整页文字。
function parseAppView(html, baseUrl) {
    const body = stripNoise(html);
    const baseHost = hostOf(baseUrl);
    const cards = [];
    const items = [];
    const tabs = [];
    const headings = [];
    const seen = {};

    // 导航区（nav / header / class 含 nav|tab|menu 的容器）里的链接做成顶部标签
    const navLinks = {};
    const zonePattern = /<(?:nav|header)\b[^>]*>[\s\S]*?<\/(?:nav|header)>|<(?:div|ul|section)\b[^>]*class=["'][^"']*(?:nav|tab|menu)[^"']*["'][^>]*>[\s\S]*?<\/(?:div|ul|section)>/gi;
    let zone = zonePattern.exec(body);
    while (zone !== null) {
        const linkPattern = /<a\s[^>]*href=["']([^"']+)["'][^>]*>/gi;
        let link = linkPattern.exec(zone[0]);
        while (link !== null) {
            const href = resolveUrl(baseUrl, link[1].trim());
            if (href && link[1].trim() !== '#') navLinks[href] = true;
            link = linkPattern.exec(zone[0]);
        }
        zone = zonePattern.exec(body);
    }

    const pattern = /<a\s([^>]*?)href=["']([^"']+)["']([^>]*)>([\s\S]*?)<\/a>/gi;
    let match = pattern.exec(body);
    while (match !== null) {
        const rawHref = match[2].trim();
        const inner = match[4];
        const href = resolveUrl(baseUrl, rawHref);
        const text = cleanText(decodeEntities(stripTags(inner)));
        const imgTag = firstMatch(/<img([^>]*)>/i, inner) || '';
        const src = imageSrc(imgTag);
        const alt = cleanText(decodeEntities(firstMatch(/<img[^>]*alt=["']([^"']*)["']/i, inner) || ''));
        const label = text || alt || pathOf(href);
        const valid = href !== ''
            && rawHref !== '#'
            && hostOf(href) === baseHost
            && rawHref.indexOf('javascript:') !== 0
            && rawHref.indexOf('mailto:') !== 0
            && !seen[href];

        if (valid && label) {
            seen[href] = true;
            const row = {
                href: href,
                title: label.length > 42 ? label.slice(0, 42) + '…' : label,
                image: src ? resolveUrl(baseUrl, src) : '',
                desc: text !== label && text.length > 12 ? text.slice(0, 60) : '',
            };
            if (navLinks[href] && label.length <= 10 && tabs.length < 8) {
                tabs.push({ href: href, text: label });
            } else if (src && cards.length < 30) {
                cards.push(row);
            } else if (items.length < 40) {
                items.push(row);
            }
        }
        match = pattern.exec(body);
    }

    const headPattern = /<h([1-4])[^>]*>([\s\S]*?)<\/h\1>/gi;
    let headMatch = headPattern.exec(body);
    while (headMatch !== null && headings.length < 12) {
        const text = cleanText(decodeEntities(stripTags(headMatch[2])));
        if (text.length > 1 && text.length < 40) headings.push(text);
        headMatch = headPattern.exec(body);
    }

    const heroMatch = /<img([^>]*)>/i.exec(body);
    const heroSrc = heroMatch ? imageSrc(heroMatch[1]) : '';
    return {
        cards: cards,
        items: items,
        tabs: tabs,
        headings: headings,
        hero: heroSrc ? resolveUrl(baseUrl, heroSrc) : '',
        linkCount: cards.length + items.length + tabs.length,
    };
}

// ---- 网页原貌：把 HTML 清洗成 rich-text 可渲染的富文本（零服务端） ----
// rich-text 是小程序自带的富文本组件，能还原标题、段落、图片、表格与内联样式，
// 不执行 JS、不加载外部 CSS，所以它需要配合下面的简易 CSS 内联使用。
const RICH_KEEP = [
    'div', 'span', 'p', 'a', 'img', 'br', 'hr', 'blockquote', 'pre', 'code',
    'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'ul', 'ol', 'li', 'table', 'thead',
    'tbody', 'tfoot', 'tr', 'td', 'th', 'strong', 'b', 'em', 'i', 'u', 's',
    'small', 'big', 'sub', 'sup', 'center', 'font', 'figure', 'figcaption',
    'section', 'article', 'header', 'footer', 'nav', 'main', 'aside', 'label',
    'dl', 'dt', 'dd', 'time', 'abbr', 'mark', 'del', 'ins', 'q', 'cite',
];
const RICH_REPLACE = {
    button: 'span', summary: 'div', details: 'div', form: 'div',
    fieldset: 'div', legend: 'span', picture: 'span', picture_source: 'span',
};
const RICH_STYLE_KEYS = [
    'color', 'background-color', 'background', 'font-size', 'font-weight', 'font-style',
    'text-align', 'text-decoration', 'line-height', 'letter-spacing',
    'padding', 'padding-left', 'padding-right', 'padding-top', 'padding-bottom',
    'margin', 'margin-left', 'margin-right', 'margin-top', 'margin-bottom',
    'border', 'border-radius', 'width', 'max-width', 'height', 'display',
    'flex-direction', 'justify-content', 'align-items', 'gap', 'overflow',
    'white-space', 'word-break', 'vertical-align', 'opacity',
];

function extractCssRules(cssText) {
    const rules = [];
    const text = String(cssText || '')
        .replace(/\/\*[\s\S]*?\*\//g, '')
        .replace(/@(media|supports|keyframes|font-face)[^{]*\{[\s\S]*?\}\s*\}/gi, '')
        .replace(/@import[^;]*;/gi, '');
    const pattern = /([^{}]+)\{([^{}]+)\}/g;
    let match = pattern.exec(text);
    while (match !== null && rules.length < 300) {
        const selector = match[1].trim().split(',')[0].trim().split(/\s+/).pop() || '';
        const decl = styleDeclarations(match[2]);
        if (selector && decl && !/[>~+:\[\]*]/.test(selector) && selector.indexOf('::') < 0) {
            const token = /^([a-z0-9-]*)((?:[.#][a-z0-9_-]+)*)$/i.exec(selector);
            if (token) {
                rules.push({ tag: (token[1] || '').toLowerCase(), token: token[2] || '', style: decl });
            }
        }
        match = pattern.exec(text);
    }
    return rules;
}

function styleDeclarations(raw) {
    const out = {};
    const parts = String(raw || '').split(';');
    for (let i = 0; i < parts.length; i += 1) {
        const colon = parts[i].indexOf(':');
        if (colon < 0) continue;
        const key = parts[i].slice(0, colon).trim().toLowerCase();
        let value = parts[i].slice(colon + 1).trim();
        if (RICH_STYLE_KEYS.indexOf(key) < 0) continue;
        if (/url\(|expression|javascript:|position|!important/i.test(value)) continue;
        if (/position\s*:\s*(fixed|absolute)/i.test(value)) continue;
        if (value.length > 60) continue;
        if (key === 'width' || key === 'height' || key === 'max-width') {
            if (/vh|vw|%/.test(value)) continue;
        }
        out[key] = value.replace(/!important/gi, '').trim();
    }
    const entries = [];
    for (const key in out) {
        if (Object.prototype.hasOwnProperty.call(out, key)) entries.push(key + ':' + out[key]);
    }
    return entries.join(';');
}

function inlineCss(html, cssText) {
    const rules = extractCssRules(cssText);
    if (rules.length === 0) return html;
    return String(html).replace(/<(\/?)([a-z][a-z0-9-]*)([^>]*)>/gi, (all, slash, rawTag, attrs) => {
        if (slash) return all;
        const tag = rawTag.toLowerCase();
        const classMatch = /\bclass=["']([^"']*)["']/i.exec(attrs);
        const idMatch = /\bid=["']([^"']*)["']/i.exec(attrs);
        const classes = classMatch ? classMatch[1].split(/\s+/) : [];
        let added = '';
        for (let i = 0; i < rules.length; i += 1) {
            const rule = rules[i];
            if (rule.tag && rule.tag !== tag) continue;
            if (rule.token) {
                const isClass = rule.token.charAt(0) === '.';
                const token = rule.token.slice(1);
                if (isClass ? classes.indexOf(token) < 0 : !idMatch || idMatch[1] !== token) continue;
            }
            added += (added && !/;$/.test(added) ? ';' : '') + rule.style + ';';
        }
        if (!added) return all;
        const styleMatch = /\sstyle=["']([^"']*)["']/i.exec(attrs);
        if (styleMatch) {
            const merged = styleDeclarations(styleMatch[1]) + ';' + added;
            return '<' + tag + attrs.replace(styleMatch[0], ' style="' + merged + '"') + '>';
        }
        return '<' + tag + attrs.replace(/\s*\/?$/, '') + ' style="' + added + '">';
    });
}

function toRichNodes(html, baseUrl, cssText) {
    const bodyMatch = /<body[^>]*>([\s\S]*?)<\/body>/i.exec(html);
    let body = bodyMatch ? bodyMatch[1] : stripNoise(html);
    body = body
        .replace(/<!doctype[^>]*>/gi, '')
        .replace(/<!--[\s\S]*?-->/g, '')
        .replace(/<(script|style|noscript|iframe|object|embed|canvas|template|video|audio|source|svg|math|head)\b[^>]*>[\s\S]*?<\/\1>/gi, '')
        .replace(/<(script|style|noscript|iframe|object|embed|canvas|template|input|link|meta|base)\b[^>]*\/?>/gi, '');
    body = cssText ? inlineCss(body, cssText) : body;

    const keepSet = {};
    for (let i = 0; i < RICH_KEEP.length; i += 1) keepSet[RICH_KEEP[i]] = true;
    const result = body.replace(/<(\/?)([a-z][a-z0-9-]*)([^>]*?)(\/?)>/gi, (all, slash, rawTag, attrs) => {
        const tag = rawTag.toLowerCase();
        const closing = slash === '/';
        const mapped = RICH_REPLACE[tag] || (keepSet[tag] ? tag : 'span');
        if (closing) return '</' + mapped + '>';
        const isVoid = tag === 'img' || tag === 'br' || tag === 'hr';
        return '<' + mapped + buildRichAttrs(attrs, tag, baseUrl) + (isVoid ? ' />' : '>');
    });
    return result.slice(0, 120000);
}

function buildRichAttrs(attrs, tag, baseUrl) {
    const styleMatch = /\sstyle=["']([^"']*)["']/i.exec(attrs);
    const style = styleMatch ? styleDeclarations(styleMatch[1]) : '';
    if (tag === 'img') {
        const src = imageSrc(attrs);
        const absolute = src ? (src.indexOf('data:') === 0 ? src : resolveUrl(baseUrl, src)) : '';
        if (!absolute) return '';
        return ' style="' + (style ? style + ';' : '') + 'max-width:100%;height:auto" src="' + absolute + '"';
    }
    let out = style ? ' style="' + style + '"' : '';
    if (tag === 'a') {
        const hrefMatch = /\bhref=["']([^"']*)["']/i.exec(attrs);
        const href = hrefMatch ? resolveUrl(baseUrl, hrefMatch[1]) : '';
        if (href) out += ' href="' + href + '"';
    }
    return out;
}

// 抓站点外链 CSS，用于给 rich-text 补回基础排版；失败不影响主流程。
function fetchStyles(html, baseUrl, timeout) {
    const links = [];
    const pattern = /<link[^>]+rel=["']stylesheet["'][^>]*>/gi;
    let match = pattern.exec(html);
    while (match !== null && links.length < 3) {
        const href = firstMatch(/\bhref=["']([^"']+)["']/i, match[0]);
        if (href) links.push(resolveUrl(baseUrl, href));
        match = pattern.exec(html);
    }
    if (links.length === 0 || typeof wx === 'undefined' || typeof wx.request !== 'function') {
        return Promise.resolve('');
    }
    const jobs = links.map(url => new Promise(resolve => {
        wx.request({
            url: url,
            method: 'GET',
            timeout: timeout || 6000,
            success: response => {
                resolve(response.statusCode < 400 && typeof response.data === 'string' ? response.data : '');
            },
            fail: () => resolve(''),
        });
    }));
    return Promise.all(jobs).then(parts => parts.join('\n').slice(0, 200000));
}

// 识别「JS 驱动的应用」：脚本标签多、静态正文极少。
// 这类站点抓取 HTML 只能得到界面文案，需要改走站点 API 或原生客户端。
function isScriptDriven(html) {
    const scripts = (String(html).match(/<script\b/gi) || []).length;
    const body = stripNoise(html);
    const text = cleanText(decodeEntities(stripTags(body)));
    return scripts >= 1 && text.length < 1200;
}

function decodeEntities(text) {
    return String(text)
        .replace(/&#x([0-9a-f]+);/gi, (all, hex) => safeFromCharCode(parseInt(hex, 16)))
        .replace(/&#(\d+);/g, (all, dec) => safeFromCharCode(Number(dec)))
        .replace(/&nbsp;/gi, ' ')
        .replace(/&amp;/gi, '&')
        .replace(/&lt;/gi, '<')
        .replace(/&gt;/gi, '>')
        .replace(/&quot;/gi, '"')
        .replace(/&#39;/g, "'")
        .replace(/&hellip;/gi, '…')
        .replace(/&mdash;/gi, '—')
        .replace(/&middot;/gi, '·');
}

function safeFromCharCode(code) {
    if (!Number.isFinite(code) || code < 0 || code > 0x10ffff) {
        return '';
    }
    try {
        return String.fromCharCode(code);
    } catch (error) {
        return '';
    }
}

module.exports = {
    fetchPage,
    parseHtml,
    parseAppView,
    isScriptDriven,
    toRichNodes,
    extractCssRules,
    styleDeclarations,
    inlineCss,
    fetchStyles,
    resolveUrl,
};
