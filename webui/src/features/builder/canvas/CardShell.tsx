// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { LucideIcon } from "lucide-react";
import { AlertCircle, BookOpen, Bot, CircleCheckBig, FileText, Globe, Loader2, Pause, Plug, Workflow, Wrench } from "lucide-react";
import type { ReactNode } from "react";

import { cn } from "@/utils/cnTailwind";

import { Badge } from "@/ui/badge";
import { Card } from "@/ui/card";

import { useCanvasView } from "./canvasViewContext";
import { TestTargetButton } from "./TestTargetButton";

export interface CardShellProps {
    selected?: boolean;
    generating?: boolean;
    editing?: boolean;
    queued?: boolean;
    buildingDeps?: boolean;
    ready?: boolean;
    active?: boolean;
    doneLabel?: string;
    draft?: boolean;
    error?: boolean;
    width?: number;
    children: ReactNode;
    className?: string;
    statusText?: string;
    componentName?: string;
    testEligible?: boolean;
    testButtonAlign?: "top" | "center";
}

type StatusKind = "error" | "waiting" | "generating" | "buildingDeps" | "queued" | "done" | "draft";

interface StatusFlags {
    error?: boolean;
    editing?: boolean;
    generating?: boolean;
    buildingDeps?: boolean;
    queued?: boolean;
    active?: boolean;
    ready?: boolean;
    draft?: boolean;
    isWaiting?: boolean;
    done?: boolean;
    statusText?: string;
    doneLabel?: string;
}

export function deriveLabelText(f: StatusFlags): string {
    if (f.statusText) return f.statusText;
    if (f.error) return "Error";
    if (f.editing) return "Editing";
    if (f.generating) return "Creating";
    const isMidBuildSubState = f.buildingDeps || f.queued;
    if (isMidBuildSubState) return "Creating";
    if (f.active) return f.doneLabel ?? "Active";
    if (f.ready) return f.doneLabel ?? "Ready";
    if (f.draft) return "Draft";
    return "";
}

export function deriveStatusKind(f: StatusFlags): StatusKind | null {
    if (f.error) return "error";
    if (f.isWaiting) return "waiting";
    if (f.generating) return "generating";
    if (f.buildingDeps) return "buildingDeps";
    if (f.queued) return "queued";
    if (f.done) return "done";
    if (f.draft) return "draft";
    return null;
}

const STATUS_ICON: Record<StatusKind, ReactNode> = {
    error: <AlertCircle className="h-4 w-4" style={{ color: "var(--error-wMain)" }} />,
    waiting: <Pause className="h-4 w-4" style={{ color: "var(--warning-wMain)" }} />,
    generating: <Loader2 className="h-4 w-4 animate-spin" style={{ color: "var(--brand-wMain)" }} />,
    buildingDeps: <Loader2 className="h-4 w-4 animate-spin" style={{ color: "var(--brand-wMain)" }} />,
    queued: <Loader2 className="h-4 w-4 animate-spin" style={{ color: "var(--brand-wMain)" }} />,
    done: <CircleCheckBig className="h-4 w-4" style={{ color: "var(--success-wMain)" }} />,
    draft: <FileText className="h-4 w-4" />,
};

const STATUS_TEXT_CLASS: Record<StatusKind, string> = {
    error: "text-(--error-wMain)",
    waiting: "text-(--primary-text-wMain)",
    generating: "text-(--primary-text-wMain)",
    buildingDeps: "text-(--primary-text-wMain)",
    queued: "text-(--primary-text-wMain)",
    done: "",
    draft: "",
};

const PILL_BORDER_COLOR: Partial<Record<StatusKind, string>> = {
    error: "var(--error-wMain)",
    waiting: "var(--warning-wMain)",
    generating: "var(--brand-wMain)",
};

function StatusPill({ kind, labelText }: Readonly<{ kind: StatusKind | null; labelText: string }>) {
    let pillType: "success" | "error" | "warning" | undefined;
    if (kind === "done") pillType = "success";
    else if (kind === "error") pillType = "error";
    else if (kind === "waiting") pillType = "warning";

    return (
        <Badge type={pillType} className="absolute -top-3.5 left-1/2 z-10 -translate-x-1/2 whitespace-nowrap" style={{ borderColor: kind && !pillType ? PILL_BORDER_COLOR[kind] : undefined }}>
            {kind && STATUS_ICON[kind]}
            <span className={cn("text-xs font-medium", !pillType && kind ? STATUS_TEXT_CLASS[kind] : undefined)}>{labelText}</span>
        </Badge>
    );
}

