# -*- coding: utf-8 -*-
"""Roguelike 数值表设计：单一数据源 -> 规范文档 (Markdown) + 导表工作簿 (XLSX)。

原始素材：C:/Users/Administrator/Desktop/ROUGE表设计.txt
重新运行本脚本即可同步更新两份交付物。

XLSX 输出优先使用 openpyxl（带样式）；未安装时自动降级为标准库实现（数据一致，无样式）。
"""
import os
import re

BASE = os.path.dirname(os.path.abspath(__file__))
MD_PATH = os.path.join(BASE, "ROGUE_数值表设计_规整版.md")
XLSX_PATH = os.path.join(BASE, "ROGUE_数值表_分表.xlsx")

LAYERS = [
    ("A", "枚举字典层", "Dict", "策划可维护的码值字典；只增不改，供配置表外键引用"),
    ("B", "静态配置层", "Cfg", "随版本发布的只读数值；进包 / 热更，不随玩家变化"),
    ("C", "运行时层", "Player", "玩家存档数据；随游戏进程读写，禁止被配置表引用"),
    ("D", "记录层", "Log", "结算 / 过期流水；只追加，用于追溯与对账"),
]

# ---------- 术语对照 ----------
GLOSSARY = [
    ("抽取规则", "Draw Rule / DrawRule", "保底、洗牌袋、连续上限等抽取策略；独立于权重表，按 DrawRuleId 复用", "新增"),
    ("随机种子", "Run Seed", "每局开局生成并存档，用于复现该局的关卡 / 词条 / 掉落；按命名空间分随机流", "新增"),
    ("配置版本", "Config Version", "一次导表产物的唯一版本号，随配置变更递增；仅结算记录表快照，用于复盘", "新增"),
    ("存档结构版本", "Schema Version", "玩家存档表的结构版本，读档时按迁移注册表升级", "新增"),
    ("洗牌袋", "Shuffle Bag", "一个周期内保证每个条目出现固定次数的抽取方式，抑制极端连续", "新增"),
    ("伪随机分布", "PRD", "连续失败逐步提升概率的分布，用于压制长串不中（Dota 2 同类做法）", "新增"),
    ("肉鸽 / Roguelike", "Roguelike / Roguelite", "品类正名。原文件名 ROUGE 是 NLP 评测指标 / 法语「红色」，非游戏品类写法", "建议统一为 Roguelike"),
    ("标识", "Id / XxxId", "所有表主键统一命名；字典 / 配置层用号段 Id（CfgGlobal 除外），禁用 ID、编号、key 混用", "统一"),
    ("名称", "Name / Id", "仅一列 Name 作内部备注；程序键统一用号段 Id，客户端按 Id 本地化，服务端不存多语言", "精简"),
    ("数值", "按语义定名", "全库不使用小数类型；整数、字符串、枚举一律逐词首字母大写，不加类型前缀、不用裸列名", "统一"),
    ("类型", "XxxType", "枚举类型加语义限定词，不用裸 type 作列名", "统一"),
    ("地形类型", "Terrain Type", "陆地 land / 水域 water / 特殊地形 special", "直译"),
    ("基础属性", "Base Stat / Core Stat", "血量、护甲、防御、攻击、穿透、暴击率、攻击间隔、移动速度", "直译"),
    ("武器属性", "Weapon Stat", "弹匣、连发、换弹时间、弹道", "直译"),
    ("攻击模式", "Attack Pattern", "远程 ranged / 扇形 cone / 范围 aoe / 穿透 pierce", "直译"),
    ("特殊效果", "Status Effect / Debuff", "持续伤害 DoT、减速 Slow、攻速降低、命中降低", "直译"),
    ("英雄", "Hero", "玩家可选角色", "直译"),
    ("通行器", "Vehicle（载具）", "已确认：载具；带槽位与地形适配，英文定名 Vehicle，不再用 Mount / Transporter", "已确认"),
    ("怪兽", "Monster / Enemy", "建议统一用 Monster 或 Enemy 之一，不要两套", "直译"),
    ("技能", "Skill / Ability", "局内主动或被动能力", "直译"),
    ("词条", "Affix / Modifier / Perk", "关卡 / 装备上的属性修饰；暗黑系用 Affix，局内成长用 Perk", "建议 Affix"),
    ("加成道具", "Boost Item", "已并入道具配置表 CfgItem，按 UsageType=duration 区分；不再单独成表", "已归并"),
    ("加成", "Buff", "玩家身上生效中的增益实例", "直译"),
    ("联盟", "Alliance / Guild", "原表范围枚举 1；若无公会系统可保留枚举但不启用", "待确认"),
    ("资源", "Resource / Currency", "账号级 Coin / Stamina / Exp；关卡内 Mana（蓝量），见第 8 节资源边界表", "已确认"),
    ("道具", "Item", "可获得 / 可使用的实体本身即资源聚合包；一次性用 RewardJson 发多资源，持续性用加成字段", "已确认"),
    ("使用方式", "Usage Type", "道具一级分类：once 一次性 / duration 持续性；枚举码值保持小写", "已确认"),
    ("掉落配置", "Drop Table / Loot Table", "道具 id + 数量区间 + 权重", "直译"),
    ("玩家配置", "Loadout / Preset", "通行器 / 武器 / 技能的一套搭配方案", "建议 Loadout"),
    ("玩家实例", "Player Item Instance", "玩家实际拥有的、带等级与强化的实体", "直译"),
    ("关卡", "Stage / Level", "Level 易与等级混淆，建议用 Stage", "建议 Stage"),
    ("弹匣", "Magazine Size", "容量，单位：发", "直译"),
    ("连发", "Burst Count / Fire Mode", "连发数或射击模式", "待确认"),
    ("换弹时间", "Reload Time", "单位 ms", "直译"),
    ("弹道", "Projectile / Ballistics", "子弹类型、速度、轨迹参数", "直译"),
    ("穿透", "Penetration", "既作基础属性又作攻击模式，需区分：属性用 armor_pen，模式用 pierce", "拆分"),
    ("暴击率", "Crit Rate", "整数存储，缩放比例由服务端与客户端约定，表内不固化", "统一"),
    ("攻击间隔", "Attack Interval", "单位 ms；与攻击速度互为倒数，只保留一个", "统一"),
    ("持续伤害", "Damage over Time (DoT)", "按 tick 结算，需 tick 间隔与叠层规则", "直译"),
    ("减速", "Movement Slow", "百分比或固定值", "直译"),
    ("降低目标攻速", "Attack Speed Reduction", "Debuff", "直译"),
    ("降低目标命中", "Accuracy / Hit Rate Reduction", "Debuff", "直译"),
    ("减伤率上限", "Damage Reduction Cap", "全局常量，防止免伤", "直译"),
    ("单次伤害下限", "Min Damage per Hit", "全局常量，保底伤害", "直译"),
    ("同屏怪物上限", "Max Concurrent Enemies", "性能与数值双重约束", "直译"),
    ("体力恢复速度", "Stamina Regen Rate", "原稿 10 点/h；仅账号级体力", "直译"),
    ("体力（玩家）", "Stamina", "已确认：账号级纯数值，只用于进行关卡（门票），离线恢复，不参与战斗计算", "已确认"),
    ("技能体力 / 蓝量", "Mana / Skill Energy", "已确认：关卡内部使用的技能能量消耗，与账号级体力是两套资源", "更名"),
    ("技能体力上限", "Mana Cap", "原稿全局项，实为局内能量上限 nManaCap，不再与体力共用词根", "更名"),
    ("关卡内状态", "Run / Run State", "一次进入关卡的生命周期，承载能量、已选词条与局内 buff", "新增"),
    ("离线奖励", "Offline / Idle Reward", "按最大通关关卡折算，需上限小时数", "直译"),
    ("游戏同步间隔", "Sync Interval", "原稿 5s", "直译"),
    ("号段 Id", "Segmented Id", "Id = 表号 × 100000 + 序号；Id 自带表号，可反查所属配置表，取代字符串 Code", "新增"),
    ("代码 / Code", "—", "v4 的字符串程序键；本版删除，统一由号段 Id 承担，不再设 Code 列", "删除"),
]

# ---------- 问题诊断 ----------
DIAGNOSIS = [
    ("P0", "全局", "配置表与运行时表混在同一份清单", "按 tbDict / tbCfg / tbPlayer / tbLog 四层拆库拆表，配置表禁止反向引用玩家表"),
    ("P0", "属性等级表 / 升级模型", "单表把消耗与加成混在 entry_type 一列，一次升级的多资源消耗、多属性提升、新增属性与事务边界都无法表达", "拆成 cfg_level_up 主表 + cfg_level_up_cost / cfg_level_up_effect 两张明细；一次升级一个事务，允许跨级由服务端合并处理"),
    ("P0", "道具配置表", "只有「经验值/游戏币/体力值」三个值列，没有主键、名称、类型，实际是把值当字段", "改为一行一个道具的竖表；道具即资源聚合包，用 reward_json 发多资源，值列不再做列名"),
    ("P0", "加成表", "只有加成标识、玩家、起止时间，缺少加成内容，无法计算效果", "补加成道具外键与 stat_modifiers_json，加成内容从 CfgItem 反查并快照到实例"),
    ("P0", "玩家实例表", "「武器/通行器标识/强化」一列塞了三件事；槽位1-4 是横表", "拆为 entity_type / ref_id / enhance_level，槽位拆子表 player_instance_slot"),
    ("P0", "全局 / 技能配置", "局内能量（蓝量）没有承载表：既不在玩家主档，也不在关卡配置", "新增 tbPlayerRunState，开局按 nManaCap 初始化、结算清零；账号级体力不得被局内逻辑读写"),
    ("P1", "基础/武器/攻击模式/特殊效果 4 表", "结构完全相同（标识/名称/数值），与属性类型表、词条配置表职责重叠", "合并为 cfg_stat_def；属性分组与效果类型降为枚举列，攻击模式保留字典表"),
    ("P1", "攻击模式表", "远程/扇形/范围/穿透是枚举，不是「数值」", "改 dict_attack_pattern，用 shape_params_json 承载射程 / 夹角 / 半径 / 穿透数"),
    ("P1", "关卡配置表", "怪物配置、词条配置未写格式，掉落配置单独写 json，三列格式不一致", "统一结构化为 cfg_stage_monster / cfg_stage_affix / cfg_drop_entry 三张明细表"),
    ("P1", "全局配置表", "9 个「字段」其实是一组常量；括号未闭合（离线奖励行）", "改为 KV 表 cfg_global，一行一个常量，带类型与合法区间"),
    ("P1", "玩家表 / 全局配置表", "「最大体力(240)」硬编码与全局「体力上限」重复且可能冲突", "单一数据源放 cfg_global，玩家表只存快照 stamina_cap_snapshot 便于审计"),
    ("P1", "全局配置表", "「体力上限」与「技能体力上限」共用「体力」词根，易被误当成两套体力池", "已澄清：体力=账号级纯数值关卡门票，技能体力=局内能量 Mana；更名 nManaCap 并新增 tbPlayerRunState 承载"),
    ("P1", "玩家配置表", "「通行器/武器/技能要怎么搭配」是自由文本，无法落库", "改为 loadout_id + hero_id + vehicle_instance_id + weapon_instance_ids_json + skill_ids_json"),
    ("P1", "英雄/怪兽/技能/武器配置表", "只有标识与名称/描述，缺少属性数值来源与启用标记", "补 base_stats_json / effect_ids_json / is_enabled"),
    ("P2", "地形类型表 / 资源定义表", "字典表缺排序、启用状态，难以做热更", "统一补 name / sort_order / is_enabled；主键改号段 Id，不再设字符串 Code 列"),
    ("P2", "加成道具表 / 道具配置表", "同一道具被拆成两张表描述；scope 枚举写在列名里且缺加成数值", "合并进 cfg_item，用 usage_type 区分 once / duration；scope 列化，补 stat_modifiers_json / max_stack / duration_sec"),
    ("P2", "全部表", "缺启用标记、时间戳、软删，热更与回滚困难", "配置表补 is_enabled，版本随整包发布；运行时表补 created_at / updated_at"),
    ("P2", "全部表", "数值口径未声明：大数值属性用什么类型、百分比缩放比例、时间是秒还是毫秒、权重是否归一化", "第 8 节统一约定：大数值属性用 BIGINT，序号 / 等级 / 权重 / 耗时用 INT，标志与枚举用 TINYINT；全库不使用小数，比例由服务端与客户端约定"),
    ("P1", "全部表", "原稿中文字段名无大小写与词形规范，程序键与显示名混用", "全库改纯大驼峰 PascalCase；枚举码值与 JSON 数据键保持小写 snake_case"),
    ("P1", "全部配置 / 字典表", "Id 与 Code 构成双唯一键，但全库没有一处外键使用 Code，仅用于 JSON 键与文档措辞", "删除 17 个 Code 列，主键改号段 Id（表号 × 100000 + 序号）；多态关联由 Id 反查表，再删 3 个判别列"),
]

