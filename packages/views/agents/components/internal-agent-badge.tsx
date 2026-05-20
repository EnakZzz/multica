"use client";

import { useT } from "../../i18n";

export function InternalAgentBadge() {
  const { t } = useT("agents");
  return (
    <span className="inline-flex shrink-0 items-center rounded border border-amber-500/25 bg-amber-500/10 px-1.5 py-0.5 text-[10px] font-medium text-amber-700 dark:text-amber-300">
      {t(($) => $.row.built_in)}
    </span>
  );
}
