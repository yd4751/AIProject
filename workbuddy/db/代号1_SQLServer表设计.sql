/**************************************************************************************************
 *  文件名：代号1_SQLServer表设计.sql
 *  项目  ：代号1（3D 竖屏俯视角 · 僵尸幸存者 · 割草射击）
 *  依据  ：《代号1 功能开发文档 261028》（18 个 Sheet，模块 M01~M17）
 *  版本  ：v1.0
 *  适用  ：SQL Server 2016 及以上
 *
 *  ── 设计前提（来自文档 Sheet3「模块总览」，必须先达成共识）──────────────────────────────
 *   1. 战斗过程由【客户端本地演算】保证手感；结算、掉落、经验、货币、关卡进度一律【以服务器为准】。
 *   2. 客户端上报过程数据（5 秒快照、拾取计数、用时）；服务器下发结果（奖励、经验、进度）。
 *   3. 存档只在三个时间节点写入：关卡结算、离线结算、避难所内任何消耗或装配变更。局内过程不写存档。
 *   4. 同一局使用唯一 run_id，所有写入按 run_id 幂等。
 *   5. 三端（Android / iOS / 微信小游戏）同账号、同存档、同进度 → 账号层按 UnionID 归一。
 *
 *  ── 库表总览（43 张表 + 1 个视图）──────────────────────────────────────────────────────
 *  字典/全局   2 ：CfgCurrency、CfgGlobalParam
 *  配置数值  19 ：CfgAttribute、CfgPlayerLevel、CfgWeapon、CfgWeaponUpgrade、CfgAmmo、CfgArmor、
 *                 CfgArmorUpgrade、CfgAccessory、CfgAccessoryUpgrade、CfgSkill、CfgSkillUpgrade、
 *                 CfgMonster、CfgStage、CfgStageSpawn、CfgInBattleDrop、CfgBattleBuff、
 *                 CfgOfflineReward、CfgShopGoods、CfgShopGoodsDetail
 *  账号存档   5 ：TAccount、TAccountBind、TPlayer、TPlayerCurrency、TPlayerStage
 *  装备养成   6 ：TPlayerLoadout、TPlayerWeapon、TPlayerWeaponAccessory、TPlayerAmmo、
 *                 TPlayerArmor、TPlayerSkill
 *  战斗结算   4 ：TBattleRun、TBattleSnapshot、TBattleReward、TBattlePickup
 *  外围系统   4 ：TOfflineSettle、TShopPurchaseLog、CfgQuest、TPlayerQuest
 *  流水风控   3 ：TCurrencyLog、TItemLog、TAntiCheatLog
 *  视图       1 ：VStageRank
 *
 *  ── 匈牙利字段命名规范（全库强制）────────────────────────────────────────────────────────
 *   前缀    类型                    前缀    类型
 *   by      TINYINT                 sz      NVARCHAR(n)      字符串（sz = string,zero-terminated）
 *   w       SMALLINT                t       NVARCHAR(MAX)    长文本
 *   n       INT                     j       NVARCHAR(MAX)    JSON（附 ISJSON 约束）
 *   l       BIGINT                  dt      DATETIME2(3)     时间（一律 UTC）
 *   f       REAL                    bin     ROWVERSION       乐观锁版本
 *   d       DECIMAL(18,4)           g       UNIQUEIDENTIFIER
 *   b       BIT
 *   规则：前缀(小写) + PascalCase 主体 + 语义后缀。主键固定为 <前缀><实体>Id，例：lAccountId、nWeaponId。
 *        布尔列一律 b 开头且以 Is/Has/Enabled 结尾；计数字段以 Count 结尾；掩码以 Mask 结尾。
 *
 *  ── 表命名规范 ─────────────────────────────────────────────────────────────────────────
 *   T + PascalCase   ：运行时数据表（T Account）
 *   Cfg + PascalCase ：策划配置表（数值同学填，只读，运营期热加载）
 *   V + PascalCase   ：视图
 *
 *  ── 文档模块 → 表 映射 ─────────────────────────────────────────────────────────────────
 *   M01 账号与存档 → TAccount / TAccountBind / TPlayer / TPlayerStage
 *   M02 属性系统   → CfgAttribute / CfgPlayerLevel / TPlayer(nPlayerLevel, lExpTotal)
 *   M03 战斗公式   → CfgGlobalParam + TBattleRun(服务器复核上限)
 *   M04 武器       → CfgWeapon / CfgWeaponUpgrade / TPlayerWeapon / TPlayerLoadout
 *   M05 子弹       → CfgAmmo / TPlayerAmmo
 *   M06 配件       → CfgAccessory / CfgAccessoryUpgrade / TPlayerWeaponAccessory
 *   M07 防具       → CfgArmor / CfgArmorUpgrade / TPlayerArmor
 *   M08 技能       → CfgSkill / CfgSkillUpgrade / TPlayerSkill
 *   M09 关卡与怪物 → CfgStage / CfgStageSpawn / CfgMonster
 *   M10 局内掉落   → CfgInBattleDrop / CfgBattleBuff / TBattlePickup
 *   M11 避难所     → TPlayerLoadout / TShopPurchaseLog（装配、强化、商店）
 *   M12 离线挂机   → CfgOfflineReward / TOfflineSettle / TPlayer(dtLastOfflineSettleTime)
 *   M13 同步与结算 → TBattleRun / TBattleSnapshot / TBattleReward
 *   M14 任务       → CfgQuest / TPlayerQuest（文档中「未补」，此处先给骨架，待需求补齐）
 *   M15 排行榜     → VStageRank（同分规则见视图注释）
 *   M16 商城       → CfgShopGoods / CfgShopGoodsDetail / TShopPurchaseLog
 *   M17 邮件补发   → 文档未涉及，建议随 M16 发货失败场景补充 TMail（本次未建表，留 TODO）
 **************************************************************************************************/

-- 如需全新库，先放开下面三行单独执行
-- CREATE DATABASE [GameDB_Codename1] COLLATE Chinese_PRC_CI_AS;
-- GO
USE [GameDB_Codename1];
GO

SET NOCOUNT ON;
GO


/**************************************************************************************************
 * 一、字典与全局参数
 **************************************************************************************************/

/* 1.1 币种 / 养成材料字典
   文档 Sheet4「存档字段」：金币、武器强化石、技能强化石；头盔/背心/手套/背包 四种材料按部位分账。 */
CREATE TABLE dbo.CfgCurrency
(
    nCurrencyId     INT             NOT NULL,                    -- 币种ID：1金币 2武器强化石 3技能强化石 4头盔材料 5背心材料 6手套材料 7背包材料
    szCurrencyName  NVARCHAR(32)    NOT NULL,                    -- 显示名
    byCategory      TINYINT         NOT NULL,                    -- 分类：1通用货币 2养成材料
    byPartId        TINYINT         NULL,                        -- 材料对应的防具部位（1头盔2背心3手套4背包），通用货币置 NULL
    bIsNeedLog      BIT             NOT NULL CONSTRAINT DF_CfgCurrency_bIsNeedLog     DEFAULT (1),  -- 是否产生流水
    dtUpdateTime    DATETIME2(3)    NOT NULL CONSTRAINT DF_CfgCurrency_dtUpdateTime    DEFAULT (SYSUTCDATETIME()),
    CONSTRAINT PK_CfgCurrency PRIMARY KEY CLUSTERED (nCurrencyId)
);
GO


/* 1.2 全局参数表：所有「数值负责」但尚未定型的可调常量走这里，避免为单点数值单独建表 */
CREATE TABLE dbo.CfgGlobalParam
(
    szParamKey      NVARCHAR(64)    NOT NULL,                    -- 参数键，例：BATTLE_SNAPSHOT_INTERVAL_SEC
    szParamValue    NVARCHAR(256)   NOT NULL,                    -- 参数值（统一字符串，应用层转换）
    byValueType     TINYINT         NOT NULL CONSTRAINT DF_CfgGlobalParam_byValueType  DEFAULT (1),  -- 1整数 2小数 3字符串 4布尔
    szRemark        NVARCHAR(256)   NULL,                        -- 备注 / 调优说明
    dtUpdateTime    DATETIME2(3)    NOT NULL CONSTRAINT DF_CfgGlobalParam_dtUpdateTime  DEFAULT (SYSUTCDATETIME()),
    CONSTRAINT PK_CfgGlobalParam PRIMARY KEY CLUSTERED (szParamKey)
);
GO
-- 初始化一批与本文档强相关的参数（数值同学可直接改）
INSERT INTO dbo.CfgGlobalParam (szParamKey, szParamValue, byValueType, szRemark)
VALUES (N'BATTLE_SNAPSHOT_INTERVAL_SEC',  N'5',                  1, N'客户端快照上报间隔：5 秒（M13）'),
       (N'BATTLE_RECONNECT_LIMIT_SEC',    N'60',                 1, N'断线重连窗口：1 分钟内可续局（M13）'),
       (N'BATTLE_MAX_DURATION_SEC',       N'900',                1, N'单局最大时长：超 900 秒结算被拒（M13 验收）'),
       (N'LOADOUT_SLOT_COUNT',            N'4',                  1, N'武器槽位数（四、避难所）'),
       (N'ACCESSORY_SLOT_PER_WEAPON',     N'2',                  1, N'每把武器通用配件槽数（六、配件）'),
       (N'SKILL_SLOT_COUNT',              N'2',                  1, N'局内最多携带技能数（八、技能）'),
       (N'PLAYER_MAX_LEVEL',              N'60',                 1, N'角色等级上限 1~60（二、属性）'),
       (N'LOSE_REWARD_RATE',              N'0.5',                2, N'失败结算：金币与经验发 50%，材料与掉落不发放（九、关卡）'),
       (N'REPEAT_CLEAR_REWARD_RATE',      N'1.0',                2, N'重复挑战不触发首通，金币经验 100%（九、关卡）'),
       (N'DROP_GROUND_KEEP_SEC',          N'15',                 1, N'负重/携带池满时掉落物原地保留秒数（十、局内掉落）'),
       (N'OFFLINE_SETTLE_MIN_SEC',        N'300',                1, N'离线不足 300 秒不结算，累计到下次（十二、离线挂机）'),
       (N'OFFLINE_SEC_PER_TICK',          N'300',                1, N'5 分钟折算 1 次（十二、离线挂机）'),
       (N'OFFLINE_MAX_TICK_COUNT',        N'96',                 1, N'最多 96 次 = 8 小时封顶（十二、离线挂机）');