# ---------- 原表映射 ----------
MAPPING = [
    ("（业界实践补充）抽取规则表", "cfg_draw_rule", "新增", "权重表只存概率，保底 / 洗牌袋 / 连续上限抽成规则表复用"),
    ("地形类型表", "dict_terrain_type", "保留 + 补字段", "补 name / passable / sort_order / is_enabled；主键改号段 Id"),
    ("基础属性表", "cfg_stat_def (part 1)", "合并", "与武器属性表合并，用 stat_group 枚举区分"),
    ("武器属性表", "cfg_stat_def (part 2)", "合并", "弹匣/连发/换弹/弹道作为武器属性组"),
    ("攻击模式表", "dict_attack_pattern", "改类型", "枚举字典，数值列改 shape_params_json"),
    ("特殊效果表", "cfg_effect_def", "合并", "效果类型降为 EffectType 枚举列，单位与叠层规则内联，tick 进 params_json"),
    ("英雄配置表", "cfg_hero", "保留 + 补字段", "补 base_stats_json / default_weapon_id / is_enabled"),
    ("武器配置表", "cfg_weapon + dict_weapon_type", "拆分", "类型1/类型2 抽成字典，owner_type 保留列"),
    ("通行器配置表", "cfg_vehicle + cfg_vehicle_slot + dict_slot_type", "拆分", "槽位1-4 由横表改竖表，适配地形改 terrain_ids_json"),
    ("怪兽配置表", "cfg_monster", "保留 + 补字段", "补 level / base_stats_json / skill_ids_json"),
    ("技能配置表", "cfg_skill", "保留 + 补字段", "补消耗 / 冷却 / effect_ids_json"),
    ("属性等级表", "cfg_level_up + cfg_level_up_cost + cfg_level_up_effect", "重构", "一次升级拆成主表与两张明细；多资源消耗、多属性提升、新增属性各自成行，允许跨级由服务端合并处理"),
    ("资源定义表", "dict_resource", "保留 + 补字段", "补 scope 区分账号级 / 局内；新增局内能量资源行（Id 300004）"),
    ("关卡配置表", "cfg_stage + cfg_stage_monster + cfg_stage_affix + cfg_drop_entry", "拆分", "三列配置全部结构化，权重可校验"),
    ("道具配置表 + 加成道具表", "cfg_item", "重构 + 合并", "值列改数据行；加成道具并入，补 usage_type / reward_json / 加成字段"),
    ("词条配置表", "cfg_affix", "保留 + 补字段", "补 value_type / rarity / weight / stack_rule"),
    ("加成表", "player_buff + log_buff_expired", "拆分", "生效中与已过期分表，补加成内容"),
    ("玩家表", "player", "保留 + 修正", "补 player_id 主键；最大体力改快照，去掉硬编码 240"),
    ("玩家实例表", "player_instance + player_instance_slot", "拆分", "多态列拆开，槽位改竖表"),
    ("玩家配置表", "player_loadout", "重命名 + 结构化", "自由文本改外键与 JSON 数组"),
    ("全局配置表", "cfg_global", "重构", "横表改 KV，补 ValueType / MinValue / MaxValue"),
    ("全局配置表「技能体力上限」", "player_run_state + cfg_global", "新增 / 更名", "实为局内能量上限 nManaCap，需局内状态表承载，与账号级体力分离"),
]

