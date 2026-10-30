# 双主题：老主题 / 新主题

> 一个开关，两套界面语言。默认仍是老主题；新主题是一层「皮肤」，不复制组件、不改页面逻辑。

## 1. 是什么

| | 老主题 `skin = "classic"` | 新主题 `skin = "editorial"` |
| --- | --- | --- |
| 界面语言 | 九套预设原本的观感（默认 / 琥珀终端 / 粗野主义 / 深空指挥室 / 矩阵绿 / 纸本 / 柔和流行 / 柑橘 / 双墨） | 纸本：书刊排版、发丝线、下划线页签、等宽数字、无卡片、无阴影 |
| 颜色令牌 | 由所选预设决定 | 固定取 `presets/paper.css`（与调试版前端逐值一致） |
| 版式 | 组件原样 | 由 `styles/skin-editorial.css` 重写 |
| 默认 | ✅ 升级后第一眼仍是它 | 需要手动切过去 |

两条硬规矩：

1. **老主题零影响**。皮肤层每一条规则都挂在 `:root[data-skin="editorial"]` 之下，切回 `classic` 时一条都不生效。
2. **新主题只有一种颜色**。切到新主题时预设被钉死为「纸本」，预设下拉同时置灰——界面语言已经换了，再让颜色跟着九套色板漂移，就不再是同一个主题。

## 2. 怎么切

| 入口 | 位置 | 行为 |
| --- | --- | --- |
| 偏好浮层「外观」 | 右上齿轮 → 第一组 | 经典 / 纸本 二选一，带一句说明 |
| 顶栏快捷按钮 | 齿轮右侧的字形图标 | 一键来回切，不用开浮层 |
| Cookie | `skin=classic \| editorial` | 与其它偏好同一条链路（`client-cookie`） |

切换是**无闪烁**的：`theme-boot` 在首帧之前就把 `data-skin` / `data-theme-preset` 写进 `<html>`，服务端渲染保持静态。

## 3. 实现

```
web/src/
├── lib/preferences/
│   ├── theme.ts                  SKIN_OPTIONS / Skin / EDITORIAL_SKIN_PRESET
│   ├── preferences-config.ts     skin 键（默认 classic，client-cookie）
│   └── theme-utils.ts            applySkin(skin, preset) / applyThemePreset 受 skin 约束
├── scripts/theme-boot.tsx        首帧写 data-skin；新主题把 preset 钉到 paper
├── stores/preferences/
│   ├── preferences-store.ts      state.skin + setSkin
│   └── preferences-provider.tsx  从 DOM 读 skin；订阅 skin 变化重应用
├── app/
│   ├── layout.tsx                 <html data-skin>
│   ├── globals.css                @import styles/skin-editorial.css
│   └── (main)/_components/
│       ├── main-content.tsx       顶栏加 SkinSwitcher
│       └── sidebar/
│           ├── layout-controls.tsx  偏好浮层「外观」两组
│           └── theme-switcher.tsx   SkinSwitcher 组件
└── styles/skin-editorial.css     ← 全部版式翻译都在这一份里（约 850 行）
```

### 为什么要「钉预设」

`paper.css` 提供的不只是颜色：它把 `--radius` 压到 0.25rem、把九档阴影全部归零、把 Tailwind 原生色（red/amber/emerald/blue…）整体降到同一条低彩度暖墨色系。新主题直接复用它，等于**白拿一套已经调好的令牌**，皮肤层只负责「结构 → 排版」。这也是为什么颜色能和调试版前端逐值相同。

### 皮肤层改了什么（15 组）

