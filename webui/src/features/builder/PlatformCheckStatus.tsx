// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { CircleCheckBig, Loader2 } from "lucide-react";

import { cn } from "@/utils/cnTailwind";

import { Button } from "@/ui/button";

import { MessageBanner } from "@/common";

import type { PlatformCheckView, PlatformProblem } from "./platformCheck";

interface PlatformCheckStatusProps {
    view?: PlatformCheckView;
    onCheckAgain?: () => void;
    onAskBuilder?: () => void;
    className?: string;
}

function problemLine({ kind, name, message }: PlatformProblem): string {
    const subject = [kind, name].filter(Boolean).join(" ");
    return subject ? `${subject}: ${message}` : message;
}

function problemKey({ kind, name, message }: PlatformProblem): string {
    return `${kind}|${name}|${message}`;
}

function ProblemList({ problems }: Readonly<{ problems: PlatformProblem[] }>) {
    return (
        <ul className="list-disc pl-4">
            {problems.map(problem => (
                <li key={problemKey(problem)}>{problemLine(problem)}</li>
            ))}
        </ul>
    );
}

function CheckAgainButton({ onClick, disabled, children }: Readonly<{ onClick?: () => void; disabled: boolean; children: string }>) {
    return (
        <Button size="sm" variant="outline" onClick={onClick} disabled={disabled}>
            {children}
        </Button>
    );
}

function CheckBody({ view, onCheckAgain, onAskBuilder }: Readonly<{ view: PlatformCheckView } & Pick<PlatformCheckStatusProps, "onCheckAgain" | "onAskBuilder">>) {
    const { check, canAskBuilder, turnRunning = false } = view;
    switch (check.kind) {
        case "checking":
            return (
                <div className="flex items-center gap-1.5 text-xs text-(--secondary-text-wMain)">
                    <Loader2 aria-hidden="true" className="h-3.5 w-3.5 animate-spin" />
                    Checking with the platform…
                </div>
            );
        case "passed":
            return (
                <div className="flex flex-col gap-1 text-xs">
                    <span className="inline-flex items-center gap-1.5 font-medium text-(--primary-text-wMain)">
                        <CircleCheckBig aria-hidden="true" className="h-3.5 w-3.5 text-(--success-wMain)" />
                        Ready
                    </span>
                    {check.warnings.length > 0 && (
                        <ul className="list-disc pl-8 text-(--secondary-text-wMain)">
                            {check.warnings.map(warning => (
                                <li key={warning}>{warning}</li>
                            ))}
                        </ul>
                    )}
                </div>
            );
        case "refused":
            return (
                <>
                    <MessageBanner variant="error" message={<ProblemList problems={[...check.problems, ...check.forbidden]} />} />
                    <div className="flex flex-wrap gap-2">
                        <CheckAgainButton onClick={onCheckAgain} disabled={turnRunning}>
                            Check again
                        </CheckAgainButton>
                        {canAskBuilder && (
                            <Button size="sm" onClick={onAskBuilder} disabled={turnRunning}>
                                Ask the Builder to fix these
                            </Button>
                        )}
                    </div>
                </>
            );
        case "notAllowed":
            return (
                <>
                    <MessageBanner variant="warning" message={<ProblemList problems={check.problems} />} />
                    <div>
                        <CheckAgainButton onClick={onCheckAgain} disabled={turnRunning}>
                            Check again
                        </CheckAgainButton>
                    </div>
                </>
            );
        case "unavailable":
            return (
                <>
                    <MessageBanner
                        variant="warning"
                        message={
                            <div className="flex flex-col">
                                <span>{"Couldn't check with the platform"}</span>
                                <span className="font-normal">{check.message}</span>
                            </div>
                        }
                    />
                    <div>
                        <CheckAgainButton onClick={onCheckAgain} disabled={turnRunning}>
                            Retry
                        </CheckAgainButton>
                    </div>
                </>
            );
    }
}

export function PlatformCheckStatus({ view, onCheckAgain, onAskBuilder, className }: Readonly<PlatformCheckStatusProps>) {
    if (!view || view.check.kind === "idle") return null;
    return (
        <output aria-live="polite" className={cn("flex flex-col gap-2 px-4 pt-2.5", className)}>
            <CheckBody view={view} onCheckAgain={onCheckAgain} onAskBuilder={onAskBuilder} />
        </output>
    );
}