# ---------- 表定义 ----------
# fields: (en, cn, type, required, note)
F = lambda *rows: list(rows)
TABLES = [
    # ===== A 字典层 =====
    dict(layer="A", cn="地形类型表", en="dict_terrain_type", origin="地形类型表",
         purpose="地形枚举，供通行器适配与关卡地块引用", pk="id", fk="—",
         change="删 Code 列改号段 Id；补 Name / Passable / SortOrder / IsEnabled",
         fields=F(
             ("id", "标识", "BIGINT", "Y", "主键"),
             ("name", "名称", "VARCHAR(64)", "N", "仅配置台阅读的内部备注名；程序键用号段 Id，客户端按 Id 本地化"),
             ("category", "大类", "VARCHAR(32)", "Y", "陆地 / 水域 / 特殊地形，取自固定枚举"),
             ("passable", "是否可通行", "TINYINT", "Y", "0 否 1 是"),
             ("sort_order", "排序", "INT", "N", "配置台展示顺序"),
             ("is_enabled", "启用", "TINYINT", "Y", "0 停用 1 启用"))),
    dict(layer="A", cn="攻击模式表", en="dict_attack_pattern", origin="攻击模式表",
         purpose="远程 / 扇形 / 范围 / 穿透四种攻击形状枚举", pk="id", fk="—",
         change="去掉「数值」列，改形状参数模板",
         fields=F(
             ("id", "标识", "BIGINT", "Y", "主键"),
             ("name", "名称", "VARCHAR(64)", "N", "仅配置台阅读的内部备注名；程序键用号段 Id，客户端按 Id 本地化"),
             ("shape_params_json", "形状参数模板", "JSON", "N", "射程 range / 夹角 angle / 半径 radius / 穿透数 pierce_count"),
             ("is_enabled", "启用", "TINYINT", "Y", "0 停用 1 启用"))),
    dict(layer="A", cn="资源定义表", en="dict_resource", origin="资源定义表",
         purpose="资源定义；区分账号级（游戏币 / 体力 / 经验）与关卡内（能量 / 蓝量）", pk="id", fk="—",
         change="补 scope 区分资源作用域；新增局内能量资源（Id 300004）；上限来源避免双重定义",
         fields=F(
             ("id", "标识", "BIGINT", "Y", "主键"),
             ("name", "名称", "VARCHAR(64)", "N", "仅配置台阅读的内部备注名；程序键用号段 Id，客户端按 Id 本地化"),
             ("scope", "资源作用域", "VARCHAR(16)", "Y", "account 账号级持久 / run 关卡内临时；体力属 account，能量属 run"),
             ("cap_source", "上限来源", "VARCHAR(32)", "Y", "global 取全局配置 / player 玩家自定 / none 无上限"),
             ("icon", "图标", "VARCHAR(128)", "N", "资源图标路径"),
             ("is_enabled", "启用", "TINYINT", "Y", "0 停用 1 启用"))),
    dict(layer="A", cn="武器类型表", en="dict_weapon_type", origin="武器配置表 类型2（抽出）",
         purpose="近战 / 手枪 / 冲锋枪 / 步枪 / 弓箭 / 弩 / 火箭炮", pk="id", fk="—",
         change="原「类型2」枚举抽出成表，新增类型不再改表结构",
         fields=F(
             ("id", "标识", "BIGINT", "Y", "主键"),
             ("name", "名称", "VARCHAR(64)", "N", "仅配置台阅读的内部备注名；程序键用号段 Id，客户端按 Id 本地化"),
             ("owner_type", "归属", "VARCHAR(32)", "Y", "hero 英雄武器 / vehicle 通行器武器"),
             ("sort_order", "排序", "INT", "N", "配置台展示顺序"),
             ("is_enabled", "启用", "TINYINT", "Y", "0 停用 1 启用"))),
    dict(layer="A", cn="槽位类型表", en="dict_slot_type", origin="通行器配置表 槽位1-4（抽出）",
         purpose="定义槽位可装配的实体类型", pk="id", fk="—",
         change="原四个「槽位N类型」列名改为字典引用",
         fields=F(
             ("id", "标识", "BIGINT", "Y", "主键"),
             ("name", "名称", "VARCHAR(64)", "N", "仅配置台阅读的内部备注名；程序键用号段 Id，客户端按 Id 本地化"),
             ("allowed_entity_types", "允许实体类型", "JSON", "Y", "字符串数组，装配时校验"),
             ("sort_order", "排序", "INT", "N", "配置台展示顺序"))),

    # ===== B 配置层 =====
    dict(layer="B", cn="属性定义表", en="cfg_stat_def", origin="基础属性表 + 武器属性表（合并）",
         purpose="所有可参与数值计算的属性定义及合法区间", pk="stat_id",
         fk="—",
         change="两表合并；攻击模式与特殊效果拆为独立字典，属性分组降为枚举列不再单独建表",
         fields=F(
             ("stat_id", "属性标识", "BIGINT", "Y", "主键"),
             ("stat_group", "属性分组", "VARCHAR(16)", "Y", "base 基础属性 / weapon 武器属性；枚举码值，不单独建表"),
             ("name", "名称", "VARCHAR(64)", "N", "仅配置台阅读的内部备注名；程序键用号段 Id，客户端按 Id 本地化"),
             ("value_type", "数值形态", "VARCHAR(32)", "Y", "number / percent / ms / json"),
             ("unit", "单位", "VARCHAR(16)", "N", "点 / 发 / 毫秒 / 百分比"),
             ("min_value", "下限", "BIGINT", "N", "配置校验用"),
             ("max_value", "上限", "BIGINT", "N", "配置校验用，减伤率等需与全局上限联动"),
             ("is_percent", "是否百分比", "TINYINT", "Y", "0 否 1 是；数值按约定比例存整数"),
             ("stack_rule", "叠加规则", "VARCHAR(32)", "Y", "add 相加 / mul 相乘 / max 取最大 / replace 覆盖"),
             ("is_enabled", "启用", "TINYINT", "Y", "0 停用 1 启用"),
             ("remark", "备注", "VARCHAR(255)", "N", "公式说明"))),
    dict(layer="B", cn="特殊效果定义表", en="cfg_effect_def", origin="特殊效果表",
         purpose="具体效果的数值参数，可被武器 / 技能 / 词条引用", pk="effect_id",
         fk="—",
         change="效果类型降为枚举列；原类型表的默认单位与叠层规则内联，tick 统一放 params_json",
         fields=F(
             ("effect_id", "效果标识", "BIGINT", "Y", "主键"),
             ("effect_type", "效果类型", "VARCHAR(32)", "Y", "dot / slow / attack_speed_down / accuracy_down 等；枚举码值"),
             ("name", "名称", "VARCHAR(64)", "N", "仅配置台阅读的内部备注名；程序键用号段 Id，客户端按 Id 本地化"),
             ("value_unit", "数值单位", "VARCHAR(16)", "Y", "percent / flat / ms；由原效果类型表的默认单位内联"),
             ("params_json", "效果参数", "JSON", "Y", "效果参数键用小写：value / duration_ms / tick_ms / target；dot 类必填 tick_ms"),
             ("duration_ms", "持续时间", "INT", "N", "单位毫秒；-1 表示永久"),
             ("max_stack", "最大层数", "INT", "N", "配合 stack_rule"),
             ("stack_rule", "叠层规则", "VARCHAR(32)", "Y", "refresh 刷新 / stack 叠层 / independent 独立；原类型表内联"),
             ("is_enabled", "启用", "TINYINT", "Y", "0 停用 1 启用"))),
    dict(layer="B", cn="英雄配置表", en="cfg_hero", origin="英雄配置表",
         purpose="可选角色基础模板", pk="hero_id", fk="default_weapon_id -> cfg_weapon.weapon_id",
         change="补基础属性、默认武器与启用",
         fields=F(
             ("hero_id", "英雄标识", "BIGINT", "Y", "主键"),
             ("name", "名称", "VARCHAR(64)", "N", "仅配置台阅读的内部备注名；程序键用号段 Id，客户端按 Id 本地化"),
             ("description", "描述", "TEXT", "N", "原「描述」字段，desc 为保留字故改名"),
             ("base_stats_json", "基础属性", "JSON", "Y", "数组：属性 Id + 数值；属性 Id 取 cfg_stat_def 的号段 Id"),
             ("default_weapon_id", "默认武器", "BIGINT", "N", "外键 cfg_weapon"),
             ("icon", "图标", "VARCHAR(128)", "N", "资源路径"),
             ("is_enabled", "启用", "TINYINT", "Y", "0 停用 1 启用"))),
    dict(layer="B", cn="武器配置表", en="cfg_weapon", origin="武器配置表",
         purpose="武器模板，含类型、攻击模式与武器属性", pk="weapon_id",
         fk="weapon_type_id -> dict_weapon_type.id; attack_pattern_id -> dict_attack_pattern.id",
         change="类型1/类型2 拆为 owner_type 与外键；弹匣等改 base_stats_json",
         fields=F(
             ("weapon_id", "武器标识", "BIGINT", "Y", "主键"),
             ("name", "名称", "VARCHAR(64)", "N", "仅配置台阅读的内部备注名；程序键用号段 Id，客户端按 Id 本地化"),
             ("owner_type", "归属类型", "VARCHAR(32)", "Y", "hero / vehicle，原类型1"),
             ("weapon_type_id", "武器类型", "BIGINT", "Y", "外键，原类型2"),
             ("attack_pattern_id", "攻击模式", "BIGINT", "Y", "外键 dict_attack_pattern"),
             ("base_stats_json", "武器属性", "JSON", "Y", "数组：属性 Id + 数值；属性 Id 取 cfg_stat_def 的号段 Id（弹匣 / 连发 / 换弹 / 弹道）"),
             ("effect_ids_json", "附带效果", "JSON", "N", "cfg_effect_def.effect_id 数组"),
             ("icon", "图标", "VARCHAR(128)", "N", "资源路径"),
             ("is_enabled", "启用", "TINYINT", "Y", "0 停用 1 启用"))),
    dict(layer="B", cn="通行器配置表", en="cfg_vehicle", origin="通行器配置表",
         purpose="载具模板（Vehicle，已确认），含地形适配与槽位数", pk="vehicle_id",
         fk="default_weapon_id -> cfg_weapon.weapon_id",
         change="槽位1-4 拆到子表；适配地形改 id 数组",
         fields=F(
             ("vehicle_id", "通行器标识", "BIGINT", "Y", "主键"),
             ("name", "名称", "VARCHAR(64)", "N", "仅配置台阅读的内部备注名；程序键用号段 Id，客户端按 Id 本地化"),
             ("description", "描述", "TEXT", "N", "保留字改写"),
             ("terrain_ids_json", "适配地形", "JSON", "Y", "dict_terrain_type.id 数组，替代自由文本"),
             ("slot_count", "槽位数", "INT", "Y", "原稿固定 4，改为可配"),
             ("default_weapon_id", "默认武器", "BIGINT", "N", "外键 cfg_weapon，owner_type 须为 vehicle"),
             ("icon", "图标", "VARCHAR(128)", "N", "资源路径"),
             ("is_enabled", "启用", "TINYINT", "Y", "0 停用 1 启用"))),
    dict(layer="B", cn="通行器槽位表", en="cfg_vehicle_slot", origin="通行器配置表 槽位1-4（新增）",
         purpose="一行一个槽位，支持任意槽位数与差异化校验", pk="id",
         fk="vehicle_id -> cfg_vehicle.vehicle_id; slot_type_id -> dict_slot_type.id",
         change="横表改竖表；新增槽位无需改表结构",
         fields=F(
             ("id", "标识", "BIGINT", "Y", "主键"),
             ("vehicle_id", "通行器标识", "BIGINT", "Y", "外键 cfg_vehicle"),
             ("slot_index", "槽位序号", "INT", "Y", "从 1 开始，同 vehicle_id 内唯一"),
             ("slot_type_id", "槽位类型", "BIGINT", "Y", "外键 dict_slot_type"),
             ("allowed_type_ids_json", "允许装配白名单", "JSON", "N", "覆盖槽位类型的默认白名单"),
             ("default_item_id", "默认装配", "BIGINT", "N", "初始赠送的武器 / 技能 id"),
             ("is_locked", "初始锁定", "TINYINT", "Y", "0 解锁 1 锁定"))),
    dict(layer="B", cn="怪兽配置表", en="cfg_monster", origin="怪兽配置表",
         purpose="怪物模板，等级与属性来源", pk="monster_id",
         fk="attack_pattern_id -> dict_attack_pattern.id",
         change="补属性、技能、AI；等级允许关卡覆盖",
         fields=F(
             ("monster_id", "怪兽标识", "BIGINT", "Y", "主键"),
             ("name", "名称", "VARCHAR(64)", "N", "仅配置台阅读的内部备注名；程序键用号段 Id，客户端按 Id 本地化"),
             ("level", "基础等级", "INT", "Y", "关卡可通过 cfg_stage_monster 覆盖"),
             ("description", "描述", "TEXT", "N", "保留字改写"),
             ("base_stats_json", "基础属性", "JSON", "Y", "数组：属性 Id + 数值；属性 Id 取 cfg_stat_def 的号段 Id"),
             ("skill_ids_json", "技能列表", "JSON", "N", "cfg_skill.skill_id 数组"),
             ("attack_pattern_id", "攻击模式", "BIGINT", "N", "外键 dict_attack_pattern"),
             ("ai_profile", "AI 配置", "VARCHAR(64)", "N", "行为树 / 索敌参数标识"),
             ("icon", "图标", "VARCHAR(128)", "N", "资源路径"),
             ("is_enabled", "启用", "TINYINT", "Y", "0 停用 1 启用"))),
    dict(layer="B", cn="技能配置表", en="cfg_skill", origin="技能配置表",
         purpose="技能模板，含消耗、冷却与效果", pk="skill_id",
         fk="cost_resource_id -> dict_resource.id",
         change="补消耗资源、冷却、效果引用；消耗固定指向局内能量资源 Id 300004，不消耗账号级体力",
         fields=F(
             ("skill_id", "技能标识", "BIGINT", "Y", "主键"),
             ("name", "名称", "VARCHAR(64)", "N", "仅配置台阅读的内部备注名；程序键用号段 Id，客户端按 Id 本地化"),
             ("description", "描述", "TEXT", "N", "保留字改写"),
             ("cost_resource_id", "消耗资源", "BIGINT", "N", "外键 dict_resource，固定指向局内能量资源 Id 300004"),
             ("cost_value", "能量消耗", "BIGINT", "N", "释放技能扣除的局内蓝量，不扣账号级体力"),
             ("cooldown_ms", "冷却", "INT", "N", "单位毫秒"),
             ("effect_ids_json", "效果列表", "JSON", "N", "cfg_effect_def.effect_id 数组"),
             ("icon", "图标", "VARCHAR(128)", "N", "资源路径"),
             ("is_enabled", "启用", "TINYINT", "Y", "0 停用 1 启用"))),
    dict(layer="B", cn="升级档位表", en="cfg_level_up", origin="属性等级表（重构）",
         purpose="一次升级的档位定义：从 FromLevel 升到 ToLevel，是该次升级事务的主记录", pk="level_up_id",
         fk="owner_id -> 按号段反查 cfg_weapon / cfg_vehicle / cfg_monster / cfg_skill",
         change="原「属性等级表」把消耗与加成混在一列，拆成主表 + 消耗明细 + 变更明细；允许跨级，服务端合并途经各档一次事务处理；删 OwnerType 判别列，主体靠号段 Id 反查",
         fields=F(
             ("level_up_id", "档位标识", "BIGINT", "Y", "主键"),
             ("owner_id", "主体标识", "BIGINT", "Y", "号段 Id 自带表号，可反查主体配置表；UNIQUE(OwnerId, FromLevel)"),
             ("from_level", "起始等级", "INT", "Y", "必须等于实体当前等级"),
             ("to_level", "目标等级", "INT", "Y", "允许大于 FromLevel + 1，服务端合并途经档位一次处理"),
             ("is_enabled", "启用", "TINYINT", "Y", "0 停用 1 启用"),
             ("remark", "备注", "VARCHAR(255)", "N", "策划备注，不参与逻辑"))),
    dict(layer="B", cn="升级消耗明细表", en="cfg_level_up_cost", origin="属性等级表（重构）",
         purpose="一次升级扣掉的多种资源 / 道具，一行一种", pk="cost_id",
         fk="level_up_id -> cfg_level_up.level_up_id; cost_target_id -> 按号段反查 dict_resource.id / cfg_item.item_id",
         change="原 entry_type=0 的行独立成表；一行一个消耗项，支持一次扣多种资源与材料；删 CostType 判别列，靠号段 Id 反查",
         fields=F(
             ("cost_id", "消耗标识", "BIGINT", "Y", "主键"),
             ("level_up_id", "档位标识", "BIGINT", "Y", "外键 cfg_level_up，同一次升级共享"),
             ("cost_target_id", "消耗对象", "BIGINT", "Y", "号段 Id 自带表号，可反查资源 / 道具配置；应用层校验存在性"),
             ("cost_value", "消耗数量", "BIGINT", "Y", "整数，必须大于 0"))),
    dict(layer="B", cn="升级变更明细表", en="cfg_level_up_effect", origin="属性等级表（重构）",
         purpose="一次升级带来的多属性提升、新增属性与解锁项，一行一个变更", pk="effect_id",
         fk="level_up_id -> cfg_level_up.level_up_id; stat_id -> cfg_stat_def.stat_id; target_id -> 按 effect_type 指向对应配置",
         change="原 entry_type=1 的行独立成表；新增 unlock_stat 等类型，支持新增属性与解锁",
         fields=F(
             ("effect_id", "变更标识", "BIGINT", "Y", "主键"),
             ("level_up_id", "档位标识", "BIGINT", "Y", "外键 cfg_level_up，同一次升级共享"),
             ("effect_type", "变更类型", "VARCHAR(32)", "Y", "stat_add 加值 / stat_set 设值 / stat_rate 按比例 / unlock_stat 新增属性 / unlock_slot 解锁槽位 / unlock_skill 解锁技能 / grant_affix 授予词条"),
             ("stat_id", "属性标识", "BIGINT", "N", "属性类变更必填，外键 cfg_stat_def"),
             ("target_id", "解锁对象", "BIGINT", "N", "解锁类变更必填，按 effect_type 指向槽位 / 技能 / 词条配置"),
             ("value", "变更数值", "BIGINT", "N", "加成量或新增属性初始值；按比例类按约定比例存整数"),
             ("params_json", "扩展参数", "JSON", "N", "新增属性是否参与后续成长、叠层规则等"),
             ("sort_order", "执行顺序", "INT", "N", "同档位内按此顺序应用"))),
    dict(layer="B", cn="关卡配置表", en="cfg_stage", origin="关卡配置表",
         purpose="关卡主表，三处「配置」改为外键组 id", pk="stage_id",
         fk="unlock_stage_id -> cfg_stage.stage_id; drop_group_id -> cfg_drop_entry.drop_group_id",
         change="怪物 / 词条 / 掉落全部结构化，不再混用 JSON 与外键",
         fields=F(
             ("stage_id", "关卡标识", "BIGINT", "Y", "主键"),
             ("name", "名称", "VARCHAR(64)", "N", "仅配置台阅读的内部备注名；程序键用号段 Id，客户端按 Id 本地化"),
             ("chapter", "章节", "INT", "N", "章节序号"),
             ("difficulty", "难度", "INT", "Y", "影响词条池筛选"),
             ("unlock_stage_id", "前置关卡", "BIGINT", "N", "自关联"),
             ("recommended_power", "推荐战力", "BIGINT", "N", "展示用"),
             ("monster_group_id", "怪物组", "BIGINT", "Y", "关联 cfg_stage_monster.group_id"),
             ("affix_group_id", "词条组", "BIGINT", "N", "关联 cfg_stage_affix.group_id"),
             ("drop_group_id", "掉落组", "BIGINT", "Y", "关联 cfg_drop_entry.drop_group_id"),
             ("stamina_cost", "体力消耗", "BIGINT", "N", "进入关卡按此值扣除账号级体力 nStamina；与局内能量无关"),
             ("is_enabled", "启用", "TINYINT", "Y", "0 停用 1 启用"))),
    dict(layer="B", cn="关卡怪物表", en="cfg_stage_monster", origin="关卡配置表「怪物配置」（新增）",
         purpose="一行一种怪物，支持波次、数量与权重", pk="id",
         fk="monster_id -> cfg_monster.monster_id",
         change="JSON 明细化，权重可被校验与复用",
         fields=F(
             ("id", "标识", "BIGINT", "Y", "主键"),
             ("group_id", "怪物组", "BIGINT", "Y", "与 cfg_stage.monster_group_id 对应"),
             ("monster_id", "怪兽标识", "BIGINT", "Y", "外键 cfg_monster"),
             ("min_level", "最低等级", "INT", "N", "空则取怪兽基础等级"),
             ("max_level", "最高等级", "INT", "N", "与 min_level 构成随机区间"),
             ("count", "数量", "INT", "Y", "该波次生成数量"),
             ("wave", "波次", "INT", "Y", "从 1 起"),
             ("spawn_delay_ms", "出场延迟", "INT", "N", "单位毫秒"),
             ("weight", "权重", "INT", "N", "随机抽取时使用，非归一化"))),
    dict(layer="B", cn="关卡词条表", en="cfg_stage_affix", origin="关卡配置表「词条配置」（新增）",
         purpose="关卡可选词条池，肉鸽局内成长来源", pk="id",
         fk="affix_id -> cfg_affix.affix_id",
         change="词条与关卡解耦，支持复用与按难度筛选",
         fields=F(
             ("id", "标识", "BIGINT", "Y", "主键"),
             ("group_id", "词条组", "BIGINT", "Y", "与 cfg_stage.affix_group_id 对应"),
             ("affix_id", "词条标识", "BIGINT", "Y", "外键 cfg_affix"),
             ("weight", "抽取权重", "INT", "Y", "非归一化，运行时按组内总和归一"),
             ("min_difficulty", "最低难度", "INT", "N", "低于该难度不进池"),
             ("max_roll_count", "单局最多出现", "INT", "N", "防重复"),
             ("draw_rule_id", "抽取规则", "BIGINT", "N", "外键 cfg_draw_rule；空表示纯权重抽取"))),
    dict(layer="B", cn="掉落明细表", en="cfg_drop_entry", origin="关卡配置表「掉落配置 json」（新增）",
         purpose="原 json 道具id / 数量 / 权重 的规范化版本", pk="id",
         fk="item_id -> 按号段反查 cfg_item / dict_resource / cfg_affix",
         change="补数量区间与保底标记；权重口径统一；删 ItemType 判别列，靠号段 Id 反查产出类型",
         fields=F(
             ("id", "标识", "BIGINT", "Y", "主键"),
             ("drop_group_id", "掉落组", "BIGINT", "Y", "与 cfg_stage.drop_group_id 对应"),
             ("item_id", "产出标识", "BIGINT", "Y", "号段 Id 自带表号，可反查道具 / 资源 / 词条配置"),
             ("min_count", "最小数量", "INT", "Y", "数量下限"),
             ("max_count", "最大数量", "INT", "Y", "数量上限，等于下限时为固定值"),
             ("weight", "权重", "INT", "Y", "非归一化；同组内按总和计算概率"),
             ("is_guaranteed", "是否保底", "TINYINT", "Y", "1 时不参与权重抽取，必掉"),
             ("remark", "备注", "VARCHAR(255)", "N", "概率说明"),
             ("draw_rule_id", "抽取规则", "BIGINT", "N", "外键 cfg_draw_rule；空表示纯权重抽取"))),
    dict(layer="B", cn="道具配置表", en="cfg_item", origin="道具配置表 + 加成道具表（合并）",
         purpose="一行一个道具；道具本身即资源聚合包，UsageType 区分一次性 / 持续性", pk="item_id",
         fk="—",
         change="吸收原加成道具表：一次性道具用 reward_json 发多资源，持续性道具内联加成字段；删单值 value，scope 枚举列化",
         fields=F(
             ("item_id", "道具标识", "BIGINT", "Y", "主键，号段 Id"),
             ("name", "名称", "VARCHAR(64)", "N", "仅配置台阅读的内部备注名；程序键用号段 Id，客户端按 Id 本地化"),
             ("usage_type", "使用方式", "VARCHAR(16)", "Y", "once 一次性 / duration 持续性；决定读发放内容还是加成字段"),
             ("item_type", "道具类型", "VARCHAR(32)", "Y", "resource_pack 资源包 / material 材料 / energy 局内回能 / boost 增益；二级业务分类"),
             ("reward_json", "发放内容", "JSON", "N", "once 必填：资源 Id + 数量数组，一个包可发多种资源；duration 留空"),
             ("stat_modifiers_json", "加成内容", "JSON", "N", "duration 必填：属性 Id / 加成方式 / 数值数组；once 留空"),
             ("duration_sec", "持续时间", "INT", "N", "duration 必填：单位秒，-1 表示永久；once 留空"),
             ("scope", "生效范围", "TINYINT", "N", "duration 用：0 self 个人 / 1 alliance 联盟 / 2 global 全局；once 留空"),
             ("max_stack", "最大叠层", "INT", "N", "duration 用：同玩家同时生效层数上限，与背包堆叠上限区分"),
             ("stack_limit", "堆叠上限", "INT", "Y", "背包堆叠格数上限，0 表示不堆叠；与 buff 生效层数无关"),
             ("icon", "图标", "VARCHAR(128)", "N", "资源路径"),
             ("is_enabled", "启用", "TINYINT", "Y", "0 停用 1 启用"))),
    dict(layer="B", cn="词条配置表", en="cfg_affix", origin="词条配置表",
         purpose="一条词条 = 一个属性修饰，可被关卡池 / 装备引用", pk="affix_id",
         fk="stat_id -> cfg_stat_def.stat_id",
         change="补固定值 / 百分比区分、品质、权重、叠层规则；删冗余属性分组外键，分组由 StatId 反查",
         fields=F(
             ("affix_id", "词条标识", "BIGINT", "Y", "主键"),
             ("name", "名称", "VARCHAR(64)", "N", "仅配置台阅读的内部备注名；程序键用号段 Id，客户端按 Id 本地化"),
             ("stat_id", "属性标识", "BIGINT", "Y", "外键 cfg_stat_def"),
             ("value", "属性值", "BIGINT", "Y", "百分比类按约定比例存整数"),
             ("value_type", "加成方式", "VARCHAR(32)", "Y", "flat 固定值 / percent 百分比"),
             ("rarity", "品质", "INT", "N", "影响抽取池与展示"),
             ("weight", "基础权重", "INT", "N", "可被 cfg_stage_affix 覆盖"),
             ("stack_rule", "叠层规则", "VARCHAR(32)", "Y", "add / mul / max / replace"),
             ("description", "描述", "VARCHAR(255)", "N", "保留字改写"),
             ("is_enabled", "启用", "TINYINT", "Y", "0 停用 1 启用"))),
    dict(layer="B", cn="抽取规则表", en="cfg_draw_rule", origin="业界实践补充（新增）",
         purpose="保底 / 洗牌袋 / 连续上限等抽取策略，供掉落与词条池复用", pk="draw_rule_id",
         fk="—（被 cfg_drop_entry / cfg_stage_affix 的 draw_rule_id 引用）",
         change="权重表只存概率；抽取策略独立成表，避免保底逻辑散落代码",
         fields=F(
             ("draw_rule_id", "规则标识", "BIGINT", "Y", "主键，号段 Id"),
             ("name", "规则名", "VARCHAR(64)", "N", "仅配置台阅读的内部备注名"),
             ("bag_mode", "抽取模式", "VARCHAR(16)", "Y", "pure 纯权重 / shuffle_bag 洗牌袋 / prd 伪随机分布"),
             ("pity_threshold", "保底阈值", "INT", "Y", "连续未中稀有的次数阈值，0 表示不启用"),
             ("pity_step", "保底步长", "INT", "Y", "达到阈值后每次未中增加的权重，万分比整数"),
             ("max_repeat", "连续上限", "INT", "Y", "同一条目允许连续出现的最大次数，0 表示不限"),
             ("exclude_tags_json", "互斥标签", "JSON", "N", "同一次抽取中互斥的标签数组，如两个 legendary 不共存"),
             ("is_enabled", "启用", "TINYINT", "Y", "0 停用 1 启用"))),
    dict(layer="B", cn="全局配置表", en="cfg_global", origin="全局配置表（重构）",
         purpose="全局常量 KV 表，一行一个常量", pk="config_key", fk="—",
         change="横表改 KV；补值类型与合法区间，括号未闭合处已修正",
         fields=F(
             ("config_key", "配置键", "VARCHAR(64)", "Y", "主键，如 stamina_regen_per_hour"),
             ("config_value", "配置值", "VARCHAR(255)", "Y", "统一以字符串存储，按 value_type 解析"),
             ("value_type", "值类型", "VARCHAR(16)", "Y", "int / float / bool / json / string"),
             ("name", "名称", "VARCHAR(64)", "N", "仅配置台阅读的内部备注名；程序键为 ConfigKey"),
             ("description", "说明", "VARCHAR(255)", "N", "口径与公式说明"),
             ("min_value", "合法下限", "BIGINT", "N", "热更校验"),
             ("max_value", "合法上限", "BIGINT", "N", "热更校验"),
             ("updated_at", "更新时间", "DATETIME", "Y", "最近一次热更时间"))),

    # ===== C 运行时层 =====
    dict(layer="C", cn="玩家表", en="player", origin="玩家表",
         purpose="玩家账号主档", pk="player_id", fk="current_stage_id -> cfg_stage.stage_id",
         change="补主键；体力明确为账号级纯数值（关卡门票），上限改快照去掉硬编码 240；补时间戳",
         fields=F(
             ("schema_version", "存档结构版本", "INT", "Y", "读档时按迁移注册表升级；新增字段必须有默认值，删字段先标记废弃一个版本"),
             ("player_id", "玩家标识", "BIGINT", "Y", "主键，原表缺失"),
             ("account_id", "账号标识", "VARCHAR(64)", "Y", "登录体系关联"),
             ("nickname", "昵称", "VARCHAR(64)", "N", "展示名"),
             ("level", "等级", "INT", "Y", "玩家等级"),
             ("exp", "经验", "BIGINT", "Y", "当前经验"),
             ("stamina", "体力", "BIGINT", "Y", "账号级纯数值，只用于进行关卡（进关按 CfgStage.StaminaCost 扣减）；离线恢复，不参与战斗计算"),
             ("stamina_cap_snapshot", "体力上限快照", "BIGINT", "Y", "来自 cfg_global 的 nStaminaCap，记录当时值便于对账；与配置冲突时以配置为准"),
             ("current_stage_id", "当前关卡", "BIGINT", "N", "外键 cfg_stage"),
             ("offline_at", "离线时间戳", "DATETIME", "N", "用于离线奖励结算"),
             ("created_at", "创建时间", "DATETIME", "Y", "存档创建"),
             ("updated_at", "更新时间", "DATETIME", "Y", "每次写档刷新"))),
    dict(layer="C", cn="玩家实例表", en="player_instance", origin="玩家实例表",
         purpose="玩家实际拥有的武器 / 通行器，带等级与强化", pk="instance_id",
         fk="player_id -> player.player_id; ref_id -> 按 entity_type 指向 cfg_weapon / cfg_vehicle",
         change="一列三义拆开；等级与强化分离；槽位移到子表",
         fields=F(
             ("schema_version", "存档结构版本", "INT", "Y", "读档时按迁移注册表升级；新增字段必须有默认值，删字段先标记废弃一个版本"),
             ("instance_id", "实例标识", "BIGINT", "Y", "主键"),
             ("player_id", "玩家标识", "BIGINT", "Y", "外键 player"),
             ("entity_type", "实体类型", "VARCHAR(32)", "Y", "weapon / vehicle；客制化主体"),
             ("ref_id", "配置标识", "BIGINT", "Y", "指向 cfg_weapon 或 cfg_vehicle"),
             ("level", "等级", "INT", "Y", "实例等级，升级时按 cfg_level_up 的 FromLevel 校验"),
             ("enhance_level", "强化等级", "INT", "Y", "原「强化」列，与等级分开"),
             ("exp", "实例经验", "BIGINT", "N", "可选"),
             ("is_locked", "是否锁定", "TINYINT", "Y", "防止误分解"),
             ("acquired_at", "获得时间", "DATETIME", "Y", "同类可重复持有"))),
    dict(layer="C", cn="实例槽位表", en="player_instance_slot", origin="玩家实例表 槽位1-4（新增）",
         purpose="实例的装配关系，一行一个槽位", pk="id",
         fk="instance_id -> player_instance.instance_id; equipped_instance_id -> player_instance.instance_id",
         change="横表改竖表，装配目标用实例 id 而非配置 id；建联合唯一索引 UNIQUE(instance_id, slot_index)",
         fields=F(
             ("schema_version", "存档结构版本", "INT", "Y", "读档时按迁移注册表升级；新增字段必须有默认值，删字段先标记废弃一个版本"),
             ("id", "标识", "BIGINT", "Y", "主键"),
             ("instance_id", "所属实例", "BIGINT", "Y", "外键 player_instance"),
             ("slot_index", "槽位序号", "INT", "Y", "同 instance_id 内唯一"),
             ("slot_type_id", "槽位类型", "BIGINT", "Y", "外键 dict_slot_type，冗余便于校验"),
             ("equipped_instance_id", "已装配实例", "BIGINT", "N", "为空表示空槽"),
             ("is_locked", "是否锁定", "TINYINT", "Y", "随 cfg_vehicle_slot 初始锁定状态"))),
    dict(layer="C", cn="玩家配置表（配装）", en="player_loadout", origin="玩家配置表",
         purpose="一套英雄 + 通行器 + 武器 + 技能的搭配方案", pk="loadout_id",
         fk="player_id -> player.player_id; hero_id -> cfg_hero.hero_id; vehicle_instance_id -> player_instance.instance_id",
         change="自由文本改结构化外键；补槽位与激活状态，配合全局 max_loadout_slots；建联合唯一索引 UNIQUE(player_id, slot_index)",
         fields=F(
             ("schema_version", "存档结构版本", "INT", "Y", "读档时按迁移注册表升级；新增字段必须有默认值，删字段先标记废弃一个版本"),
             ("loadout_id", "配置标识", "BIGINT", "Y", "主键"),
             ("player_id", "玩家标识", "BIGINT", "Y", "外键 player"),
             ("slot_index", "方案序号", "INT", "Y", "从 1 起；默认只有 1 套，由全局配置放开"),
             ("name", "方案名", "VARCHAR(64)", "N", "玩家自定义"),
             ("hero_id", "英雄", "BIGINT", "Y", "外键 cfg_hero"),
             ("vehicle_instance_id", "通行器实例", "BIGINT", "N", "外键 player_instance"),
             ("weapon_instance_ids_json", "武器实例列表", "JSON", "N", "player_instance.instance_id 数组"),
             ("skill_ids_json", "技能列表", "JSON", "N", "cfg_skill.skill_id 数组，受 max_skill_slots 限制"),
             ("is_active", "是否启用", "TINYINT", "Y", "同玩家只允许一套 active"),
             ("created_at", "创建时间", "DATETIME", "Y", "存档时间"),
             ("updated_at", "更新时间", "DATETIME", "Y", "改装配时刷新"))),
    dict(layer="C", cn="加成表", en="player_buff", origin="加成表",
         purpose="玩家身上生效中的增益实例", pk="buff_id",
         fk="player_id -> player.player_id; item_id -> cfg_item.item_id",
         change="补加成内容、来源、叠层与状态；新增作用域层与所属局，区分账号级 / 局内 buff；时间统一用服务端 DATETIME",
         fields=F(
             ("schema_version", "存档结构版本", "INT", "Y", "读档时按迁移注册表升级；新增字段必须有默认值，删字段先标记废弃一个版本"),
             ("buff_id", "加成标识", "BIGINT", "Y", "主键"),
             ("player_id", "玩家标识", "BIGINT", "Y", "外键 player"),
             ("item_id", "道具标识", "BIGINT", "Y", "外键 cfg_item，指向持续性道具；用于反查加成数值"),
             ("run_id", "所属局", "BIGINT", "N", "局内 buff 填 player_run_state 的局标识；账号级留空"),
             ("scope_layer", "作用域层", "VARCHAR(16)", "Y", "account 账号级 / run 局内；决定结算时是否随关卡清除"),
             ("source_type", "来源类型", "VARCHAR(32)", "Y", "item / stage / skill / gm"),
             ("source_id", "来源标识", "BIGINT", "N", "对应来源记录 id"),
             ("scope", "生效范围", "TINYINT", "Y", "冗余自 cfg_item，便于结算"),
             ("stat_modifiers_json", "生效加成", "JSON", "Y", "快照，配置热更不影响已生效 buff"),
             ("stack_count", "叠层数", "INT", "Y", "默认 1"),
             ("start_at", "启动时间", "DATETIME", "Y", "服务端时间"),
             ("end_at", "结束时间", "DATETIME", "Y", "服务端时间；到期转 log_buff_expired"),
             ("status", "状态", "VARCHAR(16)", "Y", "active / expired / consumed"))),

    dict(layer="C", cn="局内状态表", en="player_run_state", origin="新增（承载技能能量 / 蓝量）",
         purpose="一次进入关卡的运行时状态：技能能量、已选词条与局内 buff，关卡结束归档清零", pk="run_id",
         fk="player_id -> player.player_id; stage_id -> cfg_stage.stage_id",
         change="承接原稿「技能体力上限」澄清后的局内能量；与账号级体力完全隔离",
         fields=F(
             ("run_id", "局标识", "BIGINT", "Y", "主键，一次进入关卡生成一条"),
             ("player_id", "玩家标识", "BIGINT", "Y", "外键 player"),
             ("stage_id", "关卡标识", "BIGINT", "Y", "外键 cfg_stage"),
             ("mana", "当前能量", "BIGINT", "Y", "局内蓝量，技能消耗来源；关卡结束时清零"),
             ("mana_cap_snapshot", "能量上限快照", "BIGINT", "Y", "来自 cfg_global 的 ManaCap，记录词条 / buff 修改后的实际上限"),
             ("active_affix_ids_json", "已选词条", "JSON", "N", "本局获得的 cfg_affix.affix_id 数组"),
             ("active_buff_ids_json", "生效加成", "JSON", "N", "本局生效的 player_buff.buff_id 数组"),
             ("status", "局状态", "VARCHAR(16)", "Y", "running / win / fail / quit"),
             ("start_at", "开始时间", "DATETIME", "Y", "服务端时间"),
             ("end_at", "结束时间", "DATETIME", "N", "结算时写入"),
             ("run_seed", "局随机种子", "BIGINT", "Y", "开局生成并存档，用于复现本局；禁止用当前时间直接当种子"),
             ("rng_streams_json", "随机流状态", "JSON", "N", "按命名空间记录 stage / affix / drop 各流游标，支持断点续玩与复现"),
             ("schema_version", "存档结构版本", "INT", "Y", "读档时按迁移注册表升级；新增字段必须有默认值，删字段先标记废弃一个版本"))),

    # ===== D 记录层 =====
    dict(layer="D", cn="加成过期记录表", en="log_buff_expired", origin="加成表注释「结束后可转到记录表」",
         purpose="已结束加成的归档，只追加", pk="id", fk="buff_id -> player_buff.buff_id（逻辑）",
         change="落地原稿注释；不保留外键物理约束以便清档",
         fields=F(
             ("id", "标识", "BIGINT", "Y", "主键"),
             ("buff_id", "加成标识", "BIGINT", "Y", "原 player_buff.buff_id"),
             ("player_id", "玩家标识", "BIGINT", "Y", "归属玩家"),
             ("item_id", "道具标识", "BIGINT", "Y", "快照，cfg_item.item_id"),
             ("start_at", "启动时间", "DATETIME", "Y", "快照"),
             ("end_at", "结束时间", "DATETIME", "Y", "快照"),
             ("effective_sec", "实际生效秒数", "INT", "Y", "提前消耗时小于标称时长"),
             ("created_at", "归档时间", "DATETIME", "Y", "写入时间"))),
    dict(layer="D", cn="关卡结算记录表", en="log_stage_settlement", origin="建议补充",
         purpose="通关 / 失败与奖励发放流水，供掉落与体力对账", pk="id",
         fk="player_id -> player.player_id; run_id -> player_run_state.run_id; stage_id -> cfg_stage.stage_id",
         change="原稿无结算表，补上以闭合「关卡 -> 掉落 -> 玩家资源」链路",
         fields=F(
             ("id", "标识", "BIGINT", "Y", "主键"),
             ("player_id", "玩家标识", "BIGINT", "Y", "归属玩家"),
             ("run_id", "局标识", "BIGINT", "Y", "外键 player_run_state，串起本局能量与词条"),
             ("stage_id", "关卡标识", "BIGINT", "Y", "外键 cfg_stage"),
             ("result", "结果", "VARCHAR(16)", "Y", "win / fail / quit"),
             ("duration_sec", "耗时", "INT", "Y", "单位秒"),
             ("reward_json", "奖励快照", "JSON", "Y", "实际发放的道具 / 资源 / 词条，均用号段 Id"),
             ("created_at", "结算时间", "DATETIME", "Y", "服务端时间"),
             ("run_seed", "局随机种子", "BIGINT", "Y", "从 player_run_state 快照，便于按种子复现本局"),
             ("config_version", "配置版本", "VARCHAR(32)", "Y", "本局生效的配置包版本，随配置变更生成，用于投诉复盘"))),
]