| 组 | 从 | 到 |
| --- | --- | --- |
| 版面骨架 | inset 侧栏（圆角 + 外距 + 阴影） | 一张纸：侧栏只剩一条竖线，内容区按书页留白 |
| 顶栏 | 半透明 + 毛玻璃 | 纸底实色 + 一条发丝线 |
| 侧栏 | 圆角填充块选中 | 左侧 2px 书脊 + 墨色文字；组标题 10px / 0.14em |
| 卡片 | 描边盒子 | 无底色 / 无圆角 / 无阴影，只剩一条上沿细线 |
| 表格 | 网格 + 斑马纹 | 表头一条墨线，行间发丝线，行高 34px，悬停整行浅 accent |
| 页签 | 胶囊分段 | 下划线页签，选中 2px 主色 |
| 按钮 | 4 档圆角 + 阴影 | 2px 圆角，只有实心与描边两种重量 |
| 徽标 | 胶囊 | 小签：17px 高 / 2px 圆角 / 等宽数字 |
| 输入框 | 填充 + 光环 | 透明底 + 发丝边，聚焦只换边色 |
| 开关 | 默认尺寸 | 32×18（与调试版前端 `.sw` 同尺寸） |
| 浮层 | 大圆角 + 大阴影 | 3px 圆角 + 一条发丝边 + 极轻投影，标题转衬线 |
| 标题层 | 无衬线 | 书眉 / 版块名 / 浮层标题 / 空态转衬线，正文与数字仍无衬线 |
| 数字 | 比例数字 | 全站 tabular-nums，KPI 26px |
| 滚动条 | 系统默认 | 10px 透明轨 + 常驻细滑块（标准属性 + `::-webkit-*` 兜底） |
| 动效 | 淡入 / 缩放 | 装饰性入场静音，保留 spin 与骨架屏 |

## 4. 依据

- **等宽数字与发丝线**是数据密集界面的通行做法：数字列靠 `tabular-nums` 对齐，不靠边框；对比度对小字号不打折（3:1 只给最大那一行）。参考 Dashboard Typography: A Type Scale for Data-Dense UI。
- **字体分工**是这套语言真正的辨识度来源：衬线只做标题，无衬线承担正文与数字——衬线在 12–13px 下辨识度更差，这一点与 `paper.css` 的注释一致。
- 纸色取 `oklch(0.962 0.010 86)`、墨色 `oklch(0.205 0.014 70)`、主色 `oklch(0.5 0.078 200)`、危险 `#B12C2E`、圆角 4px、零阴影——与调试版前端同一组值。

## 5. 已知边界

1. 皮肤只改**外观**，不改页面结构。调试版前端里那些「书眉 + 五格数字 + 分卷 + 记录行」的版式，靠的是页面本身用了 `components/editorial.tsx` 的原语；没用到的页面在新主题下会是「纸本外观 + 原版结构」。
2. `next build` 未在真机上跑（按约定不在 `Desktop/ARTEX-HYDRA` 编译）。合入前请在你自己的构建环境里过一遍 `next build`。
3. 新主题下「主题预设」下拉置灰是刻意行为；要换色，请先切回经典。

## 6. 第二批：多级页面落地（本轮）

上一轮只做了「外观开关」，这一轮开始把二级页面搬到同一套语言里。已完成：

| 页面 | 路由 | 做了什么 |
| --- | --- | --- |
| 任务详情 | `/function/tasks/detail?id=` | 去掉「Mission Control」按钮条：工具行右对齐 → **书眉**（编号 · 分类 · 状态 · 创建时间 / 26px 标题 / 目标一行）→ **五格数字行**（目标达成、在途 Worker、已确认发现、待审批、未收口分支，每格可点跳对应页签）→ **下划线页签**（吸顶，11 个页签不再分簇画竖线） |
| 子代理运行页 | `/function/tasks/session?id=` | **新路由**：书眉 + 返回任务，正文直接复用「会话」页签的三栏（会话清单 / 转录 / 上下文与待办）。任务详情的工具行加了「主 Agent 对话」入口 |
| 工具执行详情 | `/function/commands/detail?id=` | **新路由**：书眉 + 五格字段条（工具 / Worker / 任务 / 时间 / 状态）+ 输入原文 + 输出原文（失败时左侧一条危险色）。工具执行表新增「详情」列 |
| 工具执行 | `/function/commands` | 页头换成书眉（副标题 + 总条数） |
| LLM 录制 | `/function/llm-records` | 同上 |
| 工作空间 | `/function/workspace` | 面包屑之上加书眉 |
| 数据保留 / 存储与清理 | `/system/retention`、`/system/storage` | 页头换成书眉（徽标放进 meta） |
| Skill | `/system/skills` | 紧凑页头换成书眉，高度不变 |

守卫脚本 `scripts/audit-editorial.mjs` 增加两条不变量：

