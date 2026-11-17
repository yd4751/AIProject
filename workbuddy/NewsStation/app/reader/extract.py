"""把服务端代取到的 HTML 清洗成可安全嵌入站内的正文。

只用标准库，不引入 readability / bs4。流程：
  HTMLParser 建树 → 统计文本与链接密度 → 找出最紧凑的正文容器 →
  按标签/属性白名单重新序列化（所有文本重新转义）→
  正文里的链接改写成 /read?url=、图片改写成 /api/read/asset?url=。

最后一步很关键：外站 HTML 绝不能原样塞进本站页面（XSS / 外站直连 / 追踪像素）。
"""

from __future__ import annotations

import html as html_lib
import re
from html.parser import HTMLParser
from urllib.parse import quote, urljoin, urlsplit

from app import config

# 整段丢弃（含其内部文本）
SKIP = {
    "script", "style", "noscript", "svg", "iframe", "object", "embed",
    "form", "button", "select", "option", "textarea", "input", "template",
    "canvas", "video", "audio", "source", "track", "map",
    "nav", "footer", "header", "aside", "dialog",
}
# 输出时保留标签（其余标签只透传子节点）
KEEP = {
    "p", "br", "hr", "div", "span", "section",
    "h1", "h2", "h3", "h4", "h5", "h6",
    "blockquote", "pre", "code", "strong", "em", "b", "i", "u", "s",
    "ul", "ol", "li", "dl", "dt", "dd",
    "a", "img", "figure", "figcaption",
    "table", "thead", "tbody", "tfoot", "tr", "th", "td",
    "small", "sup", "sub", "mark", "time",
}
VOID = {"br", "hr", "img", "input", "meta", "link", "source", "wbr", "col", "area", "base"}
CONTAINERS = {"article", "main", "div", "section", "body", "td", "center", "html"}
AUTO_CLOSE = {"p", "li", "h1", "h2", "h3", "h4", "h5", "h6", "tr", "td", "th", "dt", "dd"}
BAD_HINT = re.compile(
    r"(nav|menu|sidebar|side-bar|comment|footer|header|advert|ads?-|promo|related|"
    r"recommend|share|social|subscribe|breadcrumb|pagination|toolbar|copyright|"
    r"qr-?code|download|app-?banner|top-?bar|site-?head)", re.I
)
NOISE_TEXT = re.compile(
    r"(扫一扫|扫码|下载.{0,8}(APP|客户端)|二维码|关注我们|点击(查看|下载)|"
    r"相关(阅读|报道|推荐)|热门(推荐|排行)|Advertisement|Sign up|newsletter)", re.I
)


class Node:
    __slots__ = ("tag", "attrs", "children", "parent", "text_len", "link_len",
                 "p_count", "depth", "plain")

    def __init__(self, tag: str, attrs=None, parent=None, depth: int = 0):
        self.tag = tag
        self.attrs = attrs or {}
        self.children: list[Node | str] = []
        self.parent = parent
        self.text_len = 0
        self.link_len = 0
        self.p_count = 0
        self.depth = depth
        self.plain = ""


class _Tree(HTMLParser):
    def __init__(self):
        super().__init__(convert_charrefs=True)
        self.root = Node("root")
        self.stack: list[Node] = [self.root]
        self.skip_level = 0
        self._skip_tags: list[str] = []
        self.title = ""
        self._in_title = False
        self.meta: dict[str, str] = {}
        self.canonical = ""
        self.h1 = ""

    def handle_starttag(self, tag, attrs):
        tag = tag.lower()
        a = {k.lower(): (v or "") for k, v in attrs if k}
        if tag == "title":
            self._in_title = True
        if tag == "meta":
            key = (a.get("property") or a.get("name") or "").lower()
            if key in ("description", "og:description", "og:title", "og:site_name",
                       "article:published_time"):
                self.meta[key] = a.get("content", "")
            charset = a.get("charset")
            if charset:
                self.meta["charset"] = charset
            elif "charset=" in a.get("http-equiv", "").lower() + a.get("content", "").lower():
                m = re.search(r"charset\s*=\s*([a-z0-9_\-]+)", a.get("content", ""), re.I)
                if m:
                    self.meta["charset"] = m.group(1)
        if tag == "link" and "canonical" in a.get("rel", "").lower():
            self.canonical = a.get("href", "")

        if self.skip_level or tag in SKIP:
            if tag in SKIP and tag not in VOID:
                self.skip_level += 1
                self._skip_tags.append(tag)
            return

        if self.stack[-1].tag in AUTO_CLOSE and (tag in AUTO_CLOSE or tag in CONTAINERS):
            self.stack.pop()
        node = Node(tag, a, self.stack[-1], self.stack[-1].depth + 1)
        self.stack[-1].children.append(node)
        if tag not in VOID:
            self.stack.append(node)

    def handle_startendtag(self, tag, attrs):
        self.handle_starttag(tag, attrs)
        if tag.lower() not in VOID and not self.skip_level:
            self.stack.pop()

    def handle_endtag(self, tag):
        tag = tag.lower()
        if tag == "title":
            self._in_title = False
        if self.skip_level:
            if self._skip_tags and self._skip_tags[-1] == tag:
                self._skip_tags.pop()
                self.skip_level -= 1
            return
        for i in range(len(self.stack) - 1, 0, -1):
            if self.stack[i].tag == tag:
                del self.stack[i:]
                break

    def handle_data(self, data):
        if self._in_title:
            self.title += data
            return
        if self.skip_level or not data:
            return
        self.stack[-1].children.append(data)
        if not self.h1 and len(data.strip()) > 4 and self.stack[-1].tag == "h1":
            self.h1 = data.strip()


