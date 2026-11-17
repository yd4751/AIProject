-- ============================================================
-- 由 Excel 表设计自动生成 SQL Server 建表脚本
-- 源文件：ROGUE_数值表_分表.xlsx
-- 生成时间：2026-11-17 12:01:20
-- 数据表：32 张；字段：273 个
-- 表名取 sheet 名中的英文名；中文名写为表注释；字段名取第 2 行，中文名写为列注释
-- 类型映射：JSON/TEXT -> NVARCHAR(MAX)，VARCHAR(n) -> NVARCHAR(n)，DATETIME -> DATETIME2(0)
-- ============================================================

SET NOCOUNT ON;
GO


-- ==========================================================
-- 字典层 Dict
-- ==========================================================

-- 地形类型表 / DictTerrainType（6 字段）
CREATE TABLE dbo.DictTerrainType (
    [Id] BIGINT NOT NULL,
    [Name] NVARCHAR(64) NULL,
    [Category] NVARCHAR(32) NOT NULL,
    [Passable] TINYINT NOT NULL,
    [SortOrder] INT NULL,
    [IsEnabled] TINYINT NOT NULL,
    CONSTRAINT [PK_DictTerrainType] PRIMARY KEY CLUSTERED ([Id])
);
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'地形类型表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'DictTerrainType';
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'标识',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'DictTerrainType',
     @level2type = N'COLUMN', @level2name = N'Id';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'名称；仅配置台阅读的内部备注名；程序键用号段 Id，客户端按 Id 本地化',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'DictTerrainType',
     @level2type = N'COLUMN', @level2name = N'Name';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'大类；陆地 / 水域 / 特殊地形，取自固定枚举',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'DictTerrainType',
     @level2type = N'COLUMN', @level2name = N'Category';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'是否可通行；0 否 1 是',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'DictTerrainType',
     @level2type = N'COLUMN', @level2name = N'Passable';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'排序；配置台展示顺序',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'DictTerrainType',
     @level2type = N'COLUMN', @level2name = N'SortOrder';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'启用；0 停用 1 启用',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'DictTerrainType',
     @level2type = N'COLUMN', @level2name = N'IsEnabled';
GO


-- 攻击模式表 / DictAttackPattern（4 字段）
CREATE TABLE dbo.DictAttackPattern (
    [Id] BIGINT NOT NULL,
    [Name] NVARCHAR(64) NULL,
    [ShapeParamsJson] NVARCHAR(MAX) NULL,
    [IsEnabled] TINYINT NOT NULL,
    CONSTRAINT [PK_DictAttackPattern] PRIMARY KEY CLUSTERED ([Id])
);
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'攻击模式表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'DictAttackPattern';
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'标识',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'DictAttackPattern',
     @level2type = N'COLUMN', @level2name = N'Id';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'名称；仅配置台阅读的内部备注名；程序键用号段 Id，客户端按 Id 本地化',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'DictAttackPattern',
     @level2type = N'COLUMN', @level2name = N'Name';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'形状参数模板；射程 range / 夹角 angle / 半径 radius / 穿透数 pierce_count',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'DictAttackPattern',
     @level2type = N'COLUMN', @level2name = N'ShapeParamsJson';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'启用；0 停用 1 启用',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'DictAttackPattern',
     @level2type = N'COLUMN', @level2name = N'IsEnabled';
GO


-- 资源定义表 / DictResource（6 字段）
CREATE TABLE dbo.DictResource (
    [Id] BIGINT NOT NULL,
    [Name] NVARCHAR(64) NULL,
    [Scope] NVARCHAR(16) NOT NULL,
    [CapSource] NVARCHAR(32) NOT NULL,
    [Icon] NVARCHAR(128) NULL,
    [IsEnabled] TINYINT NOT NULL,
    CONSTRAINT [PK_DictResource] PRIMARY KEY CLUSTERED ([Id])
);
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'资源定义表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'DictResource';
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'标识',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'DictResource',
     @level2type = N'COLUMN', @level2name = N'Id';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'名称；仅配置台阅读的内部备注名；程序键用号段 Id，客户端按 Id 本地化',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'DictResource',
     @level2type = N'COLUMN', @level2name = N'Name';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'资源作用域；account 账号级持久 / run 关卡内临时；体力属 account，能量属 run',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'DictResource',
     @level2type = N'COLUMN', @level2name = N'Scope';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'上限来源；global 取全局配置 / player 玩家自定 / none 无上限',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'DictResource',
     @level2type = N'COLUMN', @level2name = N'CapSource';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'图标；资源图标路径',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'DictResource',
     @level2type = N'COLUMN', @level2name = N'Icon';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'启用；0 停用 1 启用',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'DictResource',
     @level2type = N'COLUMN', @level2name = N'IsEnabled';
GO


-- 武器类型表 / DictWeaponType（5 字段）
CREATE TABLE dbo.DictWeaponType (
    [Id] BIGINT NOT NULL,
    [Name] NVARCHAR(64) NULL,
    [OwnerType] NVARCHAR(32) NOT NULL,
    [SortOrder] INT NULL,
    [IsEnabled] TINYINT NOT NULL,
    CONSTRAINT [PK_DictWeaponType] PRIMARY KEY CLUSTERED ([Id])
);
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'武器类型表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'DictWeaponType';
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'标识；来源：武器配置表 类型2（抽出）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'DictWeaponType',
     @level2type = N'COLUMN', @level2name = N'Id';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'名称；仅配置台阅读的内部备注名；程序键用号段 Id，客户端按 Id 本地化；来源：武器配置表 类型2（抽出）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'DictWeaponType',
     @level2type = N'COLUMN', @level2name = N'Name';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'归属；hero 英雄武器 / vehicle 通行器武器；来源：武器配置表 类型2（抽出）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'DictWeaponType',
     @level2type = N'COLUMN', @level2name = N'OwnerType';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'排序；配置台展示顺序；来源：武器配置表 类型2（抽出）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'DictWeaponType',
     @level2type = N'COLUMN', @level2name = N'SortOrder';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'启用；0 停用 1 启用；来源：武器配置表 类型2（抽出）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'DictWeaponType',
     @level2type = N'COLUMN', @level2name = N'IsEnabled';
GO


-- 槽位类型表 / DictSlotType（4 字段）
CREATE TABLE dbo.DictSlotType (
    [Id] BIGINT NOT NULL,
    [Name] NVARCHAR(64) NULL,
    [AllowedEntityTypes] NVARCHAR(MAX) NOT NULL,
    [SortOrder] INT NULL,
    CONSTRAINT [PK_DictSlotType] PRIMARY KEY CLUSTERED ([Id])
);
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'槽位类型表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'DictSlotType';
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'标识；来源：通行器配置表 槽位1-4（抽出）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'DictSlotType',
     @level2type = N'COLUMN', @level2name = N'Id';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'名称；仅配置台阅读的内部备注名；程序键用号段 Id，客户端按 Id 本地化；来源：通行器配置表 槽位1-4（抽出）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'DictSlotType',
     @level2type = N'COLUMN', @level2name = N'Name';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'允许实体类型；字符串数组，装配时校验；来源：通行器配置表 槽位1-4（抽出）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'DictSlotType',
     @level2type = N'COLUMN', @level2name = N'AllowedEntityTypes';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'排序；配置台展示顺序；来源：通行器配置表 槽位1-4（抽出）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'DictSlotType',
     @level2type = N'COLUMN', @level2name = N'SortOrder';
GO


-- ==========================================================
-- 配置层 Cfg
-- ==========================================================

