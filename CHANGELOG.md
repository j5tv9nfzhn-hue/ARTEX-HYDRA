# 变更日志


记录本分支在上游 ARTEX 基础上增加的功能与维护改动，格式参考 [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)。上游 0.3.x 及更早版本的发布历史与贡献者署名保留在 [CHANGELOG.zh.md](CHANGELOG.zh.md)，每项改动的详细依据可查看 Git 历史。

## [Unreleased] · 分支改动

### 发布与产物

- **macOS 桌面端合并进一个 `macos-26` job，产物仍是六端**：darwin/amd64 不再占用 `macos-26-intel`，改为同一个 arm64 job 内由 `build.sh --desktop`（`ARTEX_TARGET_ARCH=amd64`）经 clang `-arch x86_64` 同机交叉构建；两个架构依次构建，`pkg-desktop-darwin` 一次上传两个 zip。目的是减少 runner 获取次数（每个 macOS job 都要单独拿机器）。`release` 的服务器 6 + 桌面 6 齐全校验不变。
- **桌面端 CI 小修**：`desktop` job 设 `timeout-minutes: 60`；linux/amd64 钉到 `ubuntu-24.04`；Go 编译缓存路径改由 `go env GOCACHE` 取得；不再缓存 `build.sh` 生成的 `desktop/go.mod`、`go.sum`。
- 本次合并方案尚未在 CI 验证，首次变更需用 `workflow_dispatch` 干跑确认。

### 模型与成本

- **删除供应商改为一次性级联**：事务内按「模型 → 供应商 → 激活位交接」删除，返回被带走的模型数；新库外键补 `ON DELETE CASCADE`，老库由 Go 事务保证同一结果。前端只留一个确认框，不再要求先删光模型。
- **激活中的模型、供应商下最后一条模型都能删**：删完自动把激活位交给下一个候选（`priority DESC, id ASC`），删空后供应商保留为空壳。
- **废弃独立凭据池**：删掉「凭证」页签、`/api/llm/credentials` 三条路由与 `db/credentials.go`、`server/llm_credentials.go`；API Key 只存在 `llm_providers.api_key` 上，升级时由 `foldCredentialsIntoProviders` 幂等折叠回供应商行。`credentials` 表与 `credential_id` 列保留为不改动现场库形状的空壳。

### 工具执行层

- **Windows 默认改用 PowerShell，且可以在设置里换**：新增「设置 → 工具执行器」，只列本机实测可用的 Shell（PowerShell / CMD / Git Bash），没有「自动」选项，也不会在所选执行器不可用时悄悄换用别的。解析顺序 `ARTEX_SHELL_BIN`（或设置里的自定义路径）→ `pwsh` → `powershell.exe`；任何候选都要真跑一次 `-NoLogo -NoProfile -NonInteractive -Command` 探针，`%LOCALAPPDATA%\Microsoft\WindowsApps` 下的 MSIX 别名排到最后（可被 `where` 命中却 spawn 报 1312）。
- **Bash 工具的执行器从 SDK 手里拿回来**：装饰点从 worker 上移到 `SDKDefaultTools()`，chat / planner / mainagent / tool_lean 不再落到 norma 写死的 `shellCmd()`。之前只有 worker 的命令受控，同一台机器上「哪条命令能跑对」取决于站在哪个 agent 里。
- **PowerShell 预热池**：常驻宿主进程 + 每命令一个 `.ps1`（UTF-8 **带 BOM** + CRLF）走 stdin 路径执行，省掉每条命令 0.8–6.9s 的 .NET 冷启动；池补不上时降级为同 exe 一次性 spawn 并记 `fallbacks`。cmd / bash 保持一次性 spawn（本身 20–150ms）。
- **契约按「模式 + 版本」生成**：PS 5.1 明确写「不支持 `&&`」、`curl` 要写 `curl.exe`、`>` 默认 UTF-16LE；PS 7 说明支持 `&&`；cmd 给 `2>nul`；Git Bash 给 `/c/...` 路径。同一段文案不再同时用于 5.1 与 7。
- **脚本编码分派**：`.ps1` = UTF-8 带 BOM（5.1 无 BOM 会按 GBK 解码），`.bat`/`.cmd` = **不带** BOM（BOM 会并进首行 `@echo off`）+ `chcp 65001`。
- **`tool_usage.error_class`**：失败按 `config / dialect / not_found / param / perm / env / timeout / transport` 八类记账（新库建列，老库自动补列）。「Bash 错误率高」从此可以拆开看，而不是一个布尔值。
- **修正超时键名**：历史实现读 `timeout`，而 SDK schema 是 `timeout_ms`，导致模型要求的 600s 一律被当成 2 分钟——这类「莫名其妙超时」本身就是错误率的一部分。

