"use client";

import * as React from "react";

import { RefreshCwIcon, ShieldAlertIcon, WifiIcon, WifiOffIcon } from "lucide-react";
import { useTranslations } from "next-intl";

import { Masthead } from "@/components/editorial";
import { Sheet, SheetHead } from "@/components/editorial-sheet";
import { CopyButton } from "@/components/copy-button";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { http } from "@/lib/api";

// 系统 → 局域网连接
//
// 这块页面回答两个问题：同一网络下的设备该打开哪条地址、没开局域网怎么开（按钮）。
// 打开的方式只有一种——浏览器：手机、平板、另一台电脑都走同一条 http 地址，
// 登录用这台机器的账号密码，不装任何客户端。
//
// 前端不自己猜 IP：机器有多块网卡时，只有后端分得清哪一条能被别的设备访问。

type LanInfo = {
  version: string;
  listen_addr: string;
  port: string;
  loopback_only: boolean;
  urls: string[];
  primary_url: string;
  password_set: boolean;
  restart_cmd: string;
  firewall_cmd: string;
  env_override: boolean;
};

type LanSwitchResult = {
  ok: boolean;
  listen: string;
  config: string;
  restart: boolean;
  firewall: string;
  env_override: boolean;
};

function InfoRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-baseline justify-between gap-3 border-b py-1.5 last:border-b-0">
      <span className="text-muted-foreground text-xs">{label}</span>
      <span className="font-medium text-sm tabular-nums">{children}</span>
    </div>
  );
}