-- 属性定义表 / CfgStatDef（11 字段）
CREATE TABLE dbo.CfgStatDef (
    [StatId] BIGINT NOT NULL,
    [StatGroup] NVARCHAR(16) NOT NULL,
    [Name] NVARCHAR(64) NULL,
    [ValueType] NVARCHAR(32) NOT NULL,
    [Unit] NVARCHAR(16) NULL,
    [MinValue] BIGINT NULL,
    [MaxValue] BIGINT NULL,
    [IsPercent] TINYINT NOT NULL,
    [StackRule] NVARCHAR(32) NOT NULL,
    [IsEnabled] TINYINT NOT NULL,
    [Remark] NVARCHAR(255) NULL,
    CONSTRAINT [PK_CfgStatDef] PRIMARY KEY CLUSTERED ([StatId])
);
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'属性定义表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgStatDef';
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'属性标识；来源：基础属性表 + 武器属性表（合并）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgStatDef',
     @level2type = N'COLUMN', @level2name = N'StatId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'属性分组；base 基础属性 / weapon 武器属性；枚举码值，不单独建表；来源：基础属性表 + 武器属性表（合并）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgStatDef',
     @level2type = N'COLUMN', @level2name = N'StatGroup';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'名称；仅配置台阅读的内部备注名；程序键用号段 Id，客户端按 Id 本地化；来源：基础属性表 + 武器属性表（合并）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgStatDef',
     @level2type = N'COLUMN', @level2name = N'Name';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'数值形态；number / percent / ms / json；来源：基础属性表 + 武器属性表（合并）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgStatDef',
     @level2type = N'COLUMN', @level2name = N'ValueType';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'单位；点 / 发 / 毫秒 / 百分比；来源：基础属性表 + 武器属性表（合并）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgStatDef',
     @level2type = N'COLUMN', @level2name = N'Unit';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'下限；配置校验用；来源：基础属性表 + 武器属性表（合并）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgStatDef',
     @level2type = N'COLUMN', @level2name = N'MinValue';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'上限；配置校验用，减伤率等需与全局上限联动；来源：基础属性表 + 武器属性表（合并）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgStatDef',
     @level2type = N'COLUMN', @level2name = N'MaxValue';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'是否百分比；0 否 1 是；数值按约定比例存整数；来源：基础属性表 + 武器属性表（合并）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgStatDef',
     @level2type = N'COLUMN', @level2name = N'IsPercent';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'叠加规则；add 相加 / mul 相乘 / max 取最大 / replace 覆盖；来源：基础属性表 + 武器属性表（合并）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgStatDef',
     @level2type = N'COLUMN', @level2name = N'StackRule';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'启用；0 停用 1 启用；来源：基础属性表 + 武器属性表（合并）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgStatDef',
     @level2type = N'COLUMN', @level2name = N'IsEnabled';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'备注；公式说明；来源：基础属性表 + 武器属性表（合并）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgStatDef',
     @level2type = N'COLUMN', @level2name = N'Remark';
GO


-- 特殊效果定义表 / CfgEffectDef（9 字段）
CREATE TABLE dbo.CfgEffectDef (
    [EffectId] BIGINT NOT NULL,
    [EffectType] NVARCHAR(32) NOT NULL,
    [Name] NVARCHAR(64) NULL,
    [ValueUnit] NVARCHAR(16) NOT NULL,
    [ParamsJson] NVARCHAR(MAX) NOT NULL,
    [DurationMs] INT NULL,
    [MaxStack] INT NULL,
    [StackRule] NVARCHAR(32) NOT NULL,
    [IsEnabled] TINYINT NOT NULL,
    CONSTRAINT [PK_CfgEffectDef] PRIMARY KEY CLUSTERED ([EffectId])
);
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'特殊效果定义表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgEffectDef';
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'效果标识；来源：特殊效果表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgEffectDef',
     @level2type = N'COLUMN', @level2name = N'EffectId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'效果类型；dot / slow / attack_speed_down / accuracy_down 等；枚举码值；来源：特殊效果表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgEffectDef',
     @level2type = N'COLUMN', @level2name = N'EffectType';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'名称；仅配置台阅读的内部备注名；程序键用号段 Id，客户端按 Id 本地化；来源：特殊效果表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgEffectDef',
     @level2type = N'COLUMN', @level2name = N'Name';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'数值单位；percent / flat / ms；由原效果类型表的默认单位内联；来源：特殊效果表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgEffectDef',
     @level2type = N'COLUMN', @level2name = N'ValueUnit';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'效果参数；效果参数键用小写：Value / DurationMs / tick_ms / target；dot 类必填 tick_ms；来源：特殊效果表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgEffectDef',
     @level2type = N'COLUMN', @level2name = N'ParamsJson';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'持续时间；单位毫秒；-1 表示永久；来源：特殊效果表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgEffectDef',
     @level2type = N'COLUMN', @level2name = N'DurationMs';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'最大层数；配合 StackRule；来源：特殊效果表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgEffectDef',
     @level2type = N'COLUMN', @level2name = N'MaxStack';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'叠层规则；refresh 刷新 / stack 叠层 / independent 独立；原类型表内联；来源：特殊效果表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgEffectDef',
     @level2type = N'COLUMN', @level2name = N'StackRule';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'启用；0 停用 1 启用；来源：特殊效果表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgEffectDef',
     @level2type = N'COLUMN', @level2name = N'IsEnabled';
GO


-- 英雄配置表 / CfgHero（7 字段）
CREATE TABLE dbo.CfgHero (
    [HeroId] BIGINT NOT NULL,
    [Name] NVARCHAR(64) NULL,
    [Description] NVARCHAR(MAX) NULL,
    [BaseStatsJson] NVARCHAR(MAX) NOT NULL,
    [DefaultWeaponId] BIGINT NULL,
    [Icon] NVARCHAR(128) NULL,
    [IsEnabled] TINYINT NOT NULL,
    CONSTRAINT [PK_CfgHero] PRIMARY KEY CLUSTERED ([HeroId])
);
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'英雄配置表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgHero';
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'英雄标识',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgHero',
     @level2type = N'COLUMN', @level2name = N'HeroId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'名称；仅配置台阅读的内部备注名；程序键用号段 Id，客户端按 Id 本地化',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgHero',
     @level2type = N'COLUMN', @level2name = N'Name';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'描述；原「描述」字段，desc 为保留字故改名',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgHero',
     @level2type = N'COLUMN', @level2name = N'Description';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'基础属性；数组：属性 Id + 数值；属性 Id 取 CfgStatDef 的号段 Id',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgHero',
     @level2type = N'COLUMN', @level2name = N'BaseStatsJson';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'默认武器；外键 CfgWeapon',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgHero',
     @level2type = N'COLUMN', @level2name = N'DefaultWeaponId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'图标；资源路径',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgHero',
     @level2type = N'COLUMN', @level2name = N'Icon';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'启用；0 停用 1 启用',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgHero',
     @level2type = N'COLUMN', @level2name = N'IsEnabled';
GO


-- 武器配置表 / CfgWeapon（9 字段）
CREATE TABLE dbo.CfgWeapon (
    [WeaponId] BIGINT NOT NULL,
    [Name] NVARCHAR(64) NULL,
    [OwnerType] NVARCHAR(32) NOT NULL,
    [WeaponTypeId] BIGINT NOT NULL,
    [AttackPatternId] BIGINT NOT NULL,
    [BaseStatsJson] NVARCHAR(MAX) NOT NULL,
    [EffectIdsJson] NVARCHAR(MAX) NULL,
    [Icon] NVARCHAR(128) NULL,
    [IsEnabled] TINYINT NOT NULL,
    CONSTRAINT [PK_CfgWeapon] PRIMARY KEY CLUSTERED ([WeaponId])
);
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'武器配置表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgWeapon';
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'武器标识',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgWeapon',
     @level2type = N'COLUMN', @level2name = N'WeaponId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'名称；仅配置台阅读的内部备注名；程序键用号段 Id，客户端按 Id 本地化',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgWeapon',
     @level2type = N'COLUMN', @level2name = N'Name';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'归属类型；hero / vehicle，原类型1',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgWeapon',
     @level2type = N'COLUMN', @level2name = N'OwnerType';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'武器类型；外键，原类型2',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgWeapon',
     @level2type = N'COLUMN', @level2name = N'WeaponTypeId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'攻击模式；外键 DictAttackPattern',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgWeapon',
     @level2type = N'COLUMN', @level2name = N'AttackPatternId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'武器属性；数组：属性 Id + 数值；属性 Id 取 CfgStatDef 的号段 Id（弹匣 / 连发 / 换弹 / 弹道）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgWeapon',
     @level2type = N'COLUMN', @level2name = N'BaseStatsJson';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'附带效果；CfgEffectDef.EffectId 数组',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgWeapon',
     @level2type = N'COLUMN', @level2name = N'EffectIdsJson';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'图标；资源路径',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgWeapon',
     @level2type = N'COLUMN', @level2name = N'Icon';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'启用；0 停用 1 启用',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgWeapon',
     @level2type = N'COLUMN', @level2name = N'IsEnabled';
GO


