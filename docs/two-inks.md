# 主题预设「双墨 / Two Inks」

> 状态：已接入，默认可用（可在 系统 → 外观 里切换）
> 关联：`web/src/styles/presets/two-inks.css` · `scripts/audit-density.mjs`

## 它解决的不是配色，是三种"累"

这套控制台的阅读疲劳可以量化。`node scripts/audit-density.mjs` 的当前基线：

| 指标 | 处数 | 文件数 | 含义 |
|---|---:|---:|---|
| `text-xs` | 1180 | 111 | **正文在用 12px**，不是注脚 |
| 硬编码色相 `-400/500/600` | 607 | 50 | 琥珀 189 / 红 123 / 翠绿 119 / 蓝 57 … |
| `animate-*` | 135 | 51 | 其中 `animate-fade-up` 22 处、`animate-spin` 48 处（spin 是功能性的） |
| `shadow-*` | 49 | 28 | 层级靠阴影而不是排版 |
| `uppercase` + `tracking-*` | 39 | 17 | 中文界面里是纯装饰 |

最重的三个文件：`dashboard/page.tsx`（173 信号）、`system/skills/page.tsx`（141）、`tasks/detail/_tabs/overview-tab.tsx`（98）。

## 双墨做了什么

1. **有彩色归零。** 不逐个改组件，而是在预设作用域内重映射 Tailwind v4 的 `--color-*` 变量：
   - 10 个色族压成一条灰阶（`--400` 仍比 `--600` 亮，明度层级不丢）
   - `red` / `rose` 映射到专色 → **全站唯一的有彩色 = 需要你动手的东西**
   - 其余 10 个低频色族别名过去
2. **阴影归零**，层级交给 1px 描边（14% / 30% / 50%）与留白。
3. **装饰动效归零**（fade-up / pulse / pulse-soft / glow-once），`spin`、`in`、`out`、`accordion` 保留。
4. **微型字号抬一档**：`text-xs` 在预设层到 12.8px / 1.55。

所有改动都锁在 `[data-theme-preset="two-inks"]` 下，**其余 6 套预设完全不受影响**。

## 对比度实测（这一步抓到了一个真缺陷）

> 浅色令牌后来又从米色纸改成了暖白纸，最新数值见文末「浅色改版」一节；本节保留当时的推理过程。

第一版预设把"低密度"做成了"没结构"：把预设文件当作真实样式表挂进一个模拟页，
用 `getComputedStyle` 读回、再对截图做像素采样，量出来的结果是：

| 面 / 线 | 修正前 | 修正后 | 判定 |
|---|---:|---:|---|
| `--border` vs 画布（浅） | 1.27:1 | **1.93:1** | 卡片边界原本会消失 |
| `--border` vs 画布（深） | 1.60:1 | **2.36:1** | 同上 |
| `--muted` vs 画布（浅） | 1.03:1 | **1.15:1** | hover 填充原本等于没有反馈 |
| `--secondary` vs 画布（浅） | 1.08:1 | 1.12:1 | 同上 |
| `--accent` vs 画布（浅） | 1.11:1 | **1.18:1** | 主 hover 面 |
| `--input` vs 画布（浅） | 1.68:1 | **3.05:1** | 表单控件边界需 ≥3:1 |
| `--card` vs 画布 | 1.07:1 | 1.11:1 | 纸压纸，靠描边定界，符合预期 |

要点：**卡片填充色本来就该几乎看不见**（纸压纸是这个方案的美学前提），
但描边和 hover 填充不能跟着一起消失。前者是信息架构，后者是操作反馈。
所以修正只动 `--border` / `--input` / `--muted` / `--secondary` / `--accent`，
没有去拉大卡片与画布的明度差——那会把方案推回"每块都浮起来"的老路。

专色底上的文字两个方向都是达标的，不需要改：

| 组合 | 比值 |
|---|---:|
| 浅色 `--primary` #C63417 + 白字 | 5.36:1 |
| 深色 `--primary` #F57052 + 墨字 | 6.62:1 |

## 已验证项（13/13）

把预设文件当真实样式表加载，工具类按 Tailwind v4 的真实输出写进 `@layer utilities`，
类名不加前缀，用 `getComputedStyle` 断言（`verify-preset.html`）：

