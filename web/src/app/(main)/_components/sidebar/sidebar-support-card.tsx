import { useTranslations } from "next-intl";
import Link from "next/link";

import { siX } from "simple-icons";

import { Sheet, SheetHead } from "@/components/editorial-sheet";
import { SimpleIcon } from "@/components/simple-icon";

export function SidebarSupportCard() {
  const t = useTranslations("sidebarSupport");
  return (
    <Sheet className="overflow-hidden shadow-none group-data-[collapsible=icon]:hidden">
      <SheetHead title={t("title")} />
      <div className="px-4 pb-3">
        <p className="line-clamp-2 text-muted-foreground text-xs">
          {t.rich("desc", {
            // X 的图标本身就是链接内容，所以只把句末标点留在标签里，
            // 图标由渲染函数补在标点前面。
            x: (chunks) => (
              <>
                &nbsp;
                <Link
                  href="https://x.com/arhamkhnz"
                  target="_blank"
                  rel="noreferrer"
                  aria-label={t("xAria")}
                  className="inline-flex items-center text-foreground"
                >
                  <SimpleIcon icon={siX} aria-hidden className="size-3 fill-current" />
                </Link>
                {chunks}
              </>
            ),
          })}
        </p>
      </div>
    </Sheet>
  );
}