-- 通行器配置表 / CfgVehicle（8 字段）
CREATE TABLE dbo.CfgVehicle (
    [VehicleId] BIGINT NOT NULL,
    [Name] NVARCHAR(64) NULL,
    [Description] NVARCHAR(MAX) NULL,
    [TerrainIdsJson] NVARCHAR(MAX) NOT NULL,
    [SlotCount] INT NOT NULL,
    [DefaultWeaponId] BIGINT NULL,
    [Icon] NVARCHAR(128) NULL,
    [IsEnabled] TINYINT NOT NULL,
    CONSTRAINT [PK_CfgVehicle] PRIMARY KEY CLUSTERED ([VehicleId])
);
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'通行器配置表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgVehicle';
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'通行器标识',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgVehicle',
     @level2type = N'COLUMN', @level2name = N'VehicleId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'名称；仅配置台阅读的内部备注名；程序键用号段 Id，客户端按 Id 本地化',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgVehicle',
     @level2type = N'COLUMN', @level2name = N'Name';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'描述；保留字改写',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgVehicle',
     @level2type = N'COLUMN', @level2name = N'Description';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'适配地形；DictTerrainType.Id 数组，替代自由文本',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgVehicle',
     @level2type = N'COLUMN', @level2name = N'TerrainIdsJson';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'槽位数；原稿固定 4，改为可配',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgVehicle',
     @level2type = N'COLUMN', @level2name = N'SlotCount';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'默认武器；外键 CfgWeapon，OwnerType 须为 vehicle',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgVehicle',
     @level2type = N'COLUMN', @level2name = N'DefaultWeaponId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'图标；资源路径',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgVehicle',
     @level2type = N'COLUMN', @level2name = N'Icon';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'启用；0 停用 1 启用',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgVehicle',
     @level2type = N'COLUMN', @level2name = N'IsEnabled';
GO


-- 通行器槽位表 / CfgVehicleSlot（7 字段）
CREATE TABLE dbo.CfgVehicleSlot (
    [Id] BIGINT NOT NULL,
    [VehicleId] BIGINT NOT NULL,
    [SlotIndex] INT NOT NULL,
    [SlotTypeId] BIGINT NOT NULL,
    [AllowedTypeIdsJson] NVARCHAR(MAX) NULL,
    [DefaultItemId] BIGINT NULL,
    [IsLocked] TINYINT NOT NULL,
    CONSTRAINT [PK_CfgVehicleSlot] PRIMARY KEY CLUSTERED ([Id])
);
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'通行器槽位表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgVehicleSlot';
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'标识；来源：通行器配置表 槽位1-4（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgVehicleSlot',
     @level2type = N'COLUMN', @level2name = N'Id';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'通行器标识；外键 CfgVehicle；来源：通行器配置表 槽位1-4（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgVehicleSlot',
     @level2type = N'COLUMN', @level2name = N'VehicleId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'槽位序号；从 1 开始，同 VehicleId 内唯一；来源：通行器配置表 槽位1-4（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgVehicleSlot',
     @level2type = N'COLUMN', @level2name = N'SlotIndex';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'槽位类型；外键 DictSlotType；来源：通行器配置表 槽位1-4（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgVehicleSlot',
     @level2type = N'COLUMN', @level2name = N'SlotTypeId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'允许装配白名单；覆盖槽位类型的默认白名单；来源：通行器配置表 槽位1-4（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgVehicleSlot',
     @level2type = N'COLUMN', @level2name = N'AllowedTypeIdsJson';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'默认装配；初始赠送的武器 / 技能 Id；来源：通行器配置表 槽位1-4（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgVehicleSlot',
     @level2type = N'COLUMN', @level2name = N'DefaultItemId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'初始锁定；0 解锁 1 锁定；来源：通行器配置表 槽位1-4（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgVehicleSlot',
     @level2type = N'COLUMN', @level2name = N'IsLocked';
GO


-- 怪兽配置表 / CfgMonster（10 字段）
CREATE TABLE dbo.CfgMonster (
    [MonsterId] BIGINT NOT NULL,
    [Name] NVARCHAR(64) NULL,
    [Level] INT NOT NULL,
    [Description] NVARCHAR(MAX) NULL,
    [BaseStatsJson] NVARCHAR(MAX) NOT NULL,
    [SkillIdsJson] NVARCHAR(MAX) NULL,
    [AttackPatternId] BIGINT NULL,
    [AiProfile] NVARCHAR(64) NULL,
    [Icon] NVARCHAR(128) NULL,
    [IsEnabled] TINYINT NOT NULL,
    CONSTRAINT [PK_CfgMonster] PRIMARY KEY CLUSTERED ([MonsterId])
);
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'怪兽配置表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgMonster';
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'怪兽标识',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgMonster',
     @level2type = N'COLUMN', @level2name = N'MonsterId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'名称；仅配置台阅读的内部备注名；程序键用号段 Id，客户端按 Id 本地化',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgMonster',
     @level2type = N'COLUMN', @level2name = N'Name';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'基础等级；关卡可通过 CfgStageMonster 覆盖',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgMonster',
     @level2type = N'COLUMN', @level2name = N'Level';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'描述；保留字改写',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgMonster',
     @level2type = N'COLUMN', @level2name = N'Description';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'基础属性；数组：属性 Id + 数值；属性 Id 取 CfgStatDef 的号段 Id',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgMonster',
     @level2type = N'COLUMN', @level2name = N'BaseStatsJson';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'技能列表；CfgSkill.SkillId 数组',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgMonster',
     @level2type = N'COLUMN', @level2name = N'SkillIdsJson';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'攻击模式；外键 DictAttackPattern',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgMonster',
     @level2type = N'COLUMN', @level2name = N'AttackPatternId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'AI 配置；行为树 / 索敌参数标识',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgMonster',
     @level2type = N'COLUMN', @level2name = N'AiProfile';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'图标；资源路径',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgMonster',
     @level2type = N'COLUMN', @level2name = N'Icon';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'启用；0 停用 1 启用',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgMonster',
     @level2type = N'COLUMN', @level2name = N'IsEnabled';
GO


-- 技能配置表 / CfgSkill（9 字段）
CREATE TABLE dbo.CfgSkill (
    [SkillId] BIGINT NOT NULL,
    [Name] NVARCHAR(64) NULL,
    [Description] NVARCHAR(MAX) NULL,
    [CostResourceId] BIGINT NULL,
    [CostValue] BIGINT NULL,
    [CooldownMs] INT NULL,
    [EffectIdsJson] NVARCHAR(MAX) NULL,
    [Icon] NVARCHAR(128) NULL,
    [IsEnabled] TINYINT NOT NULL,
    CONSTRAINT [PK_CfgSkill] PRIMARY KEY CLUSTERED ([SkillId])
);
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'技能配置表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgSkill';
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'技能标识',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgSkill',
     @level2type = N'COLUMN', @level2name = N'SkillId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'名称；仅配置台阅读的内部备注名；程序键用号段 Id，客户端按 Id 本地化',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgSkill',
     @level2type = N'COLUMN', @level2name = N'Name';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'描述；保留字改写',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgSkill',
     @level2type = N'COLUMN', @level2name = N'Description';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'消耗资源；外键 DictResource，固定指向局内能量资源 Id 300004',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgSkill',
     @level2type = N'COLUMN', @level2name = N'CostResourceId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'能量消耗；释放技能扣除的局内蓝量，不扣账号级体力',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgSkill',
     @level2type = N'COLUMN', @level2name = N'CostValue';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'冷却；单位毫秒',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgSkill',
     @level2type = N'COLUMN', @level2name = N'CooldownMs';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'效果列表；CfgEffectDef.EffectId 数组',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgSkill',
     @level2type = N'COLUMN', @level2name = N'EffectIdsJson';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'图标；资源路径',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgSkill',
     @level2type = N'COLUMN', @level2name = N'Icon';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'启用；0 停用 1 启用',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgSkill',
     @level2type = N'COLUMN', @level2name = N'IsEnabled';
GO


-- 升级档位表 / CfgLevelUp（6 字段）
CREATE TABLE dbo.CfgLevelUp (
    [LevelUpId] BIGINT NOT NULL,
    [OwnerId] BIGINT NOT NULL,
    [FromLevel] INT NOT NULL,
    [ToLevel] INT NOT NULL,
    [IsEnabled] TINYINT NOT NULL,
    [Remark] NVARCHAR(255) NULL,
    CONSTRAINT [PK_CfgLevelUp] PRIMARY KEY CLUSTERED ([LevelUpId])
);
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'升级档位表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgLevelUp';
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'档位标识；来源：属性等级表（重构）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgLevelUp',
     @level2type = N'COLUMN', @level2name = N'LevelUpId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'主体标识；号段 Id 自带表号，可反查主体配置表；UNIQUE(OwnerId, FromLevel)；来源：属性等级表（重构）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgLevelUp',
     @level2type = N'COLUMN', @level2name = N'OwnerId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'起始等级；必须等于实体当前等级；来源：属性等级表（重构）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgLevelUp',
     @level2type = N'COLUMN', @level2name = N'FromLevel';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'目标等级；允许大于 FromLevel + 1，服务端合并途经档位一次处理；来源：属性等级表（重构）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgLevelUp',
     @level2type = N'COLUMN', @level2name = N'ToLevel';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'启用；0 停用 1 启用；来源：属性等级表（重构）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgLevelUp',
     @level2type = N'COLUMN', @level2name = N'IsEnabled';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'备注；策划备注，不参与逻辑；来源：属性等级表（重构）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgLevelUp',
     @level2type = N'COLUMN', @level2name = N'Remark';