GO


/**************************************************************************************************
 * 二、配置表（Cfg*）—— 数值/策划负责填充，运行时只读，均为非自增主键
 **************************************************************************************************/

/* 2.1 属性字典
   文档 Sheet5「属性表」：属性在 角色 / 武器 / 配件 / 子弹 / 防具 / 怪物 六类主体上的适用范围。 */
CREATE TABLE dbo.CfgAttribute
(
    nAttributeId        INT             NOT NULL,                -- 属性ID：1等级 2经验值 3血量 4护甲 5攻击 6穿透 7弹匣容量 8子弹重量 9负重 10换弹速度 11技能积攒速度 12射程 13移速
    szAttributeKey      NVARCHAR(48)    NOT NULL,                -- 程序用键名，例：ATK / PENETRATION / LOAD
    szAttributeName     NVARCHAR(48)    NOT NULL,                -- 显示名
    nScopeMask          INT             NOT NULL,                -- 适用主体掩码：1角色 2武器 4配件 8子弹 16防具 32怪物（可位或叠加）
    byValueType         TINYINT         NOT NULL CONSTRAINT DF_CfgAttribute_byValueType    DEFAULT (1),  -- 1整数值 2百分比小数
    byCalcLayer         TINYINT         NOT NULL CONSTRAINT DF_CfgAttribute_byCalcLayer    DEFAULT (1),  -- 计算层：1加算 2乘算（对应 Buff「先加后乘」）
    bIsPercent          BIT             NOT NULL CONSTRAINT DF_CfgAttribute_bIsPercent     DEFAULT (0),  -- 是否按万分比存储（值域 0~10000 表示 0%~100%）
    dtUpdateTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_CfgAttribute_dtUpdateTime    DEFAULT (SYSUTCDATETIME()),
    CONSTRAINT PK_CfgAttribute PRIMARY KEY CLUSTERED (nAttributeId),
    CONSTRAINT UQ_CfgAttribute_szAttributeKey UNIQUE (szAttributeKey)
);
GO


/* 2.2 角色成长表（文档 Sheet5「2、角色成长表」——数值负责，列已预留） */
CREATE TABLE dbo.CfgPlayerLevel
(
    nPlayerLevel        INT             NOT NULL,                -- 等级 1~60
    lExpNeed            BIGINT          NOT NULL,                -- 升到下一级所需经验
    lExpTotal           BIGINT          NOT NULL,                -- 到达本级所需累计经验（冗余，便于前端画进度条）
    nHpValue            INT             NOT NULL CONSTRAINT DF_CfgPlayerLevel_nHpValue          DEFAULT (0),
    nArmorValue         INT             NOT NULL CONSTRAINT DF_CfgPlayerLevel_nArmorValue       DEFAULT (0),
    nAtkValue           INT             NOT NULL CONSTRAINT DF_CfgPlayerLevel_nAtkValue         DEFAULT (0),
    nPenetrationValue   INT             NOT NULL CONSTRAINT DF_CfgPlayerLevel_nPenetrationValue DEFAULT (0),
    nLoadValue          INT             NOT NULL CONSTRAINT DF_CfgPlayerLevel_nLoadValue        DEFAULT (0),  -- 负重上限
    dSkillChargeRate    DECIMAL(18, 4)  NOT NULL CONSTRAINT DF_CfgPlayerLevel_dSkillChargeRate  DEFAULT (1),  -- 技能积攒速度倍率
    fRangeValue         REAL            NOT NULL CONSTRAINT DF_CfgPlayerLevel_fRangeValue       DEFAULT (0),  -- 瞄准距离/射程(米)
    dtUpdateTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_CfgPlayerLevel_dtUpdateTime      DEFAULT (SYSUTCDATETIME()),
    CONSTRAINT PK_CfgPlayerLevel PRIMARY KEY CLUSTERED (nPlayerLevel)
);
GO


/* 2.3 武器配置表（文档 Sheet7「5、武器配置表」）
   7 类：砍刀(近战保底，不耗弹)、手枪、冲锋枪、步枪、弓箭、弩、火箭炮 */
CREATE TABLE dbo.CfgWeapon
(
    nWeaponId           INT             NOT NULL,
    szWeaponName        NVARCHAR(32)    NOT NULL,
    byWeaponType        TINYINT         NOT NULL,                -- 1近战(不消耗弹药) 2远程(消耗弹药)
    nAtkValue           INT             NOT NULL CONSTRAINT DF_CfgWeapon_nAtkValue           DEFAULT (0),
    nPenetrationValue   INT             NOT NULL CONSTRAINT DF_CfgWeapon_nPenetrationValue   DEFAULT (0),
    fRangeValue         REAL            NOT NULL CONSTRAINT DF_CfgWeapon_fRangeValue         DEFAULT (0),   -- 射程(米)
    nMagazineSize       INT             NOT NULL CONSTRAINT DF_CfgWeapon_nMagazineSize       DEFAULT (0),   -- 弹匣容量；近战武器填 0
    fFireIntervalMs     REAL            NOT NULL CONSTRAINT DF_CfgWeapon_fFireIntervalMs     DEFAULT (0),   -- 射击间隔(毫秒)
    fReloadSec          REAL            NOT NULL CONSTRAINT DF_CfgWeapon_fReloadSec          DEFAULT (0),   -- 换弹时长(秒)；近战武器填 0
    nUpgradeMaxLevel    INT             NOT NULL CONSTRAINT DF_CfgWeapon_nUpgradeMaxLevel    DEFAULT (0),   -- 强化上限（各武器不同）
    nAmmoTypeId         INT             NULL,                    -- 可兼容的弹种大类ID（近战武器为 NULL）
    bIsDefaultOwned     BIT             NOT NULL CONSTRAINT DF_CfgWeapon_bIsDefaultOwned     DEFAULT (0),   -- 是否初始赠送（砍刀 = 1）
    tFeatureDesc        NVARCHAR(MAX)   NULL,                    -- 特性文案，例：扇形120° 最多3目标 / 溅射2.5m 外围60%
    jzFeatureParam      NVARCHAR(MAX)   NULL,                    -- 特性结构化参数 JSON，例：{"shape":"sector","angle":120,"maxTarget":3}
    dtUpdateTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_CfgWeapon_dtUpdateTime        DEFAULT (SYSUTCDATETIME()),
    CONSTRAINT PK_CfgWeapon PRIMARY KEY CLUSTERED (nWeaponId),
    CONSTRAINT CK_CfgWeapon_jzFeatureParam CHECK (jzFeatureParam IS NULL OR ISJSON(jzFeatureParam) = 1)
);
GO


/* 2.4 武器强化节点表（文档 Sheet7「7、强化配置表」
   注意：文档中「每级属性增强 固定 or 单独」尚未定案；本表按【每一把武器 × 每一个等级】单独配，
        若最终定为「所有武器统一固定值」，把本表降级为一张 CfgWeaponUpgradeCommon 即可，字段已足够。 */
CREATE TABLE dbo.CfgWeaponUpgrade
(
    nWeaponId           INT             NOT NULL,
    nUpgradeLevel       INT             NOT NULL,                -- 目标等级（n-1 → n 消耗此行）
    nCostCurrencyId     INT             NOT NULL,                -- 消耗币种（默认 2 = 武器强化石）
    nCostCount          INT             NOT NULL CONSTRAINT DF_CfgWeaponUpgrade_nCostCount      DEFAULT (0),
    nAtkAddValue        INT             NOT NULL CONSTRAINT DF_CfgWeaponUpgrade_nAtkAddValue    DEFAULT (0),
    nPenetrationAddValue INT            NOT NULL CONSTRAINT DF_CfgWeaponUpgrade_nPenAddValue    DEFAULT (0),
    fRangeAddValue      REAL            NOT NULL CONSTRAINT DF_CfgWeaponUpgrade_fRangeAddValue  DEFAULT (0),
    nMagaAddValue       INT             NOT NULL CONSTRAINT DF_CfgWeaponUpgrade_nMagaAddValue   DEFAULT (0),
    dtUpdateTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_CfgWeaponUpgrade_dtUpdateTime    DEFAULT (SYSUTCDATETIME()),
    CONSTRAINT PK_CfgWeaponUpgrade PRIMARY KEY CLUSTERED (nWeaponId, nUpgradeLevel)
);
GO


/* 2.5 子弹配置表（文档 Sheet8「5、武器配置表」
   弹种 × 子弹 × 等级(T1~T5) 为一行；等级只做区分、不可升级。 */
CREATE TABLE dbo.CfgAmmo
(
    nAmmoId             INT             NOT NULL,                -- 子弹配置ID（= 库存键）
    nAmmoTypeId         INT             NOT NULL,                -- 弹种大类：1手枪弹 2冲锋枪弹 3步枪弹 4箭 5弩箭 6火箭弹
    szAmmoName          NVARCHAR(32)    NOT NULL,                -- 例：木箭 / 三叉箭 / 爆裂箭 / 寒冰箭 / 穿云箭
    byGrade             TINYINT         NOT NULL,                -- 等级：1=T1 … 5=T5
    nAtkValue           INT             NOT NULL CONSTRAINT DF_CfgAmmo_nAtkValue        DEFAULT (0),
    nPenetrationValue   INT             NOT NULL CONSTRAINT DF_CfgAmmo_nPenetrationValue DEFAULT (0),
    dUnitWeight         DECIMAL(18, 4)  NOT NULL CONSTRAINT DF_CfgAmmo_dUnitWeight      DEFAULT (0),   -- 单发重量 → 参与负重计算
    tEffectDesc         NVARCHAR(MAX)   NULL,                    -- 特效文案：同发命中扇形3目标各60% / 溅射2m外围70% / 命中减速30%持续2秒
    jzEffectParam       NVARCHAR(MAX)   NULL,                    -- 特效结构化 JSON，例：{"slow":{"rate":0.3,"sec":2}}
    dtUpdateTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_CfgAmmo_dtUpdateTime     DEFAULT (SYSUTCDATETIME()),
    CONSTRAINT PK_CfgAmmo PRIMARY KEY CLUSTERED (nAmmoId),
    CONSTRAINT UQ_CfgAmmo_TypeGrade UNIQUE (nAmmoTypeId, byGrade, szAmmoName),
    CONSTRAINT CK_CfgAmmo_jzEffectParam CHECK (jzEffectParam IS NULL OR ISJSON(jzEffectParam) = 1)
);
GO


