"""M6 社交媒体热榜：插件源 + 解析器注册表。"""

from app.ingest.social.base import (
    SocialSource,
    all_sources,
    clear,
    get,
    load_social_yaml,
    register,
)
from app.ingest.social.parsers import PARSERS

__all__ = [
    "PARSERS",
    "SocialSource",
    "all_sources",
    "clear",
    "get",
    "load_social_yaml",
    "register",
]