### 附件与压缩（第三阶段：参数重设计）

- **冷摘要长度改由内容量决定，不再是写死的 800 token**：`DigestBudgetTokens = clamp(0.30×输入, 512, min(5%窗口, 4096))`，并保证摘要永不超过输入的 60%。800 的病根是「摘要短于信息量」，规划者读完仍不确定只能回头重查原节点，省下的 token 又花回去。
- **注入侧预算与生成侧配套**：单条 digest 正文 `clamp(窗口/50, 800, 2000)` 字符（原写死 600），全部 digest 总量 `clamp(0.24×窗口, 6000, 24000)`，且最多占注入池的 2/3；态势 JSON 注入 `clamp(0.4×窗口, 24KB, 64KB)`（原 16KB 硬上限）。窗口未知时全部退回旧值。
- **工具输出入历史的上限按窗口推导**：`ToolOutputChars = clamp(窗口/8, 4000, 24000)` 字符，替代写死的 12000，并在超限时按 **head 70% / tail 30%** 保留（原来头尾各半或只留头部），全量仍落盘可重读——尾部的退出码与最后一屏报错正是最需要保留的部分。
- **压缩前先做无 LLM 预剪**：保护最近 40,000 token（至少 2 轮）的原文，更旧的工具输出正文清成「首尾 + 标记」，可回收 ≥20,000 字符才动手（避免为省一点体积破坏前缀缓存），剪过的块永不重复剪。对应 OpenCode `compaction.prune` 与 Hermes 的第一阶段。
- **触发点加绝对上限**：`min(0.8×窗口, 窗口−预留, 120K)`。窗口声明得再大也不许把触发点推到 12 万 token 之上——1000K 虚窗口下 8 天不触发正是这么来的。`ARTEX_COMPACT_CAP_TOKENS` 可覆盖（0 = 不设上限）。
- **摘要产出可对账**：`compaction_state` 新增 `injected_bytes` / `digest_tokens_est` 两列（老库自动补列），start 事件不会把上一轮的数字冲掉。

### 安全

- **阻止首次运行时管理员账户被远程抢先初始化。** 原来未设置密码的实例可被先访问 `POST /api/auth/init` 的人初始化并获取 JWT（私密报告者：Dangel）。现在必须提供仅在服务端控制台显示的 `setup_token`，错误尝试有速率限制；新增 `ARTEX_SETUP_TOKEN`、`ARTEX_ADMIN_PASSWORD` 环境变量和 `/setup` 令牌输入框。
- **默认仅绑定 127.0.0.1。** `artex -addr` 默认由 `:8787` 改为 `127.0.0.1:8787`，Compose 使用 `${ARTEX_BIND:-127.0.0.1}`。需要其他设备连接时须显式设置 `-addr :8787` 或 `ARTEX_BIND=0.0.0.0`，并先完成初始化。无密码、非回环绑定会打印警告。
- **修改密码后废除旧 token。** JWT 签名密钥从密码哈希派生，UI、`reset-password.sh` 或直接数据库修改均使旧会话失效；升级后需要重新登录，UI 改密响应包含新 token，当前浏览器可继续使用。
- **CORS 从全部允许改为允许列表。** 根据 `ARTEX_CORS_ORIGINS` 返回跨域响应头，开发默认允许 `http://localhost:5173`。新增服务端八字符最小密码限制，以及带客户端地址的初始化、登录失败日志。
- **桌面端不再「开了局域网就免鉴权」。** 免校验收敛到「进程内窗口」一个标记上，且刻意不看来源地址——同机浏览器发到 `127.0.0.1` 的请求、同机反向代理转发的请求，`RemoteAddr` 都是回环，按地址放行等于给代理开后门。`POST /api/lan/enable` 在未设密码时直接 409，桌面端非回环绑定恢复「未设密码」警告，窗口内可直接设置首个登录密码（`/api/password/reset` 只对窗口放行），网络侧仍只能用 `/api/auth/init` 加设置令牌。取舍对齐 Syncthing（localhost 免密码、跨主机要显式 `insecureAdminAccess`）与 Jupyter（绑定全接口必须 token）。

### 本地化（i18n）

