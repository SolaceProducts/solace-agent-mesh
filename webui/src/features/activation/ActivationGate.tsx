// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode, useCallback, useEffect, useMemo, useState } from "react";

import { useActivation, useEnsureFreeModels } from "@/api/activation";

import { EmptyState } from "@/common";
import { useIsActivationGateEnabled } from "@/hooks";

import { useSignInAnalytics } from "./activationAnalytics";
import { ActivationBackdrop } from "./ActivationBackdrop";
import { ModelSetupRequestContext } from "./ModelSetupRequest";
import { OnboardingDialog } from "./OnboardingDialog";
import { SignInCard } from "./SignInCard";

const MODEL_SETUP_COVER_MS = 3_000;
const APP_LAYOUT_CEILING_MS = 15_000;

type ModelSetupStep = "none" | "requested" | "showing";

export function ActivationGate({ children }: Readonly<{ children: ReactNode }>) {
    const gateEnabled = useIsActivationGateEnabled();
    const { data: activation, isPending, refetch } = useActivation(gateEnabled);
    const [modelSetup, setModelSetup] = useState<ModelSetupStep>("none");
    const [appMounted, setAppMounted] = useState(false);
    const markAppShown = useCallback(() => setAppMounted(true), []);
    const requestModelSetup = useCallback(() => setModelSetup("requested"), []);
    const clearModelSetup = useCallback(() => setModelSetup("none"), []);
    const markModelSetupShown = useCallback(() => setModelSetup(step => (step === "requested" ? "showing" : step)), []);
    const modelSetupRequest = useMemo(() => ({ requested: modelSetup !== "none", shown: markModelSetupShown, appShown: markAppShown, clear: clearModelSetup }), [modelSetup, markModelSetupShown, markAppShown, clearModelSetup]);
    const coveringApp = modelSetup === "requested" && activation?.state === "active";
    useEnsureFreeModels(activation);
    useSignInAnalytics(activation);

    useEffect(() => {
        if (!coveringApp) return;
        const lift = setTimeout(markModelSetupShown, appMounted ? MODEL_SETUP_COVER_MS : APP_LAYOUT_CEILING_MS);
        return () => clearTimeout(lift);
    }, [coveringApp, appMounted, markModelSetupShown]);

    if (!gateEnabled) {
        return <ModelSetupRequestContext value={modelSetupRequest}>{children}</ModelSetupRequestContext>;
    }
    if (isPending) {
        return (
            <div className="flex h-screen w-screen items-center justify-center">
                <EmptyState variant="loading" title="Loading..." />
            </div>
        );
    }
    if (!activation) {
        return (
            <div className="flex h-screen w-screen items-center justify-center">
                <EmptyState variant="error" title="Solace Agent Mesh could not start" subtitle="Check that it is running, then try again." buttons={[{ text: "Try Again", variant: "default", onClick: () => refetch() }]} />
            </div>
        );
    }
    const appShowing = activation.state === "active";
    if (appShowing || activation.state === "eula_pending") {
        return (
            <ModelSetupRequestContext value={modelSetupRequest}>
                {appShowing && children}
                {(!appShowing || coveringApp) && (
                    <div data-testid={coveringApp ? "activation-cover" : undefined} className="fixed inset-0 z-50">
                        <ActivationBackdrop wide>
                            <OnboardingDialog activation={activation} onOwnModelChosen={requestModelSetup} onOwnModelChoiceFailed={clearModelSetup} />
                        </ActivationBackdrop>
                    </div>
                )}
            </ModelSetupRequestContext>
        );
    }
    return (
        <ActivationBackdrop>
            <SignInCard activation={activation} />
        </ActivationBackdrop>
    );
}
