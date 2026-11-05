"use client";

import * as React from "react";

import { EyeIcon, EyeOffIcon } from "lucide-react";

import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";

/**
 * 带「小眼睛」的密文输入框。
 *
 * 密码、令牌、API Key 这类字段在配置页里经常要人工核对一遍有没有粘错，
 * 一律 `type="password"` 会让人只能盲填。这里统一成一个组件：
 * 默认密文，点右侧眼睛切成明文，再点切回。
 *
 * 安全边界：只切换 input 的 `type`，不做任何持久化——刷新即回到密文；
 * 也不会把值写进 title/aria-label，避免辅助技术把明文读出来。
 */
export function SecretInput({
  className,
  disabled,
  /** 用于 aria-label：如「密码」「令牌」。 */
  label = "密文",
  ...props
}: Omit<React.ComponentProps<typeof Input>, "type"> & { label?: string }) {
  const [visible, setVisible] = React.useState(false);

  return (
    <div className="relative">
      <Input
        {...props}
        type={visible ? "text" : "password"}
        disabled={disabled}
        className={cn("pr-9", className)}
      />
      <button
        type="button"
        tabIndex={disabled ? -1 : 0}
        disabled={disabled}
        onClick={() => setVisible((v) => !v)}
        aria-label={visible ? `隐藏${label}` : `显示${label}`}
        aria-pressed={visible}
        title={visible ? `隐藏${label}` : `显示${label}`}
        className="absolute inset-y-0 right-0 flex w-9 items-center justify-center rounded-r-md text-muted-foreground transition-colors hover:text-foreground focus-visible:text-foreground focus-visible:outline-none disabled:cursor-not-allowed disabled:opacity-50"
      >
        {visible ? <EyeOffIcon className="size-4" /> : <EyeIcon className="size-4" />}
      </button>
    </div>
  );
}
