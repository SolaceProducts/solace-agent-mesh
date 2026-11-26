// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type FreeModelStatus, useFreeModelStatus } from "@/api/activation";

import { Button } from "@/ui";
import { cn } from "@/utils";

import { AddOwnModelButton } from "./AddOwnModelButton";
import { type CreditsTone, creditsTone, percentUsed, useReportCreditsTone } from "./freeCredits";
import { FreeModelSignInAgain } from "./FreeModelSignInAgain";
import { useFreeModel } from "./useFreeModel";

const CONTACT_SOLACE_URL = "https://solace.com/contact/";

const ON_FREE_CREDITS = "The general and planning models run on free credits that come with the Solace ID. Add a model to replace them anytime.";

interface BannerContent {
    title: string;
    description: string;
    tone: CreditsTone;
    used?: number;
    signInAgain?: boolean;
}

function bannerContent({ status, remainingPercent }: FreeModelStatus): BannerContent {
    if (status === "reauthorization_required") {
        return { title: "Sign in again to keep using free credits", description: "The Solace ID sign-in for the free model has expired.", tone: "ok", signInAgain: true };
    }
    if (status === "paused") {
        return { title: "The free model is paused", description: "To continue chatting and running agents, add a model to replace the general and planning models.", tone: "out" };
    }
    if (status !== "available") {
        return { title: "Free credits", description: "Unable to check the free credits. Please try again later.", tone: "ok" };
    }
    const used = percentUsed(remainingPercent);
    if (used === null) {
        return { title: "Free credits", description: ON_FREE_CREDITS, tone: "ok" };
    }
    switch (creditsTone(used)) {
        case "out":
            return { title: "No free credits left", description: "The general and planning models are paused. To continue chatting and running agents, add a model to replace them.", tone: "out" };
        case "low":
            return { title: `${used}% of free credits used`, description: "Most free credits are used. Add a model before the credits run out to keep agents working.", tone: "low", used };
        default:
            return { title: `${used}% of free credits used`, description: ON_FREE_CREDITS, tone: "ok", used };
    }
}

export function FreeCreditsBanner({ className }: Readonly<{ className?: string }>) {
    const { inUse } = useFreeModel();
    const { data: status } = useFreeModelStatus(inUse, { readOnOpen: true });
    const content = inUse && status ? bannerContent(status) : null;
    useReportCreditsTone(status?.status === "available" ? content?.tone : undefined);
    if (!content) return null;
    const out = content.tone === "out";
    return (
        <section aria-label="Free credits" data-testid="freeCreditsBanner" className={cn("flex flex-col gap-4 rounded-[4px] p-4 text-(--primary-text-wMain)", out ? "bg-(--error-w10)" : "bg-(--learning-w10)", className)}>
            <div className="flex flex-col gap-0.5">
                <div className="flex flex-wrap items-center gap-4">
                    <h2 className={cn("text-base font-medium", out && "text-(--error-w100)")}>{content.title}</h2>
                    {content.used !== undefined && <CreditsMeter used={content.used} tone={content.tone} />}
                </div>
                <p className="text-sm">{content.description}</p>
            </div>
            <div className="flex flex-wrap items-center justify-end gap-2">
                {out && (
                    <Button variant="ghost" asChild>
                        <a href={CONTACT_SOLACE_URL} target="_blank" rel="noopener noreferrer">
                            Contact Solace
                        </a>
                    </Button>
                )}
                {content.signInAgain && <FreeModelSignInAgain variant="ghost" showWaitingHint={false} signedInMessage="Signed in again." />}
                <AddOwnModelButton />
            </div>
        </section>
    );
}

function CreditsMeter({ used, tone }: Readonly<{ used: number; tone: CreditsTone }>) {
    return (
        <div className="h-2 w-50 overflow-hidden rounded-full bg-(--learning-w20)">
            <meter className="sr-only" min={0} max={100} value={used} aria-label="Free credits used" aria-valuetext={`${used}% used`}>
                {used}%
            </meter>
            <div aria-hidden className={cn("h-full rounded-full", tone === "low" ? "bg-(--warning-w100)" : "bg-(--brand-wMain)")} style={{ width: `${used}%` }} />
        </div>
    );
}