/* 2.6 防具配置表（文档 Sheet10「4、防具配置表」
   4 部位（1头盔 2战术背心 3手套 4背包）× 4 品质（1=C 2=B 3=A 4=S）= 16 行。 */
CREATE TABLE dbo.CfgArmor
(
    nArmorId            INT             NOT NULL,
    byPartId            TINYINT         NOT NULL,                -- 部位：1头盔 2背心 3手套 4背包
    byQuality           TINYINT         NOT NULL,                -- 品质：1=C 2=B 3=A 4=S
    szArmorName         NVARCHAR(32)    NOT NULL,                -- 例：安全帽-C级 / 电焊头盔-B / 摩托头盔-A / 防爆头盔-S
    nAttributeId        INT             NOT NULL,                -- 主属性：护甲 / 换弹速度 / 总容量
    dInitValue          DECIMAL(18, 4)  NOT NULL CONSTRAINT DF_CfgArmor_dInitValue           DEFAULT (0),  -- 初始值（配表中的 0.08 / 0.1 / 0.15…）
    nUpgradeMaxLevel    INT             NOT NULL CONSTRAINT DF_CfgArmor_nUpgradeMaxLevel     DEFAULT (0),  -- 不同品质不同强化上限
    dtUpdateTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_CfgArmor_dtUpdateTime         DEFAULT (SYSUTCDATETIME()),
    CONSTRAINT PK_CfgArmor PRIMARY KEY CLUSTERED (nArmorId),
    CONSTRAINT UQ_CfgArmor_PartQuality UNIQUE (byPartId, byQuality)
);
GO


/* 2.7 防具强化节点表（文档 Sheet10「7、强化配置表」，消耗【按部位独立】的材料，不通用） */
CREATE TABLE dbo.CfgArmorUpgrade
(
    nArmorId            INT             NOT NULL,
    nUpgradeLevel       INT             NOT NULL,                -- 目标等级
    nCostCurrencyId     INT             NOT NULL,                -- 4头盔材料 5背心材料 6手套材料 7背包材料（按部位）
    nCostCount          INT             NOT NULL CONSTRAINT DF_CfgArmorUpgrade_nCostCount   DEFAULT (0),
    dBonusValue         DECIMAL(18, 4)  NOT NULL CONSTRAINT DF_CfgArmorUpgrade_dBonusValue  DEFAULT (0),  -- 本节点属性加成
    dtUpdateTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_CfgArmorUpgrade_dtUpdateTime DEFAULT (SYSUTCDATETIME()),
    CONSTRAINT PK_CfgArmorUpgrade PRIMARY KEY CLUSTERED (nArmorId, nUpgradeLevel)
);
GO


/* 2.8 配件配置表（文档 Sheet9「4、配件配置表」
   关键规则：配件【不通用】，每种武器有各自独立的配件 → 故 nWeaponId 必填。 */
CREATE TABLE dbo.CfgAccessory
(
    nAccessoryId        INT             NOT NULL,
    nWeaponId           INT             NOT NULL,                -- 归属武器（配件不跨武器通用）
    szAccessoryName     NVARCHAR(32)    NOT NULL,                -- 例：瞄准镜 / 制退器 / 扩容弹匣 / 快速握把 / 加压枪管
    nAttributeId        INT             NOT NULL,                -- 影响属性：射程 / 穿透 / 弹匣 / 换弹速度 / 攻击
    dInitValue          DECIMAL(18, 4)  NOT NULL CONSTRAINT DF_CfgAccessory_dInitValue         DEFAULT (0),
    nUpgradeMaxLevel    INT             NOT NULL CONSTRAINT DF_CfgAccessory_nUpgradeMaxLevel   DEFAULT (0),
    dtUpdateTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_CfgAccessory_dtUpdateTime       DEFAULT (SYSUTCDATETIME()),
    CONSTRAINT PK_CfgAccessory PRIMARY KEY CLUSTERED (nAccessoryId),
    CONSTRAINT UQ_CfgAccessory_WeaponName UNIQUE (nWeaponId, szAccessoryName)
);
GO


/* 2.9 配件强化节点表（消耗统一配件强化石） */
CREATE TABLE dbo.CfgAccessoryUpgrade
(
    nAccessoryId        INT             NOT NULL,
    nUpgradeLevel       INT             NOT NULL,
    nCostCurrencyId     INT             NOT NULL,                -- 配件强化石
    nCostCount          INT             NOT NULL CONSTRAINT DF_CfgAccessoryUpgrade_nCostCount  DEFAULT (0),
    dBonusValue         DECIMAL(18, 4)  NOT NULL CONSTRAINT DF_CfgAccessoryUpgrade_dBonusValue DEFAULT (0),
    dtUpdateTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_CfgAccessoryUpgrade_dtUpdateTime DEFAULT (SYSUTCDATETIME()),
    CONSTRAINT PK_CfgAccessoryUpgrade PRIMARY KEY CLUSTERED (nAccessoryId, nUpgradeLevel)
);
GO


/* 2.10 技能配置表（文档 Sheet11「5、技能配置表」）*/
CREATE TABLE dbo.CfgSkill
(
    nSkillId            INT             NOT NULL,
    szSkillName         NVARCHAR(32)    NOT NULL,                -- 诱饵器 / 装甲车 / 双持
    nEnergyCost         INT             NOT NULL,                -- 释放所需点数（30 / 60 / 45）
    fEffectDurationSec  REAL            NOT NULL CONSTRAINT DF_CfgSkill_fEffectDurationSec  DEFAULT (0),  -- 基础效果持续秒数
    tBaseEffectDesc     NVARCHAR(MAX)   NULL,                    -- 基础效果文案
    jzEffectParam       NVARCHAR(MAX)   NULL,                    -- 效果结构化 JSON，例：{"range":6,"radius":3.5}
    dtUpdateTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_CfgSkill_dtUpdateTime        DEFAULT (SYSUTCDATETIME()),
    CONSTRAINT PK_CfgSkill PRIMARY KEY CLUSTERED (nSkillId),
    CONSTRAINT CK_CfgSkill_jzEffectParam CHECK (jzEffectParam IS NULL OR ISJSON(jzEffectParam) = 1)
);
GO


/* 2.11 技能强化节点表（加成1/加成2/加成3 三列已按文档预留；不足则用 jzBonusJson 扩展）*/
CREATE TABLE dbo.CfgSkillUpgrade
(
    nSkillId            INT             NOT NULL,
    nUpgradeLevel       INT             NOT NULL,
    nCostCurrencyId     INT             NOT NULL,                -- 3 = 技能强化石
    nCostCount          INT             NOT NULL CONSTRAINT DF_CfgSkillUpgrade_nCostCount   DEFAULT (0),
    dBonus1Value        DECIMAL(18, 4)  NOT NULL CONSTRAINT DF_CfgSkillUpgrade_dBonus1Value DEFAULT (0),  -- 加成1
    dBonus2Value        DECIMAL(18, 4)  NOT NULL CONSTRAINT DF_CfgSkillUpgrade_dBonus2Value DEFAULT (0),  -- 加成2
    dBonus3Value        DECIMAL(18, 4)  NOT NULL CONSTRAINT DF_CfgSkillUpgrade_dBonus3Value DEFAULT (0),  -- 加成3
    jzBonusJson         NVARCHAR(MAX)   NULL,                    -- 超出三列的加成项走 JSON
    dtUpdateTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_CfgSkillUpgrade_dtUpdateTime DEFAULT (SYSUTCDATETIME()),
    CONSTRAINT PK_CfgSkillUpgrade PRIMARY KEY CLUSTERED (nSkillId, nUpgradeLevel),
    CONSTRAINT CK_CfgSkillUpgrade_jzBonusJson CHECK (jzBonusJson IS NULL OR ISJSON(jzBonusJson) = 1)
);
GO


/* 2.12 怪物配置表（文档 Sheet12「怪物配置表」：普通怪/快速怪/远程怪/精英怪/BOSS）*/
CREATE TABLE dbo.CfgMonster
(
    nMonsterId          INT             NOT NULL,
    szMonsterName       NVARCHAR(32)    NOT NULL,
    byMonsterType       TINYINT         NOT NULL,                -- 1普通怪 2快速怪 3远程怪 4精英怪 5BOSS
    nHpValue            INT             NOT NULL CONSTRAINT DF_CfgMonster_nHpValue            DEFAULT (0),
    nArmorValue         INT             NOT NULL CONSTRAINT DF_CfgMonster_nArmorValue         DEFAULT (0),
    nAtkValue           INT             NOT NULL CONSTRAINT DF_CfgMonster_nAtkValue           DEFAULT (0),
    nPenetrationValue   INT             NOT NULL CONSTRAINT DF_CfgMonster_nPenetrationValue   DEFAULT (0),
    fMoveSpeed          REAL            NOT NULL CONSTRAINT DF_CfgMonster_fMoveSpeed          DEFAULT (0),
    fRangeValue         REAL            NOT NULL CONSTRAINT DF_CfgMonster_fRangeValue         DEFAULT (0),   -- 射程(米)
    fAtkIntervalSec     REAL            NOT NULL CONSTRAINT DF_CfgMonster_fAtkIntervalSec     DEFAULT (0),   -- 攻击间隔(秒)
    byBehaviorType      TINYINT         NOT NULL CONSTRAINT DF_CfgMonster_byBehaviorType      DEFAULT (1),   -- 1直线追击 2冲刺(速度×1.5) 3远程抛射+近身后退 4自定义AI
    nExpReward          INT             NOT NULL CONSTRAINT DF_CfgMonster_nExpReward          DEFAULT (0),   -- 击杀经验
    nSkillPoint         INT             NOT NULL CONSTRAINT DF_CfgMonster_nSkillPoint         DEFAULT (0),   -- 贡献技能点
    tBehaviorDesc       NVARCHAR(MAX)   NULL,
    dtUpdateTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_CfgMonster_dtUpdateTime        DEFAULT (SYSUTCDATETIME()),
    CONSTRAINT PK_CfgMonster PRIMARY KEY CLUSTERED (nMonsterId)
);
GO