- **本次将项目自有韩文统一重构为简体中文。** 覆盖 Go 后端消息、状态、工具摘要、模型输出指令、Next.js 页面与演示数据、注释、部署脚本、配置说明、文档和 GitHub 模板。
- **默认语言改为 zh，随后收口为「中文唯一」。** 旧 ko 配置、cookie 和本地偏好归一为 zh；在此基础上进一步**删除 `web/messages/` 下的 `ko.json` / `en.json` / `es.json`**，`LOCALES` 只剩 `zh`，界面不再提供语言选择器，`ko` / `en` / `es` 与未知值统一回退为简体中文。`src/i18n/config.test.mjs` 用「`messages/` 目录必须只有 `zh.json`」把这条约束钉住。
- **统一 Agent 输出语言。** 保留行为提示词、角色职责、命令和证据语义，以 `langDirective` 明确报告、事实摘要、最终总结和对话的输出语言；自定义收尾提示也追加选定语言约束。
- **同步中文消息协议。** 审批摘要解析与后端格式一致，保持工具名及 `pending_id`；Skill 已存在提示、通知、默认回退和语言切换测试同步更新，保留供应商返回的底层诊断原文。
- **增加语言回归检查。** `check-language.py` 扫描项目自有文本及静态 HTML 的韩文字母/音节，允许中文；新增 Python 和前端语言、审批回归测试并接入 CI。
- **保留 i18n 基础设施，收敛为单一词库。** next-intl、消息键提取（`web/src/scripts/extract-i18n.ts`）、后端词库和输出语言设置继续使用；**新增消息只写 `web/messages/zh.json`**，不再同步 en/es/ko 副本，也不应重新硬编码到页面。提取脚本同步改为只回写 `zh.json` 与 `zh.sources.json`——此前它仍会写出 `ko.json` 与 `en/es` 两份，与「中文唯一」冲突且会让目录断言变红。

### 界面与主题

- **新增 7 套主题预设。** 在浅色/深色/跟随系统之上提供「默认、琥珀终端、粗野主义、深空指挥室、矩阵绿、柔和流行、柑橘」，偏好保存在浏览器本地；预设样式各自独立成文件。
- **字体自托管。** Geist、Space Grotesk、JetBrains Mono 随包分发，不请求外部字体服务，离线可用。
- **统一严重度色彩契约。** 收敛为 critical 红 → high 橙 → medium 琥珀 → low 灰蓝（修正旧版亮度倒挂），徽章、圆点、条带与图表色（亮/暗两套 hex）集中在一个事实源，页面不再各写一套。
- **命令面板与界面打磨。** 新增 ⌘K / Ctrl+K 命令面板（跳转、操作、搜索，⌘J 保留为旧版别名），以及侧边栏自定义、空状态、动效、活动流圆点、发现告警、LLM 配置徽章与 Agent 备用链可视化等组件。探索图重绘（小地图、节点类型配色与图例、节点进入动效、自适应缩放）；仪表盘新增可靠性面板（工具错误率、崩溃重放成功率、中断恢复率与按阶段分布）。
- **双主题开关（经典 / 纸本）。** 新增 `skin` 偏好，默认仍是经典：切到「纸本」时主题预设被钉为 `paper`、下拉置灰，版式交给 `styles/skin-editorial.css` —— 一张纸的骨架、发丝线表格、下划线页签、全站等宽数字、卡片与阴影归零、装饰性入场动效静音（`animate-spin` 保留）。首帧由 `theme-boot` 写 `data-skin`，切换无闪烁；皮肤层每条规则都挂在 `[data-skin="editorial"]` 下，老主题零影响。
- **两套新预设：纸本 / Paper 与双墨 / Two Inks。** 纸本取 FT Origami 的「机制不取表层」——纸 `oklch(0.962 0.010 86)`、主色紫青 `#197075`（红让给危险色）、圆角 ≤4px、阴影归零、衬线只管标题层（正文与数字仍用无衬线，12–13px 下衬线辨识度更差）。双墨把 10 个色族压成一条灰阶、只留专色给「需要你动手的」东西，并把 `text-xs` 抬到 12.8px / 1.55。两套都锁在各自 `data-theme-preset` 作用域内，浅/深两套令牌的对比度均实测达标（纸本正文 16.06:1、主色 5.18:1、危险色 5.74:1，hover 面 ≥1.12:1）。
- **编辑排版原语。** 新增 `components/editorial.tsx`（`Masthead` / `SectionHead` / `KpiBand` / `KpiRow`）与 `components/editorial-sheet.tsx`（`Sheet` / `SheetHead` / `GroupHead` / `RecordRow` / `RecordSub` / `SheetFoot`）。28 个页面里 16 个改用报头原语（内联在徽章横排里、工具栏里、或本来就没有页面标题的 12 个页面刻意不动），任务详情 9 个页签改用版块原语；会话、拦截审批、软件分析三个页签因形态独立而未改。
- **九类详情路由可独立打开。** 新增 `/function/{assets,traffic,llm-records,commands}/detail`、`/function/tasks/session`、`/system/{tools,skills,mcp,llm,agents}/detail`、`/system/intercept/approvals/detail` 与 `/function/findings/detail`，统一「书眉（编号 · 分类 · 状态 · 时间 / 26px 标题）+ 五格数字行 + 下划线页签」，能刷新、能回退、能贴链接；资产行、报文抽屉、录制详情与审批展开区都补了入口。数据缺失处如实标注（报文时序未采集、后端暂无「发现 ← 报文」反查索引、MCP 握手日志未落库）。
- **通知中心与日志行内展开。** 顶栏铃铛只列「要动手的」两件事：待裁决的审批票与最近 10 条 critical / high 发现，数字即待办数，关掉面板不轮询；日志页点一行展开前后各 3 行上下文，一次只展开一行。
- **排版门禁。** 字号 12px 门禁：9 / 10 / 11 / 11.5px 全部收敛到 `text-xs`；元数据分隔符去掉中点「·」，展示层改用间距，历史协议串的解析兼容保持不变。
- **可复跑的守卫。** `scripts/audit-editorial.mjs` 守五条不变量：`editorial.css` 规则必须限定在 `[data-theme-preset="paper"]` 下、`paper.css` 覆盖 37 项令牌契约且浅深两套都在、每个页面必须挂 `data-editorial="body"`、`skin-editorial.css` 必须挂在 `[data-skin="editorial"]` 下、双主题接线七处齐全。
- **B 类面板结构改造开张：复测面板先换成版块原语。** `components/finding-retest-panel.tsx` 的卡片与自绘描边盒子换成 `Sheet` + `SheetHead` + `RecordRow` + `RecordSub` + `SheetFoot`：徽标在左、编号与正文在中、时间与会话入口在右，第二行走 `RecordSub`，底部补「共 N 次复测」落款；补 `retest.panel.foot` / `footHint` 两个词条。`Sheet` 在老主题下仍渲染成卡片（带 `data-slot="card"`），classic 零影响。这是「卡片 → 版块原语」这条结构改造线的第一刀，其余面板按同一套语法跟进。
- **B 类深层面板接入报纸语言（皮肤层）。** `skin-editorial.css` 新增第 16 组：面板头尾压发丝线、右上角 ✕ 改成细描边小方钮（限定直接子元素，避免把页脚「取消」一起压成图标钮）、命令面板的检索行压一条墨线、菜单项改成记录行（悬停浅面 / 勾选用左侧主色竖线）、`item` / `field` / `accordion` / `scroll-area` / `progress` / `kbd` 与面板内的代码块、表格容器一并收口。只动版式与描边，不改 `display` / `position` / `overflow`、不写死尺寸上限；全部锁在 `[data-skin="editorial"]` 下，切回老主题一条都不生效。
- **配色令牌化，规则里不再出现色值。** 原先散在规则里的 4 处色值字面量（三档抬升阴影 + 一层遮罩）收进皮肤令牌块 `--ed-lift-1/2/3` 与 `--ed-scrim`。它们是中性色而不是主题色——主题色仍然只在 `presets/paper.css` 定义；抬升与遮罩刻意不用 `--foreground` 推导，否则深色主题下会从「压暗」变成「发光」。
- **守卫补一条不变量，并修掉一处自身缺陷。** `scripts/audit-editorial.mjs` 新增「皮肤层令牌块之外不得出现硬编码色值」（`#hex` / `oklch()` / `rgb()` / `hsl()`）；同时修正：接线文件缺失时错误文本里不含 "skin"，脚本却仍会打印「双主题接线完整」，改为按计数判断。

