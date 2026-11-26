// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo, useState } from "react";
import { Check, ChevronDown, ChevronRight, CircleCheckBig, Network } from "lucide-react";

import { MarkdownWrapper } from "@/common/markdown";

import { cn } from "@/utils/cnTailwind";

import { Button } from "@/ui/button";

export const TEST_REPORT_ARTIFACT_FILENAME = "test_report.json";

export type RecommendationArea = "system_prompt" | "tools" | "workflow_logic" | "model_choice" | "other";

export type RecommendationPriority = "high" | "medium" | "low";

export interface Recommendation {
    area: RecommendationArea;
    finding: string;
    suggestion: string;
    priority: RecommendationPriority;
}

export interface TestReport {
    componentName: string;
    componentKind: "workflow" | "agent" | "skill";
    sampleInput: unknown;
    expectedOutput: unknown;
    actualOutput: unknown;
    summary?: string;
    recommendations: Recommendation[];
    taskId?: string;
}

interface TestReportCardProps {
    report: TestReport;
    runNumber?: number | null;
    onApplyChanges: (report: TestReport) => void;
    onContinueTesting?: () => void;
    onViewActivity?: () => void;
    isActivityShown?: boolean;
    alreadyApplied?: boolean;
}

export const AREA_LABELS: Record<RecommendationArea, string> = {
    system_prompt: "System Prompt",
    tools: "Tools",
    workflow_logic: "Workflow Logic",
    model_choice: "Model Choice",
    other: "Other",
};

export function TestReportCard({ report, runNumber, onApplyChanges, onContinueTesting, onViewActivity, isActivityShown = false, alreadyApplied = false }: TestReportCardProps) {
    const [collapsed, setCollapsed] = useState(false);

    const hasRecommendations = report.recommendations.length > 0;
    const runLabel = `Test ${runNumber ?? 1}`;

    return (
        <div className="mt-3 flex flex-col gap-6 rounded-md border bg-(--background-w10) p-4">
            <div className="flex flex-col gap-1">
                <div className="flex items-center gap-2">
                    <Button variant="ghost" size="sm" className="h-6 w-6 p-0" onClick={() => setCollapsed(v => !v)} aria-label={collapsed ? "Expand report" : "Collapse report"} aria-expanded={!collapsed}>
                        {collapsed ? <ChevronRight className="h-3.5 w-3.5" /> : <ChevronDown className="h-3.5 w-3.5" />}
                    </Button>
                    <h3 className="flex-1 text-sm font-semibold">
                        <span className="font-normal text-(--secondary-text-wMain)">Test Report: </span>
                        {runLabel}
                    </h3>
                    {onViewActivity && (
                        <Button
                            variant="ghost"
                            size="sm"
                            onClick={onViewActivity}
                            disabled={isActivityShown}
                            className="shrink-0 gap-1.5 font-medium text-(--primary-wMain) hover:text-(--primary-wMain)"
                            tooltip={isActivityShown ? "Already shown in canvas" : "Show this run's activity diagram in the canvas"}
                        >
                            <Network className="h-3.5 w-3.5" />
                            View Test Activity
                        </Button>
                    )}
                </div>
                {!hasRecommendations && (
                    <div className="flex items-center gap-2 pl-8 text-sm">
                        <CircleCheckBig className="h-4 w-4 shrink-0 text-(--success-wMain)" />
                        <span>The test was successful. No changes are needed.</span>
                    </div>
                )}
                {hasRecommendations && report.summary && <p className="pl-8 text-sm text-(--secondary-text-wMain)">{report.summary}</p>}
            </div>

            {!collapsed && (
                <>
                    <Section label="Input" value={report.sampleInput} />
                    <Section label="Expected Output" value={report.expectedOutput} />
                    <Section label="Actual Output" value={report.actualOutput} />

                    {hasRecommendations && (
                        <div className="flex flex-col gap-2">
                            <h4 className="text-sm font-semibold">Recommendations</h4>
                            {report.recommendations.map((r, i) => (
                                <RecommendationRow key={`${r.area}-${r.suggestion}`} rec={r} isFirst={i === 0} />
                            ))}
                        </div>
                    )}

                    <div className="flex items-center justify-end gap-2">
                        {onContinueTesting && (
                            <Button size="sm" variant="ghost" onClick={onContinueTesting} className="font-medium text-(--primary-wMain) hover:text-(--primary-wMain)">
                                Run Another Test
                            </Button>
                        )}
                        {hasRecommendations && (
                            <Button
                                size="sm"
                                variant="outline"
                                onClick={() => onApplyChanges(report)}
                                disabled={alreadyApplied}
                                className="border-(--primary-wMain) font-medium text-(--primary-wMain) hover:bg-(--primary-w10) hover:text-(--primary-wMain) disabled:opacity-50"
                            >
                                {alreadyApplied && <Check className="h-3.5 w-3.5" />}
                                {alreadyApplied ? "Changes Applied" : "Apply Changes"}
                            </Button>
                        )}
                    </div>
                </>
            )}
        </div>
    );
}

function Section({ label, value }: { label: string; value: unknown }) {
    return (
        <div className="flex flex-col gap-1">
            <h4 className="text-sm font-semibold">{label}</h4>
            <OutputBlock value={value} />
        </div>
    );
}

function RecommendationRow({ rec, isFirst }: Readonly<{ rec: Recommendation; isFirst: boolean }>) {
    const [expanded, setExpanded] = useState(false);
    return (
        <div className={cn("flex flex-col gap-1 py-4", !isFirst && "border-t")}>
            <div className="flex min-w-0 flex-wrap items-baseline gap-1.5 text-sm">
                <span className="shrink-0 rounded bg-(--background-w20) px-1.5 py-0.5 text-xs">{AREA_LABELS[rec.area]}</span>
                <span className="font-medium">{rec.suggestion}</span>
            </div>
            <div className="mt-3 flex items-center gap-1">
                <Button variant="ghost" size="sm" className="h-6 w-6 shrink-0 p-0" onClick={() => setExpanded(v => !v)} aria-label={expanded ? "Hide reasoning" : "Show reasoning"} aria-expanded={expanded}>
                    {expanded ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}
                </Button>
                <button type="button" className="cursor-pointer text-sm text-(--secondary-text-wMain)" onClick={() => setExpanded(v => !v)}>
                    Reasoning
                </button>
            </div>
            {expanded && <p className="pl-8 text-sm leading-relaxed">{rec.finding}</p>}
        </div>
    );
}

function OutputBlock({ value }: Readonly<{ value: unknown }>) {
    if (value == null || (typeof value === "string" && value.trim() === "")) {
        return <span className="text-xs italic">—</span>;
    }
    if (typeof value !== "string") {
        return <JsonBlock value={value} />;
    }
    const trimmed = value.trim();
    if (trimmed.startsWith("{") || trimmed.startsWith("[")) {
        try {
            return <JsonBlock value={JSON.parse(trimmed)} />;
        } catch {}
    }
    return (
        <div className="max-h-48 overflow-auto rounded-md border bg-(--background-w20) p-2 text-xs">
            <MarkdownWrapper content={value} />
        </div>
    );
}

function JsonBlock({ value }: Readonly<{ value: unknown }>) {
    const text = useMemo(() => {
        if (typeof value === "string") return value;
        try {
            return JSON.stringify(value, null, 2);
        } catch {
            return String(value);
        }
    }, [value]);
    return <pre className="max-h-48 overflow-auto rounded-md border bg-(--background-w20) p-2 font-mono text-xs whitespace-pre-wrap">{text}</pre>;
}