/* 2.13 关卡配置表（文档 Sheet12「7、关卡配置表」）*/
CREATE TABLE dbo.CfgStage
(
    nStageId            INT             NOT NULL,
    szStageName         NVARCHAR(64)    NOT NULL,                -- 避难所/农场/乡间小路/村镇马路/市区街道/研究所外围/BOSS场地
    bySceneId           TINYINT         NOT NULL,                -- 场景ID（美术 7 套，按关卡复用）
    byRecommendLevel    TINYINT         NOT NULL CONSTRAINT DF_CfgStage_byRecommendLevel      DEFAULT (1),
    nMonsterTotalCount  INT             NOT NULL CONSTRAINT DF_CfgStage_nMonsterTotalCount    DEFAULT (0),  -- 关卡怪物总数（结算校验击杀数上限）
    nSameScreenMax      INT             NOT NULL CONSTRAINT DF_CfgStage_nSameScreenMax        DEFAULT (0),  -- 同屏存活上限，超出进队列
    nTimeLimitSec       INT             NOT NULL CONSTRAINT DF_CfgStage_nTimeLimitSec         DEFAULT (0),  -- 时限，超时判 fail(timeout)
    nGoldReward         INT             NOT NULL CONSTRAINT DF_CfgStage_nGoldReward           DEFAULT (0),  -- 通关金币（服务器据此重算，不采信客户端）
    nExpReward          INT             NOT NULL CONSTRAINT DF_CfgStage_nExpReward            DEFAULT (0),  -- 通关经验
    nMaxPickupCount     INT             NOT NULL CONSTRAINT DF_CfgStage_nMaxPickupCount       DEFAULT (0),  -- 本关拾取总计数上限（异常截断阈值）
    bIsEnabled          BIT             NOT NULL CONSTRAINT DF_CfgStage_bIsEnabled            DEFAULT (1),
    dtUpdateTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_CfgStage_dtUpdateTime          DEFAULT (SYSUTCDATETIME()),
    CONSTRAINT PK_CfgStage PRIMARY KEY CLUSTERED (nStageId)
);
GO


/* 2.14 关卡刷新点配置表（零散刷新点 + 默认遭遇点；到达遭遇点停止前进、清完再走）*/
CREATE TABLE dbo.CfgStageSpawn
(
    nStageId            INT             NOT NULL,
    bySpawnType         TINYINT         NOT NULL,                -- 1零散刷新点 2遭遇点
    nSequence           INT             NOT NULL,                -- 同关卡内序号，决定沿途先后
    nMonsterId          INT             NOT NULL,
    nMonsterCount       INT             NOT NULL CONSTRAINT DF_CfgStageSpawn_nMonsterCount    DEFAULT (1),
    fPositionPercent    REAL            NOT NULL CONSTRAINT DF_CfgStageSpawn_fPositionPercent DEFAULT (0),  -- 在关卡路径上的进度位置 0~100
    nDelaySec           INT             NOT NULL CONSTRAINT DF_CfgStageSpawn_nDelaySec        DEFAULT (0),  -- 触发后延迟刷出秒数
    dtUpdateTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_CfgStageSpawn_dtUpdateTime     DEFAULT (SYSUTCDATETIME()),
    CONSTRAINT PK_CfgStageSpawn PRIMARY KEY CLUSTERED (nStageId, bySpawnType, nSequence)
);
GO


/* 2.15 局内掉落配置表（文档 Sheet13「5、道具配置表」
   掉落物分类：1弹药箱 2医疗包 3Buff道具 4金币/金币袋。只影响本局，结算不写存档。 */
CREATE TABLE dbo.CfgInBattleDrop
(
    nDropId             INT             NOT NULL,
    szDropName          NVARCHAR(32)    NOT NULL,                -- 例：T? 弹药箱
    byDropType          TINYINT         NOT NULL,                -- 1弹药箱 2医疗包 3Buff道具 4金币
    nMaxSpawnPerRun     INT             NOT NULL CONSTRAINT DF_CfgInBattleDrop_nMaxSpawnPerRun DEFAULT (0),  -- 单局最多刷新数量（例 6 个）
    nBuffId             INT             NULL,                    -- 若产 Buff → 关联 CfgBattleBuff
    nRefItemId          INT             NULL,                    -- 若产具体道具/弹药 → 关联对应配置ID
    nRefItemCount       INT             NOT NULL CONSTRAINT DF_CfgInBattleDrop_nRefItemCount   DEFAULT (0),  -- 例：20 发
    byGenRule           TINYINT         NOT NULL CONSTRAINT DF_CfgInBattleDrop_byGenRule       DEFAULT (1),  -- 1固定 2半随机
    tRemark             NVARCHAR(MAX)   NULL,
    dtUpdateTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_CfgInBattleDrop_dtUpdateTime    DEFAULT (SYSUTCDATETIME()),
    CONSTRAINT PK_CfgInBattleDrop PRIMARY KEY CLUSTERED (nDropId)
);
GO


/* 2.16 局内 Buff 配置表（文档 Sheet13「2、Buff 规则」
   同种再拾取只刷新时长不叠层；不同种可共存并进入「先加后乘」乘法层；到期立即移除不保留残余。 */
CREATE TABLE dbo.CfgBattleBuff
(
    nBuffId             INT             NOT NULL,
    szBuffName          NVARCHAR(32)    NOT NULL,                -- 火力药剂 / 急速药剂 / 装甲板…
    byStackMode         TINYINT         NOT NULL CONSTRAINT DF_CfgBattleBuff_byStackMode      DEFAULT (1),  -- 1仅刷新时长 2可叠层
    fDurationSec        REAL            NOT NULL CONSTRAINT DF_CfgBattleBuff_fDurationSec     DEFAULT (0),
    nAttributeId        INT             NOT NULL,                -- 影响的属性
    dEffectValue        DECIMAL(18, 4)  NOT NULL CONSTRAINT DF_CfgBattleBuff_dEffectValue      DEFAULT (0),
    byCalcLayer         TINYINT         NOT NULL CONSTRAINT DF_CfgBattleBuff_byCalcLayer      DEFAULT (1),  -- 1加算层 2乘算层
    bIsFullHpBlocked    BIT             NOT NULL CONSTRAINT DF_CfgBattleBuff_bIsFullHpBlocked DEFAULT (0),  -- 医疗包：满血不可拾取、不消耗
    dtUpdateTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_CfgBattleBuff_dtUpdateTime     DEFAULT (SYSUTCDATETIME()),
    CONSTRAINT PK_CfgBattleBuff PRIMARY KEY CLUSTERED (nBuffId)
);
GO


/* 2.17 离线奖励配置表（文档 Sheet15「4、离线奖励配置表」
   5 分钟折 1 次，最多 96 次（8 小时）；产出百分比可配；以服务器时间差为准。 */
CREATE TABLE dbo.CfgOfflineReward
(
    nOfflineId          INT             NOT NULL,
    szRuleName          NVARCHAR(64)    NOT NULL,
    nSecPerTick         INT             NOT NULL CONSTRAINT DF_CfgOfflineReward_nSecPerTick   DEFAULT (300),  -- 5 分钟折 1 次
    nMaxTickCount       INT             NOT NULL CONSTRAINT DF_CfgOfflineReward_nMaxTickCount DEFAULT (96),   -- 8 小时封顶
    nMinSettleSec       INT             NOT NULL CONSTRAINT DF_CfgOfflineReward_nMinSettleSec DEFAULT (300),  -- 不足 300 秒累计不结算
    dGoldRate           DECIMAL(18, 4)  NOT NULL CONSTRAINT DF_CfgOfflineReward_dGoldRate      DEFAULT (0),   -- 相对在线通关金币的百分比
    dExpRate            DECIMAL(18, 4)  NOT NULL CONSTRAINT DF_CfgOfflineReward_dExpRate       DEFAULT (0),   -- 相对在线通关经验的百分比
    bIsEnabled          BIT             NOT NULL CONSTRAINT DF_CfgOfflineReward_bIsEnabled    DEFAULT (1),
    dtUpdateTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_CfgOfflineReward_dtUpdateTime   DEFAULT (SYSUTCDATETIME()),
    CONSTRAINT PK_CfgOfflineReward PRIMARY KEY CLUSTERED (nOfflineId)
);
GO


/* 2.18 商城商品主表（文档 Sheet14「6、商品配置表」
   规则：多商品位，每日固定时间刷新；已拥有武器不重复上架；余额不足不扣款；同一天达购买上限后按钮置灰。 */
CREATE TABLE dbo.CfgShopGoods
(
    nGoodsId            INT             NOT NULL,
    szGoodsName         NVARCHAR(64)    NOT NULL,
    byShopType          TINYINT         NOT NULL CONSTRAINT DF_CfgShopGoods_byShopType        DEFAULT (1),  -- 1避难所商店 2限时特惠 3礼包
    nPriceCurrencyId    INT             NOT NULL CONSTRAINT DF_CfgShopGoods_nPriceCurrencyId  DEFAULT (1),  -- 1 = 金币
    lPriceValue         BIGINT          NOT NULL CONSTRAINT DF_CfgShopGoods_lPriceValue       DEFAULT (0),  -- 原价
    dDiscountRate       DECIMAL(18, 4)  NOT NULL CONSTRAINT DF_CfgShopGoods_dDiscountRate     DEFAULT (1),  -- 折扣率：0.7 = 7 折
    nDailyLimitCount    INT             NOT NULL CONSTRAINT DF_CfgShopGoods_nDailyLimitCount  DEFAULT (0),  -- 单账号每日购买上限；0 = 不限
    byRefreshType       TINYINT         NOT NULL CONSTRAINT DF_CfgShopGoods_byRefreshType     DEFAULT (1),  -- 1每日固定时间刷新 2不刷新(常驻) 3一次性
    nRefreshHour        INT             NULL,                    -- 每日刷新时刻（UTC 小时）
    bIsUnique          BIT              NOT NULL CONSTRAINT DF_CfgShopGoods_bIsUnique         DEFAULT (0),  -- 已拥有则不重复上架（如武器）
    dtValidFrom         DATETIME2(3)    NULL,
    dtValidTo           DATETIME2(3)    NULL,
    bIsEnabled          BIT             NOT NULL CONSTRAINT DF_CfgShopGoods_bIsEnabled        DEFAULT (1),
    dtUpdateTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_CfgShopGoods_dtUpdateTime      DEFAULT (SYSUTCDATETIME()),
    CONSTRAINT PK_CfgShopGoods PRIMARY KEY CLUSTERED (nGoodsId)
);
GO


