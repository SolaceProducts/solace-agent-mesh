// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useState } from "react";

import { useActivation, useActivationTerms, useAgreeToFreeModelTerms, useRefreshFreeModel } from "@/api/activation";

import { MessageBanner } from "@/common/banners/MessageBanner";

import { getErrorMessage } from "@/utils/api";

import { useIsActivationGateEnabled } from "@/hooks";
import { Button, Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, Spinner } from "@/ui";

import { AddOwnModelButton } from "./AddOwnModelButton";
import { AgreementCheckbox } from "./AgreementCheckbox";
import { FREE_MODEL_TERMS_FALLBACK, TERMS_UNREACHABLE, termsLoadRetryable } from "./agreementText";
import { FreeModelSignInAgain } from "./FreeModelSignInAgain";

const AGREED_MESSAGE = "Free model terms accepted. Send the message again.";
const AGREED_BUT_PAUSED_MESSAGE = "Free model terms accepted, but the free model is paused. Add a model to replace it.";
const SIGNED_IN_AGAIN_MESSAGE = "Signed in again. Send the message again.";
const NOT_RECORDED_MESSAGE = "Solace Agent Mesh couldn't record your agreement. Try again.";

export function FreeModelTermsPrompt() {
    const gateEnabled = useIsActivationGateEnabled();
    const { data: activation } = useActivation(gateEnabled);
    const refresh = useRefreshFreeModel();
    const agree = useAgreeToFreeModelTerms();
    const [open, setOpen] = useState(false);
    const [ticked, setTicked] = useState(false);
    const [opened, setOpened] = useState<string[]>([]);

    const clickwrapId = activation?.agreements.eulaClickwrapId;
    const shown = gateEnabled && !!activation?.freeModelChosen && !!activation.account && !!clickwrapId;
    const terms = useActivationTerms(shown && open);
    const freeModelTerms = terms.isFetching ? undefined : terms.data;
    const agreedBefore = !!freeModelTerms?.agreed;
    const agreed = agreedBefore || agree.isSuccess;

    const { mutate: refreshMutate, isIdle: refreshIdle } = refresh;
    const refreshAccess = useCallback(() => refreshMutate(undefined, { onSuccess: result => (result.status === "available" || result.status === "paused") && setOpen(false) }), [refreshMutate]);
    const agreedBeforehand = open && agreedBefore;
    useEffect(() => {
        if (agreedBeforehand && refreshIdle) refreshAccess();
    }, [agreedBeforehand, refreshIdle, refreshAccess]);

    if (!shown) return null;

    const outcome = refresh.data?.status;
    const done = outcome === "available" || outcome === "paused";
    const openTerms = () => {
        refresh.reset();
        agree.reset();
        setTicked(false);
        setOpened([]);
        setOpen(true);
    };
    const openDocument = (documentId: string) => setOpened(previous => (previous.includes(documentId) ? previous : [...previous, documentId]));
    const loadFailed = terms.isError && !terms.isFetching;

    return (
        <div className="mt-2 flex flex-col items-start gap-2">
            {outcome === "paused" && (
                <>
                    <p className="text-sm text-(--secondary-text-wMain)">{AGREED_BUT_PAUSED_MESSAGE}</p>
                    <AddOwnModelButton variant="outline" />
                </>
            )}
            {outcome === "available" && <p className="text-sm text-(--secondary-text-wMain)">{AGREED_MESSAGE}</p>}
            {!done && (
                <Button variant="outline" onClick={openTerms}>
                    Review Terms
                </Button>
            )}
            <Dialog open={open} onOpenChange={setOpen}>
                <DialogContent className="sm:max-w-2xl">
                    <DialogHeader>
                        <DialogTitle>Free Model Terms</DialogTitle>
                        <DialogDescription>To keep using the free model, agree to its terms.</DialogDescription>
                    </DialogHeader>
                    <div className="flex flex-col gap-3">
                        {agreed ? (
                            <RefreshOutcome refreshing={refresh.isPending} outcome={refresh.isError ? "unavailable" : outcome} onRetryRefresh={refreshAccess} />
                        ) : (
                            <>
                                <AgreementCheckbox id="free-model-terms-again" terms={freeModelTerms} fallback={FREE_MODEL_TERMS_FALLBACK} checked={ticked} disabled={agree.isPending} onCheckedChange={setTicked} onOpen={openDocument} />
                                {loadFailed &&
                                    (termsLoadRetryable(terms.error) ? (
                                        <div className="flex flex-wrap items-center gap-2">
                                            <MessageBanner variant="error" message={TERMS_UNREACHABLE} />
                                            <Button variant="outline" onClick={() => terms.refetch()}>
                                                Try Again
                                            </Button>
                                        </div>
                                    ) : (
                                        <MessageBanner variant="error" message={getErrorMessage(terms.error, TERMS_UNREACHABLE)} />
                                    ))}
                                {agree.isError && <MessageBanner variant="error" message={getErrorMessage(agree.error, NOT_RECORDED_MESSAGE)} />}
                                <div className="flex justify-end">
                                    <Button disabled={!freeModelTerms || !ticked || agree.isPending} onClick={() => agree.mutate(opened, { onSuccess: refreshAccess })}>
                                        {agree.isPending && <Spinner />}
                                        Agree
                                    </Button>
                                </div>
                            </>
                        )}
                    </div>
                </DialogContent>
            </Dialog>
        </div>
    );
}

interface RefreshOutcomeProps {
    refreshing: boolean;
    outcome: string | undefined;
    onRetryRefresh: () => void;
}

function RefreshOutcome({ refreshing, outcome, onRetryRefresh }: Readonly<RefreshOutcomeProps>) {
    if (refreshing || outcome === undefined) {
        return (
            <output className="flex items-center gap-2 text-sm text-(--secondary-text-wMain)">
                <Spinner />
                Updating your free model access…
            </output>
        );
    }
    if (outcome === "reauthorization_required") {
        return (
            <>
                <MessageBanner variant="error" message="The Solace ID sign-in for the free model has expired. Sign in again." />
                <FreeModelSignInAgain variant="outline" signedInMessage={SIGNED_IN_AGAIN_MESSAGE} />
            </>
        );
    }
    return (
        <>
            <MessageBanner variant="error" message="Unable to reach Solace ID to update free model access. Please try again later." />
            <Button variant="outline" className="self-start" onClick={onRetryRefresh}>
                Try Again
            </Button>
        </>
    );
}
