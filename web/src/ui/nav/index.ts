import { sidebarItems, type NavGroup } from "@/navigation/sidebar/sidebar-items";

import type { UIVersion } from "@/ui/version";

import { V2_NAV } from "./v2";

export { V2_NAV };
export const V1_NAV = sidebarItems;

/**
 * 按骨架版本取导航。两份数据形状完全一致（NavGroup[]），
 * 因此渲染层不需要任何分支 —— 这就是「无感切换」的前提。
 */
export function getNavItems(version: UIVersion): NavGroup[] {
  return version === "v2" ? V2_NAV : V1_NAV;
}