function cardClassName(
    o: Readonly<{
        selected?: boolean;
        isTestTarget: boolean;
        generating?: boolean;
        isWaiting: boolean;
        buildingDeps?: boolean;
        queued?: boolean;
        className?: string;
    }>
): string {
    return cn(
        "relative !gap-0 !rounded card-surface-hover transition-all duration-200",
        o.selected && "ring-2 ring-(--accent-n2-wMain)/40",
        o.isTestTarget && "!border-(--accent-n2-wMain)",
        o.isWaiting && "border-(--warning-wMain)/60",
        (o.buildingDeps || o.queued) && !o.isWaiting && !o.generating && "border-(--brand-wMain)/30",
        o.className
    );
}

export function CardShell({ selected, generating, editing, queued, buildingDeps, ready, active, draft, error, doneLabel, width, children, className, statusText, componentName, testEligible, testButtonAlign = "top" }: Readonly<CardShellProps>) {
    const { builderActiveTab, testTargetName } = useCanvasView();
    const isWaiting = !!statusText && statusText.toLowerCase().includes("waiting");
    const done = !!(active || ready);
    const showStatusLabel = !!(statusText || generating || buildingDeps || queued || done || draft || error);
    const labelText = deriveLabelText({ statusText, error, editing, generating, buildingDeps, queued, active, ready, draft, doneLabel });
    const statusKind = deriveStatusKind({ error, isWaiting, generating, buildingDeps, queued, done, draft });

    const isTestTarget = builderActiveTab === "test" && !!componentName && testTargetName === componentName;

    const showTestButton = builderActiveTab === "test" && !!componentName && !!testEligible;

    return (
        <Card noPadding className={cardClassName({ selected, isTestTarget, generating, isWaiting, buildingDeps, queued, className })} style={{ width }}>
            {showTestButton && (
                <div className={cn("absolute right-2 z-20", testButtonAlign === "center" ? "top-1/2 -translate-y-1/2" : "top-2")}>
                    <TestTargetButton componentName={componentName} eligible={testEligible} disabledWhenSelected={isTestTarget} />
                </div>
            )}
            {showStatusLabel && <StatusPill kind={statusKind} labelText={labelText} />}
            <div className={cn(showTestButton && "builder-test-reserve")}>{children}</div>
        </Card>
    );
}

const STATUS_LABELS: Record<string, string> = {
    validated: "ready",
    deployed: "deployed",
    error: "error",
    draft: "draft",
    blocked: "needs input",
    generating: "creating",
    queued: "queued",
    new: "new",
};

export function StatusBadge({ status }: Readonly<{ status?: string }>) {
    if (!status) return null;

    const isValidated = status === "validated";
    const displayLabel = STATUS_LABELS[status] || status;

    return (
        <span
            className={cn(
                "inline-flex items-center gap-0.5 rounded px-1.5 py-0.5 text-[9px] font-medium",
                status === "validated" && "bg-(--success-w10) text-(--success-wMain)",
                status === "deployed" && "bg-(--info-w10) text-(--info-wMain)",
                status === "new" && "bg-(--info-w10) text-(--info-wMain)",
                status === "error" && "bg-(--error-w10) text-(--error-wMain)",
                status === "draft" && "bg-(--secondary-w10) text-(--secondary-text-wMain)",
                status === "blocked" && "bg-(--warning-w10) text-(--warning-wMain)",
                status === "generating" && "bg-(--brand-wMain)/10 text-(--brand-wMain)",
                status === "queued" && "bg-(--secondary-w10) text-(--secondary-text-wMain)"
            )}
        >
            {isValidated && <CircleCheckBig className="h-2.5 w-2.5" />}
            {displayLabel}
        </span>
    );
}

const COMPONENT_TYPE_ICONS: Record<string, LucideIcon> = {
    workflow: Workflow,
    connector: Plug,
    gateway: Globe,
    skill: BookOpen,
    toolset: Wrench,
    agent: Bot,
};

export function componentTypeIcon(type: string): LucideIcon {
    return COMPONENT_TYPE_ICONS[type] ?? Bot;
}

export function typeColor(type: string): string {
    switch (type) {
        case "workflow":
            return "var(--accent-n1-wMain)";
        case "agent":
            return "var(--brand-wMain)";
        case "gateway":
            return "var(--accent-n3-wMain)";
        case "connector":
            return "var(--accent-n2-wMain)";
        case "skill":
            return "var(--info-wMain)";
        case "toolset":
            return "var(--brand-wMain)";
        default:
            return "var(--secondary-text-wMain)";
    }
}