# ---------- 号段 Id 注册表（v5） ----------
ID_SEGMENT_SIZE = 100000        # 单表号段宽度；Id = 表号 * ID_SEGMENT_SIZE + 序号
ID_SEGMENT_TABLES = [t for t in TABLES if t["layer"] in ("A", "B") and t["en"] != "cfg_global"]


def seg_no(t, fallback):
    """表号：设计阶段按列表顺序连续分配 1~N；t.seg_no 可显式覆盖。"""
    return t.get("seg_no", fallback)


def id_segment_rows():
    rows = []
    for i, t in enumerate(ID_SEGMENT_TABLES, 1):
        no = seg_no(t, i)
        low = no * ID_SEGMENT_SIZE + 1
        high = (no + 1) * ID_SEGMENT_SIZE - 1
        rows.append((str(no), "%d ~ %d" % (low, high), t["cn"], t["en"], t["pk"]))
    return rows


# ---------- 全局常量清单 ----------
GLOBAL_KEYS = [
    ("StaminaRegenPerHour", "体力恢复速度", "int", "10", "点/小时，原稿「自然速度10点/h」；仅账号级体力"),
    ("StaminaCap", "体力上限", "int", "240", "账号级体力上限；原玩家表硬编码 240 收归此处"),
    ("ManaCap", "局内能量上限", "int", "待确认", "原稿「技能体力上限」，已澄清为关卡内蓝量上限，不是第二套体力"),
    ("ManaRegenPerSec", "局内回能速度", "int", "待确认", "补充项：关卡内每秒回能，0 表示不自动回复"),
    ("OfflineRewardStagesPerHour", "离线奖励速率", "int", "待确认", "原稿「最大关卡奖励 N 次/h」，按最大通关关卡折算"),
    ("OfflineRewardCapHours", "离线奖励时长上限", "int", "待确认", "防止无限累积，建议 8-24"),
    ("MaxSkillSlots", "最大可携带技能数量", "int", "待确认", "校验 PlayerLoadout.SkillIdsJson 长度；与能量池无关"),
    ("MaxLoadoutSlots", "最大玩家配置数", "int", "1", "原稿默认 1"),
    ("SyncIntervalMs", "游戏同步间隔", "int", "5000", "原稿 5s，统一毫秒"),
    ("DamageReductionCap", "减伤率上限", "int", "待确认", "按约定比例存整数，如 8000 对应 80%；比例由服务端与客户端约定"),
    ("MaxConcurrentEnemies", "同屏怪物上限", "int", "待确认", "性能与数值双重约束"),
    ("MinDamagePerHit", "单次伤害下限", "int", "待确认", "保底伤害"),
]

