"""URL 安全校验：服务端代取网页前必须过这一关。

这是 B/S 结构里最容易被忽略的风险：浏览器把任意 URL 交给服务端，
服务端若不加校验就成了可打内网的开放代理（SSRF）。这里做四层限制：
  1) 协议 / 端口 / userinfo；
  2) 域名白名单（可选）；
  3) DNS 解析结果必须是公网 IP；
  4) 每一跳重定向都重新校验（在 fetcher 里调用）。
"""

from __future__ import annotations

import ipaddress
import socket
from urllib.parse import urlsplit, urlunsplit

from app import config


class UnsafeURL(ValueError):
    """URL 不合法或不允许代取。"""


def _domain_allowed(host: str, patterns: list[str]) -> bool:
    host = host.lower().rstrip(".")
    for raw in patterns:
        p = str(raw).lower().strip().lstrip("*.").rstrip(".")
        if p and (host == p or host.endswith("." + p)):
            return True
    return False


def _check_ip(addr: str) -> None:
    """拒绝任何非公网地址：私有 / 回环 / 链路本地（含云元数据 169.254.169.254）/ 保留 / 多播。"""
    try:
        ip = ipaddress.ip_address(addr.split("%")[0])
    except ValueError as exc:
        raise UnsafeURL("不是合法 IP") from exc
    if not ip.is_global or ip.is_multicast:
        raise UnsafeURL(f"目标地址不是公网 IP：{addr}")


def validate_url(raw: str, settings: config.Settings | None = None,
                 resolve: bool = True) -> str:
    """校验并返回规范化 URL；不合法抛 UnsafeURL。

    resolve=False 供单元测试用（MockTransport 不需要真实 DNS）。
    """
    settings = settings or config.Settings()
    if not raw or not isinstance(raw, str):
        raise UnsafeURL("缺少 URL")
    raw = raw.strip()
    if len(raw) > 2048:
        raise UnsafeURL("URL 过长")

    p = urlsplit(raw)
    if p.scheme.lower() not in ("http", "https"):
        raise UnsafeURL("只允许 http / https")
    if p.username or p.password:
        raise UnsafeURL("URL 不允许带账号密码")
    host = p.hostname
    if not host:
        raise UnsafeURL("缺少主机名")
    host = host.lower().rstrip(".")
    if host in ("localhost", "localhost.localdomain") or host.endswith((".localhost", ".local", ".internal")):
        raise UnsafeURL("不允许访问本机或内网域名")

    try:
        port = p.port or (443 if p.scheme.lower() == "https" else 80)
    except ValueError as exc:
        raise UnsafeURL("端口不合法") from exc
    if port not in (80, 443):
        raise UnsafeURL("只允许 80 / 443 端口")

    allowed = getattr(settings.reader, "allowed_domains", []) or []
    if allowed and not _domain_allowed(host, list(allowed)):
        raise UnsafeURL("域名不在 reader.allowed_domains 白名单内")

    # 无论是否走 DNS，只要 host 是 IP 字面量就立即校验（防止 resolve=False 放行内网地址）
    try:
        ipaddress.ip_address(host)
    except ValueError:
        pass  # 域名，交给下面的 DNS 校验
    else:
        _check_ip(host)

    if resolve:
        try:
            infos = socket.getaddrinfo(host, port, proto=socket.IPPROTO_TCP)
        except OSError as exc:
            raise UnsafeURL("域名无法解析") from exc
        if not infos:
            raise UnsafeURL("域名无法解析")
        for info in infos:
            _check_ip(info[4][0])

    # 去掉 fragment；保留 path / query（查询参数常含文章 ID，不能丢）
    return urlunsplit((p.scheme.lower(), p.netloc, p.path or "/", p.query, ""))


def same_origin_host(a: str, b: str) -> bool:
    return (urlsplit(a).hostname or "").lower() == (urlsplit(b).hostname or "").lower()