GO


-- 升级消耗明细表 / CfgLevelUpCost（4 字段）
CREATE TABLE dbo.CfgLevelUpCost (
    [CostId] BIGINT NOT NULL,
    [LevelUpId] BIGINT NOT NULL,
    [CostTargetId] BIGINT NOT NULL,
    [CostValue] BIGINT NOT NULL,
    CONSTRAINT [PK_CfgLevelUpCost] PRIMARY KEY CLUSTERED ([CostId])
);
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'升级消耗明细表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgLevelUpCost';
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'消耗标识；来源：属性等级表（重构）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgLevelUpCost',
     @level2type = N'COLUMN', @level2name = N'CostId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'档位标识；外键 CfgLevelUp，同一次升级共享；来源：属性等级表（重构）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgLevelUpCost',
     @level2type = N'COLUMN', @level2name = N'LevelUpId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'消耗对象；号段 Id 自带表号，可反查资源 / 道具配置；应用层校验存在性；来源：属性等级表（重构）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgLevelUpCost',
     @level2type = N'COLUMN', @level2name = N'CostTargetId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'消耗数量；整数，必须大于 0；来源：属性等级表（重构）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgLevelUpCost',
     @level2type = N'COLUMN', @level2name = N'CostValue';
GO


-- 升级变更明细表 / CfgLevelUpEffect（8 字段）
CREATE TABLE dbo.CfgLevelUpEffect (
    [EffectId] BIGINT NOT NULL,
    [LevelUpId] BIGINT NOT NULL,
    [EffectType] NVARCHAR(32) NOT NULL,
    [StatId] BIGINT NULL,
    [TargetId] BIGINT NULL,
    [Value] BIGINT NULL,
    [ParamsJson] NVARCHAR(MAX) NULL,
    [SortOrder] INT NULL,
    CONSTRAINT [PK_CfgLevelUpEffect] PRIMARY KEY CLUSTERED ([EffectId])
);
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'升级变更明细表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgLevelUpEffect';
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'变更标识；来源：属性等级表（重构）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgLevelUpEffect',
     @level2type = N'COLUMN', @level2name = N'EffectId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'档位标识；外键 CfgLevelUp，同一次升级共享；来源：属性等级表（重构）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgLevelUpEffect',
     @level2type = N'COLUMN', @level2name = N'LevelUpId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'变更类型；stat_add 加值 / stat_set 设值 / stat_rate 按比例 / unlock_stat 新增属性 / unlock_slot 解锁槽位 / unlock_skill 解锁技能 / grant_affix 授予词条；来源：属性等级表（重构）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgLevelUpEffect',
     @level2type = N'COLUMN', @level2name = N'EffectType';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'属性标识；属性类变更必填，外键 CfgStatDef；来源：属性等级表（重构）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgLevelUpEffect',
     @level2type = N'COLUMN', @level2name = N'StatId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'解锁对象；解锁类变更必填，按 EffectType 指向槽位 / 技能 / 词条配置；来源：属性等级表（重构）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgLevelUpEffect',
     @level2type = N'COLUMN', @level2name = N'TargetId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'变更数值；加成量或新增属性初始值；按比例类按约定比例存整数；来源：属性等级表（重构）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgLevelUpEffect',
     @level2type = N'COLUMN', @level2name = N'Value';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'扩展参数；新增属性是否参与后续成长、叠层规则等；来源：属性等级表（重构）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgLevelUpEffect',
     @level2type = N'COLUMN', @level2name = N'ParamsJson';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'执行顺序；同档位内按此顺序应用；来源：属性等级表（重构）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgLevelUpEffect',
     @level2type = N'COLUMN', @level2name = N'SortOrder';
GO


-- 关卡配置表 / CfgStage（11 字段）
CREATE TABLE dbo.CfgStage (
    [StageId] BIGINT NOT NULL,
    [Name] NVARCHAR(64) NULL,
    [Chapter] INT NULL,
    [Difficulty] INT NOT NULL,
    [UnlockStageId] BIGINT NULL,
    [RecommendedPower] BIGINT NULL,
    [MonsterGroupId] BIGINT NOT NULL,
    [AffixGroupId] BIGINT NULL,
    [DropGroupId] BIGINT NOT NULL,
    [StaminaCost] BIGINT NULL,
    [IsEnabled] TINYINT NOT NULL,
    CONSTRAINT [PK_CfgStage] PRIMARY KEY CLUSTERED ([StageId])
);
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'关卡配置表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgStage';
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'关卡标识',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgStage',
     @level2type = N'COLUMN', @level2name = N'StageId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'名称；仅配置台阅读的内部备注名；程序键用号段 Id，客户端按 Id 本地化',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgStage',
     @level2type = N'COLUMN', @level2name = N'Name';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'章节；章节序号',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgStage',
     @level2type = N'COLUMN', @level2name = N'Chapter';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'难度；影响词条池筛选',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgStage',
     @level2type = N'COLUMN', @level2name = N'Difficulty';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'前置关卡；自关联',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgStage',
     @level2type = N'COLUMN', @level2name = N'UnlockStageId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'推荐战力；展示用',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgStage',
     @level2type = N'COLUMN', @level2name = N'RecommendedPower';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'怪物组；关联 CfgStageMonster.GroupId',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgStage',
     @level2type = N'COLUMN', @level2name = N'MonsterGroupId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'词条组；关联 CfgStageAffix.GroupId',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgStage',
     @level2type = N'COLUMN', @level2name = N'AffixGroupId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'掉落组；关联 CfgDropEntry.DropGroupId',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgStage',
     @level2type = N'COLUMN', @level2name = N'DropGroupId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'体力消耗；进入关卡按此值扣除账号级体力 Stamina；与局内能量无关',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgStage',
     @level2type = N'COLUMN', @level2name = N'StaminaCost';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'启用；0 停用 1 启用',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgStage',
     @level2type = N'COLUMN', @level2name = N'IsEnabled';
GO


-- 关卡怪物表 / CfgStageMonster（9 字段）
CREATE TABLE dbo.CfgStageMonster (
    [Id] BIGINT NOT NULL,
    [GroupId] BIGINT NOT NULL,
    [MonsterId] BIGINT NOT NULL,
    [MinLevel] INT NULL,
    [MaxLevel] INT NULL,
    [Count] INT NOT NULL,
    [Wave] INT NOT NULL,
    [SpawnDelayMs] INT NULL,
    [Weight] INT NULL,
    CONSTRAINT [PK_CfgStageMonster] PRIMARY KEY CLUSTERED ([Id])
);
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'关卡怪物表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgStageMonster';
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'标识；来源：关卡配置表「怪物配置」（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgStageMonster',
     @level2type = N'COLUMN', @level2name = N'Id';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'怪物组；与 CfgStage.MonsterGroupId 对应；来源：关卡配置表「怪物配置」（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgStageMonster',
     @level2type = N'COLUMN', @level2name = N'GroupId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'怪兽标识；外键 CfgMonster；来源：关卡配置表「怪物配置」（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgStageMonster',
     @level2type = N'COLUMN', @level2name = N'MonsterId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'最低等级；空则取怪兽基础等级；来源：关卡配置表「怪物配置」（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgStageMonster',
     @level2type = N'COLUMN', @level2name = N'MinLevel';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'最高等级；与 MinLevel 构成随机区间；来源：关卡配置表「怪物配置」（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgStageMonster',
     @level2type = N'COLUMN', @level2name = N'MaxLevel';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'数量；该波次生成数量；来源：关卡配置表「怪物配置」（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgStageMonster',
     @level2type = N'COLUMN', @level2name = N'Count';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'波次；从 1 起；来源：关卡配置表「怪物配置」（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgStageMonster',
     @level2type = N'COLUMN', @level2name = N'Wave';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'出场延迟；单位毫秒；来源：关卡配置表「怪物配置」（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgStageMonster',
     @level2type = N'COLUMN', @level2name = N'SpawnDelayMs';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'权重；随机抽取时使用，非归一化；来源：关卡配置表「怪物配置」（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgStageMonster',
     @level2type = N'COLUMN', @level2name = N'Weight';
GO


