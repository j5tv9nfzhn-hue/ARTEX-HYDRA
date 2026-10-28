# 主题预设「纸本 / Paper」

> 状态：已接入，可在 **系统 → 外观** 里切换（或命令面板搜索主题）
> 文件：`web/src/styles/presets/paper.css`
> 关联调研：`artex-console-v2/调研报告.md`（方案 A）

## 它是什么

第七套预设，与现有 6 套并存、互不影响。它是**令牌层 + 一层很薄的排版层**，不是另做一套组件。

设计来源是《金融时报》的 Origami 设计系统，但只取机制、不取表层：

| 取向 | 做法 | 依据 |
|---|---|---|
| 纸要亮 | `oklch(0.962 0.010 86)` ≈ `#F5F2EB` | FT 的 paper `#FFF1E5` 明度在 0.96 以上，靠色相偏暖而非压暗；压暗会读成「陈纸 / 灰尘」 |
| 主色不是红 | `--primary` 走紫青 `#197075`，红让给 `--destructive` | FT 的分工：青绿做链接与主操作，酒红只做栏目名。这样按钮和真错误不撞色 |
| 圆角不过 4px | `--radius: 0.25rem` → 派生 0 / 2 / 4 / 8px | FT 全页不超过 4px，是印刷品的刀切边 |
| 阴影归零 | `--shadow-*: none` | 层级交给 1px 描边与留白 |
| 字体分工 | 衬线只管 `.font-display` + `h1/h2` | 报纸感的真实来源是字体分工，不是纸张纹理；衬线在 12–13px 下辨识度更差，所以正文/数字/表格一律无衬线 |
| 图表是「印刷的图」 | `--chart-1..5` 用一条暖墨阶 | 纸张主题不引入第二套彩色 |

## 关键数值（均已实测）

浅色，纸 `#F5F2EB`：

| 项 | 值 | 对比度 |
|---|---|---|
| 正文 | `oklch(0.205 0.014 70)` #1B1610 | **16.06:1** |
| 次要文字 | `oklch(0.480 0.016 70)` #645C54 | **5.87:1** |
| 主色作文字 | `#197075` | **5.18:1** |
| 白字压主色 | — | **5.78:1** |
| 危险色作文字 | `#B12C2E` | **5.74:1** |
| `--border` | `oklch(0.205 0.014 70 / 0.34)` | **2.14:1** |
| `--input` | `oklch(0.205 0.014 70 / 0.50)` | **3.32:1** |
| hover 面 muted / secondary / accent | L 0.916 / 0.924 / 0.907 | 1.15 / 1.12 / 1.18:1 |

深色，`#17140F`：正文 15.0:1、次要 7.41:1、主色 8.14:1、危险 5.53:1。

> 这几个 hover 面必须 ≥1.12:1。第一版给的是 1.05–1.08，鼠标移上去几乎没有反馈——和之前在「双墨」里踩的是同一个坑。

## 为什么排版层要写在选择器里

`globals.css` 里的 `--font-heading` 定义在 `@theme inline` 中，会被内联进工具类，运行时改这个变量不生效。而 `.font-display` 是一个普通类（`globals.css` 第 228 行），从选择器覆盖是可靠的。

`.font-display` 全站 11 处，专用于巨型数字与空态标题——正好是「标题层」。换掉它即得到头版观感，正文与表格完全不受影响。

## 全局切换怎么发生

无需改任何组件，机制已经存在：

1. `paper.css` 里 `label:` / `value:` 两个注释头被 `src/scripts/generate-theme-presets.ts` 扫描；
2. 跑 `npm run generate:presets` 会把条目写进 `src/lib/preferences/theme.ts` 的生成区；
3. 外观下拉与命令面板都从 `THEME_PRESET_OPTIONS` 渲染，选择后 `applyThemePreset()` 给 `<html>` 打 `data-theme-preset="paper"`；
4. CSS 按 `:root[data-theme-preset="paper"]` 生效，深色由 `.dark:root[...]` 叠加。

所以**新增主题 = 加一个 CSS 文件 + 一行 `@import` + 跑一次生成器**。

## 改了什么（共 3 处）

```
M web/src/app/globals.css                  +1 行 @import
M web/src/lib/preferences/theme.ts          生成器写入的 1 个条目
A web/src/styles/presets/paper.css          新文件
```

组件零改动，现有 6 套预设零改动。

## 回滚

```bash
rm web/src/styles/presets/paper.css
# 删掉 globals.css 里 @import "../styles/presets/paper.css"; 这一行
npm run generate:presets
```

## 已验证

