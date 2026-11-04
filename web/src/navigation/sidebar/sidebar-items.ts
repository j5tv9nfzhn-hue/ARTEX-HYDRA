// 新版（v2.5.0.6 之后）侧边栏去冗余版：只删折叠层，不换设计。
//
// 改动边界：
// - 删除「运行 / 发现 / 拦截」三个纯触发器父项，其子项全部提升为一级条目。
//   这三个父项的 url 与首个子项完全相同（结构补丁），点击只展开不跳转，
//   是用户反馈「点不开、找不到入口」的直接来源。
// - 「设置」保留为可折叠父项，但改为可选收纳，不再承载导航层级，
//   折叠状态由 sidebar-prefs 的 collapsedGroups 控制。
// - 路由、页面、样式、渲染组件一律不动。

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
   * 因此这里只在「设置」上保留，且其第一个子项与父项 url 一致，保证页面可达。
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
 * 侧边栏导航：两个分组（功能 / 系统），一级条目直达，只有「设置」可折叠收纳。
 *
 * 与 v2.5.0.6 之后那版（分组 → 域 → 条目 三层）的差别只有一处：
 * 删掉「运行 / 发现 / 拦截」三个父层。理由见文件头。
 *
 * 分组依据不变：功能 = 作战面 + 观察面，系统 = 配置面 + 维护面。
 * 系统组里被提升到一级的四个条目（LLM / Agent / Skill / 通知推送）是日常高频项，
 * 留在「设置」的折叠层里会让它们变成二级，与「设置」本身的可达性不对等。
 *
 * 层级因此是：分组 → 条目（2 层），设置内部多一层可选收纳。
 */
export const sidebarItems: NavGroup[] = [
  {
    id: 1,
    label: "功能",
    items: [
      { id: "dashboard", title: "总览", url: "/dashboard", icon: LayoutDashboard },
      { id: "tasks", title: "任务", url: "/function/tasks", icon: Target },
      { id: "workspace", title: "工作空间", url: "/function/workspace", icon: FolderOpen },
      { id: "findings", title: "发现", url: "/function/findings", icon: Bug },
      { id: "assets", title: "资产", url: "/function/assets", icon: Network },
      { id: "sync", title: "资产同步", url: "/function/sync", icon: FolderSync },
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
      {
        id: "settings",
        title: "设置",
        url: "/system/settings",
        icon: Settings2,
        defaultOpen: true,
        subItems: [
          { id: "settings", title: "系统配置", url: "/system/settings", icon: Settings2 },
          { id: "mcp", title: "MCP", url: "/system/mcp", icon: Plug },
          { id: "external", title: "外部接入", url: "/system/external", icon: Globe },
          { id: "tools", title: "工具", url: "/system/tools", icon: Wrench },
          { id: "logs", title: "日志", url: "/system/logs", icon: ScrollText },
          { id: "retention", title: "数据保留", url: "/system/retention", icon: Database },
          { id: "storage", title: "存储与清理", url: "/system/storage", icon: HardDrive },
          { id: "lan", title: "局域网连接", url: "/system/lan", icon: GlobePhone as unknown as LucideIcon },
        ],
      },
    ],
  },
];
