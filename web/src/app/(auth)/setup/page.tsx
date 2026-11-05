"use client";

import { useEffect, useState } from "react";

import { useRouter } from "next/navigation";

import { useTranslations } from "next-intl";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { SecretInput } from "@/components/ui/secret-input";
import { api } from "@/lib/api";
import { auth } from "@/lib/auth";

export default function SetupPage() {
  const router = useRouter();
  const t = useTranslations("auth");
  const [setupToken, setSetupToken] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [checking, setChecking] = useState(true);
  const [skipOpen, setSkipOpen] = useState(false);
  const [skipping, setSkipping] = useState(false);

  useEffect(() => {
    api
      .authStatus()
      .then(({ initialized }) => {
        if (initialized) router.replace("/login");
      })
      .catch(() => setError(t("setup.errorBackend")))
      .finally(() => setChecking(false));
  }, [router, t]);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!setupToken.trim()) {
      setError(t("setup.errorTokenEmpty"));
      return;
    }
    if (password !== confirm) {
      setError(t("setup.errorMismatch"));
      return;
    }
    if (password.length < 8) {
      setError(t("setup.errorTooShort"));
      return;
    }
    setLoading(true);
    setError("");
    try {
      const { token } = await api.initPassword(password, setupToken.trim());
      auth.setToken(token);
      router.replace("/function/tasks");
    } catch (err) {
      setError(err instanceof Error ? err.message : t("setup.errorInit"));
    } finally {
      setLoading(false);
    }
  }

  // 跳过初始化：服务端生成随机密码并只打到控制台，这里拿到会话 token 直接进控制台。
  // 明文不回传，所以页面上不展示密码——只提示去哪里看、去哪里改。
  async function handleSkip() {
    setSkipping(true);
    setError("");
    try {
      const { token } = await api.skipInit();
      auth.setToken(token);
      router.replace("/function/tasks");
    } catch (err) {
      setError(err instanceof Error ? err.message : t("setup.errorSkip"));
      setSkipOpen(false);
    } finally {
      setSkipping(false);
    }
  }

  if (checking) return null;
  return (
    <div className="flex h-dvh">
      {/* Left panel */}
      <div className="hidden flex-col items-center justify-center bg-primary p-12 text-center lg:flex lg:w-1/3">
        <div className="relative flex items-center justify-center">
          <div className="absolute size-80 rounded-full border border-primary-foreground/10" />
          <div className="absolute size-60 rounded-full border border-primary-foreground/15" />
          <div className="absolute size-40 rounded-full border border-primary-foreground/20" />
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img src="/logo.png" alt="ARTEX-HYDRA" width={160} height={160} className="relative brightness-0 invert" />
        </div>
      </div>

      {/* Right panel */}
      <div className="flex w-full items-center justify-center bg-background p-8 lg:w-2/3">
        <div className="w-full max-w-md space-y-10 py-24 lg:py-32">
          <div className="space-y-4 text-center">
            <h2 className="font-medium text-2xl tracking-tight">{t("setup.title")}</h2>
            <p className="mx-auto max-w-xl text-muted-foreground">{t("setup.desc")}</p>
          </div>
          <form onSubmit={handleSubmit} className="flex flex-col gap-4">
            <div className="space-y-1.5">
              <Label htmlFor="setup-token">{t("setup.setupToken")}</Label>
              <Input
                id="setup-token"
                type="text"
                value={setupToken}
                onChange={(e) => setSetupToken(e.target.value)}
                placeholder={t("setup.setupTokenPlaceholder")}
                autoFocus
                autoComplete="off"
                spellCheck={false}
              />
              <p className="text-muted-foreground text-xs">{t("setup.setupTokenHelp")}</p>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="password">{t("setup.newPassword")}</Label>
              <SecretInput
                id="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder={t("setup.newPasswordPlaceholder")}
                autoComplete="new-password"
                label="登录密码"
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="confirm">{t("setup.confirmPassword")}</Label>
              <SecretInput
                id="confirm"
                value={confirm}
                onChange={(e) => setConfirm(e.target.value)}
                placeholder={t("setup.confirmPlaceholder")}
                autoComplete="new-password"
                label="确认密码"
              />
            </div>
            {error && <p className="text-destructive text-sm">{error}</p>}
            <Button type="submit" className="w-full" disabled={loading || !setupToken.trim() || !password || !confirm}>
              {loading ? t("setup.submitting") : t("setup.submit")}
            </Button>
            <div className="relative py-1 text-center">
              <span className="text-muted-foreground text-xs">{t("setup.or")}</span>
            </div>
            <Button
              type="button"
              variant="outline"
              className="w-full"
              disabled={loading || skipping}
              onClick={() => setSkipOpen(true)}
            >
              {t("setup.skip")}
            </Button>
          </form>
        </div>
      </div>

      <Dialog open={skipOpen} onOpenChange={(o) => !skipping && setSkipOpen(o)}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t("setup.skipTitle")}</DialogTitle>
            <DialogDescription>{t("setup.skipDesc")}</DialogDescription>
          </DialogHeader>
          <ul className="ml-4 list-disc space-y-1.5 text-muted-foreground text-sm">
            <li>{t("setup.skipPoint1")}</li>
            <li>{t("setup.skipPoint2")}</li>
            <li>{t("setup.skipPoint3")}</li>
          </ul>
          <DialogFooter>
            <Button type="button" variant="outline" disabled={skipping} onClick={() => setSkipOpen(false)}>
              {t("setup.skipCancel")}
            </Button>
            <Button type="button" disabled={skipping} onClick={() => void handleSkip()}>
              {skipping ? t("setup.skipping") : t("setup.skipConfirm")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