/* 2.19 商品内容明细表（一个商品可含多种道具/武器/子弹/材料）*/
CREATE TABLE dbo.CfgShopGoodsDetail
(
    nGoodsId            INT             NOT NULL,
    nSequence           INT             NOT NULL,
    byItemType          TINYINT         NOT NULL,                -- 1货币 2武器 3子弹 4配件 5防具 6技能 7材料
    nItemRefId          INT             NOT NULL,                -- 对应配置ID
    lItemCount          BIGINT          NOT NULL CONSTRAINT DF_CfgShopGoodsDetail_lItemCount  DEFAULT (1),
    nItemGrade          TINYINT         NULL,                    -- 冗余品质/等级（如直接给 T3 子弹）
    dtUpdateTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_CfgShopGoodsDetail_dtUpdateTime DEFAULT (SYSUTCDATETIME()),
    CONSTRAINT PK_CfgShopGoodsDetail PRIMARY KEY CLUSTERED (nGoodsId, nSequence)
);
GO


/**************************************************************************************************
 * 三、账号与存档（M01 / M02）
 *    存档写入三原则：关卡结算、离线结算、避难所消耗或装配变更。其余路径一律只读/内存。
 **************************************************************************************************/

/* 3.1 主账号表：三端同一账号 → 以 szUnionId 归一 */
CREATE TABLE dbo.TAccount
(
    lAccountId          BIGINT IDENTITY(1, 1)   NOT NULL,
    szUnionId           NVARCHAR(64)    NOT NULL,                -- 平台统一ID（微信开放平台 UnionID）
    byRegPlatform       TINYINT         NOT NULL,                -- 注册来源：1 Android 2 iOS 3 微信小游戏
    byAccountStatus     TINYINT         NOT NULL CONSTRAINT DF_TAccount_byAccountStatus       DEFAULT (1),  -- 1正常 2封禁 3注销
    dtBanEndTime        DATETIME2(3)    NULL,                    -- 封禁到期时间
    szLastloginIp       NVARCHAR(64)    NULL,                    -- 最近登录 IP
    dtCreateTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_TAccount_dtCreateTime          DEFAULT (SYSUTCDATETIME()),
    dtLastLoginTime     DATETIME2(3)    NULL,                    -- 最近登录时间
    dtUpdateTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_TAccount_dtUpdateTime          DEFAULT (SYSUTCDATETIME()),
    CONSTRAINT PK_TAccount PRIMARY KEY CLUSTERED (lAccountId),
    CONSTRAINT UQ_TAccount_szUnionId UNIQUE (szUnionId)
);
GO


/* 3.2 第三方平台绑定表：同一 UnionID 下不同端 OpenID 各自记录 */
CREATE TABLE dbo.TAccountBind
(
    lAccountId          BIGINT          NOT NULL,
    byPlatform          TINYINT         NOT NULL,                -- 1 Android 2 iOS 3 微信小游戏
    szOpenId            NVARCHAR(64)    NOT NULL,                -- 该端 OpenID
    bIsPrimary          BIT             NOT NULL CONSTRAINT DF_TAccountBind_bIsPrimary       DEFAULT (0),
    dtBindTime          DATETIME2(3)    NOT NULL CONSTRAINT DF_TAccountBind_dtBindTime       DEFAULT (SYSUTCDATETIME()),
    CONSTRAINT PK_TAccountBind PRIMARY KEY CLUSTERED (lAccountId, byPlatform),
    CONSTRAINT UQ_TAccountBind_OpenId UNIQUE (byPlatform, szOpenId)
);
GO


/* 3.3 玩家存档主表（一个账号一个角色，单人 PvE）
   注意：dtLastOfflineSettleTime 为【仅服务器可写】字段，客户端不得上报。 */
CREATE TABLE dbo.TPlayer
(
    lPlayerId                   BIGINT IDENTITY(1, 1)   NOT NULL,
    lAccountId                  BIGINT          NOT NULL,
    szNickName                  NVARCHAR(32)    NOT NULL CONSTRAINT DF_TPlayer_szNickName DEFAULT (N'幸存者'),
    nPlayerLevel                INT             NOT NULL CONSTRAINT DF_TPlayer_nPlayerLevel DEFAULT (1),  -- 1~60，达阈值自动升级、溢出结转
    lExpTotal                   BIGINT          NOT NULL CONSTRAINT DF_TPlayer_lExpTotal DEFAULT (0),      -- 当前累计经验
    nMaxClearedStageId          INT             NOT NULL CONSTRAINT DF_TPlayer_nMaxClearedStageId DEFAULT (0),  -- 最高已通关关卡
    dtLastOfflineSettleTime     DATETIME2(3)    NOT NULL CONSTRAINT DF_TPlayer_dtLastOfflineSettleTime DEFAULT ('1970-01-01'),  -- 上次离线结算服务器时间
    nTotalBattleCount           INT             NOT NULL CONSTRAINT DF_TPlayer_nTotalBattleCount DEFAULT (0),
    nTotalPlaySec               BIGINT          NOT NULL CONSTRAINT DF_TPlayer_nTotalPlaySec DEFAULT (0),
    binRowVersion               ROWVERSION      NULL,            -- 乐观锁：存档并发写入保护
    dtCreateTime                DATETIME2(3)    NOT NULL CONSTRAINT DF_TPlayer_dtCreateTime DEFAULT (SYSUTCDATETIME()),
    dtUpdateTime                DATETIME2(3)    NOT NULL CONSTRAINT DF_TPlayer_dtUpdateTime DEFAULT (SYSUTCDATETIME()),
    CONSTRAINT PK_TPlayer PRIMARY KEY CLUSTERED (lPlayerId),
    CONSTRAINT UQ_TPlayer_lAccountId UNIQUE (lAccountId),
    CONSTRAINT CK_TPlayer_nPlayerLevel CHECK (nPlayerLevel BETWEEN 1 AND 60)
);
GO


/* 3.4 玩家货币 / 材料账本（按币种分行，方便新增币种不用改表）*/
CREATE TABLE dbo.TPlayerCurrency
(
    lPlayerId           BIGINT          NOT NULL,
    nCurrencyId         INT             NOT NULL,
    lBalanceValue       BIGINT          NOT NULL CONSTRAINT DF_TPlayerCurrency_lBalanceValue  DEFAULT (0),
    lTotalGainValue     BIGINT          NOT NULL CONSTRAINT DF_TPlayerCurrency_lTotalGainValue DEFAULT (0),  -- 累计收入（风控 / 数值回收用）
    binRowVersion       ROWVERSION      NULL,
    dtCreateTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_TPlayerCurrency_dtCreateTime   DEFAULT (SYSUTCDATETIME()),
    dtUpdateTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_TPlayerCurrency_dtUpdateTime   DEFAULT (SYSUTCDATETIME()),
    CONSTRAINT PK_TPlayerCurrency PRIMARY KEY CLUSTERED (lPlayerId, nCurrencyId),
    CONSTRAINT CK_TPlayerCurrency_lBalanceValue CHECK (lBalanceValue >= 0)
);
GO


/* 3.5 关卡进度表（首通标记、最佳用时、重挑次数）
   文档要求：重复挑战不再触发首通奖励，金币经验按 100% 发放。 */
CREATE TABLE dbo.TPlayerStage
(
    lPlayerId           BIGINT          NOT NULL,
    nStageId            INT             NOT NULL,
    bHasCleared         BIT             NOT NULL CONSTRAINT DF_TPlayerStage_bHasCleared      DEFAULT (0),  -- 是否曾经通关
    dtFirstClearTime    DATETIME2(3)    NULL,                    -- 首通时间（首通奖励发放依据）
    dtLastClearTime     DATETIME2(3)    NULL,
    nClearCount         INT             NOT NULL CONSTRAINT DF_TPlayerStage_nClearCount      DEFAULT (0),  -- 通关次数（含重复挑战）
    nBestTimeMs         INT             NULL,                    -- 各关最佳用时（毫秒；NULL = 未通关）
    nBestKillCount      INT             NULL,
    dtCreateTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_TPlayerStage_dtCreateTime      DEFAULT (SYSUTCDATETIME()),
    dtUpdateTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_TPlayerStage_dtUpdateTime      DEFAULT (SYSUTCDATETIME()),
    CONSTRAINT PK_TPlayerStage PRIMARY KEY CLUSTERED (lPlayerId, nStageId)
);
GO
CREATE NONCLUSTERED INDEX IX_TPlayerStage_Rank
    ON dbo.TPlayerStage (nStageId, nBestTimeMs ASC) WHERE (bHasCleared = 1);
GO


/**************************************************************************************************
 * 四、装备与养成（M04~M08）
 **************************************************************************************************/

/* 4.1 出征装配槽（文档 Sheet14「5、装备培养」：4 个槽位，每槽 1 把武器 + 1 种兼容弹种 + 携带弹量）
   装配变更 = 存档写入点之一。出发时对全槽做一次负重校验：
   SUM(单发重量 × 携带弹量) ≤ 角色负重 + 背包加成 */