### 成本与效率（批次 E）

- **自动注入瘦身。** planner 每轮态势与 worker 首消息概览收紧条数并加字节硬上限（16KB / 12KB），摘要正文与触发消息限长；工具取数路径保持全量。工具输出进入历史的字符上限由 30000 收紧到 12000，超限全文落盘、可按需重读。planner 心跳空转跳过：心跳到点但无触发消息、无边沿变更且仍有在跑意图时，不再白烧一整轮规划 LLM。
- **system prompt 整段缓存。** 原来只有静态前缀进 Anthropic 缓存；现在整段 system（含延迟披露工具块）作为缓存块跨轮复用。
- **任务级 Token 预算硬闸。** 新增 `default_task_token_budget` 与任务级预算：累计输入+输出达到上限时自动暂停任务并在活动流留痕，恢复后继续累计。
- **LLM 重试可配置、意图重跑收敛。** 新增 `llm_retry_policy`（connect / empty / stream / breaker / intent 五层的次数与间隔），单 profile 可覆盖三层；`model_error` 收场的意图重跑限制为 4 次（硬失败 2 次）、指数退避。
- **价格表与费用视图。** 新增 `price_table`（模型名子串 → 单价，USD/百万 token），用量与成本视图按当前价格读时计算；不做内置价目，由用户自填。
- **Token 归因与进度指标。** 每次 LLM 调用按任务 / 意图 / worker / 配置 / 模型落账；任务新增首个发现用时、每小时产出速率与等待审批数指标（列表显示「等待审批」徽章）。
- **触发器与通知的成本护栏。** 定时触发器最小间隔 60 秒（创建/更新校验，存量按 60 秒调度并告警）；「报告撰写」触发器默认关闭；通知新增任务状态变化与等待审批事件（存量渠道 opt-in，新建渠道默认开启）。
- **steer 纠偏与诚实回滚文案。** 运行中的意图可直接接收人工纠偏，不暂停、不重跑；检查点回滚与一键回退的文案明确「不撤销目标侧副作用、数据库结构不回退」。
- **上下文压缩按 80% 真实触发（对话 TAB / 任务 TAB 全部 agent）。** 两处偏差：容量环的「已用」取的是本次 run 的**累计**用量、且把已含在 `input_tokens` 里的 cache 又加了一遍，环会在真实上下文还很小时就冲到 80%+，而压缩器按真实值判断，看起来就是「到了 80% 不压、非要顶到窗口才压」；现在按「最近一轮请求的输入规模」计数（与 harness 喂给压缩器的 `lastInputTokens` 同口径），前端老数据回退也不再叠加 cache。同时把反解「等效窗口」时的余量修正为 SDK 的 `estimateMaxTurnGrowth`（预留 8000 + 一轮增长 15000，此前漏了预留），窗口 ≥160K 时触发点精确落在设定比例上（默认 80%），更小的窗口只会更早；小窗口按 1/8 收缩摘要预留，避免 32K 窗口退化成几乎每轮都压。另加压缩日志（压缩发生 / 摘要失败），不再出现「压缩被静默关掉」而无从查证。

