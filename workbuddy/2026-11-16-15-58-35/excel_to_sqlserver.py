#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
Excel 表设计 -> SQL Server 建表脚本

读取「ROGUE_数值表_分表.xlsx」这类导表工作簿（一表一 sheet、前四行为表头），
生成 SQL Server 的 CREATE TABLE + 扩展属性注释脚本。

工作簿约定：
  sheet 名：表中文名_表英文名（如 道具配置表_CfgItem）
  第 1 行：字段中文名
  第 2 行：字段名
  第 3 行：数据类型
  第 4 行：其它描述（必填：Y/N；主键；说明：...；来源：...）

用法：
  python excel_to_sqlserver.py
  python excel_to_sqlserver.py 别的表.xlsx -o out.sql
  python excel_to_sqlserver.py --schema dbo --drop-existing

依赖：openpyxl（pip install openpyxl）
"""

from __future__ import unicode_literals

import argparse
import datetime
import os
import re
import sys

try:
    from openpyxl import load_workbook
except ImportError:
    sys.exit("缺少 openpyxl，请先执行：pip install openpyxl")

# 辅助页不参与建表
AUX_SHEETS = {"目录_Index", "号段分配_IdSegment"}

# Excel 里的逻辑类型 -> SQL Server 物理类型
# JSON / TEXT 落为 NVARCHAR(MAX)；VARCHAR 统一升级为 NVARCHAR 以容纳中文备注
TYPE_MAP = {
    "BIGINT": "BIGINT",
    "INT": "INT",
    "TINYINT": "TINYINT",
    "SMALLINT": "SMALLINT",
    "DATETIME": "DATETIME2(0)",
    "TEXT": "NVARCHAR(MAX)",
    "JSON": "NVARCHAR(MAX)",
}

# 层名（用于输出分隔注释）
LAYER_LABELS = (
    ("Dict", "字典层 Dict"),
    ("Cfg", "配置层 Cfg"),
    ("Player", "运行时层 Player"),
    ("Log", "记录层 Log"),
)

HEADER = """-- ============================================================
-- 由 Excel 表设计自动生成 SQL Server 建表脚本
-- 源文件：{src}
-- 生成时间：{now}
-- 数据表：{tables} 张；字段：{fields} 个
-- 表名取 sheet 名中的英文名；中文名写为表注释；字段名取第 2 行，中文名写为列注释
-- 类型映射：JSON/TEXT -> NVARCHAR(MAX)，VARCHAR(n) -> NVARCHAR(n)，DATETIME -> DATETIME2(0)
-- ============================================================

