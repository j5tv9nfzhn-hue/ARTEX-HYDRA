import { DEFAULT_LOCALE, type Locale, resolveLocale } from "@/i18n/config";

import packageJson from "../../package.json";

// 版本号的唯一真相来源是发布 tag：构建期由 NEXT_PUBLIC_APP_VERSION 注入
// （release.yml 传 $GITHUB_REF_NAME，build.sh 传 ARTEX_BUILD_VERSION），运行期再由
// /api/health 的二进制版本校正。package.json 的 semver 只允许三段，四段版本
// （本项目当前是 2.5.0.6 这样的 vX.Y.Z.W）落不进去，所以它只当本地开发的占位，
// 不再充当版本来源。
const BUILD_VERSION = (process.env.NEXT_PUBLIC_APP_VERSION ?? "").trim().replace(/^v(?=\d)/, "");

const currentYear = new Date().getFullYear();

// 浏览器标题与搜索引擎说明来自静态 metadata。本仓库只提供简体中文，
// 使用“产品名：说明”的标题格式。构建时确定的 metadata 不随运行时设置改变。
const META_BY_LOCALE: Record<Locale, { title: string; description: string }> = {
  zh: {
    title: "ARTEX-HYDRA：自主渗透与自适应编排控制台",
    description: "ARTEX-HYDRA — LLM 驱动的自主渗透测试与自适应编排作战指挥系统控制台",
  },
};

export const APP_CONFIG = {
  name: "ARTEX-HYDRA",
  version: BUILD_VERSION || packageJson.version,
  copyright: `© ${currentYear}, ARTEX-HYDRA.`,
  meta: META_BY_LOCALE[resolveLocale()] ?? META_BY_LOCALE[DEFAULT_LOCALE],
};
