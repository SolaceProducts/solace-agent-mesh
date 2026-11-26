// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode, useEffect, useEffectEvent, useRef, useState } from "react";
import { ArrowLeft, Sparkles } from "lucide-react";

import { type Activation, type ClickwrapTerms, useAcceptActivation, useActivationTerms, useModelsConfigured } from "@/api/activation";

import { MessageBanner } from "@/common/banners/MessageBanner";
import { flushReports, report } from "@/common/product-analytics/report";

import { getErrorMessage } from "@/utils/api";

import { Button, Spinner } from "@/ui";

import { setupPath } from "./activationAnalytics";
import { AgentMeshBrand } from "./AgentMeshBrand";
import { AgreementCheckbox } from "./AgreementCheckbox";
import { EULA_FALLBACK, TERMS_UNREACHABLE, termsLoadRetryable } from "./agreementText";
import { PoweredByClaude } from "./PoweredByClaude";

const NO_AGREEMENT_LINKED = "This build of Solace Agent Mesh has no license agreement linked, so it can't be set up.";
const NOT_RECORDED = "Solace Agent Mesh couldn't record your agreement. Try again.";

interface OnboardingDialogProps {
    activation: Activation;
    onOwnModelChosen: () => void;
    onOwnModelChoiceFailed: () => void;
}

export function OnboardingDialog({ activation, onOwnModelChosen, onOwnModelChoiceFailed }: Readonly<OnboardingDialogProps>) {
    const accept = useAcceptActivation();
    const modelsConfigured = useModelsConfigured();
    const hasOwnModels = modelsConfigured.data ?? false;
    const { account, agreements, freeModelAvailable } = activation;
    const linked = !!agreements.eulaClickwrapId;
    const terms = useActivationTerms(!!account && linked);
    const [ticked, setTicked] = useState(false);
    const [opened, setOpened] = useState<string[]>([]);
    const [confirming, setConfirming] = useState(false);
    const [replaceConfirmed, setReplaceConfirmed] = useState(false);
    const agreeing = useRef(false);

    const agreement = terms.data;
    const agreed = ticked || !!agreement?.agreed;
    const ready = !!agreement && !terms.isFetching && modelsConfigured.isSuccess;
    const writing = accept.isPending || accept.isSuccess;

    const open = (documentId: string) => setOpened(previous => (previous.includes(documentId) ? previous : [...previous, documentId]));
    const termsShown = !!agreement;
    const reportTermsShown = useEffectEvent(() => report("activation:terms_shown", { alreadyAgreed: !!agreement?.agreed }));
    useEffect(() => {
        if (termsShown) reportTermsShown();
    }, [termsShown]);
    const termsFailed = terms.isError && !terms.isFetching;
    useEffect(() => {
        if (termsFailed) report("activation:terms_error");
    }, [termsFailed]);

    const agree = async (freeModel: boolean) => {
        if (agreeing.current) return;
        agreeing.current = true;
        const reported = { setupPath: setupPath(freeModel) };
        report("activation:setup_chosen", reported);
        await flushReports();
        if (!freeModel && !hasOwnModels) onOwnModelChosen();
        try {
            await accept.mutateAsync({ freeModel, openedDocumentIds: opened });
            report("activation:accepted", reported);
        } catch {
            agreeing.current = false;
            report("activation:accept_failed", reported);
            if (!freeModel) onOwnModelChoiceFailed();
        }
    };
    const getStarted = () => {
        if (hasOwnModels && !replaceConfirmed) {
            setConfirming(true);
            return;
        }
        agree(true);
    };
    const confirmReplace = () => {
        report("activation:replace_confirmed");
        setReplaceConfirmed(true);
        setConfirming(false);
        agree(true);
    };

    let content: ReactNode;
    if (writing) {
        content = <Busy titleId="activation-onboarding-title">Setting things up…</Busy>;
    } else if (confirming) {
        content = <ConfirmReplaceStep onBack={() => setConfirming(false)} onConfirm={confirmReplace} />;
    } else {
        content = (
            <WelcomeStep
                freeModelAvailable={freeModelAvailable}
                showAgreement={linked && !agreement?.agreed}
                agreement={agreement}
                agreed={agreed}
                ready={ready}
                hasOwnModels={hasOwnModels}
                problem={<OnboardingProblem linked={linked} accept={accept} terms={terms} modelsConfigured={modelsConfigured} />}
                onTick={setTicked}
                onOpen={open}
                onAddOwnModel={() => agree(false)}
                onGetStarted={getStarted}
            />
        );
    }

    return (
        <dialog open aria-modal="true" aria-labelledby="activation-onboarding-title" aria-busy={writing} className="relative flex max-h-none w-full max-w-none flex-col gap-6 rounded-lg bg-(--background-w10) p-6 text-inherit shadow-xl">
            <AgentMeshBrand />
            {content}
        </dialog>
    );
}

interface OnboardingProblemProps {
    linked: boolean;
    accept: ReturnType<typeof useAcceptActivation>;
    terms: ReturnType<typeof useActivationTerms>;
    modelsConfigured: ReturnType<typeof useModelsConfigured>;
}

