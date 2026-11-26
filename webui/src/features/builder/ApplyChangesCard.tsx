// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { ArrowRight } from "lucide-react";

import { Button } from "@/ui/button";

import { AREA_LABELS, type RecommendationArea, type RecommendationPriority } from "./TestReportCard";

export interface ApplyChangesCardData {
    componentName: string;
    componentKind: "workflow" | "agent" | "skill";
    items: { priority: RecommendationPriority; area: RecommendationArea; finding: string }[];
}

const PRIORITY_LABELS: Record<RecommendationPriority, string> = {
    high: "High priority",
    medium: "Medium priority",
    low: "Low priority",
};

const PRIORITY_ORDER: RecommendationPriority[] = ["high", "medium", "low"];

export function ApplyChangesCard({ data, onViewTestReport }: Readonly<{ data: ApplyChangesCardData; onViewTestReport: () => void }>) {
    return (
        <div className="mt-3 flex flex-col gap-3 rounded-md border bg-(--background-w10) p-4">
            <h3 className="text-sm font-semibold">
                Apply these changes to the {data.componentKind} <span className="font-normal">{data.componentName}</span>
            </h3>

            {data.items.length === 0 ? (
                <p className="text-sm">No changes were recommended.</p>
            ) : (
                <div className="flex flex-col gap-3">
                    {PRIORITY_ORDER.map(priority => {
                        const items = data.items.filter(i => i.priority === priority);
                        if (items.length === 0) return null;
                        return (
                            <div key={priority} className="flex flex-col gap-1.5">
                                <h4 className="text-xs font-semibold">
                                    {PRIORITY_LABELS[priority]} ({items.length})
                                </h4>
                                <ul className="flex flex-col gap-1">
                                    {items.map((item, i) => (
                                        <li key={`${item.area}-${i}`} className="flex min-w-0 items-baseline gap-1.5 text-sm">
                                            <span className="shrink-0 rounded bg-(--background-w20) px-1.5 py-0.5 text-xs">{AREA_LABELS[item.area]}</span>
                                            <span className="min-w-0">{item.finding}</span>
                                        </li>
                                    ))}
                                </ul>
                            </div>
                        );
                    })}
                </div>
            )}

            <div className="flex justify-end">
                <Button size="sm" variant="ghost" onClick={onViewTestReport} className="gap-1.5 font-medium text-(--primary-wMain) hover:text-(--primary-wMain)">
                    View test report
                    <ArrowRight className="h-3.5 w-3.5" />
                </Button>
            </div>
        </div>
    );
}