export default function LanPage() {
  const t = useTranslations("lanPage");
  const [info, setInfo] = React.useState<LanInfo | null>(null);
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState<string | null>(null);
  // 正在切监听地址：请求发出后进程会自己重启，这一屏要等它回来。
  const [switching, setSwitching] = React.useState(false);
  const [firewall, setFirewall] = React.useState<string>("");

  const load = React.useCallback(async () => {
    setError(null);
    try {
      setInfo(await http<LanInfo>("/lan"));
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, []);

  React.useEffect(() => {
    void load();
  }, [load]);

  const lanOpen = info !== null && !info.loopback_only;

  // 重启后自动回来：轮询 /api/health，通了就整页刷新。
  React.useEffect(() => {
    if (!switching) return;
    let alive = true;
    const tick = async () => {
      if (!alive) return;
      try {
        await http("/health");
        if (alive) window.location.reload();
        return;
      } catch {
        // 还没起来，继续等。
      }
      if (alive) window.setTimeout(tick, 1200);
    };
    const first = window.setTimeout(tick, 1500);
    return () => {
      alive = false;
      window.clearTimeout(first);
    };
  }, [switching]);

  const switchExposure = React.useCallback(
    async (lan: boolean) => {
      setError(null);
      setFirewall("");
      setSwitching(true);
      try {
        const res = await http<LanSwitchResult>("/lan/" + (lan ? "enable" : "disable"), {
          method: "POST",
          body: JSON.stringify({ lan }),
        });
        setFirewall(res.firewall);
        if (!res.restart) {
          // 环境变量压着监听地址时写配置不会生效，直接把原因说清楚，别假装成功。
          setSwitching(false);
          setError(res.env_override ? t("envOverride") : t("noRestart"));
        }
      } catch (e) {
        setSwitching(false);
        setError(e instanceof Error ? e.message : String(e));
      }
    },
    [t],
  );

  const urls = info?.urls ?? [];
  const primary = info?.primary_url ?? "";

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <Masthead title={t("title")} subtitle={t("desc")} titleClassName="text-xl" subtitleClassName="text-sm font-normal leading-relaxed" />
        <Button type="button" size="sm" variant="outline" disabled={loading || switching} onClick={() => void load()}>
          <RefreshCwIcon />
          {t("refresh")}
        </Button>
      </div>

      {error ? (
        <Sheet>
          <div className="p-4 text-destructive text-sm">{error}</div>
        </Sheet>
      ) : null}

      {switching ? (
        <Sheet api={["GET /lan", "health"]}>
          <div className="flex items-center gap-2 p-4 text-sm">
            <RefreshCwIcon className="animate-spin" />
            {t("restarting")}
          </div>
        </Sheet>
      ) : null}

      <Sheet api={["GET /lan"]}>
        <SheetHead index="01" title={t("statusTitle")} />
        <div className="px-4 py-3">
          <p className="text-muted-foreground text-sm">{t("statusDesc")}</p>
          <InfoRow label={t("listenLabel")}>{info?.listen_addr ?? "-"}</InfoRow>
          <InfoRow label={t("lanLabel")}>
            {lanOpen ? (
              <Badge variant="secondary">{t("lanYes")}</Badge>
            ) : (
              <Badge variant="outline">{t("lanNo")}</Badge>
            )}
          </InfoRow>
          <InfoRow label={t("passwordLabel")}>
            {info?.password_set ? t("passwordSet") : t("passwordUnset")}
          </InfoRow>
        </div>
      </Sheet>

      {lanOpen ? (
        <Sheet api={["GET /lan"]}>
          <SheetHead index="02" title={t("webEntryTitle")} />
          <div className="space-y-3 px-4 py-3">
            <p className="text-muted-foreground text-sm">{t("webEntryHint")}</p>
            <div className="flex flex-wrap items-center gap-2">
              <code className="block select-all break-all rounded bg-muted p-2 text-xs">{primary}</code>
              <CopyButton text={primary} />
            </div>
            {urls.length > 1 ? (
              <div className="space-y-2">
                <div className="text-muted-foreground text-xs">{t("urlsLabel")}</div>
                {urls.map((u) => (
                  <div key={u} className="flex flex-wrap items-center gap-2">
                    <code className="block select-all break-all rounded bg-muted p-2 text-xs">{u}</code>
                    <CopyButton text={u} />
                  </div>
                ))}
              </div>
            ) : null}
            {info?.password_set ? null : (
              <p className="text-sm font-normal leading-relaxed text-destructive">{t("passwordNeedInit")}</p>
            )}
            <div className="pt-2">
              <Button
                type="button"
                size="sm"
                variant="outline"
                disabled={switching}
                onClick={() => void switchExposure(false)}
              >
                <WifiOffIcon />
                {t("disableButton")}
              </Button>
            </div>
            <p className="text-muted-foreground text-xs">{t("disableNote")}</p>
          </div>
        </Sheet>
      ) : (
        <Sheet api={["POST /lan/enable", "POST /lan/disable"]}>
          <SheetHead index="03" title={t("enableTitle")} />
          <div className="space-y-3 px-4 py-3">
            <p className="text-muted-foreground text-sm">{t("enableDesc")}</p>
            <div className="flex flex-wrap items-center gap-2">
              <Button
                type="button"
                disabled={switching || loading || !info?.password_set}
                onClick={() => void switchExposure(true)}
              >
                <WifiIcon />
                {t("enableButton")}
              </Button>
              <span className="text-muted-foreground text-xs">{t("enableButtonNote")}</span>
            </div>
            {info?.password_set ? null : (
              <p className="text-sm font-normal leading-relaxed text-destructive">{t("passwordNeedInit")}</p>
            )}

            {firewall === "ok" ? <p className="text-muted-foreground text-xs">{t("firewallAuto")}</p> : null}
            {firewall === "manual" ? <p className="text-destructive text-xs">{t("firewallManual")}</p> : null}

            <div className="space-y-2">
              <div className="text-muted-foreground text-xs">{t("firewallHint")}</div>
              <div className="flex flex-wrap items-center gap-2">
                <code className="block select-all break-all rounded bg-muted p-2 text-xs">{info?.firewall_cmd ?? ""}</code>
                <CopyButton text={info?.firewall_cmd} />
              </div>
            </div>

            <p className="text-sm font-normal leading-relaxed text-muted-foreground">{t("enableNote")}</p>

            <div className="space-y-2">
              <div className="text-muted-foreground text-xs">{t("scriptHint")}</div>
              <div className="flex flex-wrap items-center gap-2">
                <code className="block select-all break-all rounded bg-muted p-2 text-xs">{info?.restart_cmd ?? ""}</code>
                <CopyButton text={info?.restart_cmd} />
              </div>
            </div>
          </div>
        </Sheet>
      )}

      <Sheet>
        <SheetHead index="04"
          icon={ShieldAlertIcon}
          title={<span className="font-medium text-foreground">{t("securityTitle")}</span>}
        />
        <div className="px-4 py-3">
          <p className="text-sm font-normal leading-relaxed text-muted-foreground">{t("securityBody")}</p>
        </div>
      </Sheet>

      <p className="flex items-center gap-2 text-muted-foreground text-xs">
        {loading ? t("loading") : `${t("versionLabel")} ${info?.version ?? "-"}`}
      </p>
    </div>
  );
}