| # | 断言 | 浅色实测 | 深色实测 |
|---|---|---|---|
| 1 | 画布 | `oklch(0.929 0.014 86)` | `oklch(0.175 0.008 90)` |
| 2 | critical 保留专色 | `oklch(0.505 0.18 33)` | `oklch(0.78 0.14 33)` |
| 3 | high/medium/low 降为墨阶 | 0.325 / 0.525 / 0.405（C≈0.01） | 0.85 / 0.70 / 0.78 |
| 4 | 蓝·紫·青压成同一墨色 | 三者均 `0.525 0.01 88` | 三者均 `0.7 0.01 88` |
| 5 | `uppercase` | `none` | `none` |
| 6 | `tracking-widest` | 0.512px（=0.04em） | 同 |
| 7 | `shadow-md` | `none` | `none` |
| 8 | `animate-pulse-soft` / `animate-pulse` | `none` | `none` |
| 9 | `animate-spin` **必须保留** | `spin` | `spin` |
| 10 | `text-xs` | 12.8px / 19.84px | 同 |
| 11 | chart-1..5 五级灰阶 | 0.245 → 0.805 | 0.918 → 0.360 |
| 12 | `rounded-md/lg/xl` → 4px，`rounded-full` 不变 | ✓ | ✓ |
| 13 | 关闭预设后全部复原（默认主题不受污染） | ✓ | — |

**为什么一定生效**：预设文件里没有任何 `@layer` 包装。
CSS 中层外规则优先于所有分层规则，而 Tailwind v4 的工具类在 `@layer utilities` 里。
所以选择器特异性（0,2,0 > 0,1,0）和层级优先级**双重**保证覆盖，与 `@import` 的先后顺序无关。

## 已知遗漏（0.17%）

`.text-xs` / `.uppercase` 是精确类名选择器，`md:text-xs` 这类**带变体的**不进选择器。
全站实测 1180 处里只有 2 处（`avatar:text-xs`、`item:text-xs`），可忽略；
把这两个变体改成 `text-ui-xs` 即可清零。
`shadow-*` / `tracking-*` 用的是 `[class*="..."]` 子串匹配，变体天然被覆盖。

## 为什么改变量而不是改组件

Tailwind v4 的工具类引用变量而非内联值：

```css
.text-red-600 { color: var(--color-red-600); }
.bg-red-600\/10 { background-color: color-mix(in oklab, var(--color-red-600) 10%, transparent); }
```

因此重映射一次即覆盖全部调用点，透明度变体也自动跟随。
这是一次**改动 1 个文件、覆盖 665 处调用**的操作。

> 自检方法：`npm run dev` → 打开 DevTools → 搜 `.text-red-600`，
> 若规则体是 `var(--color-red-600)`，说明全局生效；若被内联成色值，
> 说明该版本内联了 theme 值，需要退回逐点替换（届时用下方的迁移脚本）。

## 安装（已完成，供回滚与重建参考）

1. `web/src/styles/presets/two-inks.css` ← 预设本体（含 `label:` / `value:` 头，可被生成器识别）
2. `web/src/app/globals.css`
   - 导入区追加 `@import "../styles/presets/two-inks.css";`
   - `@theme inline` 里追加 `--color-spot: var(--spot, var(--primary));`
3. `web/src/lib/preferences/theme.ts` ← 手工补了「双墨」条目（与生成器输出同格式，可被 `npm run generate:presets` 覆盖后保持一致）
4. `web/src/lib/severity.ts` ← 画布 hex 在双墨下改走墨/专色（canvas 拿不到 CSS 变量）
5. `web/src/app/(main)/dashboard/page.tsx` ← `rateClass()` 由绿/琥珀/红改为 墨/次要墨/专色

## 回滚

```bash
git checkout -- web/src/app/globals.css web/src/lib/preferences/theme.ts \
                web/src/lib/severity.ts "web/src/app/(main)/dashboard/page.tsx"
rm web/src/styles/presets/two-inks.css
```
除此之外没有任何文件被修改。

## 后续六刀（预设只解决视觉压力，密度要改代码）