-- 关卡词条表 / CfgStageAffix（7 字段）
CREATE TABLE dbo.CfgStageAffix (
    [Id] BIGINT NOT NULL,
    [GroupId] BIGINT NOT NULL,
    [AffixId] BIGINT NOT NULL,
    [Weight] INT NOT NULL,
    [MinDifficulty] INT NULL,
    [MaxRollCount] INT NULL,
    [DrawRuleId] BIGINT NULL,
    CONSTRAINT [PK_CfgStageAffix] PRIMARY KEY CLUSTERED ([Id])
);
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'关卡词条表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgStageAffix';
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'标识；来源：关卡配置表「词条配置」（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgStageAffix',
     @level2type = N'COLUMN', @level2name = N'Id';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'词条组；与 CfgStage.AffixGroupId 对应；来源：关卡配置表「词条配置」（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgStageAffix',
     @level2type = N'COLUMN', @level2name = N'GroupId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'词条标识；外键 CfgAffix；来源：关卡配置表「词条配置」（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgStageAffix',
     @level2type = N'COLUMN', @level2name = N'AffixId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'抽取权重；非归一化，运行时按组内总和归一；来源：关卡配置表「词条配置」（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgStageAffix',
     @level2type = N'COLUMN', @level2name = N'Weight';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'最低难度；低于该难度不进池；来源：关卡配置表「词条配置」（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgStageAffix',
     @level2type = N'COLUMN', @level2name = N'MinDifficulty';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'单局最多出现；防重复；来源：关卡配置表「词条配置」（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgStageAffix',
     @level2type = N'COLUMN', @level2name = N'MaxRollCount';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'抽取规则；外键 CfgDrawRule；空表示纯权重抽取；来源：关卡配置表「词条配置」（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgStageAffix',
     @level2type = N'COLUMN', @level2name = N'DrawRuleId';
GO


-- 掉落明细表 / CfgDropEntry（9 字段）
CREATE TABLE dbo.CfgDropEntry (
    [Id] BIGINT NOT NULL,
    [DropGroupId] BIGINT NOT NULL,
    [ItemId] BIGINT NOT NULL,
    [MinCount] INT NOT NULL,
    [MaxCount] INT NOT NULL,
    [Weight] INT NOT NULL,
    [IsGuaranteed] TINYINT NOT NULL,
    [Remark] NVARCHAR(255) NULL,
    [DrawRuleId] BIGINT NULL,
    CONSTRAINT [PK_CfgDropEntry] PRIMARY KEY CLUSTERED ([Id])
);
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'掉落明细表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgDropEntry';
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'标识；来源：关卡配置表「掉落配置 json」（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgDropEntry',
     @level2type = N'COLUMN', @level2name = N'Id';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'掉落组；与 CfgStage.DropGroupId 对应；来源：关卡配置表「掉落配置 json」（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgDropEntry',
     @level2type = N'COLUMN', @level2name = N'DropGroupId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'产出标识；号段 Id 自带表号，可反查道具 / 资源 / 词条配置；来源：关卡配置表「掉落配置 json」（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgDropEntry',
     @level2type = N'COLUMN', @level2name = N'ItemId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'最小数量；数量下限；来源：关卡配置表「掉落配置 json」（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgDropEntry',
     @level2type = N'COLUMN', @level2name = N'MinCount';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'最大数量；数量上限，等于下限时为固定值；来源：关卡配置表「掉落配置 json」（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgDropEntry',
     @level2type = N'COLUMN', @level2name = N'MaxCount';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'权重；非归一化；同组内按总和计算概率；来源：关卡配置表「掉落配置 json」（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgDropEntry',
     @level2type = N'COLUMN', @level2name = N'Weight';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'是否保底；1 时不参与权重抽取，必掉；来源：关卡配置表「掉落配置 json」（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgDropEntry',
     @level2type = N'COLUMN', @level2name = N'IsGuaranteed';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'备注；概率说明；来源：关卡配置表「掉落配置 json」（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgDropEntry',
     @level2type = N'COLUMN', @level2name = N'Remark';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'抽取规则；外键 CfgDrawRule；空表示纯权重抽取；来源：关卡配置表「掉落配置 json」（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgDropEntry',
     @level2type = N'COLUMN', @level2name = N'DrawRuleId';
GO


-- 道具配置表 / CfgItem（12 字段）
CREATE TABLE dbo.CfgItem (
    [ItemId] BIGINT NOT NULL,
    [Name] NVARCHAR(64) NULL,
    [UsageType] NVARCHAR(16) NOT NULL,
    [ItemType] NVARCHAR(32) NOT NULL,
    [RewardJson] NVARCHAR(MAX) NULL,
    [StatModifiersJson] NVARCHAR(MAX) NULL,
    [DurationSec] INT NULL,
    [Scope] TINYINT NULL,
    [MaxStack] INT NULL,
    [StackLimit] INT NOT NULL,
    [Icon] NVARCHAR(128) NULL,
    [IsEnabled] TINYINT NOT NULL,
    CONSTRAINT [PK_CfgItem] PRIMARY KEY CLUSTERED ([ItemId])
);
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'道具配置表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgItem';
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'道具标识；主键，号段 Id；来源：道具配置表 + 加成道具表（合并）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgItem',
     @level2type = N'COLUMN', @level2name = N'ItemId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'名称；仅配置台阅读的内部备注名；程序键用号段 Id，客户端按 Id 本地化；来源：道具配置表 + 加成道具表（合并）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgItem',
     @level2type = N'COLUMN', @level2name = N'Name';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'使用方式；once 一次性 / duration 持续性；决定读发放内容还是加成字段；来源：道具配置表 + 加成道具表（合并）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgItem',
     @level2type = N'COLUMN', @level2name = N'UsageType';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'道具类型；resource_pack 资源包 / material 材料 / energy 局内回能 / boost 增益；二级业务分类；来源：道具配置表 + 加成道具表（合并）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgItem',
     @level2type = N'COLUMN', @level2name = N'ItemType';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'发放内容；once 必填：资源 Id + 数量数组，一个包可发多种资源；duration 留空；来源：道具配置表 + 加成道具表（合并）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgItem',
     @level2type = N'COLUMN', @level2name = N'RewardJson';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'加成内容；duration 必填：属性 Id / 加成方式 / 数值数组；once 留空；来源：道具配置表 + 加成道具表（合并）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgItem',
     @level2type = N'COLUMN', @level2name = N'StatModifiersJson';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'持续时间；duration 必填：单位秒，-1 表示永久；once 留空；来源：道具配置表 + 加成道具表（合并）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgItem',
     @level2type = N'COLUMN', @level2name = N'DurationSec';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'生效范围；duration 用：0 self 个人 / 1 alliance 联盟 / 2 global 全局；once 留空；来源：道具配置表 + 加成道具表（合并）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgItem',
     @level2type = N'COLUMN', @level2name = N'Scope';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'最大叠层；duration 用：同玩家同时生效层数上限，与背包堆叠上限区分；来源：道具配置表 + 加成道具表（合并）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgItem',
     @level2type = N'COLUMN', @level2name = N'MaxStack';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'堆叠上限；背包堆叠格数上限，0 表示不堆叠；与 buff 生效层数无关；来源：道具配置表 + 加成道具表（合并）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgItem',
     @level2type = N'COLUMN', @level2name = N'StackLimit';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'图标；资源路径；来源：道具配置表 + 加成道具表（合并）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgItem',
     @level2type = N'COLUMN', @level2name = N'Icon';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'启用；0 停用 1 启用；来源：道具配置表 + 加成道具表（合并）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgItem',
     @level2type = N'COLUMN', @level2name = N'IsEnabled';
GO


-- 词条配置表 / CfgAffix（10 字段）
CREATE TABLE dbo.CfgAffix (
    [AffixId] BIGINT NOT NULL,
    [Name] NVARCHAR(64) NULL,
    [StatId] BIGINT NOT NULL,
    [Value] BIGINT NOT NULL,
    [ValueType] NVARCHAR(32) NOT NULL,
    [Rarity] INT NULL,
    [Weight] INT NULL,
    [StackRule] NVARCHAR(32) NOT NULL,
    [Description] NVARCHAR(255) NULL,
    [IsEnabled] TINYINT NOT NULL,
    CONSTRAINT [PK_CfgAffix] PRIMARY KEY CLUSTERED ([AffixId])
);
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'词条配置表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgAffix';
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'词条标识',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgAffix',
     @level2type = N'COLUMN', @level2name = N'AffixId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'名称；仅配置台阅读的内部备注名；程序键用号段 Id，客户端按 Id 本地化',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgAffix',
     @level2type = N'COLUMN', @level2name = N'Name';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'属性标识；外键 CfgStatDef',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgAffix',
     @level2type = N'COLUMN', @level2name = N'StatId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'属性值；百分比类按约定比例存整数',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgAffix',
     @level2type = N'COLUMN', @level2name = N'Value';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'加成方式；flat 固定值 / percent 百分比',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgAffix',
     @level2type = N'COLUMN', @level2name = N'ValueType';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'品质；影响抽取池与展示',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgAffix',
     @level2type = N'COLUMN', @level2name = N'Rarity';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'基础权重；可被 CfgStageAffix 覆盖',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgAffix',
     @level2type = N'COLUMN', @level2name = N'Weight';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'叠层规则；add / mul / max / replace',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgAffix',
     @level2type = N'COLUMN', @level2name = N'StackRule';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'描述；保留字改写',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgAffix',
     @level2type = N'COLUMN', @level2name = N'Description';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'启用；0 停用 1 启用',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgAffix',
     @level2type = N'COLUMN', @level2name = N'IsEnabled';