CREATE TABLE dbo.TPlayerLoadout
(
    lPlayerId           BIGINT          NOT NULL,
    bySlotId            TINYINT         NOT NULL,                -- 槽位 1~4
    lWeaponId           BIGINT          NULL,                    -- 关联的玩家武器实例
    nAmmoId             INT             NULL,                    -- 兼容弹种（出关后不可更换）
    nCarryCount         INT             NOT NULL CONSTRAINT DF_TPlayerLoadout_nCarryCount    DEFAULT (0),  -- 携带弹量（携带池）
    nMagazineSnapshot   INT             NOT NULL CONSTRAINT DF_TPlayerLoadout_nMagazineSnapshot DEFAULT (0),  -- 出征时弹匣上限快照（含配件加成）
    dSlotWeightSum      DECIMAL(18, 4)  NOT NULL CONSTRAINT DF_TPlayerLoadout_dSlotWeightSum DEFAULT (0),  -- 单发重量 × 弹量
    dtUpdateTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_TPlayerLoadout_dtUpdateTime   DEFAULT (SYSUTCDATETIME()),
    CONSTRAINT PK_TPlayerLoadout PRIMARY KEY CLUSTERED (lPlayerId, bySlotId),
    CONSTRAINT CK_TPlayerLoadout_bySlotId CHECK (bySlotId BETWEEN 1 AND 4)
);
GO
-- 同一把武器不可重复装配到两个槽位
CREATE UNIQUE NONCLUSTERED INDEX UX_TPlayerLoadout_lWeaponId
    ON dbo.TPlayerLoadout (lPlayerId, lWeaponId) WHERE (lWeaponId IS NOT NULL);
GO


/* 4.2 玩家武器实例表
   规则来源：最多带 4 把；同一武器不可重复装配；强化不改变外观；不同武器强化上限不同。
   决策点（待确认）：本表按「同一 nWeaponCfgId 每玩家只持有一把」建模。
   若后续允许同一武器持有多个实例，去掉 UQ_TPlayerWeapon_Instance 唯一约束即可，其余不受影响。 */
CREATE TABLE dbo.TPlayerWeapon
(
    lWeaponId           BIGINT IDENTITY(1, 1)   NOT NULL,
    lPlayerId           BIGINT          NOT NULL,
    nWeaponCfgId        INT             NOT NULL,
    nUpgradeLevel       INT             NOT NULL CONSTRAINT DF_TPlayerWeapon_nUpgradeLevel   DEFAULT (0),
    byObtainSource      TINYINT         NOT NULL CONSTRAINT DF_TPlayerWeapon_byObtainSource  DEFAULT (0),  -- 1局间掉落 2挂机奖励 3任务获得 4商店购买 5初始赠送
    bIsLocked           BIT             NOT NULL CONSTRAINT DF_TPlayerWeapon_bIsLocked       DEFAULT (0),
    dtObtainTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_TPlayerWeapon_dtObtainTime    DEFAULT (SYSUTCDATETIME()),
    dtCreateTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_TPlayerWeapon_dtCreateTime    DEFAULT (SYSUTCDATETIME()),
    dtUpdateTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_TPlayerWeapon_dtUpdateTime    DEFAULT (SYSUTCDATETIME()),
    CONSTRAINT PK_TPlayerWeapon PRIMARY KEY CLUSTERED (lWeaponId),
    CONSTRAINT UQ_TPlayerWeapon_Instance UNIQUE (lPlayerId, nWeaponCfgId)
);
GO
CREATE NONCLUSTERED INDEX IX_TPlayerWeapon_lPlayerId ON dbo.TPlayerWeapon (lPlayerId);
GO


/* 4.3 武器配件装配表（每把武器 2 个通用槽；同类配件不可重复；替换回背包不销毁）
   约束：UQ 保证同一武器两个槽不会装同一种配件。 */
CREATE TABLE dbo.TPlayerWeaponAccessory
(
    lWeaponId           BIGINT          NOT NULL,
    bySlotId            TINYINT         NOT NULL,                -- 槽位 1~2
    nAccessoryCfgId     INT             NOT NULL,
    nUpgradeLevel       INT             NOT NULL CONSTRAINT DF_TPlayerWeaponAccessory_nUpgradeLevel DEFAULT (0),
    dtEquipTime         DATETIME2(3)    NOT NULL CONSTRAINT DF_TPlayerWeaponAccessory_dtEquipTime   DEFAULT (SYSUTCDATETIME()),
    dtUpdateTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_TPlayerWeaponAccessory_dtUpdateTime  DEFAULT (SYSUTCDATETIME()),
    CONSTRAINT PK_TPlayerWeaponAccessory PRIMARY KEY CLUSTERED (lWeaponId, bySlotId),
    CONSTRAINT UQ_TPlayerWeaponAccessory_Unique UNIQUE (lWeaponId, nAccessoryCfgId),
    CONSTRAINT CK_TPlayerWeaponAccessory_bySlotId CHECK (bySlotId BETWEEN 1 AND 2)
);
GO


/* 4.4 子弹库存表（按弹种 + 等级分行；拾取/购买/合成增减，携带池与库存同一账本）*/
CREATE TABLE dbo.TPlayerAmmo
(
    lPlayerId           BIGINT          NOT NULL,
    nAmmoId             INT             NOT NULL,                -- 含等级信息（T1~T5）
    lTotalCount         BIGINT          NOT NULL CONSTRAINT DF_TPlayerAmmo_lTotalCount       DEFAULT (0),  -- 库存总量
    lLockedCount        BIGINT          NOT NULL CONSTRAINT DF_TPlayerAmmo_lLockedCount      DEFAULT (0),  -- 已装配占用的数量
    dtUpdateTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_TPlayerAmmo_dtUpdateTime      DEFAULT (SYSUTCDATETIME()),
    CONSTRAINT PK_TPlayerAmmo PRIMARY KEY CLUSTERED (lPlayerId, nAmmoId),
    CONSTRAINT CK_TPlayerAmmo_lTotalCount CHECK (lTotalCount >= 0)
);
GO


/* 4.5 玩家防具实例表（4 部位各一套：头盔 / 战术背心 / 手套 / 背包）
   同一部位只能穿一件 → 由筛选唯一索引保证；不同部位材料独立不通用。 */
CREATE TABLE dbo.TPlayerArmor
(
    lArmorId            BIGINT IDENTITY(1, 1)   NOT NULL,
    lPlayerId           BIGINT          NOT NULL,
    nArmorCfgId         INT             NOT NULL,                -- 关联 CfgArmor（部位 × 品质）
    byPartId            TINYINT         NOT NULL,                -- 冗余部位：1头盔 2背心 3手套 4背包
    byQuality           TINYINT         NOT NULL,                -- 冗余品质：1=C 2=B 3=A 4=S
    nUpgradeLevel       INT             NOT NULL CONSTRAINT DF_TPlayerArmor_nUpgradeLevel    DEFAULT (0),
    bIsEquipped         BIT             NOT NULL CONSTRAINT DF_TPlayerArmor_bIsEquipped      DEFAULT (0),
    byObtainSource      TINYINT         NOT NULL CONSTRAINT DF_TPlayerArmor_byObtainSource   DEFAULT (0),
    dtObtainTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_TPlayerArmor_dtObtainTime     DEFAULT (SYSUTCDATETIME()),
    dtCreateTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_TPlayerArmor_dtCreateTime     DEFAULT (SYSUTCDATETIME()),
    dtUpdateTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_TPlayerArmor_dtUpdateTime     DEFAULT (SYSUTCDATETIME()),
    CONSTRAINT PK_TPlayerArmor PRIMARY KEY CLUSTERED (lArmorId)
);
GO
CREATE NONCLUSTERED INDEX IX_TPlayerArmor_lPlayerId ON dbo.TPlayerArmor (lPlayerId);
GO
-- 同一部位只能同时穿戴一件
CREATE UNIQUE NONCLUSTERED INDEX UX_TPlayerArmor_Equipped
    ON dbo.TPlayerArmor (lPlayerId, byPartId) WHERE (bIsEquipped = 1);
GO


/* 4.6 玩家技能表（每个角色多个技能，局内最多携带 2 个）
   携带数量上限：数据库层由应用层事务保证（筛选索引无法限计数），此处提供查询索引。 */
CREATE TABLE dbo.TPlayerSkill
(
    lPlayerSkillId      BIGINT IDENTITY(1, 1)   NOT NULL,
    lPlayerId           BIGINT          NOT NULL,
    nSkillCfgId         INT             NOT NULL,
    nUpgradeLevel       INT             NOT NULL CONSTRAINT DF_TPlayerSkill_nUpgradeLevel    DEFAULT (1),
    bIsEquipped         BIT             NOT NULL CONSTRAINT DF_TPlayerSkill_bIsEquipped      DEFAULT (0),
    byEquipSlotNo       TINYINT         NULL,                    -- 携带位 1~2
    dtObtainTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_TPlayerSkill_dtObtainTime     DEFAULT (SYSUTCDATETIME()),
    dtCreateTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_TPlayerSkill_dtCreateTime     DEFAULT (SYSUTCDATETIME()),
    dtUpdateTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_TPlayerSkill_dtUpdateTime     DEFAULT (SYSUTCDATETIME()),
    CONSTRAINT PK_TPlayerSkill PRIMARY KEY CLUSTERED (lPlayerSkillId),
    CONSTRAINT UQ_TPlayerSkill_Own UNIQUE (lPlayerId, nSkillCfgId)
);
GO
CREATE NONCLUSTERED INDEX IX_TPlayerSkill_Equipped
    ON dbo.TPlayerSkill (lPlayerId, byEquipSlotNo) WHERE (bIsEquipped = 1);
GO


/**************************************************************************************************
 * 五、战斗、快照与结算（M13）
 *    文档原文：战斗数据不实时同步，客户端每 5 秒上报一次快照；结算一次性上报，服务器按配表重算奖励。
 *    整局唯一 szRunId，所有写入按此幂等。
 **************************************************************************************************/

