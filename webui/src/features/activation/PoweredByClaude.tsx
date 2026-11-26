// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import claudeIcon from "@/assets/images/claude.svg";

import { cn } from "@/utils";

export function PoweredByClaude({ pill = false, className }: Readonly<{ pill?: boolean; className?: string }>) {
    return (
        <span className={cn("inline-flex shrink-0 items-center gap-1 text-xs whitespace-nowrap text-(--secondary-text-wMain)", pill && "rounded-full border border-(--secondary-w20) bg-(--secondary-w10) px-2 py-0.5", className)}>
            <span>Powered by</span>
            <img src={claudeIcon} alt="" className="size-3.5" />
            <span className="font-semibold text-(--primary-text-wMain)">Claude</span>
        </span>
    );
}