静态验证（未启动 Next、未构建），把真实 `paper.css` 挂到一个最小页面后用 `getComputedStyle` 断言：

- 正文/纸、次要文字、主色、危险色、描边、表单边 —— 全部达标；
- `.font-display` 与 `h1` 解析到衬线栈，`body` 仍为无衬线；
- `--radius: 0.25rem` 派生 sm=0px、xl=8px；
- `.card` 的 `box-shadow` 为 `none`；
- `table` 的 `font-variant-numeric` 为 `tabular-nums`；
- 深色切换后六项令牌全部换值；
- 把 `data-theme-preset` 改成别的值，衬线立刻消失 —— 证明排版层没有外溢；
- 0 控制台错误；`biome check` 通过。

## 已知取舍

1. **中文衬线依赖系统字体。** Windows 上 `Songti SC` / `Noto Serif CJK SC` 多半没有，会落到 `SimSun`（宋体）——观感偏硬但仍是衬线。若要统一，需要在 `next/font` 里引入一个中文衬线 Web Font（体积代价较大，约 3–8MB 子集化后仍不小）。
2. **只覆盖了标题层。** 若要让表格、工具栏也带报纸味，需要动组件，那是另一批工作。
3. **与「双墨」并存。** 你机器上的仓库里还有 `two-inks.css`；如果确定走「纸本」，可以一并删掉，否则外观列表里会有两套相近的浅色主题。

---

## 落地记录（分阶段）

真正的约束是**现有主题一个不能坏**，所以每一步都遵循同一条规则：

> 结构锚点（`data-editorial`）与主题无关地渲染；
> 报纸形态只在 `[data-theme-preset="paper"]` 下开启。

切到其余 8 套主题时，报头退回普通页头、章节号自动隐藏、卡片网格恢复原样。

### 阶段一 · 令牌层 + 总览页（已完成）

| 文件 | 改动 |
|---|---|
| `styles/presets/paper.css` | 新增 · 浅/深两套令牌 + 衬线标题层 |
| `styles/editorial.css` | 新增 · 编辑排版层 |
| `components/editorial.tsx` | 新增 · `Masthead` / `SectionHead` / `KpiBand` / `KpiRow` |
| `app/globals.css` | +3 行 `@import` |
| `lib/preferences/theme.ts` | +8 行（生成器格式，纸本） |
| `app/(main)/dashboard/page.tsx` | 报头 + 章节号 01–04 + 数据栏 |

### 阶段二 · 四个核心页 + 表格语法（已完成）

| 文件 | 改动 |
|---|---|
| `function/findings/page.tsx` | `h1` + 副标题 → `Masthead`；挂 `data-editorial="body"` |
| `components/approval-records.tsx` | 挂 body 锚点（页面本身只是壳） |
| `function/tasks/page.tsx` | 挂 body 锚点（该页原本没有页面标题，不新增） |
| `function/tasks/detail/page.tsx` | 挂 body 锚点（full-bleed，自带头部） |
| `dashboard/page.tsx` | **回扣**：挂 body 锚点，让 `.uppercase` 收窄后仍然生效 |
| `styles/editorial.css` | 表格语法 + `.uppercase` 作用域收窄 |

### 两条必须记住的教训

1. **`.uppercase` 不能全局收口。** 阶段一图省事写成 `[data-theme-preset="paper"] .uppercase`，
   会连侧栏、顶栏这些「机器外壳」一起改。已收窄到 `[data-editorial="body"]`，
   所以新页面接入时必须挂 body 锚点，否则拿不到正文腔调。
2. **不要对被 `@theme inline` 内联的令牌做运行时改写。** 颜色能用变量覆盖，
   是因为 `globals.css` 里写的是 `--color-background: var(--background)`（引用，不是字面值）；
   而 `.font-display` 是写死的字体栈，只能在选择器层覆盖。

### 表格语法（阶段二补上）

纸本的表格没有网格、没有斑马纹：

- `thead` 上 2px 实线（用 `--foreground` 而不是 `--border`，这是报纸的「表头压线」）
- 表头文字走 `--muted-foreground`，字距 0.04em，全部小写化
- 行间 1px 发丝线，斑马纹强制透明
- 整表 `tabular-nums`

同时覆盖 `components/ui/table` 与手写的 `<table>` 两种写法，都靠
`[data-editorial="body"]` 限定范围，不波及尚未接入的其他页面。

### 阶段三 · 全站铺开（已完成）

阶段一二只覆盖 5 个页面。阶段三要解决的是「剩下 23 个页面怎么进来」，答案不是逐个手改。

**第一步：把锚点上移到全局壳。**