4. `skin-editorial.css` 的每条规则都必须挂在 `:root[data-skin="editorial"]` 之下；
5. 双主题接线必须齐全（`theme.ts` / `preferences-config` / `theme-utils` / `theme-boot` / `layout` / `provider` / `globals.css`），少一处就会「切了没生效」或「刷新后掉回老主题」。

当前 `node scripts/audit-editorial.mjs` 输出：editorial 24 块、paper 令牌 37 项、9 套主题、页面 30 个其中 24 个用报头原语、skin 103 块、接线完整。

## 7. 第二批下半：页签正文（版块原语）

新增 `components/editorial-sheet.tsx`，六个原语把「版块」这件事从页面里抽出来：

| 原语 | 锚点 | 作用 |
| --- | --- | --- |
| `Sheet` | `data-editorial="sheet"`（同时带 `data-slot="card"`） | 一个版块。老主题下就是原来的卡片，纸本下压平成「上沿一条发丝线」 |
| `SheetHead` | `data-editorial="sheet-head"` | 标题 + 计数 + 拉到右端的细线 + 提示 + 右端动作，可带图标与色相 |
| `GroupHead` | `data-editorial="group-head"` | 分卷头：圆点（red/amber/green/sky/muted）+ 卷名 + 计数 + 细线 |
| `RecordRow` | `data-editorial="record"` | 记录行，`--rec-cols` 控列宽；可点击、可展开 |
| `RecordSub` | — | 记录行的第二排（来源 · 时间 · 证据数），2px 圆点分隔 |
| `SheetFoot` | `data-editorial="sheet-foot"` | 落款：左「共 N 条」，右一句提醒 |

已经搬到这套语言里的页签：

| 页签 | 做了什么 |
| --- | --- |
| 发现 | 整体重写：按严重度分卷（critical → high → medium → low → other），每卷一列表；行内第一排是「编号 · 摘要 · 置信度 · 状态 · 详情」，第二排是来源与时间；证据块改成左竖线引文；页脚「共 N 条 · 分 M 卷」 |
| 总览 | 15 张卡片里 11 张换成 `Sheet + SheetHead`（保留图标色相），两块告警卡（blocked / branches）保留原卡片——它们靠红/琥珀描边表达状态，压平会丢信息 |
| 测试资产 | 表格卡换成 `Sheet`，表头吸顶不变，分页条改 `sheet-foot` |
| 播报板 | 卡片换 `Sheet`，工具条保留在标题行下，底部统计条改 `sheet-foot` |
| 资产覆盖图 | 卡片换 `Sheet`，画布铺平（去圆角内嵌） |
| 复测 | 卡片换 `Sheet + SheetHead`，页脚「共 N 条 · 每页 20」 |
| 报告 | 卡片换 `Sheet + SheetHead`（字数走 i18n「N 字」），页脚落款 |
| 时间线 | 卡片换 `Sheet + SheetHead`，页脚「检查点按时间正序」 |
| 探索链路 | 卡片换 `Sheet + SheetHead`，标题与提示改走 i18n（`taskDetail.graph.*`） |

没动的三个：**会话**（2778 行的三栏对话台，本身就是一套独立语言，改动风险大于收益）、**拦截审批**（复用 `components/approval-records.tsx`，已是分段式排版）、**软件分析**（自带页头与操作区）。

### 发现详情（`/function/findings/detail?id=`）

和任务详情同一套书眉：工具行只留侧栏按钮 + 返回；书眉一行是「编号 · 漏洞类型 · 严重度 · 状态 · 发现时间」，标题 26px，下一行是所属任务（可点回任务）；五格数字行换成「严重度 / 处理状态 / 复核态 / 涉及资产 / 证据体量」；页签合成下划线索引（总览 / 链路）。四张卡片换成 `Sheet + SheetHead`：摘要、证据（块改 `data-editorial="evidence"`，标题带字符数）、详细报告（标题带字数、动作位放复制）、右栏状态区（`divide-y` 平铺，去卡片边）。

### 借这一轮补的 i18n 洞

逐页核对「`useTranslations` 命名空间 → 键」时发现：这批新写的书眉文案有几处是**只写了调用、没进 `zh.json`**（`llmRecords.subtitle`、`commands.subtitle`、`workspace.title/subtitle/colHint`），以及两个整块的命名空间漏建（`commandsDetail`、`taskSessionsPage`）——真机上会直接显示键名并刷 `MISSING_MESSAGE`。已补齐，并把核对脚本固化：按「最近的 `useTranslations` 绑定」解析每个调用，全站 33 个 `page.tsx` 现在是 0 缺失。

