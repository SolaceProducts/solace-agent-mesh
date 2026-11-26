// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { CircleCheckBig, Play, Rocket, Wrench } from "lucide-react";

import { cn } from "@/utils/cnTailwind";

import { Button } from "@/ui/button";

import type { PlatformCheckView } from "./platformCheck";
import { PlatformCheckStatus } from "./PlatformCheckStatus";

export { APPLY_COMPLETE_ARTIFACT_FILENAME } from "@/features/builder/builderArtifacts";

export interface ApplyComplete {
    summary: string;
    changesApplied: ChangeApplied[];
}

export interface ChangeApplied {
    component: string;
    area: "system_prompt" | "tools" | "workflow_logic" | "model_choice" | "other";
    change: string;
}

const AREA_LABELS: Record<ChangeApplied["area"], string> = {
    system_prompt: "System prompt",
    tools: "Tools",
    workflow_logic: "Workflow logic",
    model_choice: "Model choice",
    other: "Other",
};

interface Props {
    applyComplete: ApplyComplete;
    onSwitchToTest: () => void;
    ctaMode?: "deploy" | "test";
    onBuildActivate?: () => void;
    isBuildActivating?: boolean;
    canBuildActivate?: boolean;
    platformCheck?: PlatformCheckView;
    onCheckAgain?: () => void;
    onAskBuilder?: () => void;
}

export function EditCompleteCard({ applyComplete, onSwitchToTest, ctaMode = "test", onBuildActivate, isBuildActivating = false, canBuildActivate = true, platformCheck, onCheckAgain, onAskBuilder }: Readonly<Props>) {
    const hasChanges = applyComplete.changesApplied.length > 0;
    const showDeploy = ctaMode === "deploy" && !!onBuildActivate;
    return (
        <div className="mt-3 flex flex-col gap-3 rounded-md border bg-(--background-w10) p-4">
            <header className="flex items-start gap-3">
                <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-(--success-w10)">
                    <CircleCheckBig className="h-4 w-4 text-(--success-wMain)" />
                </div>
                <div className="min-w-0 flex-1">
                    <h3 className="text-sm font-semibold">Changes are completed! Here is the summary:</h3>
                    <p className="mt-2 text-sm leading-relaxed">{applyComplete.summary}</p>
                </div>
            </header>

            {hasChanges && (
                <ul className="flex max-h-72 flex-col gap-1.5 overflow-y-auto overscroll-contain pr-1 pl-1">
                    {applyComplete.changesApplied.map((c, i) => (
                        <ChangeRow key={i} change={c} />
                    ))}
                </ul>
            )}

            {showDeploy ? (
                <>
                    <PlatformCheckStatus view={platformCheck} onCheckAgain={onCheckAgain} onAskBuilder={onAskBuilder} className="px-0 pt-0" />
                    <div className="flex items-center justify-between gap-3">
                        <p className="text-xs">These changes are not live yet — activate them before testing.</p>
                        <Button size="sm" onClick={onBuildActivate} disabled={isBuildActivating || !canBuildActivate} className="shrink-0 gap-1.5">
                            <Rocket className="h-3.5 w-3.5" />
                            {isBuildActivating ? "Activating…" : <>Build &amp; Activate</>}
                        </Button>
                    </div>
                </>
            ) : (
                <div className="flex justify-end">
                    <Button size="sm" onClick={onSwitchToTest} className="gap-1.5">
                        <Play className="h-3.5 w-3.5" />
                        Switch to Test Mode
                    </Button>
                </div>
            )}
        </div>
    );
}

function ChangeRow({ change }: { change: ChangeApplied }) {
    return (
        <li className={cn("flex items-start gap-2 rounded-md border bg-(--background-w20) px-3 py-2 text-xs")}>
            <Wrench className="mt-0.5 h-3.5 w-3.5 shrink-0" />
            <div className="min-w-0 flex-1">
                <div className="flex items-center gap-1.5">
                    <span className="font-medium">{change.component}</span>
                    <span className="rounded bg-(--background-w10) px-1.5 py-0.5 text-[10px]">{AREA_LABELS[change.area] ?? change.area}</span>
                </div>
                <p className="mt-0.5 leading-relaxed">{change.change}</p>
            </div>
        </li>
    );
}