GO


-- 抽取规则表 / CfgDrawRule（8 字段）
CREATE TABLE dbo.CfgDrawRule (
    [DrawRuleId] BIGINT NOT NULL,
    [Name] NVARCHAR(64) NULL,
    [BagMode] NVARCHAR(16) NOT NULL,
    [PityThreshold] INT NOT NULL,
    [PityStep] INT NOT NULL,
    [MaxRepeat] INT NOT NULL,
    [ExcludeTagsJson] NVARCHAR(MAX) NULL,
    [IsEnabled] TINYINT NOT NULL,
    CONSTRAINT [PK_CfgDrawRule] PRIMARY KEY CLUSTERED ([DrawRuleId])
);
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'抽取规则表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgDrawRule';
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'规则标识；主键，号段 Id；来源：业界实践补充（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgDrawRule',
     @level2type = N'COLUMN', @level2name = N'DrawRuleId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'规则名；仅配置台阅读的内部备注名；来源：业界实践补充（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgDrawRule',
     @level2type = N'COLUMN', @level2name = N'Name';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'抽取模式；pure 纯权重 / shuffle_bag 洗牌袋 / prd 伪随机分布；来源：业界实践补充（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgDrawRule',
     @level2type = N'COLUMN', @level2name = N'BagMode';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'保底阈值；连续未中稀有的次数阈值，0 表示不启用；来源：业界实践补充（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgDrawRule',
     @level2type = N'COLUMN', @level2name = N'PityThreshold';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'保底步长；达到阈值后每次未中增加的权重，万分比整数；来源：业界实践补充（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgDrawRule',
     @level2type = N'COLUMN', @level2name = N'PityStep';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'连续上限；同一条目允许连续出现的最大次数，0 表示不限；来源：业界实践补充（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgDrawRule',
     @level2type = N'COLUMN', @level2name = N'MaxRepeat';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'互斥标签；同一次抽取中互斥的标签数组，如两个 legendary 不共存；来源：业界实践补充（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgDrawRule',
     @level2type = N'COLUMN', @level2name = N'ExcludeTagsJson';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'启用；0 停用 1 启用；来源：业界实践补充（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgDrawRule',
     @level2type = N'COLUMN', @level2name = N'IsEnabled';
GO


-- 全局配置表 / CfgGlobal（8 字段）
CREATE TABLE dbo.CfgGlobal (
    [ConfigKey] NVARCHAR(64) NOT NULL,
    [ConfigValue] NVARCHAR(255) NOT NULL,
    [ValueType] NVARCHAR(16) NOT NULL,
    [Name] NVARCHAR(64) NULL,
    [Description] NVARCHAR(255) NULL,
    [MinValue] BIGINT NULL,
    [MaxValue] BIGINT NULL,
    [UpdatedAt] DATETIME2(0) NOT NULL,
    CONSTRAINT [PK_CfgGlobal] PRIMARY KEY CLUSTERED ([ConfigKey])
);
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'全局配置表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgGlobal';
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'配置键；主键，如 StaminaRegenPerHour；来源：全局配置表（重构）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgGlobal',
     @level2type = N'COLUMN', @level2name = N'ConfigKey';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'配置值；统一以字符串存储，按 ValueType 解析；来源：全局配置表（重构）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgGlobal',
     @level2type = N'COLUMN', @level2name = N'ConfigValue';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'值类型；int / float / bool / json / string；来源：全局配置表（重构）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgGlobal',
     @level2type = N'COLUMN', @level2name = N'ValueType';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'名称；仅配置台阅读的内部备注名；程序键为 ConfigKey；来源：全局配置表（重构）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgGlobal',
     @level2type = N'COLUMN', @level2name = N'Name';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'说明；口径与公式说明；来源：全局配置表（重构）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgGlobal',
     @level2type = N'COLUMN', @level2name = N'Description';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'合法下限；热更校验；来源：全局配置表（重构）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgGlobal',
     @level2type = N'COLUMN', @level2name = N'MinValue';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'合法上限；热更校验；来源：全局配置表（重构）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgGlobal',
     @level2type = N'COLUMN', @level2name = N'MaxValue';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'更新时间；最近一次热更时间；来源：全局配置表（重构）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'CfgGlobal',
     @level2type = N'COLUMN', @level2name = N'UpdatedAt';
GO


-- ==========================================================
-- 运行时层 Player
-- ==========================================================

-- 玩家表 / Player（12 字段）
CREATE TABLE dbo.Player (
    [SchemaVersion] INT NOT NULL,
    [PlayerId] BIGINT NOT NULL,
    [AccountId] NVARCHAR(64) NOT NULL,
    [Nickname] NVARCHAR(64) NULL,
    [Level] INT NOT NULL,
    [Exp] BIGINT NOT NULL,
    [Stamina] BIGINT NOT NULL,
    [StaminaCapSnapshot] BIGINT NOT NULL,
    [CurrentStageId] BIGINT NULL,
    [OfflineAt] DATETIME2(0) NULL,
    [CreatedAt] DATETIME2(0) NOT NULL,
    [UpdatedAt] DATETIME2(0) NOT NULL,
    CONSTRAINT [PK_Player] PRIMARY KEY CLUSTERED ([PlayerId])
);
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'玩家表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'Player';
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'存档结构版本；读档时按迁移注册表升级；新增字段必须有默认值，删字段先标记废弃一个版本',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'Player',
     @level2type = N'COLUMN', @level2name = N'SchemaVersion';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'玩家标识；主键，原表缺失',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'Player',
     @level2type = N'COLUMN', @level2name = N'PlayerId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'账号标识；登录体系关联',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'Player',
     @level2type = N'COLUMN', @level2name = N'AccountId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'昵称；展示名',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'Player',
     @level2type = N'COLUMN', @level2name = N'Nickname';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'等级；玩家等级',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'Player',
     @level2type = N'COLUMN', @level2name = N'Level';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'经验；当前经验',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'Player',
     @level2type = N'COLUMN', @level2name = N'Exp';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'体力；账号级纯数值，只用于进行关卡（进关按 CfgStage.StaminaCost 扣减）；离线恢复，不参与战斗计算',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'Player',
     @level2type = N'COLUMN', @level2name = N'Stamina';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'体力上限快照；来自 CfgGlobal 的 StaminaCap，记录当时值便于对账；与配置冲突时以配置为准',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'Player',
     @level2type = N'COLUMN', @level2name = N'StaminaCapSnapshot';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'当前关卡；外键 CfgStage',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'Player',
     @level2type = N'COLUMN', @level2name = N'CurrentStageId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'离线时间戳；用于离线奖励结算',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'Player',
     @level2type = N'COLUMN', @level2name = N'OfflineAt';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'创建时间；存档创建',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'Player',
     @level2type = N'COLUMN', @level2name = N'CreatedAt';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'更新时间；每次写档刷新',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'Player',
     @level2type = N'COLUMN', @level2name = N'UpdatedAt';
GO


-- 玩家实例表 / PlayerInstance（10 字段）
CREATE TABLE dbo.PlayerInstance (
    [SchemaVersion] INT NOT NULL,
    [InstanceId] BIGINT NOT NULL,
    [PlayerId] BIGINT NOT NULL,
    [EntityType] NVARCHAR(32) NOT NULL,
    [RefId] BIGINT NOT NULL,
    [Level] INT NOT NULL,
    [EnhanceLevel] INT NOT NULL,
    [Exp] BIGINT NULL,
    [IsLocked] TINYINT NOT NULL,
    [AcquiredAt] DATETIME2(0) NOT NULL,
    CONSTRAINT [PK_PlayerInstance] PRIMARY KEY CLUSTERED ([InstanceId])
);
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'玩家实例表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerInstance';
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'存档结构版本；读档时按迁移注册表升级；新增字段必须有默认值，删字段先标记废弃一个版本',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerInstance',
     @level2type = N'COLUMN', @level2name = N'SchemaVersion';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'实例标识',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerInstance',
     @level2type = N'COLUMN', @level2name = N'InstanceId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'玩家标识；外键 Player',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerInstance',
     @level2type = N'COLUMN', @level2name = N'PlayerId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'实体类型；weapon / vehicle；客制化主体',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerInstance',
     @level2type = N'COLUMN', @level2name = N'EntityType';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'配置标识；指向 CfgWeapon 或 CfgVehicle',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerInstance',
     @level2type = N'COLUMN', @level2name = N'RefId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'等级；实例等级，升级时按 CfgLevelUp 的 FromLevel 校验',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerInstance',
     @level2type = N'COLUMN', @level2name = N'Level';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'强化等级；原「强化」列，与等级分开',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerInstance',
     @level2type = N'COLUMN', @level2name = N'EnhanceLevel';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'实例经验；可选',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerInstance',
     @level2type = N'COLUMN', @level2name = N'Exp';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'是否锁定；防止误分解',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerInstance',
     @level2type = N'COLUMN', @level2name = N'IsLocked';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'获得时间；同类可重复持有',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerInstance',
     @level2type = N'COLUMN', @level2name = N'AcquiredAt';
