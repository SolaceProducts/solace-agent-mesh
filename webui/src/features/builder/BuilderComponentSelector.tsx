// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Bot, ChevronDown, Plug, Workflow } from "lucide-react";

import { cn } from "@/utils/cnTailwind";

import { Button } from "@/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger } from "@/ui/dropdown-menu";

import type { BuildManifest } from "./hooks/useBuilderManifest";

interface BuilderComponentSelectorProps {
    manifest: BuildManifest;
    activeComponentName?: string;
    onComponentSelect?: (componentName: string) => Promise<void>;
}

export function BuilderComponentSelector({ manifest, activeComponentName, onComponentSelect = async () => {} }: BuilderComponentSelectorProps) {
    const components = manifest.components ?? [];
    if (components.length === 0) return null;

    const workflows = components.filter(c => c.type === "workflow");
    const connectors = components.filter(c => c.type === "connector");
    const agents = components.filter(c => c.type !== "workflow" && c.type !== "connector");

    return (
        <DropdownMenu>
            <DropdownMenuTrigger asChild>
                <Button variant="outline" size="sm" className="h-auto gap-1.5 px-2.5 py-1.5 text-xs font-medium">
                    <span className="max-w-[180px] truncate">{activeComponentName || "Components"}</span>
                    <ChevronDown className="h-3 w-3 shrink-0" />
                </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-56">
                {workflows.length > 0 && (
                    <>
                        <DropdownMenuLabel className="text-[10px] tracking-wide uppercase">Workflows</DropdownMenuLabel>
                        {workflows.map(comp => (
                            <ComponentMenuItem key={comp.filename || comp.name} name={comp.name} type="workflow" status={comp.status} origin={comp.origin} onSelect={() => onComponentSelect(comp.name)} />
                        ))}
                    </>
                )}
                {workflows.length > 0 && (agents.length > 0 || connectors.length > 0) && <DropdownMenuSeparator />}
                {agents.length > 0 && (
                    <>
                        <DropdownMenuLabel className="text-[10px] tracking-wide uppercase">Agents</DropdownMenuLabel>
                        {agents.map(comp => (
                            <ComponentMenuItem key={comp.filename || comp.name} name={comp.name} type="agent" status={comp.status} origin={comp.origin} onSelect={() => onComponentSelect(comp.name)} />
                        ))}
                    </>
                )}
                {connectors.length > 0 && (workflows.length > 0 || agents.length > 0) && <DropdownMenuSeparator />}
                {connectors.length > 0 && (
                    <>
                        <DropdownMenuLabel className="text-[10px] tracking-wide uppercase">Connectors</DropdownMenuLabel>
                        {connectors.map(comp => (
                            <ComponentMenuItem key={comp.filename || comp.name} name={comp.name} type="connector" status={comp.status} origin={comp.origin} onSelect={() => onComponentSelect(comp.name)} />
                        ))}
                    </>
                )}
            </DropdownMenuContent>
        </DropdownMenu>
    );
}

const STATUS_BADGE_CLASS = new Map([
    ["validated", "bg-(--success-w10) text-(--success-wMain)"],
    ["deployed", "bg-(--info-w10) text-(--info-wMain)"],
    ["error", "bg-(--error-w10) text-(--error-wMain)"],
]);

function ComponentMenuItem({ name, type, status, origin, onSelect }: { name: string; type: string; status?: string; origin?: string; onSelect: () => void }) {
    const Icon = type === "workflow" ? Workflow : type === "connector" ? Plug : Bot;
    const displayStatus = origin === "platform" ? "deployed" : status;

    return (
        <DropdownMenuItem onClick={onSelect} className="flex cursor-pointer items-center gap-2">
            <Icon className="h-3.5 w-3.5 shrink-0" />
            <span className="min-w-0 flex-1 truncate text-xs">{name}</span>
            {displayStatus && <span className={cn("shrink-0 rounded px-1 py-0.5 text-[9px] font-medium", STATUS_BADGE_CLASS.get(displayStatus))}>{displayStatus}</span>}
        </DropdownMenuItem>
    );
}
