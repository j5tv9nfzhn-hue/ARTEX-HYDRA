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

import type { NavGroup } from "@/navigation/sidebar/sidebar-items";

/**
 * 新版骨架导航（v2）—— 形态由后端接口面推导，不是照抄 STRIX。
 *
 * 推导依据（后端 120 个 /api 端点的一级分组）：
 *   tasks×25 · protocol×12 · intercept×12 · exploration×9 · skills×6 ·
 *   notify×6 · companies×5 · auth×5 · conversations×4 · agents×4 ·
 *   workspace×3 · migrate×3 · chat×3 · tools×2 · mcp×2 · llm×2 …
 *
 * 由此得到 5 个一等域，其余全部下沉为二级：
 *   总览   /stats + /exploration/activity + /exploration/tokens 的聚合视图
 *   运行   /api/tasks 是绝对主体（25 个端点）；/api/tasks/{id}/chat 证明
 *          「对话」从属于任务，/api/workspace/* 是任务工作区 → 全部收进运行
 *   发现   /api/exploration/findings/* 与 /api/assets → 资产、资产同步、
 *          工具执行（/api/tasks/{id}/intents）、LLM 录制（/api/tasks/{id}/llm）
 *          原本是独立页面，但后端上它们都是「任务的观察面」，不是独立资源
 *   拦截  /api/intercept/* 占 12 个端点，是 ARTEX 相对 STRIX 的差异化能力
 *          （STRIX 里只是 tool-renderers/ProxyRenderer 一个渲染器）。
 *          「流量」的后端就是 /api/intercept/history，本就属于拦截，不再单列
 *   设置   llm / agents / mcp / skills / tools / notify 各自只有 2–6 个端点，
 *          是配置面而非作战面，不占一等导航
 *
 * 没有单列「历史」与「报告」：后端 /api/task-archives 与报告导出虽然存在，
 * 但前端目前没有对应的独立页面端点，硬造顶栏只会指向 404。等 P1 建立
 * /function/archives 与 /function/reports 视图后，再提升为顶栏。
 *
 * 25 个原始条目一个不少，全部下沉为二级；因此本次整合是「重组」而非「删减」。
 */
export const V2_NAV: NavGroup[] = [
  {
    id: 10,
    label: "导航",
    items: [
      { id: "dashboard", title: "总览", url: "/dashboard", icon: LayoutDashboard },
      {
        id: "runs",
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
        id: "findings",
        title: "发现",
        url: "/function/findings",
        icon: Bug,
        subItems: [
          { id: "findings-list", title: "发现", url: "/function/findings", icon: Bug },
          { id: "assets", title: "资产", url: "/function/assets", icon: Network },
          { id: "sync", title: "资产同步", url: "/function/sync", icon: FolderSync },
          { id: "commands", title: "工具执行", url: "/function/commands", icon: Terminal },
          { id: "llm-records", title: "LLM 录制", url: "/function/llm-records", icon: Radio },
        ],
      },
      {
        id: "intercept",
        title: "拦截",
        url: "/system/intercept",
        icon: ShieldAlert,
        subItems: [
          { id: "intercept-rules", title: "拦截规则", url: "/system/intercept", icon: ShieldAlert },
          { id: "asset-intercept", title: "资产拦截", url: "/system/intercept/assets", icon: Ban },
          { id: "approvals", title: "审批记录", url: "/system/intercept/approvals", icon: ClipboardList },
          { id: "traffic", title: "流量", url: "/function/traffic", icon: Activity },
        ],
      },
      {
        id: "settings",
        title: "设置",
        url: "/system/settings",
        icon: Settings2,
        subItems: [
          { id: "llm", title: "LLM", url: "/system/llm", icon: Brain },
          { id: "agents", title: "Agent", url: "/system/agents", icon: Bot },
          { id: "mcp", title: "MCP", url: "/system/mcp", icon: Globe },
          { id: "external", title: "外部接入", url: "/system/external", icon: Sparkles },
          { id: "skills", title: "Skill", url: "/system/skills", icon: Wrench },
          { id: "tools", title: "工具", url: "/system/tools", icon: Settings2 },
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