# ---------- 待确认 ----------
OPEN_QUESTIONS = [
    "升级档位表的主体是否只有 武器 / 载具 / 怪兽 / 技能 四类？英雄等级是否也要走这套升级模型？",
    "「攻击模式表」与「特殊效果表」的「数值」列原本打算放数值还是枚举说明？本版按枚举处理。",
    "道具已统一为一张 CfgItem，二级类型 resource_pack / material / energy / boost 是否覆盖全部道具品类？是否还要加装备等类型？",
    "关卡「怪物配置」「词条配置」原计划是 JSON 还是外键？本版统一改为明细表，若必须保留 JSON 可加镜像列。",
    "「联盟」范围是否已排期？无公会系统时建议保留枚举值 1 但置为停用。",
    "加成生效时间以服务端时间还是客户端时间为准？涉及 5s 同步间隔与离线期间的处理。",
    "百分比 / 倍率的整数缩放比例需与客户端约定（表结构不固化该值）；请确认与现有公式的缩放系数一致。",
    "配置版本号随整包发布（配置一变即升版），仅结算记录表快照；灰度用 IsEnabled。若已有热更体系可按其规范调整。",
    "局内能量开局是否满值？本版按 nManaCap 满值初始化且结算清零，是否要继承上一关剩余？",
    "局内能量是否自然回复？本版预留 nManaRegenPerSec，取 0 表示不自动回复。",
    "能量上限能否被词条 / buff 提升？本版允许并记录在 nManaCapSnapshot，需确认是否允许超过全局 nManaCap。",
    "是否需要保留类型前缀？本版为纯大驼峰，可用 USE_TYPE_PREFIX 开关切回带前缀的匈牙利驼峰版；请确认与现有代码风格一致。",
]

# ---------- 命名转换：类型前缀 + 大驼峰词干 ----------
USE_TYPE_PREFIX = False  # True -> 切回带类型前缀的匈牙利驼峰版

LAYER_PREFIX = {"A": "Dict", "B": "Cfg", "C": "Player", "D": "Log"}
ENUM_FIELDS = {
    "category", "stat_group", "value_unit", "cap_source", "stack_rule", "owner_type", "value_type",
    "entry_type", "item_type", "entity_type", "source_type", "scope", "scope_layer",
    "cost_type", "effect_type",
    "status", "result",
}
GLOBAL_KEY_RENAME = {
    "stamina_regen_per_hour": "StaminaRegenPerHour",
    "stamina_cap": "StaminaCap",
    "skill_stamina_cap": "ManaCap",
    "offline_reward_stages_per_hour": "OfflineRewardStagesPerHour",
    "offline_reward_cap_hours": "OfflineRewardCapHours",
    "max_skill_slots": "MaxSkillSlots",
    "max_loadout_slots": "MaxLoadoutSlots",
    "sync_interval_ms": "SyncIntervalMs",
    "damage_reduction_cap": "DamageReductionCap",
    "max_concurrent_enemies": "MaxConcurrentEnemies",
    "min_damage_per_hit": "MinDamagePerHit",
}
NAMING_EXAMPLES = [
    ("武器配置表", "CfgWeapon", "表名"),
    ("局内状态表", "PlayerRunState", "表名"),
    ("玩家标识", "PlayerId", "字段"),
    ("名称", "Name", "字段"),
    ("是否启用", "IsEnabled", "字段"),
    ("基础属性 JSON", "BaseStatsJson", "字段"),
    ("体力上限", "StaminaCap", "全局配置键"),
    ("属性定义表第 6 行", "600006", "号段 Id"),
]
HUNGARIAN_PREFIXES = [
    ("n", "整数", "BIGINT / INT", "nId, nLevel, nStamina"),
    ("f", "浮点 / 定点小数（本版不使用）", "DECIMAL / FLOAT", "fValue, fCritRate"),
    ("b", "布尔", "TINYINT(1)", "bEnabled, bLocked"),
    ("sz", "字符串", "VARCHAR / TEXT", "szName, szDescription"),
    ("dt", "时间点", "DATETIME", "dtCreatedAt, dtOfflineAt"),
    ("e", "枚举码值", "TINYINT / VARCHAR", "eOwnerType, eScope"),
    ("arr", "JSON 数组", "JSON array", "arrSkillIds, arrStatModifiers"),
    ("obj", "JSON 对象", "JSON object", "objBaseStats, objShapeParams"),
    ("tb", "数据表", "表名前缀", "tbCfgWeapon, tbPlayerRunState"),
]
RESOURCE_ROWS = [
    ("300001", "游戏币", "account", "账号级，掉落 / 商店结算发放"),
    ("300002", "经验", "account", "账号级，升级来源"),
    ("300003", "体力", "account", "账号级纯数值，只用于进行关卡；离线恢复，上限 StaminaCap"),
    ("300004", "能量 / 蓝量", "run", "关卡内部使用，技能消耗；开局初始化，结算清零，上限 ManaCap"),
]


def _pascal(s, keep_all=False):
    if not keep_all and s.endswith("_json"):
        s = s[:-5]
    parts = s.split("_")
    if not keep_all and parts and parts[0] in ("is", "has") and len(parts) > 1:
        parts = parts[1:]
    return "".join(x[:1].upper() + x[1:] for x in parts if x)


def _prefix_for(en, dtype, note):
    if en in ENUM_FIELDS:
        return "e"
    if dtype == "JSON":
        return "arr" if ("_ids" in en or "数组" in note) else "obj"
    if dtype == "DATETIME":
        return "dt"
    if dtype.startswith("DECIMAL"):
        return "f"
    if dtype.startswith("VARCHAR") or dtype == "TEXT":
        return "sz"
    if dtype == "TINYINT":
        return "b"
    return "n"


def hungarian_field(en, dtype, note):
    if USE_TYPE_PREFIX:
        return _prefix_for(en, dtype, note) + _pascal(en)
    return _pascal(en, keep_all=True)


def hungarian_table(en, layer):
    stem = en
    for p in ("dict_", "cfg_", "player_", "log_"):
        if en.startswith(p):
            stem = en[len(p):]
            break
    if en == "player":
        stem = ""
    head = ("tb" if USE_TYPE_PREFIX else "") + LAYER_PREFIX[layer]
    return head + _pascal(stem)


def _build_tokens():
    tk = {}
    for t in TABLES:
        tk[t["en"]] = hungarian_table(t["en"], t["layer"])
        for en, cn, dtype, req, note in t["fields"]:
            tk.setdefault(en, hungarian_field(en, dtype, note))
    for old, new in GLOBAL_KEY_RENAME.items():
        tk.setdefault(old, new)
    return tk


TOKENS = _build_tokens()
TOKEN_RE = re.compile(r"\b(" + "|".join(
    re.escape(k) for k in sorted(TOKENS, key=len, reverse=True)) + r")\b")


STRIP_PREFIX_RE = re.compile(r"\b(tb|arr|obj|sz|dt|n|f|b|e)([A-Z][A-Za-z0-9]*)\b")


# 表名 player 与枚举码值 player 同形；枚举码值不是标识符，转换后需按字面还原
LITERAL_RESTORE = [
    ("/ Player 玩家自定", "/ player 玩家自定"),
]


def apply_naming(text):
    parts = text.split("```")
    for i in range(0, len(parts), 2):
        seg = TOKEN_RE.sub(lambda m: TOKENS[m.group(0)], parts[i])
        if not USE_TYPE_PREFIX:
            seg = STRIP_PREFIX_RE.sub(lambda m: m.group(2), seg)
        for old, new in LITERAL_RESTORE:
            seg = seg.replace(old, new)
        parts[i] = seg
    return "```".join(parts)

# ---------- 最小可用集 ----------
MVP_TABLES = [
    ("cfg_stat_def", "所有数值的合法区间与叠层规则，最先定"),
    ("cfg_hero", "英雄模板"),
    ("cfg_weapon", "武器模板"),
    ("cfg_vehicle", "通行器模板"),
    ("cfg_monster", "怪物模板"),
    ("cfg_skill", "技能模板"),
    ("cfg_affix", "肉鸽局内成长词条"),
    ("cfg_item", "道具与资源发放"),
    ("cfg_stage", "关卡主表"),
    ("cfg_drop_entry", "掉落与结算对账"),
    ("cfg_global", "全局常量唯一来源"),
    ("player", "玩家主档"),
    ("player_instance", "客制化实体"),
    ("player_loadout", "配装方案"),
    ("player_run_state", "技能消耗能量必须有局内承载表"),
]


def esc(s):
    return str(s).replace("|", "\\|").replace("\n", " ")


def md_table(headers, rows):
    out = ["| " + " | ".join(esc(h) for h in headers) + " |",
           "| " + " | ".join(["---"] * len(headers)) + " |"]
    for r in rows:
        out.append("| " + " | ".join(esc(c) for c in r) + " |")
    return "\n".join(out)


