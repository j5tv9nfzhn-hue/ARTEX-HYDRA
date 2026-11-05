// 侧边栏导航数据（扁平化版）。
//
// 与上一版的差别：
// - 删除「设置」折叠父项。它和它的第一个子项（系统配置）指向同一个 url，
//   在侧栏里表现为两个「系统配置」标签，点哪个都一样——去重后只保留一个。
// - 删除「资产同步」。该功能已整体下线（ScopeSentry 数据源页面移除）。
// - 删除「外部接入」「局域网连接」。二者是同一份 MCP / 监听配置的第二入口，
//   日常改配置只走系统配置一处，避免两处写同一张表。
// - 「数据保留」「存储与清理」并入工作空间页，侧栏不再单列。
//
// 层级因此是：分组 → 条目（2 层），没有任何折叠层。分组标签只作为视觉分段，
// 不承担导航层级。

import {
  Activity,
  Ban,
  BellRing,
  Bot,
  Brain,
  Bug,
  ClipboardList,
  FolderOpen,
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
   * 现行导航已无折叠父项，此处仅为类型与未来扩展保留。
   */
  url?: string;
  /** 首次挂载时是否展开子项；不填则只在路由命中该父项时展开。 */
  defaultOpen?: boolean;
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
 * 侧边栏导航：两个分组（功能 / 系统），全部为一级条目，无折叠层。
 *
 * 分组依据不变：功能 = 作战面 + 观察面，系统 = 配置面 + 维护面。
 */
export const sidebarItems: NavGroup[] = [
  {
    id: 1,
    label: "功能",
    items: [
      { id: "dashboard", title: "总览", url: "/dashboard", icon: LayoutDashboard },
      { id: "tasks", title: "任务", url: "/function/tasks", icon: Target },
      { id: "chat", title: "HARNESS", url: "/chat", icon: MessageSquare },
      { id: "workspace", title: "工作空间", url: "/function/workspace", icon: FolderOpen },
      { id: "findings", title: "发现", url: "/function/findings", icon: Bug },
      { id: "assets", title: "资产", url: "/function/assets", icon: Network },
      { id: "commands", title: "工具执行", url: "/function/commands", icon: Terminal },
      { id: "llm-records", title: "LLM 录制", url: "/function/llm-records", icon: Radio },
      { id: "traffic", title: "流量", url: "/function/traffic", icon: Activity },
      { id: "intercept", title: "拦截规则", url: "/system/intercept", icon: ShieldAlert },
      { id: "asset-intercept", title: "资产拦截", url: "/system/intercept/assets", icon: Ban },
      { id: "approvals", title: "审批记录", url: "/system/intercept/approvals", icon: ClipboardList },
    ],
  },
  {
    id: 2,
    label: "系统",
    items: [
      { id: "llm", title: "LLM", url: "/system/llm", icon: Brain },
      { id: "agents", title: "Agent", url: "/system/agents", icon: Bot },
      { id: "skills", title: "Skill", url: "/system/skills", icon: Sparkles },
      { id: "notify", title: "通知推送", url: "/system/notify", icon: BellRing },
      { id: "settings", title: "系统配置", url: "/system/settings", icon: Settings2 },
      { id: "mcp", title: "MCP", url: "/system/mcp", icon: Plug },
      { id: "tools", title: "工具", url: "/system/tools", icon: Wrench },
      { id: "logs", title: "日志", url: "/system/logs", icon: ScrollText },
    ],
  },
];
