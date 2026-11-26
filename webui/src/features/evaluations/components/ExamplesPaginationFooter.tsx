// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { cn } from "@/utils/cnTailwind";

import { PaginationControls } from "@/common";

interface ExamplesPaginationFooterProps {
    readonly page: number;
    readonly pageSize: number;
    readonly totalPages: number;
    readonly totalCount: number;
    readonly onPageChange: (page: number) => void;
    readonly summariseSinglePage?: boolean;
    readonly layout?: "inline" | "stacked";
}

export function ExamplesPaginationFooter({ page, pageSize, totalPages, totalCount, onPageChange, summariseSinglePage = false, layout = "inline" }: ExamplesPaginationFooterProps) {
    if (totalCount === 0) return null;

    const stacked = layout === "stacked";
    const summary = (
        <>
            Showing {(page - 1) * pageSize + 1}–{Math.min(page * pageSize, totalCount)} of {totalCount} results
        </>
    );

    if (totalPages > 1) {
        const controls = <PaginationControls variant="compact" totalPages={totalPages} currentPage={page} onPageChange={onPageChange} className={cn("mt-0", !stacked && "border-t px-4 py-1")} summary={stacked ? undefined : summary} />;
        if (!stacked) return controls;
        return (
            <div className="flex flex-col items-center gap-1">
                {controls}
                <span className="text-xs text-(--secondary-text-wMain)">{summary}</span>
            </div>
        );
    }

    if (!summariseSinglePage) return null;
    return <div className={cn("flex items-center py-1 text-xs text-(--secondary-text-wMain)", stacked ? "justify-center" : "justify-end border-t px-4 py-2")}>Showing {totalCount} results</div>;
}