HEADER = """# Roguelike 数值表设计 · 规整版 v6

> 来源：`C:/Users/Administrator/Desktop/ROUGE表设计.txt`（原稿 22 张表）
> 整理日期：2026-11-16 · v3：全库纯大驼峰 / 通行器定为 Vehicle / 体力与局内能量分离
> v4：大数值属性统一 BIGINT / 升级拆成事务型主表与两张明细 / 名称只留一列 Name
> v5：删除字符串 Code，主键改号段 Id / 多态关联靠 Id 反查 / 新增号段注册表与 4 条 CI 校验
> v6：加成道具并入道具表（UsageType 区分一次性 / 持续性，道具即资源聚合包）/ 移除 CSV 交付 / 号段连续重排为 1~23
> 配套：`ROGUE_数值表_分表.xlsx`（一表一 sheet 的导表工作簿，四行表头；另含目录页与号段分配页）
> 生成脚本：`build_schema_docs.py`（单一数据源；`USE_TYPE_PREFIX` 开关可切回带类型前缀的匈牙利驼峰版）

## 0. 速览

原稿的骨架是对的：**字典 → 配置 → 玩家实例 → 加成** 这条链已经想清楚了。主要问题不在缺表，而在三件事：
配置表和玩家存档表混在一层、多张表结构重复且职责重叠、若干表缺主键或把"值"当成了"列"。
本版按四层重新归置，全库改用纯大驼峰命名，并补齐主外键、启用与校验约束。

**本次关键决策**

1. 通行器 = 载具，英文定名 **Vehicle**，表 `CfgVehicle`；不再使用 Mount / Transporter。
2. 全库标识符采用**纯大驼峰（PascalCase）**：`PlayerId` / `Name` / `IsEnabled` / `CfgWeapon`。
   语义键按 `_` 切分后逐词首字母大写直接拼接，**不加小写类型前缀、不删词、不改词**。
   脚本 `USE_TYPE_PREFIX = False` 为当前方案；设为 `True` 重跑可切回带类型前缀的匈牙利驼峰版。
3. 资源拆成两层：账号级 **体力 Stamina** 是纯数值、只用于进行关卡；
   技能消耗的是关卡内部使用的 **能量 / 蓝量 Mana**，与体力无关，由局内状态表承载，结算清零。
4. 取消字符串 **Code** 列：字典 / 配置层 23 张实体表主键统一用**号段 Id**（`Id = 表号 × 100000 + 序号`；`CfgGlobal` KV 表仍用 `ConfigKey`），
   Id 自带表号可反查所属表；客户端按 Id 本地化，服务端只认数字 Id。完整分配见第 8 节。
5. 道具只有一张 `CfgItem`：**道具本身即资源聚合包**。`UsageType=once` 用 `RewardJson` 发多种资源，
   `UsageType=duration` 用加成字段产生限时增益；原加成道具表已并入，资源定义 `DictResource` 保持单一。

**关于拼写**：`ROUGE` 是 NLP 里的自动摘要评测指标（也是法语「红色」），游戏品类的正确写法是
**Roguelike / Roguelite / Rogue**。本交付物文件名用 `ROGUE_`（取 Rogue 词根），
文档与表名按品类语义统一为 Roguelike。建议把原稿文件名与工程目录一并改掉，避免检索时混淆。

### 导表工作簿

`ROGUE_数值表_分表.xlsx`：一张表一个 sheet（另有两个辅助页：目录页、号段分配页），sheet 名为「表中文名_表英文名」（如 `地形类型表_DictTerrainType`），
顺序与本文档一致（字典层 -> 配置层 -> 运行时层 -> 记录层）。每个 sheet 固定四行表头，一列一个字段：

| 行 | 内容 | 示例 |
| --- | --- | --- |
| 1 | 字段中文名 | 标识 |
| 2 | 字段英文名（纯大驼峰） | Id |
| 3 | 数据类型 | BIGINT |
| 4 | 字段其它描述 | 必填：Y；主键 |

第 4 行由 `必填` / `主键` / `说明` / `来源` 合并而成，来源与表名相同时省略；
脚本 `DESC_MODE` 设为 `note` 可改为只写说明/约束。`SHEET_NAME_SEP` 控制中英文名分隔符，
默认 `_`，设为空字符串即连写。前四行已冻结，可直接在此模板下方填写数值。

**目录页 `目录_Index`**（不参与导表，排在第一个 sheet）：列出全部表的层级、中英文名、字段数与用途，
点 F 列「打开 →」直接跳到对应 sheet；第 2 行 B~E 是四层快捷跳转；每个表页的 A1 带「返回目录」链接，
只加超链接、不改单元格内容，四行表头结构保持不变。目录页冻结前三行并带自动筛选，可按层级快速定位，
第 2 行 F 列另有「号段分配 →」快捷入口。

**号段分配页 `号段分配_IdSegment`**（不参与导表，排在第二个 sheet）：把第 8 节的号段注册表展开成 23 行，
一行一个表号，列出起始 / 结束 Id、单表容量、层级、中英文表名、主键、字段数与用途；
点每行「打开 →」直接跳到对应表页，A1 返回目录，冻结前三行并带自动筛选。
页末两行标注 `CfgGlobal` 不占号段、8 张运行时表用自增 Id、新增实体表从表号 24 起。
该页是**导表元数据**，不是游戏数据表，因此不进字段字典、不占号段、也不套四行表头模板；
与 `目录_Index` 同属辅助页，32 张数据表页的结构一字未动。

### 命名规范

| 约定 | 规则 | 示例 |
| --- | --- | --- |
| 命名法 | 纯大驼峰 PascalCase，每个单词首字母大写后直接拼接 | PlayerId |
| 表名 | 层前缀（Dict / Cfg / Player / Log）+ 大驼峰实体 | CfgWeapon |
| 字段名 | 逐词首字母大写，无下划线、无小写类型前缀 | Name / IsEnabled |
| 转换规则 | 下划线语义键逐词首字母大写，保留 is 与 json 等词 | 不删词、不改词 |
| 主键 | Id 或 XxxId，全库 BIGINT；字典 / 配置层 23 张按号段分配，CfgGlobal 除外 | 600006 |
| 外键 | XxxId，与被引主键同名 | WeaponId |
| 名称 | 仅一列 Name 作内部备注；程序键统一用号段 Id，客户端按 Id 本地化 | — |
| 时间 | XxxAt 时间点 (DATETIME)，XxxMs / XxxSec 时长 | OfflineAt |
| JSON | 字段以 Json 结尾，必须给出 schema | BaseStatsJson |
| 枚举码值 | 码值本身保持小写 snake_case，不加前缀（不是标识符） | hero / vehicle / unlock_stat |
| JSON 数据键 | JSON 内部 key 保持小写 snake_case（属数据协议） | pierce_count / tick_ms |
| 多态关联 | 单列 OwnerId（号段 Id 自带表号），不建物理外键 | OwnerId |
| 程序键 | 不设 Code 列，号段 Id 即唯一程序键 | 100001 |
| 保留字 | 不用裸英文单词作列名 | Description |

### 四层依赖方向（严格单向）

配置表可以引用字典表，运行时表引用配置表，**配置表禁止反向引用运行时表**。
这条约束是原稿最需要补的：一旦配置里塞了玩家标识，热更与合服就会出问题。

"""

NORM_TEXT = """## 8. 落表规范与数值口径

**统一口径**

| 项 | 约定 |
| --- | --- |
| 命名 | 全库标识符纯大驼峰；枚举码值与 JSON 数据键保持小写 snake_case |
| 百分比 / 倍率 | 一律整数存储，缩放比例由服务端与客户端约定，表结构不固化；展示层按约定还原 |
| 时间 | 持续 / 冷却 / 延迟统一毫秒（XxxMs）；持续性道具时长用秒（DurationSec）；时间点用服务端 DATETIME（XxxAt） |
| 权重 | 全部为「非归一化整数」，运行时按同组总和归一；组内总和必须大于 0 |
| 数值类型 | 全库不使用浮点与 DECIMAL；可能出大数值的属性数值 / 资源量 / 战力用 BIGINT，序号 / 等级 / 权重 / 耗时用 INT，布尔与枚举用 TINYINT |
| 叠层 | `add` 相加 / `mul` 相乘 / `max` 取最大 / `replace` 覆盖，同属性不得混用两种规则 |
| 引用 | 所有外键与 JSON 内的标识一律用号段 Id，导出时校验存在性；Id 自带表号，无需判别列 |
| 热更 | 号段与 Id 一经分配不再变更、不复用；下线用 `IsEnabled=0`，不物理删除；配置版本随整包发布，不在每行维护 |
| 程序键 | 不设字符串 Code 列，号段 Id 即唯一程序键；枚举码值列仍用字符串，不在此限 |
| 号段 Id | `Id = 表号 × 100000 + 序号`；字典 / 配置层 23 张实体表按序分配（CfgGlobal 除外），见下方注册表 |

**号段 Id 注册表（v5）**

字典 / 配置层共 23 张实体表按现有顺序**连续**分配表号 1~23，主键统一 BIGINT：`Id = 表号 × 100000 + 序号`，
序号从 1 起、单表容量 99999。`Id // 100000` 即表号，可反查目标表，因此 `OwnerId` / `CostTargetId` / `ItemId`
不再需要判别列。玩家 / 局内 / 日志等 8 张运行时表继续用自增 Id，不占号段；`CfgGlobal` 是 KV 表，主键为字符串 `ConfigKey`，同样不占号段。
号段一经分配不回收、Id 不复用、不换表；跨环境迁移直接搬 Id，无需再做字符串键映射。
可读性由导表时生成的常量表补偿（如 `StatId.CritRate = 600042`），业务代码只引常量不写魔法数字，配置台靠 `Name` 列认人。

{{ID_SEGMENTS}}

号段宽度 100000、23 张实体表连续占 1~23（删除冗余字典表与合并道具表后已整体重排）、新增实体表从 24 起 —— **设计阶段号段不预留空洞**，
并已同步为工作簿的 `号段分配_IdSegment` sheet（每行可点击跳转到对应表）。
注册表本身是导表侧元数据，不入库、不占号段；若服务端需要按 Id 反查目标表，可另建一张
`DictIdSegment`（字段：表号 / 起始 Id / 结束 Id / 容量 / 目标英文表名 / 是否预留 / 备注），
主键为表号，不进入玩家数据链路，本版默认只在导表工具侧维护。

**抽取规则（P0）**

权重只回答「概率多大」，回答不了「多久必出」「会不会连出两次」。新增 `CfgDrawRule` 承载策略，
`CfgDropEntry` 与 `CfgStageAffix` 通过 `DrawRuleId` 复用，空值表示纯权重：

- `BagMode`：`pure` 纯权重 / `shuffle_bag` 洗牌袋（一个周期内保证固定次数）/ `prd` 伪随机分布（连续失败逐步提权）。
- `PityThreshold` + `PityStep`：连续未中达到阈值后按步长递增权重，抑制长串不出；`PityThreshold=0` 表示关闭。
- `MaxRepeat` 限制同条目连续出现次数，`ExcludeTagsJson` 表达互斥（如两个顶级词条不共存）。
- 随机流按命名空间隔离（stage / affix / drop），改一处抽取不影响其他系统的随机序列。

**随机种子与配置版本（P0）**

- `PlayerRunState.RunSeed` 开局生成并存档，`RngStreamsJson` 记录各随机流游标：同一 Seed + 同一配置包 + 同一操作序列必须复现同一局，便于 bug 复盘与回放验证。
- `LogStageSettlement` 快照 `RunSeed` 与 `ConfigVersion`；玩家投诉「昨天概率不对」时直接查当时生效的配置包，不靠猜。
- 导表产物带唯一 `ConfigVersion`（建议 `data_YYYYMMDD_<git短哈希>`）；配置一变即升版，版本号属于整个配置包、不下沉到各行；与构建时间、来源提交、适用客户端版本一起记录。
- 回滚发布历史完整快照，不人肉改回旧值。

**存档结构版本与迁移（P0）**

- 6 张 C 层玩家表全部带 `SchemaVersion`；读档时按迁移注册表逐级升级到当前版本。
- 兼容规则：新增字段必须有默认值；删除字段先标记废弃一个版本再物理移除；改语义必须升版本并提供迁移函数。
- 配置可以热更，存档结构不能 —— 后者必须跟客户端版本一起走完整发布流程。

**两套资源的边界（已确认）**

| 项 | 体力 Stamina | 能量 Mana（蓝量） |
| --- | --- | --- |
| 归属 | 账号级，`Player.Stamina` 纯数值 | 局内，`PlayerRunState.Mana` |
| 用途 | 进行关卡的门票，按 `CfgStage.StaminaCost` 扣减 | 关卡内释放技能，按 `CfgSkill.CostValue` 扣减 |
| 生命周期 | 持久；离线按 `StaminaRegenPerHour` 恢复，上限 `StaminaCap` | 开局初始化，关卡结束清零，不写回玩家主档 |
| 参与战斗计算 | 否 | 是（技能释放与回能） |
| 上限来源 | `CfgGlobal` 的 `StaminaCap` | `CfgGlobal` 的 `ManaCap` |
| 词条 / buff 可改 | 不建议 | 可以（局内成长） |

原稿「技能体力上限」按此口径更名为「局内能量上限」`ManaCap`，不再与体力共用词根；
「最大可携带技能数量」限制的是配装槽位，与能量池是两件事。

**升级事务（v4 重构）**

原 `cfg_growth_curve` 把消耗与加成混在一列，无法表达一次升级的多资源、多属性与新增属性。
现拆成 `CfgLevelUp`（档位主表，一行一次升级）+ `CfgLevelUpCost`（N 行消耗）+ `CfgLevelUpEffect`（N 行变更）：

- 服务端在**单事务**内完成：校验 `FromLevel` 等于实体当前等级 → 汇总并扣除全部 Cost → 依次应用全部 Effect → 等级置 `ToLevel`；任一步失败整体回滚。
- 允许跨级：`ToLevel > FromLevel + 1` 时，服务端把途经各档的 Cost 与 Effect 合并后一次处理，不要求客户端逐档调用；途经档位必须连续且全部启用。
- 新增属性用 `EffectType = unlock_stat`，`Value` 为初始值，`ParamsJson` 记录是否参与后续成长；后续成长由更高档位的明细承接，不引入公式。
- 明细表不得单独热更，以配置包版本为单位整档替换；配置表禁止引用玩家表，升级结果落在 `PlayerInstance` 与实例属性快照中。本版不设升级流水表。
- 多态主体不再需要 `OwnerType`：号段 Id 自带表号（如 900001 必属 `CfgWeapon`），`CfgLevelUpCost.CostTargetId` 与 `CfgDropEntry.ItemId` 同理，三张表共删掉 3 个判别列。

**配置导出 CI 校验（建议做成导表脚本的硬失败项）**

1. 所有非空外键在目标表存在；`OwnerId` 等单列号段 Id 能按表号反查到唯一配置行。
2. 每个 `CfgLevelUp` 至少 1 条 `CfgLevelUpCost` 与 1 条 `CfgLevelUpEffect`；`ToLevel > FromLevel`，同主体档位链连续无断档、`FromLevel` 唯一。
3. JSON 列里的属性 Id 必须落在 `CfgStatDef` 的 6 号段（600001 ~ 699999）且该 Id 存在；`EffectId` / `SkillId` / `ItemId` 同理。
4. 同一 `GroupId` / `DropGroupId` 内权重总和 > 0；`MinCount <= MaxCount`；`MinValue <= Value <= MaxValue`。
5. `CfgGlobal.ConfigValue` 能按 `ValueType` 解析，且落在 `MinValue` ~ `MaxValue` 内。
6. `PlayerLoadout.SkillIdsJson` 长度 <= `MaxSkillSlots`；`SlotIndex` <= `MaxLoadoutSlots`。
7. 所有联合唯一索引无重复：升级档位、升级消耗明细、实例槽位、配装方案。
8. 配置表中不得出现任何 `Player` / `Log` 表引用。
9. 账号级体力只在进入关卡时按 `CfgStage.StaminaCost` 扣减，局内逻辑禁止写 `Player.Stamina`。
10. `Scope=run` 的资源（能量，Id 300004）不得写入账号主档；关卡结算时 `PlayerRunState.Mana` 必须清零。
11. 释放技能前校验 `PlayerRunState.Mana >= CfgSkill.CostValue`；能量上限不得低于全局 `ManaCap`。
12. `CfgLevelUpCost.CostTargetId` 按号段反查能定位到唯一资源 / 道具；`CostValue > 0`，同档位内同一对象不重复。
13. `CfgLevelUpEffect.EffectType` 与必填列匹配（属性类必有 `StatId`、解锁类必有 `TargetId`），且 `StatId` / `TargetId` 在目标表存在。
14. 升级为单事务：全部消耗扣成功才应用变更；跨级时途经档位连续、全部启用，合并结果一次写入 `PlayerInstance`。
15. 参与号段的 23 张表，主键 Id 必须落在本表号段内（表号 × 100000 + 1 ~ +99999），超出即导表失败；`CfgGlobal` 用字符串 `ConfigKey`，不在此限。
16. 号段注册表内表号不重复、范围不重叠；全库配置 Id 全局唯一，不得跨表撞号。
17. Id 一经分配不复用、不换表、不回收；停用行保留 Id 并置 `IsEnabled=0`，物理删除需走号段回收审批。
18. 实体引用（外键、JSON 内的属性 Id / 资源 Id / 道具 Id）一律为号段 Id，禁止再出现字符串 Code；枚举码值列（`EffectType` / `ValueType` 等）仍用字符串，不在此限。
19. 抽取规则：`CfgDropEntry` / `CfgStageAffix` 的 `DrawRuleId` 非空时必须在 `CfgDrawRule` 存在且启用；`PityThreshold > 0` 则 `PityStep > 0`；`BagMode` 只能是 pure / shuffle_bag / prd。
20. 随机可复现：`PlayerRunState.RunSeed` 与 `LogStageSettlement.RunSeed` 必填且一致；禁止以服务端当前时间直接作随机源；单测校验同 Seed + 同配置包 + 同操作序列结果一致。
21. 配置版本：仅 `LogStageSettlement` 保留 `ConfigVersion`，必填且能定位到构建产物；其它表禁止存配置版本字段。
22. 存档迁移：6 张 C 层玩家表必须带 `SchemaVersion`，每个历史版本都有可执行迁移入口；新增字段必须有默认值，禁止直接删字段。
23. 道具分流：`usage_type=once` 必须填 `RewardJson` 且加成字段留空，`usage_type=duration` 必须填 `StatModifiersJson` / `DurationSec` 且 `RewardJson` 留空；`RewardJson` 内资源 Id 按号段反查。

**关键 JSON 结构示例**（为便于说明带注释，实际落库需去掉 `//` 注释）

下列示例数值一律为整数；带比例语义的字段（加成值、射程等）按服务端与客户端约定的缩放系数表达，表结构不固化该系数。
JSON 内部数据键保持小写 snake_case（属数据协议，不是表字段）；**所有实体标识均为号段 Id**，不再出现字符串键。

```jsonc
// 数据键一律小写 snake_case：stat_id / value_type / value / resource_id / item_id
// CfgHero.BaseStatsJson / CfgMonster.BaseStatsJson（数组，属性 Id 取 6 号段）
[ { "stat_id": 600001, "value": 1200 }, { "stat_id": 600002, "value": 40 },
  { "stat_id": 600003, "value": 85 },   { "stat_id": 600004, "value": 1500 },
  { "stat_id": 600005, "value": 800 },  { "stat_id": 600006, "value": 3200 } ]

// CfgWeapon.BaseStatsJson（同为 stat_id / value 数组）
[ { "stat_id": 600007, "value": 30 }, { "stat_id": 600008, "value": 1 },
  { "stat_id": 600009, "value": 1800 } ]

// DictAttackPattern.ShapeParamsJson（扇形示例；结构参数键非实体标识）
{ "range": 6000, "angle": 60, "radius": 0, "pierce_count": 0 }

// CfgItem.StatModifiersJson（usage_type=duration）/ PlayerBuff.StatModifiersJson
[ { "stat_id": 600003, "value_type": "percent", "value": 2000 },
  { "stat_id": 600006, "value_type": "flat", "value": 5000 } ]

// CfgItem.RewardJson（usage_type=once，道具即资源聚合包：一次发金币 + 经验 + 体力）
{ "resources": [ { "resource_id": 300001, "value": 500 },
                 { "resource_id": 300002, "value": 120 },
                 { "resource_id": 300003, "value": 30 } ] }

// CfgDropEntry 一行（号段 Id 自带产出类型，无需判别列；21 号段 = 道具）
{ "item_id": 2100001, "min_count": 1, "max_count": 3, "weight": 100, "is_guaranteed": 0 }

// LogStageSettlement.RewardJson（3 号段 = 资源，21 = 道具，22 = 词条）
{ "resources": [ { "resource_id": 300001, "value": 320 } ],
  "items": [ { "item_id": 2100001, "count": 2 } ],
  "affixes": [ { "affix_id": 2200001, "value": 800 } ] }
```

**四条核心数据链路**

1. 属性链路：`CfgStatDef`（含 `StatGroup` 枚举）→ `CfgAffix` / `CfgLevelUpEffect` → 实体最终属性。
   所有伤害计算只认 `CfgStatDef.StatId`（号段 Id），不在业务代码里写死「血量」这类中文或裸字段名；导表时生成 `StatId.Xxx` 常量供代码引用。
2. 关卡链路：`CfgStage` → `CfgStageMonster` / `CfgStageAffix` / `CfgDropEntry` → 结算 → `LogStageSettlement` → 玩家资源。
3. 养成链路：`CfgWeapon` / `CfgVehicle` → `CfgLevelUp` + `CfgLevelUpCost` / `CfgLevelUpEffect` → `PlayerInstance` → `PlayerInstanceSlot` → `PlayerLoadout` → `PlayerBuff` → `LogBuffExpired`；
   持续性道具在 `CfgItem`（`UsageType=duration`）定义加成，生效后快照进 `PlayerBuff`。
   配置只提供模板，玩家持有的一切数值都落在实例与 buff 快照里，配置热更不会改写已有存档。
4. 局内链路：`PlayerLoadout` → `PlayerRunState`（开局按 `ManaCap` 初始化能量）→ 技能消耗 → `LogStageSettlement`（能量清零、体力已在进关时扣除）。
   账号级体力与局内能量在这条链上彻底分流，任何一边的变化都不会串到另一边。

"""