GO


-- 实例槽位表 / PlayerInstanceSlot（7 字段）
CREATE TABLE dbo.PlayerInstanceSlot (
    [SchemaVersion] INT NOT NULL,
    [Id] BIGINT NOT NULL,
    [InstanceId] BIGINT NOT NULL,
    [SlotIndex] INT NOT NULL,
    [SlotTypeId] BIGINT NOT NULL,
    [EquippedInstanceId] BIGINT NULL,
    [IsLocked] TINYINT NOT NULL,
    CONSTRAINT [PK_PlayerInstanceSlot] PRIMARY KEY CLUSTERED ([Id])
);
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'实例槽位表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerInstanceSlot';
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'存档结构版本；读档时按迁移注册表升级；新增字段必须有默认值，删字段先标记废弃一个版本；来源：玩家实例表 槽位1-4（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerInstanceSlot',
     @level2type = N'COLUMN', @level2name = N'SchemaVersion';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'标识；来源：玩家实例表 槽位1-4（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerInstanceSlot',
     @level2type = N'COLUMN', @level2name = N'Id';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'所属实例；外键 PlayerInstance；来源：玩家实例表 槽位1-4（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerInstanceSlot',
     @level2type = N'COLUMN', @level2name = N'InstanceId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'槽位序号；同 InstanceId 内唯一；来源：玩家实例表 槽位1-4（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerInstanceSlot',
     @level2type = N'COLUMN', @level2name = N'SlotIndex';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'槽位类型；外键 DictSlotType，冗余便于校验；来源：玩家实例表 槽位1-4（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerInstanceSlot',
     @level2type = N'COLUMN', @level2name = N'SlotTypeId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'已装配实例；为空表示空槽；来源：玩家实例表 槽位1-4（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerInstanceSlot',
     @level2type = N'COLUMN', @level2name = N'EquippedInstanceId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'是否锁定；随 CfgVehicleSlot 初始锁定状态；来源：玩家实例表 槽位1-4（新增）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerInstanceSlot',
     @level2type = N'COLUMN', @level2name = N'IsLocked';
GO


-- 玩家配置表（配装） / PlayerLoadout（12 字段）
CREATE TABLE dbo.PlayerLoadout (
    [SchemaVersion] INT NOT NULL,
    [LoadoutId] BIGINT NOT NULL,
    [PlayerId] BIGINT NOT NULL,
    [SlotIndex] INT NOT NULL,
    [Name] NVARCHAR(64) NULL,
    [HeroId] BIGINT NOT NULL,
    [VehicleInstanceId] BIGINT NULL,
    [WeaponInstanceIdsJson] NVARCHAR(MAX) NULL,
    [SkillIdsJson] NVARCHAR(MAX) NULL,
    [IsActive] TINYINT NOT NULL,
    [CreatedAt] DATETIME2(0) NOT NULL,
    [UpdatedAt] DATETIME2(0) NOT NULL,
    CONSTRAINT [PK_PlayerLoadout] PRIMARY KEY CLUSTERED ([LoadoutId])
);
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'玩家配置表（配装）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerLoadout';
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'存档结构版本；读档时按迁移注册表升级；新增字段必须有默认值，删字段先标记废弃一个版本；来源：玩家配置表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerLoadout',
     @level2type = N'COLUMN', @level2name = N'SchemaVersion';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'配置标识；来源：玩家配置表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerLoadout',
     @level2type = N'COLUMN', @level2name = N'LoadoutId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'玩家标识；外键 Player；来源：玩家配置表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerLoadout',
     @level2type = N'COLUMN', @level2name = N'PlayerId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'方案序号；从 1 起；默认只有 1 套，由全局配置放开；来源：玩家配置表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerLoadout',
     @level2type = N'COLUMN', @level2name = N'SlotIndex';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'方案名；玩家自定义；来源：玩家配置表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerLoadout',
     @level2type = N'COLUMN', @level2name = N'Name';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'英雄；外键 CfgHero；来源：玩家配置表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerLoadout',
     @level2type = N'COLUMN', @level2name = N'HeroId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'通行器实例；外键 PlayerInstance；来源：玩家配置表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerLoadout',
     @level2type = N'COLUMN', @level2name = N'VehicleInstanceId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'武器实例列表；PlayerInstance.InstanceId 数组；来源：玩家配置表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerLoadout',
     @level2type = N'COLUMN', @level2name = N'WeaponInstanceIdsJson';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'技能列表；CfgSkill.SkillId 数组，受 MaxSkillSlots 限制；来源：玩家配置表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerLoadout',
     @level2type = N'COLUMN', @level2name = N'SkillIdsJson';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'是否启用；同玩家只允许一套 active；来源：玩家配置表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerLoadout',
     @level2type = N'COLUMN', @level2name = N'IsActive';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'创建时间；存档时间；来源：玩家配置表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerLoadout',
     @level2type = N'COLUMN', @level2name = N'CreatedAt';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'更新时间；改装配时刷新；来源：玩家配置表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerLoadout',
     @level2type = N'COLUMN', @level2name = N'UpdatedAt';
GO


-- 加成表 / PlayerBuff（14 字段）
CREATE TABLE dbo.PlayerBuff (
    [SchemaVersion] INT NOT NULL,
    [BuffId] BIGINT NOT NULL,
    [PlayerId] BIGINT NOT NULL,
    [ItemId] BIGINT NOT NULL,
    [RunId] BIGINT NULL,
    [ScopeLayer] NVARCHAR(16) NOT NULL,
    [SourceType] NVARCHAR(32) NOT NULL,
    [SourceId] BIGINT NULL,
    [Scope] TINYINT NOT NULL,
    [StatModifiersJson] NVARCHAR(MAX) NOT NULL,
    [StackCount] INT NOT NULL,
    [StartAt] DATETIME2(0) NOT NULL,
    [EndAt] DATETIME2(0) NOT NULL,
    [Status] NVARCHAR(16) NOT NULL,
    CONSTRAINT [PK_PlayerBuff] PRIMARY KEY CLUSTERED ([BuffId])
);
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'加成表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerBuff';
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'存档结构版本；读档时按迁移注册表升级；新增字段必须有默认值，删字段先标记废弃一个版本',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerBuff',
     @level2type = N'COLUMN', @level2name = N'SchemaVersion';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'加成标识',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerBuff',
     @level2type = N'COLUMN', @level2name = N'BuffId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'玩家标识；外键 Player',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerBuff',
     @level2type = N'COLUMN', @level2name = N'PlayerId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'道具标识；外键 CfgItem，指向持续性道具；用于反查加成数值',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerBuff',
     @level2type = N'COLUMN', @level2name = N'ItemId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'所属局；局内 buff 填 PlayerRunState 的局标识；账号级留空',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerBuff',
     @level2type = N'COLUMN', @level2name = N'RunId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'作用域层；account 账号级 / run 局内；决定结算时是否随关卡清除',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerBuff',
     @level2type = N'COLUMN', @level2name = N'ScopeLayer';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'来源类型；item / stage / skill / gm',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerBuff',
     @level2type = N'COLUMN', @level2name = N'SourceType';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'来源标识；对应来源记录 Id',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerBuff',
     @level2type = N'COLUMN', @level2name = N'SourceId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'生效范围；冗余自 CfgItem，便于结算',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerBuff',
     @level2type = N'COLUMN', @level2name = N'Scope';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'生效加成；快照，配置热更不影响已生效 buff',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerBuff',
     @level2type = N'COLUMN', @level2name = N'StatModifiersJson';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'叠层数；默认 1',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerBuff',
     @level2type = N'COLUMN', @level2name = N'StackCount';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'启动时间；服务端时间',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerBuff',
     @level2type = N'COLUMN', @level2name = N'StartAt';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'结束时间；服务端时间；到期转 LogBuffExpired',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerBuff',
     @level2type = N'COLUMN', @level2name = N'EndAt';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'状态；active / expired / consumed',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerBuff',
     @level2type = N'COLUMN', @level2name = N'Status';
