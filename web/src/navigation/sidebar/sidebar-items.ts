import {
  Activity,
  Ban,
  BellRing,
  Bot,
  Brain,
  Bug,
  ClipboardList,
  Database,
  FolderOpen,
  FolderSync,
  Globe,
  HardDrive,
  LayoutDashboard,
  type LucideIcon,
  MessageSquare,
  Network,
  Plug,
  Radio,
  ScrollText,
  Settings2,
  ShieldAlert,
  Sparkles,
  Target,
  Terminal,
  Wrench,
} from "lucide-react";

import { GlobePhone } from "@/components/icons/globe-phone";

export type NavBadge = "new" | "soon";

export interface NavSubItem {
  id: string;
  title: string;
  url: string;
  icon?: LucideIcon;
  badge?: NavBadge;
  disabled?: boolean;
  newTab?: boolean;
}

interface NavItemBase {
  id: string;
  title: string;
  icon?: LucideIcon;
  badge?: NavBadge;
  disabled?: boolean;
  newTab?: boolean;
}

export interface NavMainLinkItem extends NavItemBase {
  url: string;
  subItems?: never;
}

export interface NavMainParentItem extends NavItemBase {
  /**
   * 父项自身的落地页。**当前渲染方把父项当作纯触发器**：NavCollapsibleItem 的
   * CollapsibleTrigger 是 button 不是 Link，点击只展开/收起，不跳转。
   * 因此每个域的主页面必须同时挂一个同名子项，否则该页面在侧边栏不可达 ——
   * 五个域下面第一个子项就是为此存在，不是冗余。
   */
  url?: string;
  subItems: NavSubItem[];
}

export type NavMainItem = NavMainLinkItem | NavMainParentItem;

export interface NavGroup {
  id: number;
  label?: string;
  items: NavMainItem[];
}

// 组编号对应 nav.group.* 消息键；标签由渲染方经 useTranslations 翻译，
// 不在 sidebar-items.ts 数据中保存。未映射的组回退到 group.<id> 键，
// 渲染方用 t.has 判断后再回退到原始 label。
export const GROUP_MESSAGE_KEY: Record<number, string> = { 1: "function", 2: "system" };

export function navGroupMessageKey(groupId: number): string {
  return GROUP_MESSAGE_KEY[groupId] ? `group.${GROUP_MESSAGE_KEY[groupId]}` : `group.${groupId}`;
}

/**
 * 侧边栏导航：总览 + 5 个一等域，25 个原始条目一个不少，全部下沉为二级。
 *
 * 形态由后端接口面推导，不是照抄 STRIX（后端 120 个 /api 端点的一级分组）：
 *   tasks×25 · protocol×12 · intercept×12 · exploration×9 · skills×6 ·
 *   notify×6 · companies×5 · auth×5 · conversations×4 · agents×4 ……
 * 由此得到五个域：
 *   总览  /stats + /exploration/* 的聚合视图，单独占一个入口
 *   运行  tasks 是绝对主体（25 个端点）；/api/tasks/{id}/chat 证明「对话」
 *         从属于任务，/api/workspace/* 是任务工作区
 *   发现  findings / assets / 工具执行 / LLM 录制在后端上都是「任务的观察面」，
 *         不是独立资源
 *   拦截  /api/intercept/* 占 12 个端点，是 ARTEX 相对 STRIX 的差异化能力；
 *         「流量」的后端就是 /api/intercept/history，本就属于拦截
 *   设置  llm / agents / mcp / skills / tools / notify 各自只有 2–6 个端点，
 *         是配置面而非作战面，不占一等导航
 *
 * 所以这是「重组」而非「删减」：功能一个没少，只是层级深了一层。
 *
 * 顶层仍保留 功能 / 系统 两个分组（id 1 / 2，沿用 nav.group.function 与
 * nav.group.system 两个既有消息键，不新增键）：作战面（总览/运行/发现/拦截）
 * 与配置面（设置）分开，用户在旧版建立的心智模型不丢。层级因此是
 * 分组 → 域 → 条目 三层，但每一层的条目数都从 10~15 降到 1~5。
 */
export const sidebarItems: NavGroup[] = [
  {
    id: 1,
    label: "功能",
    items: [
      { id: "dashboard", title: "总览", url: "/dashboard", icon: LayoutDashboard },
      {
        id: "runs-domain",
        title: "运行",
        url: "/function/tasks",
        icon: Target,
        subItems: [
          { id: "tasks", title: "任务", url: "/function/tasks", icon: Target },
          { id: "chat", title: "对话", url: "/chat", icon: MessageSquare },
          { id: "workspace", title: "工作空间", url: "/function/workspace", icon: FolderOpen },
        ],
      },
      {
        id: "findings-domain",
        title: "发现",
        url: "/function/findings",
        icon: Bug,
        subItems: [
          { id: "findings", title: "发现", url: "/function/findings", icon: Bug },
          { id: "assets", title: "资产", url: "/function/assets", icon: Network },
          { id: "sync", title: "资产同步", url: "/function/sync", icon: FolderSync },
          { id: "commands", title: "工具执行", url: "/function/commands", icon: Terminal },
          { id: "llm-records", title: "LLM 录制", url: "/function/llm-records", icon: Radio },
        ],
      },
      {
        id: "intercept-domain",
        title: "拦截",
        url: "/system/intercept",
        icon: ShieldAlert,
        subItems: [
          { id: "intercept", title: "拦截规则", url: "/system/intercept", icon: ShieldAlert },
          { id: "asset-intercept", title: "资产拦截", url: "/system/intercept/assets", icon: Ban },
          { id: "approvals", title: "审批记录", url: "/system/intercept/approvals", icon: ClipboardList },
          { id: "traffic", title: "流量", url: "/function/traffic", icon: Activity },
        ],
      },
    ],
  },
  {
    id: 2,
    label: "系统",
    items: [
      {
        id: "settings-domain",
        title: "设置",
        url: "/system/settings",
        icon: Settings2,
        subItems: [
          { id: "settings", title: "系统配置", url: "/system/settings", icon: Settings2 },
          { id: "llm", title: "LLM", url: "/system/llm", icon: Brain },
          { id: "agents", title: "Agent", url: "/system/agents", icon: Bot },
          { id: "mcp", title: "MCP", url: "/system/mcp", icon: Plug },
          { id: "external", title: "外部接入", url: "/system/external", icon: Globe },
          { id: "skills", title: "Skill", url: "/system/skills", icon: Sparkles },
          { id: "tools", title: "工具", url: "/system/tools", icon: Wrench },
          { id: "notify", title: "通知推送", url: "/system/notify", icon: BellRing },
          { id: "logs", title: "日志", url: "/system/logs", icon: ScrollText },
          { id: "retention", title: "数据保留", url: "/system/retention", icon: Database },
          { id: "storage", title: "存储与清理", url: "/system/storage", icon: HardDrive },
          { id: "lan", title: "局域网连接", url: "/system/lan", icon: GlobePhone as unknown as LucideIcon },
        ],
      },
    ],
  },
];