def _measure(node: Node) -> None:
    """后序统计：纯文本长度、链接文本长度、合格段落数、噪声判断用纯文本。"""
    text = 0
    link = 0
    pc = 0
    parts: list[str] = []
    for child in node.children:
        if isinstance(child, str):
            text += len(child.strip())
            parts.append(child.strip())
        else:
            _measure(child)
            text += child.text_len
            link += child.link_len
            pc += child.p_count
            parts.append(child.plain)
    node.text_len = text
    node.link_len = link + (text if node.tag == "a" else 0)
    if node.tag == "p" and text >= 20:
        pc += 1
    node.p_count = pc
    node.plain = " ".join(parts)[:300]


def _walk(node: Node):
    for child in node.children:
        if isinstance(child, Node):
            yield child
            yield from _walk(child)


def _score(n: Node) -> float:
    """正文倾向：段落多、层级深、链接少；大而无当的 body 会被压下去。"""
    hint = n.attrs.get("class", "") + " " + n.attrs.get("id", "")
    return (n.p_count * 80 + n.depth * 12 - n.link_len * 1.2
            - n.text_len * 0.015 - (400 if BAD_HINT.search(hint) else 0))


def _pick_body(root: Node) -> Node:
    """选正文容器。

    优先「段落父节点投票」：绝大多数新闻正文的段落共享同一个直接父节点，
    这比按整棵子树打分更准，能避开页头的下载 APP / 导航图区块与相关新闻列表。
    """
    paragraphs = [
        n for n in _walk(root)
        if n.tag == "p" and n.text_len >= 20
        and not BAD_HINT.search(n.attrs.get("class", "") + " " + n.attrs.get("id", ""))
    ]
    if len(paragraphs) >= 2:
        votes: dict[int, tuple[int, Node]] = {}
        for p in paragraphs:
            parent = p.parent
            if parent is None or parent.link_len > parent.text_len * 0.65:
                continue
            key = id(parent)
            count, _ = votes.get(key, (0, parent))
            votes[key] = (count + 1, parent)
        if votes:
            best_count, best = max(votes.values(), key=lambda x: (x[0], -x[1].depth))
            if best_count >= max(2, int(len(paragraphs) * 0.55)):
                return best

    candidates = [n for n in _walk(root) if n.tag in CONTAINERS and n.text_len > 0]
    if not candidates:
        return root
    loose = [n for n in candidates if n.link_len < n.text_len * 0.65]
    pool = loose or candidates
    max_p = max((n.p_count for n in pool), default=0)
    if max_p >= 2:
        floor = max(2, int(max_p * 0.5))
        tight = [n for n in pool if n.p_count >= floor]
        if tight:
            return max(tight, key=_score)
    max_text = max((n.text_len for n in pool), default=0)
    if max_text >= 80:
        tight = [n for n in pool if n.text_len >= max_text * 0.4]
        return max(tight, key=_score)
    return max(pool, key=_score)


def _clean_href(raw: str, base: str) -> str:
    if not raw:
        return ""
    u = urljoin(base, raw.strip())
    p = urlsplit(u)
    if p.scheme.lower() not in ("http", "https"):
        return ""
    return "/read?url=" + quote(u, safe="")


def _clean_src(raw: str, base: str, settings: config.Settings) -> str:
    if not raw or raw.strip().lower().startswith(("data:", "javascript:", "blob:")):
        return ""
    u = urljoin(base, raw.strip())
    p = urlsplit(u)
    if p.scheme.lower() not in ("http", "https"):
        return ""
    if settings.reader.proxy_images:
        return "/api/read/asset?url=" + quote(u, safe="")
    return u


