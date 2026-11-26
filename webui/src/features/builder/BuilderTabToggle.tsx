// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { FlaskConical, Hammer } from "lucide-react";

import { cn } from "@/utils/cnTailwind";

import { Tabs, TabsList, TabsTrigger } from "@/ui/tabs";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/ui/tooltip";

import type { BuilderActiveTab } from "./useBuilderPageState";

interface BuilderTabToggleProps {
    builderActiveTab: BuilderActiveTab;
    canTest: boolean;
    hasTestTarget: boolean;
    isCompact: boolean;
    onRequestTest: () => void;
    onRequestBuild: () => void;
}

export function BuilderTabToggle({ builderActiveTab, canTest, hasTestTarget, isCompact, onRequestTest, onRequestBuild }: Readonly<BuilderTabToggleProps>) {
    const testDisabledReason = hasTestTarget ? "Build and activate this component first to test it" : "Build and activate components first to test them";
    return (
        <Tabs
            value={builderActiveTab}
            onValueChange={value => {
                const next = value as BuilderActiveTab;
                if (next === builderActiveTab) return;
                if (next === "test") {
                    onRequestTest();
                } else {
                    onRequestBuild();
                }
            }}
        >
            <TabsList className="pointer-events-auto flex items-center overflow-hidden rounded-lg border bg-transparent p-0">
                <TabsTrigger value="build" className="cursor-pointer rounded-none border-0 border-r px-4 py-1.5 text-sm hover:bg-(--secondary-w20) data-[state=active]:bg-(--primary-w20)! data-[state=active]:shadow-none">
                    <Hammer className={cn("h-4 w-4 shrink-0", !isCompact && "mr-1.5")} />
                    {!isCompact && "Build"}
                </TabsTrigger>
                <Tooltip>
                    <TooltipTrigger asChild>
                        <span tabIndex={canTest ? undefined : 0} className={cn("inline-flex", !canTest && "cursor-not-allowed")}>
                            <TabsTrigger
                                value="test"
                                disabled={!canTest}
                                className="cursor-pointer rounded-none border-0 px-4 py-1.5 text-sm hover:bg-(--secondary-w20) disabled:cursor-not-allowed disabled:opacity-50 data-[state=active]:bg-(--primary-w20)! data-[state=active]:shadow-none"
                            >
                                <FlaskConical className={cn("h-4 w-4 shrink-0", !isCompact && "mr-1.5")} />
                                {!isCompact && "Test"}
                            </TabsTrigger>
                        </span>
                    </TooltipTrigger>
                    {!canTest && <TooltipContent>{testDisabledReason}</TooltipContent>}
                </Tooltip>
            </TabsList>
        </Tabs>
    );
}
