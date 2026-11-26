// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode, useState } from "react";
import { ChevronDown, ChevronRight, Eye, EyeOff } from "lucide-react";

import { cn } from "@/utils/cnTailwind";

import { Button } from "@/ui/button";

import { isSecretKey } from "../builderUtils";

export function DetailSection({ label, children, className }: { label: string; children: ReactNode; className?: string }) {
    return (
        <div className={cn("mb-4", className)}>
            <span className="mb-1.5 block text-[10px] font-semibold tracking-wider uppercase">{label}</span>
            {children}
        </div>
    );
}

type PillVariant = "brand" | "accent" | "info" | "muted" | "success" | "warning" | "error";

const PILL_CLASSES: Record<PillVariant, string> = {
    brand: "bg-(--brand-wMain)/10 text-(--brand-wMain)",
    accent: "bg-(--accent-n2-wMain)/10 text-(--accent-n2-wMain)",
    info: "bg-(--info-w10) text-(--info-wMain)",
    muted: "bg-(--secondary-w10) text-(--secondary-text-wMain)",
    success: "bg-(--success-w10) text-(--success-wMain)",
    warning: "bg-(--warning-w10) text-(--warning-wMain)",
    error: "bg-(--error-w10) text-(--error-wMain)",
};

export function DetailPill({ children, variant = "muted" }: { children: ReactNode; variant?: PillVariant }) {
    return <span className={cn("inline-block rounded-md px-2 py-0.5 text-xs font-medium", PILL_CLASSES[variant])}>{children}</span>;
}

export function ChipList({ items, variant = "muted" }: { items: string[]; variant?: PillVariant }) {
    if (items.length === 0) return <span className="text-xs italic">empty</span>;
    return (
        <div className="scrollbar-subtle flex flex-nowrap gap-1 overflow-x-auto [&>*]:shrink-0">
            {items.map((item, i) => (
                <DetailPill key={i} variant={variant}>
                    {item}
                </DetailPill>
            ))}
        </div>
    );
}

export function ExpandableText({ text, maxLines = 3, defaultExpanded = false }: { text: string; maxLines?: number; defaultExpanded?: boolean }) {
    const [expanded, setExpanded] = useState(defaultExpanded);
    const isLong = text.length > 100 || text.split("\n").length > maxLines;

    if (!isLong) {
        return <span className="text-xs whitespace-pre-wrap">{text}</span>;
    }

    return (
        <div>
            <div className={cn("text-xs whitespace-pre-wrap", !expanded && "line-clamp-3")}>{text}</div>
            <Button variant="ghost" size="sm" onClick={() => setExpanded(!expanded)} className="mt-0.5 h-auto px-0 py-0 text-[10px] hover:underline">
                {expanded ? "Show less" : "Show more"}
            </Button>
        </div>
    );
}

export function BooleanBadge({ value }: { value: boolean }) {
    return <span className={cn("inline-block rounded px-1.5 py-0.5 text-[10px] font-medium", value ? "bg-(--success-w10) text-(--success-wMain)" : "bg-(--secondary-w10) text-(--secondary-text-wMain)")}>{value ? "true" : "false"}</span>;
}

export function MaskedValue({ value }: { value: string }) {
    const [revealed, setRevealed] = useState(false);
    return (
        <span className="inline-flex items-center gap-1 text-xs">
            <code className="font-mono">{revealed ? value : "\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022"}</code>
            <Button variant="ghost" size="icon" onClick={() => setRevealed(!revealed)} className="h-auto p-0" title={revealed ? "Hide" : "Reveal"} aria-label={revealed ? "Hide value" : "Reveal value"}>
                {revealed ? <EyeOff className="h-3 w-3" /> : <Eye className="h-3 w-3" />}
            </Button>
        </span>
    );
}

export function CollapsibleSection({ label, defaultOpen = true, children }: { label: string; defaultOpen?: boolean; children: ReactNode }) {
    const [open, setOpen] = useState(defaultOpen);
    return (
        <div className="mb-2">
            <Button variant="ghost" size="sm" onClick={() => setOpen(!open)} className="flex h-auto w-full items-center justify-start gap-1 px-0 py-0 text-xs font-medium hover:underline">
                {open ? <ChevronDown className="h-3 w-3" /> : <ChevronRight className="h-3 w-3" />}
                {label}
            </Button>
            {open && <div className="mt-1 ml-4">{children}</div>}
        </div>
    );
}

const EXPANDED_BY_DEFAULT_KEYS = ["description", "system_purpose"];

export function formatKeyLabel(key: string): string {
    return key.replace(/_/g, " ").replace(/\b\w/g, c => c.toUpperCase());
}

export function DetailKeyValue({ label, value, rawKey }: { label: string; value: unknown; rawKey?: string }) {
    const effectiveKey = rawKey ?? label;

    if (value === null || value === undefined) {
        return (
            <div className="mb-1.5">
                <span className="text-[10px] font-medium">{label}</span>
                <div className="text-xs italic">not set</div>
            </div>
        );
    }

    if (typeof value === "boolean") {
        return (
            <div className="mb-1.5 flex items-center gap-2">
                <span className="text-[10px] font-medium">{label}</span>
                <BooleanBadge value={value} />
            </div>
        );
    }

    if (typeof value === "number") {
        return (
            <div className="mb-1.5">
                <span className="text-[10px] font-medium">{label}</span>
                <div>
                    <code className="font-mono text-xs">{value}</code>
                </div>
            </div>
        );
    }

    if (typeof value === "string") {
        if (isSecretKey(effectiveKey)) {
            return (
                <div className="mb-1.5">
                    <span className="text-[10px] font-medium">{label}</span>
                    <div>
                        <MaskedValue value={value} />
                    </div>
                </div>
            );
        }
        if (value.length > 100 || value.includes("\n")) {
            const shouldExpand = EXPANDED_BY_DEFAULT_KEYS.some(k => effectiveKey.toLowerCase().includes(k));
            return (
                <div className="mb-1.5">
                    <span className="text-[10px] font-medium">{label}</span>
                    <div className="mt-0.5 rounded p-2">
                        <ExpandableText text={value} defaultExpanded={shouldExpand} />
                    </div>
                </div>
            );
        }
        return (
            <div className="mb-1.5">
                <span className="text-[10px] font-medium">{label}</span>
                <div>
                    <code className="font-mono text-xs">{value}</code>
                </div>
            </div>
        );
    }

    return (
        <div className="mb-1.5">
            <span className="text-[10px] font-medium">{label}</span>
            <div>
                <code className="font-mono text-xs">{JSON.stringify(value)}</code>
            </div>
        </div>
    );
}