### 功能与可靠性

- **LLM「测试连接」独立轻量探测。** 只发一次最小补全（不带工具、系统提示与流式），成功判据包含协议结构校验；失败诊断内联 HTTP 状态码与供应商原文。
- **模型配置声明输入模态。** LLM 配置可声明 text / image / audio / pdf，路由时对不支持的附件显式报错、绝不静默丢弃；未声明时按模型名保守推断。
- **Agent LLM 备用链。** 每个 Agent 可在主配置之外配置有序备用链，主配置不可用时按序切换，界面展示备用链。
- **共享基础包与审计关联。** 新增 metrics / stageerr / fsutil 三个基础包（工具调用与错误按阶段计数、错误文案统一分阶段、文件写入通用工具）；guard 审计条目携带拦截记录的 ToolUseID（`call_id`），审计与审批可配对。
- **任务环境快照与检查点。** 任务准入时探测运行环境（OS / shell / 可用工具 / 工作目录）并注入 planner 与 worker 提示词；新增检查点时间线与回滚（平台侧记录），上下文压缩状态在重启后自校验。
- **消息级回滚（IDE 式气泡箭头）。** 聊天会话与任务「总 AGENT」的每条用户消息气泡下方悬停出现回滚箭头：确认后该消息及之后的全部步骤从时间线隐藏（任务侧含 Worker/规划器记录，未完成意图重排为待执行），主 Agent/会话转录截断回该轮之前，消息原文与附件回填输入框；与检查点回滚一致，工具副作用与已落库的图产物不回滚。任务侧由主 Agent 用户轮新增的 `user_message` 检查点提供「该轮之前」的转录边界。
- **凭据与 MCP 环境变量加密。** 新增集中凭据管理（可被多个模型配置引用）；凭据与 MCP 环境变量密文统一 `enc:v1:` 前缀，无前缀的存量明文继续按明文处理、无需迁移。
- **SQLite 写竞争健壮性（部署实例 BUSY/超时风暴修复）。** 连接契约新增 `synchronous(NORMAL)`（WAL 下提交不再逐条 fsync，写锁持有时间从「磁盘 fsync」降到「追加」），`busy_timeout` 提高为 10 秒；后台持久化（旁问快照、检查点、环境快照、通知事件）的超时下限统一为 `busy_timeout + 5s`，消除「3 秒预算 vs 5 秒等待」倒挂——倒挂会把排队中的写入打断成 `context deadline exceeded`；旁问会话快照刷盘从每 250ms 重写整份（可达数百 KB）降为 1s；通知分派与归档领取在队列为空时改为只读探测早退，不再空拿写锁；活动记录写入改为 4 次递增退避重试（丢一条配对记录会让界面永久停在「执行中」）。
- **重启后任务默认不自动运行。** 后端启动加载持久化任务时，未终态任务一律以暂停态载入（原先「运行中/排队」的任务不再自动拉起 Planner 与 Worker），并发出一条带原因的「任务已暂停」通知；在任务页点击「恢复」后才继续执行——避免上游 LLM 等外部依赖暂不可用时，重启即触发连续报错。
- **失败步骤改单句提示。** Worker/主 Agent 运行失败不再在时间线铺开「终态 / 底层错误 / 已执行 / 耗时 / 工具调用」整段红色详情，只显示一句失败摘要（如「执行失败（model_error）：模型调用失败（网络、鉴权、限流或供应商错误）。」），也不再为失败行拉取详情；完整详情继续保留在活动记录里供排障与审计。
- **Windows 进程树收口（Job Object）。** ARTEX 启动即把自己放进「最后一个句柄关闭时终止全部成员」的 Job Object，此后 Bash 工具派生的 powershell/python（含 multiprocessing、ThreadPoolExecutor 的子进程）、rg、npx 等全部随 ARTEX 同生共死：控制台关闭、任务管理器强杀、进程崩溃，以及新实例自举 `TerminateProcess` 掉持端口的老实例（自更新换装、重复启动都会走到），都不再留下继续占满 CPU（而且继续打目标）的孤儿脚本。创建/赋值失败只降级并在日志说明原因，绝不阻断启动；启动日志给出「已启用/未启用」结论。`cmd/artex/jobobject_{windows,other}.go`，Windows 侧回归测试覆盖「孙进程继承 Job」「关闭句柄即终止整树」「父进程被强杀后孙进程随之消失」。
- **CPU 饱和提示与命令自限约束。** `workers` 超过本机逻辑 CPU 数时启动日志给出明确告警（只提示、不擅自改设置）；任务环境简报（注入 planner/worker 提示词）新增一条 CPU 治理约束：穷举/爆破/大规模 fuzz 命令自限并发（进程/线程 ≤ min(逻辑核数, 4)，禁止无上限 multiprocessing），预计超过 1 分钟的命令用 `run_in_background`、不再需要时立即 `TaskStop`，避免多个 worker 同时打满 CPU 拖慢平台服务与 SQLite 写入。
- **自动更新不再依赖启动脚本。** 双击 exe 就能完成一次完整更新：退出前先写 `updates/restart.request` 意图，入口按指针把新版本拉起来，`start.bat` / `start.sh` 降级为可选兜底；Ctrl-C 正常停止不会被自动拉起，自拉频率超过 5 次/5 分钟就停手并写日志。
- **单实例锁与跨进程安装锁。** Windows 命名互斥量 / Unix `flock`：先拿锁再绑端口，第二个实例只打印地址并退出，不再杀掉正在服务的进程；并发 `POST /api/update/apply` 只有一个能进入安装阶段，另一个直接 409。