`app/(main)/_components/main-content.tsx` 里那个 `p-4 md:p-6` 的包装层是所有普通页的公共父节点。
给它挂一次 `data-editorial="body"`，27 个页面同时获得表格语法、去大写、等宽数字。
比在 26 个文件里各猜一次「哪个 div 是根元素」安全得多——**26 次猜测就是 26 次猜错的机会**。

full-bleed 的任务详情页不走这个包装层，它自己的 `Tabs` 根已经挂了锚点。

**第二步：codemod 改造页头，而不是人肉改。**

按页头的实际形态分了类，只动能安全机械化的那一种：

| 形态 | 处理 | 例子 |
|---|---|---|
| `h1` + 副标题独立成块 | → `Masthead` | assets / traffic / agents / lan / logs / settings / sync / external / intercept ×2 / notify / tools / llm / mcp |
| `h1` 内联在图标 + 徽章横排里 | **不动** | commands / llm-records / retention / storage |
| 详情页内联小标题 | **不动** | findings/detail / tasks/detail / agents/detail |
| 工具栏里的 `text-sm` 标题 | **不动** | skills |
| 本来就没有页面标题 | **不动**，只继承锚点 | tasks / chat / workspace |

字号通过 `titleClassName` / `subtitleClassName` 透传，原文是 `text-xl` 就还是 `text-xl`——
**统一的是结构，不是把别人的字号悄悄改小**。

结果：28 个页面里 16 个使用报头原语，其余 12 个各自有正当理由。

**第三步：把承诺写成可复跑的守卫。**

`scripts/audit-editorial.mjs`（零依赖、不需要构建）检查三个不变量：

1. `editorial.css` 每条规则都必须限定在 `[data-theme-preset="paper"]` 下
   （唯二例外是两个「默认态」选择器，且必须显式列白名单）；
2. `paper.css` 覆盖完整令牌契约（37 项），且浅色/深色两套作用域都在；
3. 每个页面要么继承全局壳锚点，要么自己挂一个。

```bash
node scripts/audit-editorial.mjs
```

这条检查守的正是最容易悄悄破掉的那句承诺：**新主题不能影响其余 8 套主题**。

### 阶段四 · 真实渲染检验（已完成）

前三阶段全部建立在静态检查上，**没有任何一次真实渲染**。这一轮在沙箱里起了真前端
（`next dev`，mock 数据，无后端），用 Playwright 截图核对。静态检查发现不了的两件事立刻现形：

**① 原生 Tailwind 色板没有收口。**

组件里散落着 `text-blue-400` / `bg-amber-500/10` 这类原生色（全站约 600 处）。
纸张主题的令牌层管不到它们，于是「探索 / 暂停 / 完成」三个状态仍然是纯蓝、纯黄、纯绿——
「整屏只有一种彩色」的纪律等于没落地。

修法不是改 600 处类名，而是**覆盖 `--color-<family>-<shade>` 变量本身**：
按色相等价映射到纸张的低彩度语义色（红=危险、黄=警示、绿=正常、蓝=信息、灰=结构），
彩度压到 0.04–0.14。一条令牌改动，全站收口，包括 `lib/severity.ts` 里那套 severity 色。

**② 主 KPI 被装进了盒子里。**

报纸的导语不该有边框。补一条规则让 `kpi-band` 的首张卡片在纸张主题下背景透明。

**顺带补上的**：装饰性入场动效（`animate-fade-up` / `scale-in` / `pulse-soft` / `glow-once`）
在纸张主题下静音，`animate-spin` 保留——加载态是真实反馈，不是装饰。

实测确认作用域：同一元素 `paper → animation-name: none`，`tangerine → fade-up`。

### 检验结论

| 页面 | 浅色 | 深色 | 对照主题 |
|---|---|---|---|
| 总览 | ✓ 报头 / 章节号 / 数据栏 / 色板收口 | ✓ | ✓ tangerine 未受影响 |
| 工具 | ✓ 报头衬线 + 双线 | ✓ | — |
| 任务 | ✓ 表格 2px 表头压线 + 行间发丝线 | ✓ | ✓ tangerine 无表头压线、圆角阴影如常 |

**已知遗留（未修，非本次引入）**：
- `nav.item.external` 在侧栏显示为原始 key —— i18n 缺词条，改造前就存在
- 沙箱容器缺少中文字体与 Georgia，衬线数字回落到容器默认字体；
  真机（Windows）有宋体/SimSun/Georgia，观感会不同
- 生产构建在本容器内 OOM（1.9 GB 内存跑不动 Turbopack + React Compiler），
  故用 `next dev` 验证；类型检查、CSS 解析、结构守卫三项均已通过