/* 5.1 战斗局主表 —— run_id 幂等的落点，重复提交只入账一次 */
CREATE TABLE dbo.TBattleRun
(
    szRunId             NVARCHAR(64)    NOT NULL,                -- 全局唯一 run_id，开局生成
    lPlayerId           BIGINT          NOT NULL,
    nStageId            INT             NOT NULL,
    byRunState          TINYINT         NOT NULL CONSTRAINT DF_TBattleRun_byRunState         DEFAULT (0),  -- 0进行中 1已结算 2异常作废
    byResult            TINYINT         NULL,                    -- 1 win 2 lose 3 timeout 4 abort
    nElapsedSec         INT             NULL,                    -- 上报时长 t，用于 900 秒上限校验
    nEndHpValue         INT             NULL,                    -- 结算剩余血量
    nKillCount          INT             NULL,                    -- 击杀总数（校验 ≤ CfgStage.nMonsterTotalCount）
    nAmmoUsedCount      INT             NULL,                    -- 弹药消耗（校验 ≤ 装配量 + 拾取量）
    nPickupCount        INT             NULL,                    -- 拾取总数（按类型明细见 TBattlePickup）
    nSkillUseCount      INT             NULL,                    -- 技能释放次数（校验 ≤ 击杀点数折算上限）
    nSnapshotCount      INT             NOT NULL CONSTRAINT DF_TBattleRun_nSnapshotCount     DEFAULT (0),  -- 快照次数，用于连续性校验
    bIsReconnected      BIT             NOT NULL CONSTRAINT DF_TBattleRun_bIsReconnected     DEFAULT (0),  -- 是否发生过 1 分钟内断线续局
    nAntiCheatMask      INT             NOT NULL CONSTRAINT DF_TBattleRun_nAntiCheatMask     DEFAULT (0),  -- 作弊标记位：1时长异常 2击杀超限 3弹药超限 4拾取超限 8技能超限 16 快照缺失
    nGoldReward         INT             NOT NULL CONSTRAINT DF_TBattleRun_nGoldReward        DEFAULT (0),  -- 服务器重算结果，不采信客户端
    nExpReward          INT             NOT NULL CONSTRAINT DF_TBattleRun_nExpReward         DEFAULT (0),
    dRewardRate         DECIMAL(18, 4)  NOT NULL CONSTRAINT DF_TBattleRun_dRewardRate         DEFAULT (1),  -- 失败 0.5 / 重复通关 1.0 等系数
    dtStartTime         DATETIME2(3)    NOT NULL CONSTRAINT DF_TBattleRun_dtStartTime        DEFAULT (SYSUTCDATETIME()),
    dtEndTime           DATETIME2(3)    NULL,
    dtCreateTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_TBattleRun_dtCreateTime       DEFAULT (SYSUTCDATETIME()),
    dtUpdateTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_TBattleRun_dtUpdateTime       DEFAULT (SYSUTCDATETIME()),
    CONSTRAINT PK_TBattleRun PRIMARY KEY CLUSTERED (szRunId)
);
GO
CREATE NONCLUSTERED INDEX IX_TBattleRun_lPlayerId ON dbo.TBattleRun (lPlayerId, dtStartTime DESC);
GO
CREATE NONCLUSTERED INDEX IX_TBattleRun_nStageId  ON dbo.TBattleRun (nStageId, dtStartTime DESC);
GO


/* 5.2 战斗快照表（客户端每 5 秒上报一次；用于断线恢复与作弊识别）
   保留策略建议：结算完成后 7 天可由定时任务归档清理，主表只留「最近一次快照」用于续局。 */
CREATE TABLE dbo.TBattleSnapshot
(
    lSnapshotId         BIGINT IDENTITY(1, 1)   NOT NULL,
    szRunId             NVARCHAR(64)    NOT NULL,
    nSequenceNo         INT             NOT NULL,                -- 快照序号（0,1,2…），断连/乱序识别
    nElapsedSec         INT             NOT NULL,                -- t：本局已进行秒数
    nHpValue            INT             NOT NULL,                -- hp：剩余血量（异常掉血检测）
    nKillCount          INT             NOT NULL,                -- kills：累计击杀
    nAmmoUsedCount      INT             NOT NULL,                -- ammo_used：累计弹药消耗
    nPickupCount        INT             NOT NULL,                -- pickups：累计拾取
    nSkillUseCount      INT             NOT NULL,                -- skill_uses：累计技能释放
    dtReportTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_TBattleSnapshot_dtReportTime  DEFAULT (SYSUTCDATETIME()),  -- 服务器接收时间
    CONSTRAINT PK_TBattleSnapshot PRIMARY KEY CLUSTERED (lSnapshotId),
    CONSTRAINT UQ_TBattleSnapshot_RunSeq UNIQUE (szRunId, nSequenceNo)
);
GO
CREATE NONCLUSTERED INDEX IX_TBattleSnapshot_Run ON dbo.TBattleSnapshot (szRunId, nElapsedSec);
GO


/* 5.3 战斗奖励明细表（由服务器按关卡配表 × result 计算后落库）*/
CREATE TABLE dbo.TBattleReward
(
    lRewardId           BIGINT IDENTITY(1, 1)   NOT NULL,
    szRunId             NVARCHAR(64)    NOT NULL,
    byItemType          TINYINT         NOT NULL,                -- 1货币 2武器 3子弹 4配件 5防具 6技能 7材料
    nItemRefId          INT             NOT NULL,
    lItemCount          BIGINT          NOT NULL CONSTRAINT DF_TBattleReward_lItemCount      DEFAULT (0),
    byRewardSource      TINYINT         NOT NULL CONSTRAINT DF_TBattleReward_byRewardSource  DEFAULT (1),  -- 1通关奖励 2首通奖励 3重复挑战奖励 4失败保底 5拾取折算
    dtCreateTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_TBattleReward_dtCreateTime    DEFAULT (SYSUTCDATETIME()),
    CONSTRAINT PK_TBattleReward PRIMARY KEY CLUSTERED (lRewardId),
    CONSTRAINT UQ_TBattleReward_Unique UNIQUE (szRunId, byItemType, nItemRefId, byRewardSource)
);
GO


/* 5.4 局内拾取计数表（服务器校验上限：超上限则结算阶段截断并记录异常）*/
CREATE TABLE dbo.TBattlePickup
(
    szRunId             NVARCHAR(64)    NOT NULL,
    byDropType          TINYINT         NOT NULL,                -- 1弹药箱 2医疗包 3Buff道具 4金币
    nPickupCount        INT             NOT NULL CONSTRAINT DF_TBattlePickup_nPickupCount    DEFAULT (0),  -- 客户端上报值
    nAllowedMaxCount    INT             NOT NULL CONSTRAINT DF_TBattlePickup_nAllowedMaxCount DEFAULT (0),  -- 服务器按配表算出的上限
    nSettledCount       INT             NOT NULL CONSTRAINT DF_TBattlePickup_nSettledCount   DEFAULT (0),  -- 实际按 min(上报, 上限) 认定
    bIsOverLimit        BIT             NOT NULL CONSTRAINT DF_TBattlePickup_bIsOverLimit    DEFAULT (0),  -- 是否触发截断（同步写 TAntiCheatLog）
    dtUpdateTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_TBattlePickup_dtUpdateTime    DEFAULT (SYSUTCDATETIME()),
    CONSTRAINT PK_TBattlePickup PRIMARY KEY CLUSTERED (szRunId, byDropType)
);
GO


/**************************************************************************************************
 * 六、离线挂机（M12）
 *    离线收益 = 当前最高已通关关卡的单次通关奖励 × 配置比例；5 分钟折 1 次，最多 96 次（8 小时）；
 *    不足 300 秒不结算并累计到下次；一律以服务器时间差为准，修改本地时间无效。
 **************************************************************************************************/

/* 6.1 离线结算记录表（幂等键：lPlayerId + dtPrevSettleTime，保证「同一秒重复请求只发一次」）*/
CREATE TABLE dbo.TOfflineSettle
(
    lSettleId           BIGINT IDENTITY(1, 1)   NOT NULL,
    lPlayerId           BIGINT          NOT NULL,
    dtPrevSettleTime    DATETIME2(3)    NOT NULL,                -- 本次结算所覆盖的起点（即上次结算时间）
    dtCurrentTime       DATETIME2(3)    NOT NULL,                -- 本次结算的服务器现在时刻
    nOfflineSec         INT             NOT NULL CONSTRAINT DF_TOfflineSettle_nOfflineSec     DEFAULT (0),  -- 离线总秒数（>8h 按 8h 截断）
    nTickCount          INT             NOT NULL CONSTRAINT DF_TOfflineSettle_nTickCount      DEFAULT (0),  -- 折算次数 = nOfflineSec / nSecPerTick，上限 96
    nRefStageId         INT             NOT NULL CONSTRAINT DF_TOfflineSettle_nRefStageId     DEFAULT (0),  -- 折算依据的最高已通关关卡
    jRewardDetail       NVARCHAR(MAX)   NULL,                    -- 本次发放明细 JSON
    bIsIdempotentHit    BIT             NOT NULL CONSTRAINT DF_TOfflineSettle_bIsIdempotentHit DEFAULT (0), -- 是否重复请求命中幂等
    dtSettleTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_TOfflineSettle_dtSettleTime    DEFAULT (SYSUTCDATETIME()),
    CONSTRAINT PK_TOfflineSettle PRIMARY KEY CLUSTERED (lSettleId),
    CONSTRAINT UQ_TOfflineSettle_Idempotent UNIQUE (lPlayerId, dtPrevSettleTime),
    CONSTRAINT CK_TOfflineSettle_jRewardDetail CHECK (jRewardDetail IS NULL OR ISJSON(jRewardDetail) = 1)
);
GO
CREATE NONCLUSTERED INDEX IX_TOfflineSettle_lPlayerId ON dbo.TOfflineSettle (lPlayerId, dtSettleTime DESC);
GO


/**************************************************************************************************
 * 七、商城（M16）与任务（M14，文档标注「未补，后补」，先给骨架）
 **************************************************************************************************/

