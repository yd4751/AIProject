"""M7/M10 关键字搜索：按引擎组织，站内检索 + 外部结果由服务端代取。"""

from app.search import engines, local, providers
from app.search.service import search, status

__all__ = ["search", "status", "local", "providers", "engines"]