def build_md():
    p = [HEADER]

    p.append("## 1. 命名与翻译口径\n\n")
    p.append("本版只描述服务端表结构：**名称不承载多语言，只留一列 Name 作内部备注；不设字符串 Code，程序键一律用号段 Id，客户端按 Id 自行本地化**。每个表名、字段名、全局配置键都给出可直接进代码的英文标识符。\n\n")
    p.append("命名采用**纯大驼峰（PascalCase）**：语义键按 `_` 切分，每个单词首字母大写后直接拼接；"
             "**不加小写类型前缀、不删词、不改词**。\n\n")
    p.append(md_table(["中文语义", "落地标识符", "类别"], NAMING_EXAMPLES) + "\n\n")
    p.append("字典 / 配置层 23 张实体表主键采用**号段 Id**：`Id = 表号 × 100000 + 序号`，`Id // 100000` 即表号，"
             "可反查所属表；`CfgGlobal` KV 表主键仍为 `ConfigKey`，不占号段。全库不设字符串 Code 列，完整分配见第 8 节注册表。\n\n")
    p.append("- 表名：层前缀（Dict / Cfg / Player / Log）+ 大驼峰实体，如 `CfgStatDef`、`PlayerRunState`。\n")
    p.append("- 字段与配置键：逐词首字母大写，如 `PlayerId`、`IsEnabled`、`StatModifiersJson`。\n")
    p.append("- **不加前缀的两类**：枚举码值（`hero`、`vehicle`、`unlock_stat`）与 JSON 内部数据键（`pierce_count`、`tick_ms`）"
             "属于数据而非标识符，保持小写 snake_case。\n")
    p.append("- 开关：脚本 `USE_TYPE_PREFIX = False` 为当前纯大驼峰方案；改为 `True` 重跑，"
             "全部标识符切回带类型前缀的匈牙利驼峰版（整数 n、字符串 sz、布尔 b、表名 tb 等前缀，前缀字典见脚本 `HUNGARIAN_PREFIXES`）。\n\n")
    p.append("转换由脚本统一按语义键完成，导表脚本与本文档共用同一份映射。\n\n")

    p.append("## 2. 中英术语对照表\n\n")
    p.append(md_table(["原稿中文", "英文定名", "说明", "处理"], GLOSSARY) + "\n\n")

    p.append("## 3. 原稿问题诊断\n\n")
    p.append("P0 = 不修会落不了库或算不出数值；P1 = 结构性问题，越早改成本越低；P2 = 规范与可维护性。\n\n")
    p.append(md_table(["级别", "位置", "问题", "本版处理"], DIAGNOSIS) + "\n\n")

    p.append("## 4. 原表 → 规整后映射\n\n")
    p.append(md_table(["原稿表", "规整后", "动作", "说明"], MAPPING) + "\n\n")

    p.append("## 5. 分层总览\n\n")
    p.append(md_table(["层", "名称", "前缀", "定位"], LAYERS) + "\n\n")
    overview = []
    for code, name, prefix, _ in LAYERS:
        for t in TABLES:
            if t["layer"] == code:
                overview.append((code, t["cn"], "`" + t["en"] + "`", "`" + t["pk"] + "`",
                                 t["origin"], t["purpose"]))
    p.append("原稿 22 张 → 规整后 **%d 张**（新增关系子表、升级明细表、局内状态表与结算记录，重复结构合并）。\n\n" % len(TABLES))
    p.append(md_table(["层", "中文表名", "英文表名", "主键", "来源", "用途"], overview) + "\n\n")
    p.append("其中字典层与配置层共 **%d 张实体表**采用号段 Id 主键（注册表见第 8 节）；"
             "其余 **%d 张表**（8 张运行时 / 记录表 + `CfgGlobal` KV 表）不占号段。\n\n"
             % (len(ID_SEGMENT_TABLES), len(TABLES) - len(ID_SEGMENT_TABLES)))

    p.append("## 6. 字段定义（逐表）\n\n")
    p.append("字段表列：英文名 / 中文名 / 类型 / 必填（Y 必填 N 可选） / 说明与约束。"
             "类型按通用 SQL 书写，MySQL 8.x 与 PostgreSQL 均可直接映射。\n\n")
    for code, name, prefix, desc in LAYERS:
        p.append("### %s. %s `%s`\n\n%s。\n\n" % (code, name, prefix, desc))
        for t in TABLES:
            if t["layer"] != code:
                continue
            p.append("#### `%s` · %s\n\n" % (t["en"], t["cn"]))
            p.append("- 用途：%s\n" % t["purpose"])
            p.append("- 来源：%s\n" % t["origin"])
            p.append("- 主键：`%s`；外键：%s\n" % (t["pk"], t["fk"]))
            p.append("- 调整：%s\n\n" % t["change"])
            p.append(md_table(["英文名", "中文名", "类型", "必填", "说明 / 约束"], t["fields"]) + "\n\n")

    p.append("## 7. 全局配置表默认值清单\n\n")
    p.append("原稿「全局配置表」的 9 个字段改为 `cfg_global` 的若干行（另补能量与防溢出项）。"
             "「待确认」表示原稿未给数值，需要拍板后填入；配置键同样采用纯大驼峰。\n\n")
    p.append(md_table(["ConfigKey", "中文名", "ValueType", "默认 / 建议值", "说明"], GLOBAL_KEYS) + "\n\n")
    p.append("### 资源定义表初始数据\n\n")
    p.append("Scope 区分账号级资源与关卡内资源，避免把能量误当成第二套体力；资源 Id 取 3 号段（`dict_resource`）。\n\n")
    p.append(md_table(["Id（号段）", "中文名", "Scope", "说明"], RESOURCE_ROWS) + "\n\n")

    p.append(NORM_TEXT.replace("{{ID_SEGMENTS}}",
                              md_table(["表号", "Id 范围", "中文表名", "英文表名", "主键"],
                                       id_segment_rows()) + "\n"))

    p.append("## 9. 落地优先级建议\n\n")
    p.append("完整集 %d 张对早期原型偏重。建议先落 **最小可用集 %d 张**，"
             "跑通一局「选英雄 → 进关卡 → 掉词条 → 结算 → 存档」再逐步补齐。\n\n"
             % (len(TABLES), len(MVP_TABLES)))
    p.append(md_table(["序号", "英文表名", "为什么先做"],
                      [(i + 1, "`" + en + "`", why) for i, (en, why) in enumerate(MVP_TABLES)]) + "\n\n")
    p.append("第二梯队：`cfg_draw_rule`（保底 / 洗牌袋，与掉落和词条池同批落）、`cfg_level_up` + `cfg_level_up_cost` + `cfg_level_up_effect`（升级养成）、`cfg_effect_def`（Debuff）、"
             "`player_buff` + `log_buff_expired`（增益；道具定义在 `cfg_item`）。\n\n")
    p.append("第三梯队：`dict_terrain_type` / `cfg_vehicle_slot` / `dict_slot_type` / `player_instance_slot`（地形与槽位细化）、"
             "`cfg_stage_affix`（局内词条池）、各类字典的备注名与排序字段。\n\n")
    p.append("如果团队规模小、配置量不大，也可以把 `cfg_stage_monster` / `cfg_stage_affix` / `cfg_drop_entry` "
             "暂时退回 JSON 列，但**必须保留第 8 节的 CI 校验**，否则后期拆表会很痛。\n\n")

    p.append("## 10. 待确认清单\n\n")
    p.append(md_table(["#", "需要 bighat 拍板的问题"],
                      [(i + 1, q) for i, q in enumerate(OPEN_QUESTIONS)]) + "\n\n")

    p.append("---\n\n*本版为结构规整与命名翻译，未改动任何数值平衡。所有「待确认」项确认后，"
             "改 `build_schema_docs.py` 重跑即可同步 Markdown / XLSX 两份交付物。*\n")
    return apply_naming("".join(p))



# ---------- 导表工作簿：一表一 sheet，四行表头 ----------
SHEET_NAME_SEP = "_"   # 表中文名与英文名之间的分隔符；设为 "" 即直接连写
DESC_MODE = "full"     # full = 必填/主键/说明/来源合并；note = 仅写说明/约束
TAB_COLORS = {"A": "4472C4", "B": "548235", "C": "ED7D31", "D": "7F7F7F"}
ROW_FILLS = ["DCE6F1", "EDF2F9", "F5F5F5", "FFF8E7"]
INDEX_SHEET = "目录_Index"
SEGMENT_SHEET = "号段分配_IdSegment"
LAYER_NAMES = {c: n for c, n, _, _ in LAYERS}
INDEX_LAYER_FILLS = {"A": "E6F1FB", "B": "EAF3DE", "C": "FAEEDA", "D": "F1EFE8"}


def _sheet_name(cn_table, en_table):
    name = cn_table + SHEET_NAME_SEP + en_table
    name = re.sub(r"[:\\/\?\*\[\]]", "_", name)
    return name[:31]                      # Excel sheet 名上限 31 字符


def _field_desc(t, f, cn_table):
    en, cn, dtype, req, note = f
    note = apply_naming(note)
    if DESC_MODE == "note":
        return note
    parts = ["必填：%s" % (req or "N")]
    is_pk = en == t["pk"]
    if is_pk:
        parts.append("主键")
    if note and not (is_pk and note.strip() == "主键"):
        parts.append("说明：%s" % note)
    origin = apply_naming(t["origin"])
    if origin and origin != cn_table:
        parts.append("来源：%s" % origin)
    return "；".join(parts)