/* 7.1 购买记录表 —— 支撑日限购计数、发货校验与查账 */
CREATE TABLE dbo.TShopPurchaseLog
(
    lPurchaseId         BIGINT IDENTITY(1, 1)   NOT NULL,
    lPlayerId           BIGINT          NOT NULL,
    nGoodsId            INT             NOT NULL,
    nPayCurrencyId      INT             NOT NULL,
    lPayAmount          BIGINT          NOT NULL CONSTRAINT DF_TShopPurchaseLog_lPayAmount   DEFAULT (0),
    nBuyCount           INT             NOT NULL CONSTRAINT DF_TShopPurchaseLog_nBuyCount    DEFAULT (1),
    byDeliveryState     TINYINT         NOT NULL CONSTRAINT DF_TShopPurchaseLog_byDeliveryState DEFAULT (1),  -- 1已发货 2发货失败待补
    szOrderNo           NVARCHAR(64)    NULL,                    -- 内部流水号
    szTradeDate         CHAR(8)         NOT NULL,                -- 交易日 yyyyMMdd，便于日限购统计
    dtBuyTime           DATETIME2(3)    NOT NULL CONSTRAINT DF_TShopPurchaseLog_dtBuyTime    DEFAULT (SYSUTCDATETIME()),
    CONSTRAINT PK_TShopPurchaseLog PRIMARY KEY CLUSTERED (lPurchaseId)
);
GO
CREATE NONCLUSTERED INDEX IX_TShopPurchaseLog_Limit
    ON dbo.TShopPurchaseLog (lPlayerId, nGoodsId, szTradeDate);
GO


/* 7.2 任务配置表（骨架 —— 文档中 M14 未补，结构已按常规任务系统预留）*/
CREATE TABLE dbo.CfgQuest
(
    nQuestId            INT             NOT NULL,
    szQuestName         NVARCHAR(64)    NOT NULL,
    byQuestType         TINYINT         NOT NULL,                -- 1主线 2日常 3周常 4成就
    byProgressType      TINYINT         NOT NULL,                -- 1累计击杀 2通关次数 3强化次数 4购买次数 …
    nTargetValue        INT             NOT NULL CONSTRAINT DF_CfgQuest_nTargetValue        DEFAULT (0),
    bIsRepeatable       BIT             NOT NULL CONSTRAINT DF_CfgQuest_bIsRepeatable      DEFAULT (0),
    szRemark            NVARCHAR(256)   NULL,
    dtUpdateTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_CfgQuest_dtUpdateTime        DEFAULT (SYSUTCDATETIME()),
    CONSTRAINT PK_CfgQuest PRIMARY KEY CLUSTERED (nQuestId)
);
GO


/* 7.3 玩家任务进度表（byQuestState：0进行中 1可领取 2已领取；对应文档「红点与可领取提示」）*/
CREATE TABLE dbo.TPlayerQuest
(
    lPlayerId           BIGINT          NOT NULL,
    nQuestId            INT             NOT NULL,
    nProgressValue      INT             NOT NULL CONSTRAINT DF_TPlayerQuest_nProgressValue  DEFAULT (0),
    byQuestState        TINYINT         NOT NULL CONSTRAINT DF_TPlayerQuest_byQuestState    DEFAULT (0),
    dtAcceptTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_TPlayerQuest_dtAcceptTime    DEFAULT (SYSUTCDATETIME()),
    dtRewardTime        DATETIME2(3)    NULL,
    dtUpdateTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_TPlayerQuest_dtUpdateTime    DEFAULT (SYSUTCDATETIME()),
    CONSTRAINT PK_TPlayerQuest PRIMARY KEY CLUSTERED (lPlayerId, nQuestId)
);
GO
CREATE NONCLUSTERED INDEX IX_TPlayerQuest_Claimable
    ON dbo.TPlayerQuest (lPlayerId, byQuestState) WHERE (byQuestState = 1);
GO


/**************************************************************************************************
 * 八、流水与风控日志（建议按月分区 / 定期归档，避免主库存膨胀）
 **************************************************************************************************/

/* 8.1 货币流水表 —— 金币与所有强化材料的每一次收支 */
CREATE TABLE dbo.TCurrencyLog
(
    lLogId              BIGINT IDENTITY(1, 1)   NOT NULL,
    lPlayerId           BIGINT          NOT NULL,
    nCurrencyId         INT             NOT NULL,
    lChangeValue        BIGINT          NOT NULL,                -- 正=收入 负=支出
    lBalanceAfter       BIGINT          NOT NULL CONSTRAINT DF_TCurrencyLog_lBalanceAfter    DEFAULT (0),
    byReasonType        TINYINT         NOT NULL,                -- 1关卡结算 2离线结算 3强化消耗 4商店购买 5任务奖励 6GM 7补偿 8其他
    szReasonExt         NVARCHAR(128)   NULL,                    -- 扩展说明
    szRunId             NVARCHAR(64)    NULL,                    -- 关联战斗局（可追溯到局）
    dtCreateTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_TCurrencyLog_dtCreateTime     DEFAULT (SYSUTCDATETIME()),
    CONSTRAINT PK_TCurrencyLog PRIMARY KEY CLUSTERED (lLogId)
);
GO
CREATE NONCLUSTERED INDEX IX_TCurrencyLog_lPlayerId ON dbo.TCurrencyLog (lPlayerId, dtCreateTime DESC);
GO
CREATE NONCLUSTERED INDEX IX_TCurrencyLog_szRunId   ON dbo.TCurrencyLog (szRunId) WHERE (szRunId IS NOT NULL);
GO


/* 8.2 物品流水表 —— 武器/子弹/配件/防具/技能的获得与消耗 */
CREATE TABLE dbo.TItemLog
(
    lLogId              BIGINT IDENTITY(1, 1)   NOT NULL,
    lPlayerId           BIGINT          NOT NULL,
    byItemType          TINYINT         NOT NULL,                -- 2武器 3子弹 4配件 5防具 6技能（与 TBattleReward 对齐）
    nItemRefId          INT             NOT NULL,
    nChangeValue        INT             NOT NULL,
    nLevelAfter         INT             NULL,                    -- 强化后的等级（仅养成类有效）
    byReasonType        TINYINT         NOT NULL,                -- 1关卡结算 2挂机 3任务 4商店购买 5强化消耗 6 GM 7其他
    szRunId             NVARCHAR(64)    NULL,
    dtCreateTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_TItemLog_dtCreateTime         DEFAULT (SYSUTCDATETIME()),
    CONSTRAINT PK_TItemLog PRIMARY KEY CLUSTERED (lLogId)
);
GO
CREATE NONCLUSTERED INDEX IX_TItemLog_lPlayerId ON dbo.TItemLog (lPlayerId, dtCreateTime DESC);
GO


/* 8.3 风控异常记录表 —— 服务器校验不通过时的留痕（不直接判罚，供人工/策略二次处理）*/
CREATE TABLE dbo.TAntiCheatLog
(
    lLogId              BIGINT IDENTITY(1, 1)   NOT NULL,
    lPlayerId           BIGINT          NOT NULL,
    szRunId             NVARCHAR(64)    NULL,
    byRuleType          TINYINT         NOT NULL,                -- 1时长超限 2击杀超限 3弹药超限 4拾取超限 5技能超限 6快照缺失/乱序 7重复提交
    nReportValue        INT             NOT NULL CONSTRAINT DF_TAntiCheatLog_nReportValue    DEFAULT (0),  -- 上报值
    nAllowedValue       INT             NOT NULL CONSTRAINT DF_TAntiCheatLog_nAllowedValue   DEFAULT (0),  -- 允许值
    tDetail             NVARCHAR(MAX)   NULL,
    byHandleState       TINYINT         NOT NULL CONSTRAINT DF_TAntiCheatLog_byHandleState   DEFAULT (0),  -- 0未处理 1已忽略 2已处罚
    dtCreateTime        DATETIME2(3)    NOT NULL CONSTRAINT DF_TAntiCheatLog_dtCreateTime    DEFAULT (SYSUTCDATETIME()),
    CONSTRAINT PK_TAntiCheatLog PRIMARY KEY CLUSTERED (lLogId)
);
GO
CREATE NONCLUSTERED INDEX IX_TAntiCheatLog_lPlayerId ON dbo.TAntiCheatLog (lPlayerId, dtCreateTime DESC);
GO


/**************************************************************************************************
 * 九、视图（M15 排行榜）
 **************************************************************************************************/

/* 9.1 关卡最佳用时排行榜
   同分规则：按 nBestTimeMs 升序 → 并列时以 dtFirstClearTime 早者优先（先到先得）。
   注意：实时榜单建议由应用层同步至 Redis 做分页，本视图仅用于离线核对与冷启动回填。 */
CREATE VIEW dbo.VStageRank
AS
SELECT      s.nStageId,
            p.lPlayerId,
            p.szNickName,
            s.nBestTimeMs,
            s.dtFirstClearTime,
            p.nPlayerLevel,
            ROW_NUMBER() OVER (PARTITION BY s.nStageId
                               ORDER BY s.nBestTimeMs ASC, s.dtFirstClearTime ASC) AS nRankNo
FROM        dbo.TPlayerStage AS s
INNER JOIN  dbo.TPlayer      AS p ON p.lPlayerId = s.lPlayerId
WHERE       s.bHasCleared = 1
  AND       s.nBestTimeMs IS NOT NULL;
GO


/**************************************************************************************************
 * 十、后续待办 TODO（与文档对齐）
 *   1. M03 战斗公式：文档中「战斗公式」页为空白，数值补齐后建议新增 CfgCombatFormula 明确伤害/命中判定参数，
 *      否则服务器只能做「上限复核」无法精确复算。
 *   2. M14 任务：CfgQuest / TPlayerQuest 目前是通用骨架，等任务案补完后细化 byProgressType 与目标奖励。
 *   3. 邮件系统：本次未建 TMail 表。商城发货失败、补偿发放均需要它，建议下一版补上。
 *   4. 文档 Sheet18 美术需求中的资源量（7 武器模型 / 16 防具 / 12 子弹项等）建议在合约锁定后
 *      回填到 Cfg* 表作为命名与图标资源索引字段（当前未列资源管理表）。
 *   5. 分库分表预案：所有玩家数据均以 lPlayerId 为首列聚集/索引前缀，
 *      后续按 lPlayerId % N 水平拆分时无需改表结构。
 *   6. TBattleSnapshot 数据量大，建议独立文件组 + 7 天滚动归档。
 **************************************************************************************************/