### 防御与检测资料

- **提供防御与检测指南。** [简体中文指南](docs/defense.md)保留旧文件名以兼容链接，与[英文版](docs/defense.md)包含相同的自主 AI 行为、可观测指纹、加固、规则与事件响应资料。
- **提供部署用规则。** [Sigma](detections/sigma/) 处理主机、日志与关联检测；[Suricata](detections/suricata/) 检测 enrich 探测和 norma WebFetch 的 User-Agent。
- **可视化 ATT&CK 覆盖率。** [Navigator 层](detections/attack/)仅使用规则实际 `attack.*` 标签，不增加推测技术。
- **标准化机器可读指标。** [CSV 和 MISP](detections/indicators/)逐行同步，规则支持的指标使用 `to_ids`，主机取证线索与可部署检测区分。
- **提供八类可复现测试。** Sigma 结构/转换、真实事件匹配、后端可移植性、SigmaHQ lint、Suricata 加载/触发、ATT&CK 一致性、指标与源码一致性、MISP/CSV 同步；恶意样本应触发，正常样本应静默。批量 runner、pre-commit 与 CI 执行相同契约。

### 桌面端（Wails V2）

- **桌面端窗口免登录，Web 端与局域网照旧要密码。** `boot.Options.DisableAuth` → `Server.SetAuthDisabled`，`requireAuth` 只放行带进程内标记的窗口请求（`server.authExempt`），`/api/auth/status` 按请求回 `auth_disabled`：打开窗口直接进主页面，服务器版仍走账号密码登录与首次初始化。两端共用同一份静态导出，差异只由 `desktop/shell.go` 的 `artex_shell` cookie 与 `web/src/lib/platform.ts` 一处判定承载。
- **双击不再弹控制台窗口。** 桌面端按 `-H windowsgui` 编译（`build.sh` 自建 ldflags 时显式补回），启动守卫拒绝启动、单实例已被占用、后端起不来改为原生 `MessageBoxW` 弹窗；`-reset-password` / `-import-old` 这类命令行用法反向 `AttachConsole` 回父终端，保留终端输出。
- **进程日志落盘并可回看。** `setupDesktopLog` 把 stdout/stderr 同时写入发行目录日志文件，`server/logsink.go` 让后端日志在页面内可读——GUI 子系统下没有控制台，这一层是唯一的事后证据。
- **窗口尺寸按屏推导，取实测参考值。** 默认 1440×900、最小 1024×640；小屏按「屏宽 88% / 屏高 94%」收缩，1366×768 上得到 1202×722，与实测参考（本机既有窗口 1200×728）几乎重合。尺寸只认 Wails 的 `Screen.Size`（逻辑像素，官方注明用于设窗口尺寸），不混用 `PhysicalSize`。
- **窗口里不会掉进登录页。** `IS_DESKTOP` 除 cookie 外还认 Wails 宿主（`wails.localhost` / `wails:` scheme）；`/login`、`/setup` 与 `(main)` 布局都以 `/api/auth/status` 的 `auth_disabled` 兜底；窗口内隐藏「退出登录」、改密后不再跳登录页。
- **托盘与更大的默认窗口（Windows）。** 关闭窗口改为收进托盘：引入 `fyne.io/systray`（Windows 后端是纯 syscall 的 Shell_NotifyIcon，不加 cgo 依赖），托盘就绪后 `OnBeforeClose` 隐藏窗口而不退出，退出走托盘菜单（先置 `trayQuitting` 再 `Quit`，否则退不掉）；托盘起不来就退回"关闭即退出"。macOS/Linux 暂不开托盘（需 `RunWithExternalLoop` / StatusNotifier，得真机验证）。默认窗口从 1280×800 提到 1440×900，小屏按屏宽 88%、屏高 94% 收缩——1366×768 上得到 1202×722，与实测参考窗口（1200×728）几乎一致。
- **本地构建的 exe 也带品牌图标。** 标题栏 / 任务栏 / Alt-Tab 的图标来自 exe 资源（`rsrc` 把 `.ico` 与 DPI manifest 编成 `.syso`）；该步骤原先只在 `release.yml` 里，于是 `build.sh` 本地打出的 exe 用系统默认图标。现在 `build.sh` 的 Windows 桌面目标自己生成 `.syso`，缺失 `rsrc` 时尝试 `go install`，仍失败只告警不阻断。
- **桌面端是单文件发行，命名带 `-desktop` 后缀。** 产物 `artex-hydra-desktop[.exe]`（macOS 为 `artex-hydra-desktop.app`）既是入口也是本体：包内没有 `-core`、没有 `versions/`、没有 `pointer.json`，`config/`、`artex.db`、`data/`、`skills/` 都在它旁边，整个目录拷走即完成迁移（新旧合并也不必对齐核心副本）。`selfupdate` 用 `singleFile=1` 落地：`Bootstrap` 直接放行、`Handoff` 永不转交、`/api/update/apply` 返回「下载新版替换单文件」；服务器版仍保留「入口 + 核心 + versions/」，因为它的换装不能覆盖运行中的 exe。产物名与 WEB 版 `artex-rea-*` 一眼可分。
- **Settings now has a "Data migration" card, shared by both builds.** New `migrate` package plus three endpoints (`GET /api/migrate/scan`, `POST /api/migrate/inspect`, `POST /api/migrate/import`); the card lives in the single shared frontend, so the desktop window can import an old WEB install and a WEB install can import a desktop directory. Inspect reports size, row/table counts, credential/JWT key presence, workspace and skill entries, and probes 8787/8788 to tell whether the other program is still running. Two modes: **merge** (keep the current DB, per-table `INSERT OR IGNORE` over the columns both sides share, conflicting rows skipped; `llm_*` tables are skipped when the two `credential.key` files differ so no undecryptable API keys land) and **replace** (wipe each table, then load the old DB; current DB is snapshotted with `VACUUM INTO` first). The legacy `-import-old` CLI stays for brand-new empty targets and now points at the card when the target already has a database.

