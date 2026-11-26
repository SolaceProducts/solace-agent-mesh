// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import * as TabsPrimitive from "@radix-ui/react-tabs";
import { AlertCircle, BookOpen, Bot, CircleCheckBig, Clock, FileText, Globe, Loader2, type LucideIcon, MessageCircleQuestion, Pause, Plug, Workflow } from "lucide-react";

import { cn } from "@/utils/cnTailwind";

import { Button } from "@/ui/button";

import type { ComponentGroup, GroupStatus } from "./canvasGroups";

interface CanvasTabsProps {
    groups: ComponentGroup[];
    activeId: string;
    onActiveChange: (id: string) => void;
    statusFor: (group: ComponentGroup) => GroupStatus;
}

function GroupIcon({ type }: { type: ComponentGroup["type"] }) {
    switch (type) {
        case "workflow":
            return <Workflow className="h-3.5 w-3.5" />;
        case "agent":
            return <Bot className="h-3.5 w-3.5" />;
        case "gateway":
            return <Globe className="h-3.5 w-3.5" />;
        case "connector":
            return <Plug className="h-3.5 w-3.5" />;
        case "a2a_agent":
            return <Globe className="h-3.5 w-3.5" />;
        case "skill":
            return <BookOpen className="h-3.5 w-3.5" />;
    }
}

const GROUP_STATUS: Record<GroupStatus, { label: string; Icon: LucideIcon; className: string }> = {
    creating: { label: "Creating", Icon: Loader2, className: "animate-spin text-(--brand-wMain)" },
    ready: { label: "Ready", Icon: CircleCheckBig, className: "text-(--success-wMain)" },
    waiting: { label: "Waiting for input", Icon: MessageCircleQuestion, className: "text-(--warning-wMain)" },
    error: { label: "Error", Icon: AlertCircle, className: "text-(--error-wMain)" },
    paused: { label: "Paused", Icon: Pause, className: "text-(--warning-wMain)" },
    draft: { label: "Draft", Icon: FileText, className: "text-(--secondary-text-wMain)" },
    queued: { label: "Queued", Icon: Clock, className: "text-(--secondary-text-wMain)" },
};

function StatusDot({ status }: { status: GroupStatus }) {
    const { Icon, className } = GROUP_STATUS[status];
    return <Icon data-slot="status-icon" className={cn("h-3.5 w-3.5", className)} />;
}

function GroupTab({ group, isActive, status, onSelect }: Readonly<{ group: ComponentGroup; isActive: boolean; status: GroupStatus; onSelect: (id: string) => void }>) {
    return (
        <TabsPrimitive.Trigger value={group.id} asChild>
            <Button
                variant="ghost"
                size="sm"
                className={cn(
                    "relative flex h-auto flex-shrink-0 items-center gap-2 rounded-none border-b-2 px-3 py-2 text-sm transition-colors",
                    isActive ? "border-(--brand-wMain) font-semibold text-(--primary-text-wMain)" : "border-transparent text-(--secondary-text-wMain) hover:text-(--primary-text-wMain)"
                )}
                aria-label={`${group.label}, ${GROUP_STATUS[status].label}`}
                aria-controls={undefined}
                onClick={() => onSelect(group.id)}
                title={group.label}
            >
                <StatusDot status={status} />
                <GroupIcon type={group.type} />
                <span className="max-w-[180px] truncate">{group.label}</span>
            </Button>
        </TabsPrimitive.Trigger>
    );
}

export function CanvasTabs({ groups, activeId, onActiveChange, statusFor }: CanvasTabsProps) {
    return (
        <TabsPrimitive.Root value={activeId} activationMode="manual" className="flex-shrink-0">
            <TabsPrimitive.List aria-label="Component groups" className="flex items-center gap-1 overflow-x-auto overflow-y-hidden border-b border-(--secondary-w30) bg-(--background-w20) px-4" style={{ minHeight: 40, scrollbarWidth: "thin" }}>
                {groups.map(g => (
                    <GroupTab key={g.id} group={g} isActive={g.id === activeId} status={statusFor(g)} onSelect={onActiveChange} />
                ))}
            </TabsPrimitive.List>
        </TabsPrimitive.Root>
    );
}
