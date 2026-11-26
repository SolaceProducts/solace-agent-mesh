// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useRef } from "react";
import { Link, useSearchParams } from "react-router-dom";

import { useVerifyChannelCode } from "@/api/notifications";

import { Button, Spinner } from "@/ui";

const CHANNEL_LABELS: Record<string, string> = {
    slack: "Slack",
    teams: "Microsoft Teams",
};

const HTTP_BAD_REQUEST = 400;

const CODE_REJECTED_MESSAGE = "This code is invalid or has expired. Open Settings → Notifications in SAM and turn the channel on.";
const NEW_CODE_HINT = "To turn the channel on, open Settings → Notifications in SAM.";

const PERMANENT_FAILURES: Readonly<Record<number, OutcomeDescription>> = {
    401: { title: "Sign in required", body: "Sign in to SAM, then open the link again." },
    403: { title: "Can't confirm this channel", body: "You don't have permission to confirm notification channels." },
};

function channelLabel(channelType: string): string {
    return CHANNEL_LABELS[channelType] ?? channelType;
}

export function NotificationChannelConfirmPage() {
    const [params] = useSearchParams();
    const channelType = params.get("channel") ?? "";
    const code = params.get("code") ?? "";
    const verify = useVerifyChannelCode();
    const { mutate } = verify;
    const fired = useRef(false);

    useEffect(() => {
        if (fired.current || !channelType || !code) {
            return;
        }
        fired.current = true;
        mutate({ channelType, code });
    }, [channelType, code, mutate]);

    const retry = () => {
        fired.current = true;
        mutate({ channelType, code });
    };

    const outcome = describeOutcome(channelType, !channelType || !code, verify);

    return (
        <div className="flex min-h-full items-center justify-center p-6">
            <div className="flex max-w-md flex-col items-center gap-4 text-center">
                {outcome ? (
                    <Outcome title={outcome.title} body={outcome.body} hint={outcome.hint} onRetry={outcome.retryable ? retry : undefined} />
                ) : (
                    <>
                        <Spinner variant="muted" />
                        <p className="text-sm text-(--secondary-text-wMain)">Confirming {channelLabel(channelType)}…</p>
                    </>
                )}
            </div>
        </div>
    );
}

type OutcomeProps = Readonly<{ title: string; body: string; hint?: string; onRetry?: () => void }>;

type OutcomeDescription = Readonly<{ title: string; body: string; hint?: string; retryable?: boolean }>;

function describeOutcome(channelType: string, missingParams: boolean, verify: ReturnType<typeof useVerifyChannelCode>): OutcomeDescription | null {
    if (missingParams) {
        return { title: "Invalid confirmation link", body: "This link is missing its channel or code. Open Settings → Notifications in SAM and turn the channel on." };
    }
    if (verify.isSuccess) {
        return { title: `${channelLabel(channelType)} confirmed`, body: `You'll now receive SAM notifications on ${channelLabel(channelType)}.` };
    }
    if (!verify.isError) {
        return null;
    }
    const status = (verify.error as { status?: number } | null)?.status;
    if (status === HTTP_BAD_REQUEST) {
        const reason = verify.error?.message;
        return reason ? { title: "Confirmation failed", body: reason, hint: NEW_CODE_HINT } : { title: "Confirmation failed", body: CODE_REJECTED_MESSAGE };
    }
    const permanent = status === undefined ? undefined : PERMANENT_FAILURES[status];
    return permanent ?? { title: "Couldn't confirm right now.", body: "Something went wrong while confirming. Try again.", retryable: true };
}

function Outcome({ title, body, hint, onRetry }: OutcomeProps) {
    return (
        <>
            <h1 className="text-lg font-semibold text-(--primary-text-wMain)">{title}</h1>
            <p className="text-sm text-(--secondary-text-wMain)">{body}</p>
            {hint && <p className="text-sm text-(--secondary-text-wMain)">{hint}</p>}
            {onRetry && (
                <Button variant="outline" size="sm" onClick={onRetry}>
                    Retry
                </Button>
            )}
            <Link to="/chat" className="text-sm font-semibold text-(--primary-wMain) hover:underline">
                Back to SAM
            </Link>
        </>
    );
}