## 8. 第三批：详情路由 · 系统族 · 浮层

### 7-1 四类详情路由（都能刷新、能回退、能贴给人）

| 路由 | 数据来源 | 做了什么 |
| --- | --- | --- |
| `/function/assets/detail?id=` | `/assets`（按 id 定位） | 书眉（`#id · 类型 · 最后出现`）+ 五格数字（端口 / 技术栈 / 任务 / 发现 / 状态码）+ 画像（11 个字段平铺）+ 开放端口 + 关联发现（按严重度标注，箭头进发现详情）+ 出现过的任务 + 该主机最近 20 条流量 |
| `/function/traffic/detail?id=` | `/traffic/exchange` + `/traffic` 列表 | 请求 / 响应原文分两栏（复用 `HttpCodeBlock`），五格数字（状态码 / 方法 / 请求行数 / 响应行数 / 响应体量）+ 时序版块（**如实标注未采集**）+ 引用它的发现（**如实标注后端没有反查索引**） |
| `/function/llm-records/detail?id=` | `/llm/records/:id` | 输入 / 输出两栏（归一化视图与 provider 原文可切），五格数字（入 / 出 / 缓存 / 耗时 / 费用）+ token 账 + 计费 + 任务与会话 |
| `/system/intercept/approvals/detail?id=` | `/intercept/history` + `ApprovalDetail` | 正文直接复用行内展开的 `ApprovalDetail`（逐字相同），待裁决的票在这里也能裁决；五格数字是裁决 / 命中来源 / 发起方 / 申请时间 / 裁决时间 |

入口都补上了：资产表每行一个箭头、报文抽屉头「独立页面」、录制内嵌详情「独立页面」、审批展开区「独立页面」。

### 7-2 系统族详情

| 路由 | 要点 |
| --- | --- |
| `/system/tools/detail?key=` | 参数 schema 逐条平铺（类型 / 必填 / 说明）+ 绑定 Agent + 最近 5 次调用（按工具名从 `/commands?q=` 捞，点击进执行详情） |
| `/system/skills/detail?name=` | 正文（SKILL.md 原样摊开，不做二次解析）+ 文件清单 + 最近 20 次调用（skill_usage 账本）+ 用过它的 Agent |
| `/system/mcp/detail?id=` | 连接方式（URL / 命令 / 环境变量键名；**值不回传**）+ 工具清单 + 握手如实标注未落库 |
| `/system/llm/detail?id=` | 参数（思考 / 流式 / 模态 / 故障转移）+ 端点（供应商 / Base URL / 代理 / Key 指纹）+ 近 30 天用量 + 回退链（点任一环进对应详情） |
| `/system/agents/detail?key=` | 从「裸编辑器」改成书眉 + 版块，编辑器仍是同一个 `AgentEditor` |

### 7-3 浮层与面板（B / C 类清点）

`通知中心` 是这一批唯一缺的 C 类浮层，已新建 `_components/notification-center.tsx` 挂在顶栏铃铛上：只列「要动手的」两件事 —— 待裁决的审批票（点开进审批详情）与最近 10 条 critical/high 发现（点开进发现详情），数字即待办数，关掉面板不做轮询。

日志页补上 B 类「行内展开」：点一行展开前后各 3 行上下文 + 单行复制，一次只展开一行。

其余 13 个 B 类面板与 5 个 C 类浮层（⌘K 命令面板、偏好浮层、用户菜单、新建任务、导出）在库里本来就有，逐一核对后只补了入口与文案，没有重写。

### 还剩什么

- B 类「通知推送 · 模板编辑」：后端 `NotificationChannel` 没有模板字段，只做了渠道配置 + 测试发送；要做真模板编辑得先加后端字段。
- 发现详情 / 资产详情 / 报文详情 / 录制详情 / 审批票详情 / Agent·工具·Skill·MCP·模型配置详情 还没有独立路由。
- B 类页内深层面板（14 个）与 C 类全局浮层（6 个）未做。