GO


-- 局内状态表 / PlayerRunState（13 字段）
CREATE TABLE dbo.PlayerRunState (
    [RunId] BIGINT NOT NULL,
    [PlayerId] BIGINT NOT NULL,
    [StageId] BIGINT NOT NULL,
    [Mana] BIGINT NOT NULL,
    [ManaCapSnapshot] BIGINT NOT NULL,
    [ActiveAffixIdsJson] NVARCHAR(MAX) NULL,
    [ActiveBuffIdsJson] NVARCHAR(MAX) NULL,
    [Status] NVARCHAR(16) NOT NULL,
    [StartAt] DATETIME2(0) NOT NULL,
    [EndAt] DATETIME2(0) NULL,
    [RunSeed] BIGINT NOT NULL,
    [RngStreamsJson] NVARCHAR(MAX) NULL,
    [SchemaVersion] INT NOT NULL,
    CONSTRAINT [PK_PlayerRunState] PRIMARY KEY CLUSTERED ([RunId])
);
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'局内状态表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerRunState';
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'局标识；主键，一次进入关卡生成一条；来源：新增（承载技能能量 / 蓝量）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerRunState',
     @level2type = N'COLUMN', @level2name = N'RunId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'玩家标识；外键 Player；来源：新增（承载技能能量 / 蓝量）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerRunState',
     @level2type = N'COLUMN', @level2name = N'PlayerId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'关卡标识；外键 CfgStage；来源：新增（承载技能能量 / 蓝量）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerRunState',
     @level2type = N'COLUMN', @level2name = N'StageId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'当前能量；局内蓝量，技能消耗来源；关卡结束时清零；来源：新增（承载技能能量 / 蓝量）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerRunState',
     @level2type = N'COLUMN', @level2name = N'Mana';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'能量上限快照；来自 CfgGlobal 的 ManaCap，记录词条 / buff 修改后的实际上限；来源：新增（承载技能能量 / 蓝量）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerRunState',
     @level2type = N'COLUMN', @level2name = N'ManaCapSnapshot';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'已选词条；本局获得的 CfgAffix.AffixId 数组；来源：新增（承载技能能量 / 蓝量）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerRunState',
     @level2type = N'COLUMN', @level2name = N'ActiveAffixIdsJson';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'生效加成；本局生效的 PlayerBuff.BuffId 数组；来源：新增（承载技能能量 / 蓝量）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerRunState',
     @level2type = N'COLUMN', @level2name = N'ActiveBuffIdsJson';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'局状态；running / win / fail / quit；来源：新增（承载技能能量 / 蓝量）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerRunState',
     @level2type = N'COLUMN', @level2name = N'Status';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'开始时间；服务端时间；来源：新增（承载技能能量 / 蓝量）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerRunState',
     @level2type = N'COLUMN', @level2name = N'StartAt';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'结束时间；结算时写入；来源：新增（承载技能能量 / 蓝量）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerRunState',
     @level2type = N'COLUMN', @level2name = N'EndAt';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'局随机种子；开局生成并存档，用于复现本局；禁止用当前时间直接当种子；来源：新增（承载技能能量 / 蓝量）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerRunState',
     @level2type = N'COLUMN', @level2name = N'RunSeed';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'随机流状态；按命名空间记录 stage / affix / drop 各流游标，支持断点续玩与复现；来源：新增（承载技能能量 / 蓝量）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerRunState',
     @level2type = N'COLUMN', @level2name = N'RngStreamsJson';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'存档结构版本；读档时按迁移注册表升级；新增字段必须有默认值，删字段先标记废弃一个版本；来源：新增（承载技能能量 / 蓝量）',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'PlayerRunState',
     @level2type = N'COLUMN', @level2name = N'SchemaVersion';
GO


-- ==========================================================
-- 记录层 Log
-- ==========================================================

-- 加成过期记录表 / LogBuffExpired（8 字段）
CREATE TABLE dbo.LogBuffExpired (
    [Id] BIGINT NOT NULL,
    [BuffId] BIGINT NOT NULL,
    [PlayerId] BIGINT NOT NULL,
    [ItemId] BIGINT NOT NULL,
    [StartAt] DATETIME2(0) NOT NULL,
    [EndAt] DATETIME2(0) NOT NULL,
    [EffectiveSec] INT NOT NULL,
    [CreatedAt] DATETIME2(0) NOT NULL,
    CONSTRAINT [PK_LogBuffExpired] PRIMARY KEY CLUSTERED ([Id])
);
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'加成过期记录表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'LogBuffExpired';
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'标识；来源：加成表注释「结束后可转到记录表」',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'LogBuffExpired',
     @level2type = N'COLUMN', @level2name = N'Id';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'加成标识；原 PlayerBuff.BuffId；来源：加成表注释「结束后可转到记录表」',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'LogBuffExpired',
     @level2type = N'COLUMN', @level2name = N'BuffId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'玩家标识；归属玩家；来源：加成表注释「结束后可转到记录表」',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'LogBuffExpired',
     @level2type = N'COLUMN', @level2name = N'PlayerId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'道具标识；快照，CfgItem.ItemId；来源：加成表注释「结束后可转到记录表」',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'LogBuffExpired',
     @level2type = N'COLUMN', @level2name = N'ItemId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'启动时间；快照；来源：加成表注释「结束后可转到记录表」',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'LogBuffExpired',
     @level2type = N'COLUMN', @level2name = N'StartAt';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'结束时间；快照；来源：加成表注释「结束后可转到记录表」',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'LogBuffExpired',
     @level2type = N'COLUMN', @level2name = N'EndAt';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'实际生效秒数；提前消耗时小于标称时长；来源：加成表注释「结束后可转到记录表」',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'LogBuffExpired',
     @level2type = N'COLUMN', @level2name = N'EffectiveSec';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'归档时间；写入时间；来源：加成表注释「结束后可转到记录表」',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'LogBuffExpired',
     @level2type = N'COLUMN', @level2name = N'CreatedAt';
GO


-- 关卡结算记录表 / LogStageSettlement（10 字段）
CREATE TABLE dbo.LogStageSettlement (
    [Id] BIGINT NOT NULL,
    [PlayerId] BIGINT NOT NULL,
    [RunId] BIGINT NOT NULL,
    [StageId] BIGINT NOT NULL,
    [Result] NVARCHAR(16) NOT NULL,
    [DurationSec] INT NOT NULL,
    [RewardJson] NVARCHAR(MAX) NOT NULL,
    [CreatedAt] DATETIME2(0) NOT NULL,
    [RunSeed] BIGINT NOT NULL,
    [ConfigVersion] NVARCHAR(32) NOT NULL,
    CONSTRAINT [PK_LogStageSettlement] PRIMARY KEY CLUSTERED ([Id])
);
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'关卡结算记录表',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'LogStageSettlement';
GO

EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'标识；来源：建议补充',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'LogStageSettlement',
     @level2type = N'COLUMN', @level2name = N'Id';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'玩家标识；归属玩家；来源：建议补充',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'LogStageSettlement',
     @level2type = N'COLUMN', @level2name = N'PlayerId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'局标识；外键 PlayerRunState，串起本局能量与词条；来源：建议补充',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'LogStageSettlement',
     @level2type = N'COLUMN', @level2name = N'RunId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'关卡标识；外键 CfgStage；来源：建议补充',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'LogStageSettlement',
     @level2type = N'COLUMN', @level2name = N'StageId';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'结果；win / fail / quit；来源：建议补充',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'LogStageSettlement',
     @level2type = N'COLUMN', @level2name = N'Result';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'耗时；单位秒；来源：建议补充',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'LogStageSettlement',
     @level2type = N'COLUMN', @level2name = N'DurationSec';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'奖励快照；实际发放的道具 / 资源 / 词条，均用号段 Id；来源：建议补充',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'LogStageSettlement',
     @level2type = N'COLUMN', @level2name = N'RewardJson';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'结算时间；服务端时间；来源：建议补充',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'LogStageSettlement',
     @level2type = N'COLUMN', @level2name = N'CreatedAt';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'局随机种子；从 PlayerRunState 快照，便于按种子复现本局；来源：建议补充',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'LogStageSettlement',
     @level2type = N'COLUMN', @level2name = N'RunSeed';
GO
EXEC sys.sp_addextendedproperty @name = N'MS_Description',
     @value = N'配置版本；本局生效的配置包版本，随配置变更生成，用于投诉复盘；来源：建议补充',
     @level0type = N'SCHEMA', @level0name = N'dbo',
     @level1type = N'TABLE',  @level1name = N'LogStageSettlement',
     @level2type = N'COLUMN', @level2name = N'ConfigVersion';
GO
