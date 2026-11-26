// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Link } from "react-router-dom";

import { iconWrapperStyles, navButtonStyles } from "@/features/navigation/navigationStyles";

import { Tooltip, TooltipContent, TooltipTrigger, UsageRing } from "@/ui";
import { cn } from "@/utils";

import { AddOwnModelButton } from "./AddOwnModelButton";
import { type CreditsTone, useFreeCreditsSummary } from "./freeCredits";

const TONE_TEXT: Record<CreditsTone, string> = {
    ok: "text-(--darkSurface-textMuted)",
    low: "text-(--warning-w70)",
    out: "text-(--error-w70)",
};
const TONE_RING: Record<CreditsTone, string> = {
    ok: "text-(--darkSurface-brandMain)",
    low: "text-(--warning-w70)",
    out: "text-(--error-w70)",
};

const MODELS_PATH = "/models";

function CreditsRing({ used, tone }: Readonly<{ used: number; tone: CreditsTone }>) {
    return <UsageRing value={used} className={TONE_RING[tone]} trackClassName="stroke-(--darkSurface-border)" />;
}

export function FreeCreditsNavItem({ collapsed }: Readonly<{ collapsed: boolean }>) {
    const credits = useFreeCreditsSummary();
    if (!credits) return null;

    if (collapsed) {
        return (
            <Tooltip>
                <TooltipTrigger asChild>
                    <Link to={MODELS_PATH} aria-label={credits.label} className={cn(navButtonStyles(), "mb-3")}>
                        <span className={iconWrapperStyles()}>
                            <CreditsRing used={credits.used} tone={credits.tone} />
                        </span>
                    </Link>
                </TooltipTrigger>
                <TooltipContent side="right">{credits.label}</TooltipContent>
            </Tooltip>
        );
    }

    return (
        <div className="mb-3 flex flex-col border-b border-(--darkSurface-border) pb-3">
            <Link to={MODELS_PATH} title="View usage in Models" className={cn(navButtonStyles(), "gap-1.5")}>
                <CreditsRing used={credits.used} tone={credits.tone} />
                <span className={cn("truncate", TONE_TEXT[credits.tone])}>{credits.label}</span>
            </Link>
            {credits.tone === "out" && (
                <div className="flex px-4">
                    <AddOwnModelButton
                        variant="ghost"
                        size="sm"
                        className="px-2 text-sm font-bold text-(--darkSurface-buttonText) enabled:hover:bg-transparent enabled:hover:text-(--darkSurface-buttonTextHover) enabled:active:bg-transparent enabled:active:text-(--darkSurface-buttonTextHover)"
                    />
                </div>
            )}
        </div>
    );
}
