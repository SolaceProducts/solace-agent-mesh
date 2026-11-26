// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useRef, useState } from "react";

import type { BuildActivateApi } from "../BuilderActions";
import { IDLE_PLATFORM_CHECK, type PlatformCheck, type PlatformCheckView } from "../platformCheck";

export type BuildActivateFailure = { message: string; canRetry: boolean; declined?: false } | { message: string; canRetry: true; declined: true };

export interface BuildActivateBridge {
    buildActivate: { can: boolean; busy: boolean };
    platformCheck: PlatformCheckView;
    handleCheckAgain: () => void;
    handleAskBuilder: () => void;
    deployFailure: BuildActivateFailure | null;
    handleBuildActivateApiChange: (api: BuildActivateApi | null) => void;
    handleBuildActivate: () => void;
    handleRedeploy: () => void;
}

function failureFromApi(api: BuildActivateApi | null): BuildActivateFailure | null {
    const failure = api?.failure;
    if (!failure || failure.kind === "none") return null;
    if (failure.kind === "declined") return { message: failure.message, canRetry: true, declined: true };
    return { message: failure.message, canRetry: failure.kind === "retryable" };
}

function sameCheck(a: PlatformCheck, b: PlatformCheck): boolean {
    return a === b || JSON.stringify(a) === JSON.stringify(b);
}

export function useBuildActivateBridge(): BuildActivateBridge {
    const runRef = useRef<(() => Promise<void>) | null>(null);
    const retryRef = useRef<(() => void) | null>(null);
    const checkAgainRef = useRef<(() => void) | null>(null);
    const askBuilderRef = useRef<(() => void) | null>(null);
    const [buildActivate, setBuildActivate] = useState({ can: false, busy: false });
    const [platformCheck, setPlatformCheck] = useState<PlatformCheckView>({ check: IDLE_PLATFORM_CHECK, canAskBuilder: false, turnRunning: false });
    const [deployFailure, setDeployFailure] = useState<BuildActivateFailure | null>(null);

    const handleBuildActivateApiChange = useCallback((api: BuildActivateApi | null) => {
        runRef.current = api?.run ?? null;
        retryRef.current = api?.retry ?? null;
        checkAgainRef.current = api?.checkAgain ?? null;
        askBuilderRef.current = api?.askBuilder ?? null;
        const can = api?.canBuildActivate ?? false;
        const busy = api?.isBusy ?? false;
        setBuildActivate(prev => (prev.can === can && prev.busy === busy ? prev : { can, busy }));
        const check = api?.platformCheck ?? IDLE_PLATFORM_CHECK;
        const canAskBuilder = api?.canAskBuilder ?? false;
        const turnRunning = api?.turnRunning ?? false;
        setPlatformCheck(prev => (prev.canAskBuilder === canAskBuilder && prev.turnRunning === turnRunning && sameCheck(prev.check, check) ? prev : { check, canAskBuilder, turnRunning }));
        const next = failureFromApi(api);
        setDeployFailure(prev => (prev?.message === next?.message && prev?.canRetry === next?.canRetry && prev?.declined === next?.declined ? prev : next));
    }, []);

    const handleBuildActivate = useCallback(() => {
        runRef.current?.().catch(() => {});
    }, []);

    const handleRedeploy = useCallback(() => {
        retryRef.current?.();
    }, []);

    const handleCheckAgain = useCallback(() => {
        checkAgainRef.current?.();
    }, []);

    const handleAskBuilder = useCallback(() => {
        askBuilderRef.current?.();
    }, []);

    return { buildActivate, platformCheck, deployFailure, handleBuildActivateApiChange, handleBuildActivate, handleRedeploy, handleCheckAgain, handleAskBuilder };
}