def build_xlsx_data():
    """返回 [(sheet 名, 层级, [第1行..第4行]), ...]，与 CSV 同一数据源。"""
    out, seen = [], set()
    for t in TABLES:
        cn_t, en_t = apply_naming(t["cn"]), apply_naming(t["en"])
        rows = [
            [apply_naming(f[1]) for f in t["fields"]],   # 1 字段中文名
            [apply_naming(f[0]) for f in t["fields"]],   # 2 字段名（纯大驼峰）
            [apply_naming(f[2]) for f in t["fields"]],   # 3 数据类型
            [_field_desc(t, f, cn_t) for f in t["fields"]],  # 4 其它描述
        ]
        name, base, k = _sheet_name(cn_t, en_t), _sheet_name(cn_t, en_t), 2
        while name in seen:                              # 截断后去重
            suffix = "_%d" % k
            name = base[:31 - len(suffix)] + suffix
            k += 1
        seen.add(name)
        out.append((name, t["layer"], rows))
    return out


def build_segment_sheet(sheets):
    """号段分配页：一行一个表号，J 列「打开 →」跳到对应表页；A1 返回目录。"""
    sheet_by_en = {t["en"]: item[0] for t, item in zip(TABLES, sheets)}
    rows = [
        ["号段 Id 分配表 · Id = 表号 × 100000 + 序号；点「打开 →」跳到对应表，A1 返回目录",
         "", "", "", "", "", "", "", "", "", ""],
        ["分配规则",
         "23 张实体表连续占 1~23（删表后已重排）；CfgGlobal 不占号段，新增实体表从 24 起；Id 不复用；点表号反查目标表",
         "", "", "", "", "", "", "", "", ""],
        ["表号", "起始 Id", "结束 Id", "单表容量", "层级", "中文表名", "英文表名",
         "主键", "字段数", "跳转", "用途"],
    ]
    links = [("A1", "'%s'!A1" % INDEX_SHEET, "返回目录")]
    for i, t in enumerate(ID_SEGMENT_TABLES, 1):
        no = seg_no(t, i)
        low = no * ID_SEGMENT_SIZE + 1
        high = (no + 1) * ID_SEGMENT_SIZE - 1
        rows.append([str(no), str(low), str(high), str(ID_SEGMENT_SIZE - 1),
                     LAYER_NAMES.get(t["layer"], t["layer"]),
                     apply_naming(t["cn"]), apply_naming(t["en"]),
                     apply_naming(t["pk"]), str(len(t["fields"])), "打开 →",
                     apply_naming(t["purpose"])])
        links.append(("J%d" % (i + 3), "'%s'!A1" % sheet_by_en[t["en"]],
                      "打开 %s" % t["cn"]))
    rows.append(["—", "—", "—", "—", "配置层", "全局配置表", "CfgGlobal",
                 "ConfigKey", "", "", "KV 表，主键为字符串 ConfigKey，不占号段"])
    rows.append(["—", "—", "—", "—", "运行时层", "8 张玩家 / 局内 / 日志表",
                 "Player* / Log*", "自增 Id", "", "",
                 "不占号段；新增实体表从表号 25 起申请，已分配号段一律不动"])
    return (SEGMENT_SHEET, "SEGMENT", rows, links)


def build_xlsx_payload(sheets):
    """返回 [(sheet 名, 层级, rows, 超链接列表), ...]，首项为可点击目录页。

    超链接为 Excel 内部跳转（location = "'sheet 名'!A1"），不产生外部关系文件。
    目录页：F 列跳到各表；第 2 行 B~E 跳到四层第一张表，F2 跳到号段分配页。
    数据表页：A1 跳回目录，单元格内容仍是字段中文名，不破坏四行表头。
    """
    idx_rows = [
        ["数值表目录 · 点击「打开」跳转到对应表；F2 打开号段分配表；每个表页的 A1 可返回目录", "", "", "", "", "", ""],
        ["快捷跳转", "A 字典层", "B 配置层", "C 运行时层", "D 记录层", "号段分配 →", "共 %d 张表" % len(sheets)],
        ["序号", "层级", "表中文名", "表英文名", "字段数", "跳转", "用途"],
    ]
    idx_links = []
    first_of_layer = {}
    for name, layer, _ in sheets:
        first_of_layer.setdefault(layer, name)
    idx_links.append(("F2", "'%s'!A1" % SEGMENT_SHEET, "打开号段分配表"))
    for ref, code in zip(["B2", "C2", "D2", "E2"], ["A", "B", "C", "D"]):
        if code in first_of_layer:
            idx_links.append((ref, "'%s'!A1" % first_of_layer[code],
                              "跳到 %s 第一张表" % LAYER_NAMES[code]))
    for i, (t, item) in enumerate(zip(TABLES, sheets), 1):
        name, layer, rows = item
        idx_rows.append([str(i), LAYER_NAMES.get(layer, layer), apply_naming(t["cn"]),
                         apply_naming(t["en"]), str(len(rows[0])), "打开 →",
                         apply_naming(t["purpose"])])
        idx_links.append(("F%d" % (i + 3), "'%s'!A1" % name, "打开 %s" % t["cn"]))
    payload = [(INDEX_SHEET, "INDEX", idx_rows, idx_links),
               build_segment_sheet(sheets)]
    for name, layer, rows in sheets:
        payload.append((name, layer, rows,
                        [("A1", "'%s'!A1" % INDEX_SHEET, "返回目录")]))
    return payload


def _write_xlsx_openpyxl(path, sheets):
    from openpyxl import Workbook
    from openpyxl.styles import Alignment, Font, PatternFill
    from openpyxl.utils import get_column_letter
    from openpyxl.worksheet.hyperlink import Hyperlink

    wb = Workbook()
    wb.remove(wb.active)
    for name, layer, rows, links in build_xlsx_payload(sheets):
        ws = wb.create_sheet(title=name)
        if layer == "SEGMENT":
            ws.sheet_properties.tabColor = "7030A0"
            for ri, row in enumerate(rows, 1):
                for ci, val in enumerate(row, 1):
                    if val == "":
                        continue
                    c = ws.cell(row=ri, column=ci, value=val)
                    c.alignment = Alignment(horizontal="left", vertical="center",
                                            wrap_text=(ri == 2))
                    if ri == 1:
                        c.font = Font(bold=True, size=12, color="4C1D95")
                    elif ri == 2:
                        c.font = Font(size=10, color="333333")
                        c.fill = PatternFill("solid", fgColor="F3EAFB")
                    elif ri == 3:
                        c.font = Font(bold=True, size=10, color="FFFFFF")
                        c.fill = PatternFill("solid", fgColor="7030A0")
                    else:
                        c.font = Font(size=10, color="333333")
                if 3 < ri <= 3 + len(ID_SEGMENT_TABLES):
                    code = next((c for c, n, _, _ in LAYERS if n == str(row[4])), None)
                    if code:
                        ws.cell(row=ri, column=5).fill = PatternFill(
                            "solid", fgColor=INDEX_LAYER_FILLS[code])
            for ref, loc, tip in links:
                c = ws[ref]
                c.hyperlink = Hyperlink(ref=ref, location=loc, tooltip=tip)
                if ref != "A1":
                    c.font = Font(size=10, color="0563C1", underline="single")
            ws.freeze_panes = "A4"
            ws.auto_filter.ref = "A3:K%d" % (3 + len(ID_SEGMENT_TABLES))
            for ci, w in enumerate([10, 12, 12, 12, 14, 24, 28, 18, 10, 10, 62], 1):
                ws.column_dimensions[get_column_letter(ci)].width = w
            for ri, h in [(1, 28), (2, 26), (3, 22)]:
                ws.row_dimensions[ri].height = h
            continue
        if layer == "INDEX":
            ws.sheet_properties.tabColor = "1F3864"
            for ri, row in enumerate(rows, 1):
                for ci, val in enumerate(row, 1):
                    if val == "":
                        continue                      # 留空以便标题溢出显示
                    c = ws.cell(row=ri, column=ci, value=val)
                    c.alignment = Alignment(horizontal="left", vertical="center")
                    if ri == 1:
                        c.font = Font(bold=True, size=12, color="0C447C")
                    elif ri == 3:
                        c.font = Font(bold=True, size=10, color="1F3864")
                        c.fill = PatternFill("solid", fgColor="DCE6F1")
                    else:
                        c.font = Font(size=10, color="333333")
                if ri > 3:
                    code = next((c for c, n, _, _ in LAYERS if n == str(row[1])), None)
                    if code:
                        ws.cell(row=ri, column=2).fill = PatternFill(
                            "solid", fgColor=INDEX_LAYER_FILLS[code])
            for ref, loc, tip in links:
                c = ws[ref]
                c.hyperlink = Hyperlink(ref=ref, location=loc, tooltip=tip)
                c.font = Font(size=10, color="0563C1", underline="single",
                              bold=(ref in ("B2", "C2", "D2", "E2")))
            ws.freeze_panes = "A4"
            ws.auto_filter.ref = "A3:G%d" % len(rows)
            for ci, w in enumerate([6, 14, 24, 28, 9, 10, 62], 1):
                ws.column_dimensions[get_column_letter(ci)].width = w
            for ri, h in [(1, 28), (2, 24), (3, 22)]:
                ws.row_dimensions[ri].height = h
            continue
        ws.sheet_properties.tabColor = TAB_COLORS.get(layer, "4472C4")
        for ri, row in enumerate(rows, 1):
            fill = PatternFill("solid", fgColor=ROW_FILLS[ri - 1])
            for ci, val in enumerate(row, 1):
                c = ws.cell(row=ri, column=ci, value=val)
                c.fill = fill
                c.font = Font(bold=(ri <= 2), size=10,
                              color="1F3864" if ri <= 2 else "333333")
                c.alignment = Alignment(horizontal="left", vertical="center",
                                        wrap_text=(ri == 4))
                if isinstance(val, str) and val.startswith(("=", "+", "-", "@")):
                    c.data_type = "s"                 # 禁止被 Excel 当成公式
        for ref, loc, tip in links:
            c = ws[ref]
            c.hyperlink = Hyperlink(ref=ref, location=loc, tooltip=tip)  # A1 字体不变
        ws.freeze_panes = "A5"                       # 滚动时四行表头常驻
        for ri, h in enumerate([22, 22, 20, 58], 1):
            ws.row_dimensions[ri].height = h
        for ci in range(1, len(rows[0]) + 1):
            width = max(sum(2 if ord(ch) > 127 else 1 for ch in str(row[ci - 1]))
                        for row in rows[:3])
            ws.column_dimensions[get_column_letter(ci)].width = min(46, max(12, width + 3))
    wb.save(path)


def _write_xlsx_minimal(path, sheets):
    """无第三方依赖时的降级实现：标准库直接写 OOXML，数据一致但无样式。"""
    import html
    import zipfile

    def esc(v):
        return html.escape(str(v), quote=True)

    def col(n):
        out = ""
        while n:
            n, r = divmod(n - 1, 26)
            out = chr(65 + r) + out
        return out

    ct = ['<?xml version="1.0" encoding="UTF-8" standalone="yes"?>',
          '<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">',
          '<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>',
          '<Default Extension="xml" ContentType="application/xml"/>',
          '<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>']
    rels = ['<?xml version="1.0" encoding="UTF-8" standalone="yes"?>',
            '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">']
    wb_sheets, files = [], {}
    payload = build_xlsx_payload(sheets)
    for i, (name, _, rows, links) in enumerate(payload, 1):
        ct.append('<Override PartName="/xl/worksheets/sheet%d.xml" '
                  'ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>' % i)
        rels.append('<Relationship Id="rId%d" '
                    'Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" '
                    'Target="worksheets/sheet%d.xml"/>' % (i, i))
        wb_sheets.append('<sheet name="%s" sheetId="%d" r:id="rId%d"/>' % (esc(name), i, i))
        body = []
        for ri, row in enumerate(rows, 1):
            cells = "".join('<c r="%s%d" t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>'
                            % (col(ci), ri, esc(v))
                            for ci, v in enumerate(row, 1) if v != "")
            body.append('<row r="%d">%s</row>' % (ri, cells))
        link_xml = ""
        if links:
            link_xml = "<hyperlinks>" + "".join(
                '<hyperlink ref="%s" location="%s" tooltip="%s"/>' % (ref, esc(loc), esc(tip))
                for ref, loc, tip in links) + "</hyperlinks>"
        files["xl/worksheets/sheet%d.xml" % i] = (
            '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
            '<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">'
            '<sheetData>%s</sheetData>%s</worksheet>' % ("".join(body), link_xml))
    ct.append("</Types>")
    rels.append("</Relationships>")
    root_rels = ('<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
                 '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">'
                 '<Relationship Id="rId1" '
                 'Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" '
                 'Target="xl/workbook.xml"/></Relationships>')
    workbook = ('<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
                '<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" '
                'xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">'
                '<sheets>%s</sheets></workbook>' % "".join(wb_sheets))
    with zipfile.ZipFile(path, "w", zipfile.ZIP_DEFLATED) as z:
        z.writestr("[Content_Types].xml", "".join(ct))
        z.writestr("_rels/.rels", root_rels)
        z.writestr("xl/workbook.xml", workbook)
        z.writestr("xl/_rels/workbook.xml.rels", "".join(rels))
        for k, v in files.items():
            z.writestr(k, v)


def build_xlsx(path=XLSX_PATH, force_minimal=False):
    sheets = build_xlsx_data()
    if force_minimal:
        _write_xlsx_minimal(path, sheets)
        engine = "builtin"
    else:
        try:
            _write_xlsx_openpyxl(path, sheets)
            engine = "openpyxl"
        except ImportError:
            _write_xlsx_minimal(path, sheets)
            engine = "builtin"
    return len(build_xlsx_payload(sheets)), engine


if __name__ == "__main__":
    with open(MD_PATH, "w", encoding="utf-8") as fp:
        fp.write(build_md())
    n = sum(len(t["fields"]) for t in TABLES)
    xlsx_out = XLSX_PATH
    try:
        n_sheets, engine = build_xlsx()
    except PermissionError:
        xlsx_out = os.path.join(BASE, "ROGUE_数值表_分表_new.xlsx")
        n_sheets, engine = build_xlsx(xlsx_out)
        print("  ! 原工作簿被预览 / Excel 占用，已另存为", os.path.basename(xlsx_out))
    print("OK  tables=%d  fields=%d  sheets=%d  index=1  segment=1  xlsx_engine=%s" %
          (len(TABLES), n, n_sheets, engine))
    print("  ->", MD_PATH)
    print("  ->", xlsx_out)