### 移除

- **手机端 App 整体移除（设计内的能力收敛，不是临时下线）。** 删除 `mobile/`（Expo/React Native）与 `.github/workflows/mobile.yml`、`mobile-build.yml`；后端随之删除 `server/mobile_api.go`（8 条 `/api/mobile/*`）、`db/mobile_alerts.go`（`mobile_alerts` 表）、`server/pair_api.go`（一次性配对码与 `POST /api/auth/pair-claim`），以及 `server/ui_notify.go` 的落库调用 `persistUIEvent` 与 `Server.Handler()` 的两行注册。远程访问统一为浏览器一条路：「系统 → 局域网连接」给出地址、一键开放局域网、自动放行防火墙，不再维护第二个客户端形态；该页同时去掉配对卡与 `mobile_hint`。详见 [CHANGELOG.zh.md](CHANGELOG.zh.md) 的 Unreleased → 移除。
- **二维码编码器下线。** 配对码移除后 `GET /api/lan` 的 `qr_svg` 没有消费者，`server/qr.go` 与 `qr_test.go` 一并删除，局域网页只保留地址复制与开放/收回。

### 仓库维护

- **保留安全与法律告知。** 仅允许自有或书面授权目标、隔离实验环境，说明韩国信息通信网络法、个人信息保护法及适用地区法律，不因语言重构削弱授权限制。
- **预览图片统一为中文演示截图。** 保留原作者署名和图片来源说明，不将历史演示截图伪称为本次生产验证（历史截图已于 2026-10-16 整体移除，见本节末条）。
- **完善维护与贡献指南。** [维护手册](MAINTAINING.md)涵盖上游同步、语言漂移、指标契约及发布前置条件；[贡献指南](CONTRIBUTING.md)说明验证范围和检测规则契约。
- **每次 push、PR 执行合并门禁。** Go/数据库/前端/检测规则/语言检查合并在单工作流中执行（该工作流已于 2026-10-21 并入 `release.yml`），检测规则另由[独立工作流](.github/workflows/detections.yml)按路径触发。数据库测试按包使用独立的临时 SQLite 库（无需服务容器），内部链接检查只使用标准库、不依赖网络。
- **新增 Go lint 门禁。** [.golangci.yml](.golangci.yml) 以 `govet` 的干净基线起步（后续按清单逐项收紧），CI 与 [pre-commit](.pre-commit-config.yaml) 的 go 钩子使用同一配置，本地可复现；格式检查由 CI 的 gofmt 门禁（Go 1.26 工具链）唯一把关——golangci-lint 内嵌的 gofmt 使用其构建时的 Go 版本，与工具链对中文对齐的行可能判定相反，不重复启用。
- **外部链接周期检查不阻止合并。** 作为定时任务每周或手动执行浏览器 User-Agent、GET、重定向检查；限流/机器人限制和允许列表中的上游既有失效链接不计为新的失败。
- **保留贡献与治理设施。** [Issue 模板](.github/ISSUE_TEMPLATE/)、[PR 模板](.github/PULL_REQUEST_TEMPLATE.md)、[安全政策](SECURITY.md)、[行为准则](CODE_OF_CONDUCT.md)改为简体中文，英文版本继续提供。
- **保留英文文档层。** README、变更历史、安全政策、行为准则、贡献/维护指南、防御指南、流量证据设计与检测规则均有英文入口；历史 `*-ko.md` 路径继续有效，但内容现为简体中文。
- **移除历史截图预览并重写架构章节。** 删除 `screenshots/` 目录与 `web/media/dashboard.png`；两个 README 不再展示截图，「系统架构」以六视图全面重建（全景分层 · 任务运行闭环 · 数据底座 · 协作机制 · 成本与可靠性控制面 · 单二进制部署）。
- **恢复三端打包脚本（Linux/macOS/Windows）。** 按 Git 历史还原 2026-10-11 裁剪（`3492c39`）对 [build.sh](build.sh) 的收窄：恢复 linux/darwin 目标、三端 Release 默认清单（保留裁剪期新增的 `windows/arm64`）、非 Windows 包附 `start.sh` 与 macOS UPX 参数；[README](README.md)、[构建与产物](README.md#构建与产物) 与 [维护手册](MAINTAINING.md) 同步。
- **构建流水线合并为单一工作流（2026-10-21）。** 删除 `.github/workflows/ci.yml`：它和 `release.yml` 各有一套前端静态导出与 Go 编译，重复构建、两套缓存 key，还会出现「ci 绿了但 release 挂了」。现在只保留 `release.yml`——`gate`（build / vet / embedui-vet / gofmt / golangci-lint）→ `frontend`（含类型检查）→ `binaries`（6 目标）→ `release`；测试、语言检查与文档链接移入 `signals`（`continue-on-error`，只报告不拦发布），`workflow_dispatch` 可干跑不发布。检测规则仍由独立的 `detections.yml` 按路径触发。
- **Release 工作流恢复三端打包。** [release.yml](.github/workflows/release.yml) 的发布矩阵由 Windows 双架构扩展为 Linux/macOS/Windows × amd64/arm64：随 `v*` tag 交叉编译、按平台校验收件头（PE/ELF/Mach-O）与目标架构后发布三端 zip 与 SHA256SUMS；CI（`ci.yml`，已于 2026-10-21 并入 `release.yml`）每次 push 的可下载产物同步改为三端 zip（复用同一 `build.sh`，版本取提交短 sha），两处校验共用 [scripts/check-binary-headers.py](scripts/check-binary-headers.py)；[README](README.md)（含原 README.zh.md 内容）、[CONTRIBUTING](CONTRIBUTING.md)、[维护手册](MAINTAINING.md) 与[构建与产物](README.md#构建与产物) 同步。

---

上游版本发布历史与贡献者列表见 [CHANGELOG.zh.md](CHANGELOG.zh.md)。