| # | 动作 | 覆盖问题 | 风险 |
|---|---|---|---|
| 1 | `text-xs` → `text-ui-xs`（迁移脚本） | 1180 处微型字 | 低，但影响面广，需目视回归 |
| 2 | dashboard 首屏 7 模块 → 4 | 首屏过载 | 中，涉及信息取舍 |
| 3 | `uppercase tracking-*` 去掉 | 39 处装饰 | 低 |
| 4 | 语义色归位（severity 已就绪，其余页面跟进） | 色相误读 | 低 |
| 5 | `LiveDot` 脉冲 → 静态点 + 文案 | 11 处焦虑源 | 低 |
| 6 | Card 退位：首屏用 `border-t` 分段 | 盒子过密 | 中，需设计确认 |

每刀之后跑一次 `node scripts/audit-density.mjs`，看数字下降即可，不靠感觉。


## 浅色改版：米色纸 → 暖白纸（含一次误判的复盘）

用户反馈「配色异常，整个屏幕呈现灰尘感」。查到两个原因，一个是环境的，一个是我的。

### 原因一：Arena 预览器盖了一层灰罩（不是配色问题）

用户是从 Arena 的沙箱预览窗看 `verify-preset.html` 的截图。对截图做像素反解：

`observed = orig × (1 − a) + G × a`，解得 **a = 0.595，G = #A2A2A2**。

在跨越全色调范围的多个采样点上全部吻合（误差 ≤1/255）：

| 采样点 | 文件里的真值 | 按遮罩模型预期 | 截图实测 |
|---|---|---|---|
| 画布 | #ECE7DD | #C0BEBA | #C0BEBA |
| 正文 | #15140E | #696966 | #696867 |
| 卡片 | #F6F3EC | #C4C3C0 | #C4C3C0 |
| chart-1 | #22201C | #6E6D6C | #6F6E6D |
| chart-3 | #72706A | #8F8E8B | #908F8B |
| chart-5 | #C3BFB7 | #AFAEAA | #B0AEAB |
| 专色按钮 | #C63417 | #B07569 | #B1766B |

同时 Windows 任务栏 `#252524`、Arena 侧栏 `#252523` **完全没有被压** ——
说明遮罩只作用于预览 iframe，不是显示器也不是截图管线。

**结论：那个文件的颜色一直是对的。** 要看真实效果，用浏览器直接打开文件，别用应用内预览。

### 原因二：米色纸本身也确实"脏"（我的问题）

即便没有遮罩，`oklch(0.929 0.014 86)` #EFE9DD 大面积铺开后的观感也是陳纸：
明度偏低 + 黄相 + 低彩度，三者叠加就是"灰尘"。

浅色改为暖白纸，深色**保持原样**（用户只否定了浅色）：

| 令牌 | 改前 | 改后 | 依据 |
|---|---|---|---|
| `--background` | `oklch(0.929 0.014 86)` #EFE9DD | `oklch(0.972 0.004 90)` #F7F6F3 | 纸感交给描边，不靠压暗底色 |
| `--card` | `oklch(0.965 0.010 88)` | `oklch(1 0 0)` #FFFFFF | 纯白面 |
| `--foreground` | `oklch(0.175 0.008 90)` | `oklch(0.190 0.004 90)` | 17.0:1 |
| `--muted-foreground` | `oklch(0.497 0.012 88)` | `oklch(0.485 0.006 90)` | 5.90:1 |
| `--secondary` | `0.892 0.013 86` | `0.936 0.004 90` | 1.12:1 |
| `--muted` | `0.884 0.012 86` | `0.930 0.004 90` | 1.13:1 |
| `--accent` | `0.876 0.014 86` | `0.922 0.004 90` | 1.16:1 |
| `--border` | `0.255 0.010 88 / 0.36` | `0.255 0.004 90 / 0.36` | 2.01:1（像素实测） |
| `--input` | `0.255 0.010 88 / 0.50` | `0.255 0.004 90 / 0.50` | 3.09:1 |
| `--sidebar` | `0.898 0.014 86` | `0.955 0.004 90` | 1.06:1 |

色相统一收在 90、彩度压到 0.004 —— 仍不是纯灰（不至于滑向科技风），但已看不出黄。
专色 `#C63417` 未动：纸上 4.94:1、白字 5.36:1，两个方向都达标。

改版后复验：浅色 12 项断言全过，0 控制台错误；深色完全未受影响。