def _serialize(node: Node, base: str, settings: config.Settings, out: list[str],
               counters: list[int], depth: int = 0) -> None:
    if depth > 40:                       # 防御畸形深层嵌套
        return
    for child in node.children:
        if isinstance(child, str):
            t = child.strip()
            if t:
                out.append(html_lib.escape(t, quote=False))
                counters[0] += len(t)
            continue
        tag = child.tag
        if tag not in KEEP:
            _serialize(child, base, settings, out, counters, depth + 1)
            continue
        if tag in ("br", "hr"):
            out.append(f"<{tag}>")
            continue
        if tag == "img":
            src = _clean_src(child.attrs.get("src", ""), base, settings)
            if not src:
                continue
            alt = child.attrs.get("alt", "")[:200]
            out.append(f'<img src="{html_lib.escape(src, quote=True)}"'
                       f' alt="{html_lib.escape(alt, quote=True)}" loading="lazy">')
            continue
        if tag == "a":
            href = _clean_href(child.attrs.get("href", ""), base)
            if href:
                out.append(f'<a href="{html_lib.escape(href, quote=True)}">')
                _serialize(child, base, settings, out, counters, depth + 1)
                out.append("</a>")
            else:
                _serialize(child, base, settings, out, counters, depth + 1)
            continue
        # 明显的功能区块（下载 APP / 二维码 / 相关推荐）整块丢掉
        hint = child.attrs.get("class", "") + " " + child.attrs.get("id", "")
        if tag in ("div", "span", "section", "li") and (
            (tag == "li" and NOISE_TEXT.search(child.plain) and child.text_len < 150)
            or (tag != "li" and BAD_HINT.search(hint) and child.text_len < 400)
            or (tag != "li" and NOISE_TEXT.search(child.plain) and child.text_len < 150)
        ):
            continue
        out.append(f"<{tag}>")
        _serialize(child, base, settings, out, counters, depth + 1)
        out.append(f"</{tag}>")


def decode_html(content: bytes, content_type: str = "") -> str:
    """编码识别：meta charset > header charset > utf-8 严格 > gb18030。"""
    head = content[:4096]
    declared = ""
    m = re.search(rb"charset\s*=\s*[\"']?\s*([a-zA-Z0-9_\-]+)", head, re.I)
    if m:
        declared = m.group(1).decode("ascii", "replace")
    if not declared and content_type:
        m2 = re.search(r"charset\s*=\s*([a-zA-Z0-9_\-]+)", content_type, re.I)
        declared = m2.group(1) if m2 else ""
    if declared:
        try:
            return content.decode(declared, errors="replace")
        except (LookupError, UnicodeError):
            pass
    try:
        return content.decode("utf-8")
    except UnicodeDecodeError:
        pass
    try:
        return content.decode("gb18030")
    except UnicodeDecodeError:
        return content.decode("utf-8", errors="replace")


def extract_page(html: str | bytes, base_url: str, settings: config.Settings) -> dict:
    """返回 {title, site, description, content_html, text_len, status, note}。"""
    if isinstance(html, bytes):
        html = decode_html(html, "")
    tree = _Tree()
    try:
        tree.feed(html)
        tree.close()
    except Exception:                     # 畸形 HTML 也尽量给出部分内容
        pass
    _measure(tree.root)
    body = _pick_body(tree.root)
    out: list[str] = []
    counters = [0]
    _serialize(body, base_url, settings, out, counters)
    content_html = "".join(out)
    # 合并连续空容器，减少无意义标签
    content_html = re.sub(r"(?:<div></div>|<p></p>|<span></span>)+", "", content_html)
    text_len = counters[0]

    host = urlsplit(base_url).hostname or ""
    title = (tree.meta.get("og:title") or tree.h1 or tree.title or "").strip()
    if not title:
        title = host
    site = (tree.meta.get("og:site_name") or host).strip()
    desc = (tree.meta.get("og:description") or tree.meta.get("description") or "").strip()[:400]
    minimum = settings.reader.min_text_chars
    status = "ok" if text_len >= minimum else "thin"
    note = "" if status == "ok" else "原站正文过少，可能是登录墙、反爬跳转或依赖 JavaScript 渲染的页面。"
    return {
        "title": title[:300], "site": site[:120], "description": desc,
        "content_html": content_html, "text_len": text_len,
        "status": status, "note": note,
    }