function OnboardingProblem({ linked, accept, terms, modelsConfigured }: Readonly<OnboardingProblemProps>) {
    if (!linked) {
        return <MessageBanner variant="error" message={NO_AGREEMENT_LINKED} />;
    }
    if (accept.isError) {
        return <MessageBanner variant="error" message={getErrorMessage(accept.error, NOT_RECORDED)} />;
    }
    if (terms.isError && !terms.isFetching) {
        return termsLoadRetryable(terms.error) ? <RetryableError message={TERMS_UNREACHABLE} onRetry={() => terms.refetch()} /> : <MessageBanner variant="error" message={getErrorMessage(terms.error, NO_AGREEMENT_LINKED)} />;
    }
    if (modelsConfigured.isError) {
        return <RetryableError message="Solace Agent Mesh couldn't check your models." onRetry={() => modelsConfigured.refetch()} />;
    }
    return null;
}

interface WelcomeStepProps {
    freeModelAvailable: boolean;
    showAgreement: boolean;
    agreement: ClickwrapTerms | undefined;
    agreed: boolean;
    ready: boolean;
    hasOwnModels: boolean;
    problem: ReactNode;
    onTick: (checked: boolean) => void;
    onOpen: (documentId: string) => void;
    onAddOwnModel: () => void;
    onGetStarted: () => void;
}

function WelcomeStep({ freeModelAvailable, showAgreement, agreement, agreed, ready, hasOwnModels, problem, onTick, onOpen, onAddOwnModel, onGetStarted }: Readonly<WelcomeStepProps>) {
    return (
        <>
            <div className="flex flex-col gap-2">
                <h1 id="activation-onboarding-title" className="text-xl font-semibold text-(--primary-text-wMain)">
                    Welcome to Solace Agent Mesh
                </h1>
                <p className="text-sm text-(--secondary-text-wMain)">
                    {freeModelAvailable
                        ? "You're set up with free model credits, so you can start building agents, connecting them to your tools and data, and putting them to work."
                        : "Add your model, then start building agents, connecting them to your tools and data, and putting them to work."}
                </p>
            </div>
            {freeModelAvailable && (
                <div className="flex flex-wrap items-center gap-x-4 gap-y-2 rounded-lg bg-(--secondary-w10) p-4 text-sm text-(--primary-text-wMain)">
                    <p className="flex items-center gap-2 font-medium">
                        <Sparkles className="size-4 shrink-0" aria-hidden />
                        Your free model credits are ready.
                    </p>
                    <PoweredByClaude className="ml-auto" />
                </div>
            )}
            {showAgreement && (
                <fieldset className="flex flex-col gap-3" aria-busy={!agreement}>
                    <legend className="sr-only">License agreement</legend>
                    <AgreementCheckbox id="activation-agreement" terms={agreement} fallback={EULA_FALLBACK} checked={agreed} disabled={!ready} onCheckedChange={onTick} onOpen={onOpen} />
                </fieldset>
            )}
            {problem}
            <div className={freeModelAvailable ? "flex items-center justify-between gap-4" : "flex items-center justify-end gap-4"}>
                <Button variant={freeModelAvailable ? "link" : undefined} className={freeModelAvailable ? "px-0" : undefined} disabled={!ready || !agreed} onClick={onAddOwnModel}>
                    {hasOwnModels ? "Keep Using My Models" : "Add My Own Model"}
                </Button>
                {freeModelAvailable && (
                    <Button disabled={!ready || !agreed} onClick={onGetStarted}>
                        Get Started
                    </Button>
                )}
            </div>
        </>
    );
}

function RetryableError({ message, onRetry }: Readonly<{ message: string; onRetry: () => void }>) {
    return (
        <div className="flex flex-wrap items-center gap-2">
            <MessageBanner variant="error" message={message} />
            <Button variant="outline" onClick={onRetry}>
                Try Again
            </Button>
        </div>
    );
}

function Busy({ titleId, children }: Readonly<{ titleId?: string; children: ReactNode }>) {
    return (
        <output id={titleId} className="flex items-center gap-2 text-sm text-(--secondary-text-wMain)">
            <Spinner />
            {children}
        </output>
    );
}

function ConfirmReplaceStep({ onBack, onConfirm }: Readonly<{ onBack: () => void; onConfirm: () => void }>) {
    return (
        <>
            <div className="flex flex-col gap-2">
                <h1 id="activation-onboarding-title" className="text-xl font-semibold text-(--primary-text-wMain)">
                    Replace your models with the free model?
                </h1>
                <p className="text-sm text-(--secondary-text-wMain)">Your general and planning models will be replaced by the free model, including their API keys. You can add your own models back later in Models.</p>
            </div>
            <div className="flex items-center justify-between gap-4">
                <Button variant="ghost" onClick={onBack}>
                    <ArrowLeft />
                    Back
                </Button>
                <Button onClick={onConfirm}>Replace and Get Started</Button>
            </div>
        </>
    );
}