SET NOCOUNT ON;
GO
"""


def sql_str(value):
    """SQL Unicode 字符串字面量：加 N 前缀并转义单引号"""
    return "N'" + str(value).replace("'", "''") + "'"


def ident(value):
    """SQL Server 标识符：方括号包裹并转义右方括号"""
    return "[" + str(value).replace("]", "]]") + "]"


def split_sheet_name(name):
    """'道具配置表_CfgItem' -> ('道具配置表', 'CfgItem')"""
    if "_" not in name:
        raise ValueError("sheet 名缺少 '_' 分隔符，无法拆分中文名/英文名：%s" % name)
    cn, en = name.rsplit("_", 1)
    if not cn or not en:
        raise ValueError("sheet 名中英文名不完整：%s" % name)
    if not re.match(r"^[A-Za-z][A-Za-z0-9_]*$", en):
        raise ValueError("英文表名不合法：%s" % en)
    return cn, en


def map_type(raw):
    """逻辑类型 -> SQL Server 类型"""
    t = str(raw or "").strip().upper()
    if t in TYPE_MAP:
        return TYPE_MAP[t]
    m = re.match(r"^VARCHAR\s*\(\s*(\d+|MAX)\s*\)$", t)
    if m:
        return "NVARCHAR(%s)" % m.group(1)
    m = re.match(r"^DECIMAL\s*\(\s*\d+\s*,\s*\d+\s*\)$", t)
    if m:
        return t
    # 未知类型：原样输出并在列上标注，便于后续补映射
    return None


def parse_desc(desc):
    """解析第 4 行描述 -> (必填, 是否主键, 说明, 来源)"""
    text = str(desc or "").strip()
    req_m = re.search(r"必填\s*[:：]\s*([YN])", text, re.I)
    required = req_m.group(1).upper() if req_m else "N"
    is_pk = "主键" in text
    note_m = re.search(r"说明\s*[:：]\s*(.*?)(?=\s*；\s*来源\s*[:：]|$)", text)
    note = note_m.group(1).strip() if note_m else ""
    src_m = re.search(r"来源\s*[:：]\s*(.*)$", text)
    source = src_m.group(1).strip() if src_m else ""
    return required, is_pk, note, source


def column_comment(cn_name, note, source):
    """列注释 = 中文名 + 说明 + 来源（来源属于变更记录，保留便于追溯）"""
    parts = [str(cn_name or "").strip()]
    if note:
        parts.append(note)
    if source:
        parts.append("来源：" + source)
    return "；".join(p for p in parts if p)


def layer_of(en):
    for prefix, label in LAYER_LABELS:
        if en.startswith(prefix):
            return label
    return "其它"


def read_tables(path):
    """读取工作簿，返回表定义列表"""
    wb = load_workbook(path, data_only=True, read_only=True)
    try:
        tables = []
        for name in wb.sheetnames:
            if name in AUX_SHEETS:
                continue
            ws = wb[name]
            cn_name, en_name = split_sheet_name(name)
            rows = list(ws.iter_rows(min_row=1, max_row=4, values_only=True))
            if len(rows) < 4:
                raise ValueError("%s：表头不足 4 行" % name)

            headers = [c for c in rows[0] if c is not None]
            if not headers:
                raise ValueError("%s：第 1 行没有字段中文名" % name)
            width = len(headers)
            if any(len(r) < width for r in rows):
                raise ValueError("%s：四行表头列数不一致" % name)

            columns = []
            seen = set()
            for i in range(width):
                cn_field = str(rows[0][i] or "").strip()
                field = str(rows[1][i] or "").strip()
                raw_type = str(rows[2][i] or "").strip()
                required, is_pk, note, source = parse_desc(rows[3][i])

                if not field:
                    raise ValueError("%s 第 %d 列缺少字段名" % (name, i + 1))
                if field in seen:
                    raise ValueError("%s：字段名重复 %s" % (name, field))
                seen.add(field)
                if not raw_type:
                    raise ValueError("%s.%s 缺少数据类型" % (name, field))

                sql_type = map_type(raw_type)
                columns.append({
                    "field": field,
                    "cn": cn_field,
                    "raw_type": raw_type,
                    "sql_type": sql_type,
                    "required": required,
                    "is_pk": is_pk,
                    "comment": column_comment(cn_field, note, source),
                    "warn": sql_type is None,
                })

            pks = [c["field"] for c in columns if c["is_pk"]]
            if not pks:
                raise ValueError("%s：没有标记主键的字段" % name)
            if len(pks) > 1:
                raise ValueError("%s：存在多个主键字段 %s，当前脚本只支持单列主键" % (name, pks))

            tables.append({
                "sheet": name,
                "cn": cn_name,
                "en": en_name,
                "layer": layer_of(en_name),
                "columns": columns,
                "pk": pks[0],
            })
        return tables
    finally:
        wb.close()


def add_comment_sql(lines, value, schema, level1_name, level2_name=None):
    parts = [
        "EXEC sys.sp_addextendedproperty @name = N'MS_Description',",
        "     @value = %s," % sql_str(value),
        "     @level0type = N'SCHEMA', @level0name = %s," % sql_str(schema),
        "     @level1type = N'TABLE',  @level1name = %s" % sql_str(level1_name),
    ]
    if level2_name is not None:
        parts[-1] += ","
        parts.append("     @level2type = N'COLUMN', @level2name = %s" % sql_str(level2_name))
    lines.append("\n".join(parts) + ";")
    lines.append("GO")


def generate_sql(tables, src_path, schema, drop_existing):
    lines = []
    warns = []
    field_count = sum(len(t["columns"]) for t in tables)
    lines.append(HEADER.format(
        src=os.path.basename(src_path),
        now=datetime.datetime.now().strftime("%Y-%m-%d %H:%M:%S"),
        tables=len(tables),
        fields=field_count,
    ))

    current_layer = None
    for t in tables:
        if t["layer"] != current_layer:
            current_layer = t["layer"]
            lines.append("")
            lines.append("-- " + "=" * 58)
            lines.append("-- " + current_layer)
            lines.append("-- " + "=" * 58)

        en = t["en"]
        full = "%s.%s" % (schema, en)
        lines.append("")
        lines.append("-- %s / %s（%d 字段）" % (t["cn"], en, len(t["columns"])))

        if drop_existing:
            lines.append("DROP TABLE IF EXISTS %s;" % full)
            lines.append("GO")

        defs = []
        for c in t["columns"]:
            sql_type = c["sql_type"] or "NVARCHAR(255)"
            if c["warn"]:
                warns.append("%s.%s 未知类型 %s -> NVARCHAR(255)，请补 TYPE_MAP" %
                             (en, c["field"], c["raw_type"]))
                sql_type = "-- [未识别类型] " + sql_type
            defs.append("    %s %s %s" %
                        (ident(c["field"]), sql_type,
                         "NOT NULL" if c["required"] == "Y" else "NULL"))
        defs.append("    CONSTRAINT %s PRIMARY KEY CLUSTERED (%s)" %
                    (ident("PK_" + en), ident(t["pk"])))

        lines.append("CREATE TABLE %s (" % full)
        lines.append(",\n".join(defs))
        lines.append(");")
        lines.append("GO")
        lines.append("")

        add_comment_sql(lines, t["cn"], schema, en)
        lines.append("")
        for c in t["columns"]:
            add_comment_sql(lines, c["comment"], schema, en, c["field"])
        lines.append("")

    return "\n".join(lines).rstrip() + "\n", warns


def main():
    base = os.path.dirname(os.path.abspath(__file__))
    ap = argparse.ArgumentParser(description="Excel 表设计 -> SQL Server 建表脚本")
    ap.add_argument("input", nargs="?", default=os.path.join(base, "ROGUE_数值表_分表.xlsx"),
                    help="输入工作簿路径（默认：脚本同目录下的 ROGUE_数值表_分表.xlsx）")
    ap.add_argument("-o", "--output", default=None,
                    help="输出 .sql 路径（默认：输入文件同目录 <名称>_SQLServer.sql）")
    ap.add_argument("--schema", default="dbo", help="目标 schema（默认 dbo）")
    ap.add_argument("--drop-existing", action="store_true",
                    help="每张表前追加 DROP TABLE IF EXISTS，便于重复执行（会丢数据，慎用）")
    args = ap.parse_args()

    src = os.path.abspath(args.input)
    if not os.path.isfile(src):
        sys.exit("找不到输入文件：%s" % src)
    out = args.output or os.path.join(os.path.dirname(src),
                                      os.path.splitext(os.path.basename(src))[0] + "_SQLServer.sql")
    out = os.path.abspath(out)

    try:
        tables = read_tables(src)
    except ValueError as e:
        sys.exit("读取工作簿失败：%s" % e)
    if not tables:
        sys.exit("工作簿中没有可建表的数据 sheet")

    sql, warns = generate_sql(tables, src, args.schema, args.drop_existing)
    with open(out, "w", encoding="utf-8-sig", newline="\n") as f:
        f.write(sql)

    print("OK  表=%d  字段=%d  schema=%s" %
          (len(tables), sum(len(t["columns"]) for t in tables), args.schema))
    print("  ->", out)
    for w in warns:
        print("  WARN", w)


if __name__ == "__main__":
    main()
