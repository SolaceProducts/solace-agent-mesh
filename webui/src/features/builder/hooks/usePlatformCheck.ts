// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useRef, useState } from "react";

import type { ApplyBuildResponse, BuildComponentInput } from "@/api/builder/applyTypes";

import { getErrorMessage } from "@/utils/api";

import { classifyDryRun, classifyDryRunError, type PlatformCheck } from "../platformCheck";
import { type FetchConfigByFilename, toBuildComponentInputs } from "../serverApply";

import type { BuildManifest } from "./useBuilderManifest";

type ManifestComponent = NonNullable<BuildManifest["components"]>[number];

export type PlatformDryRun = (request: { buildName: string; components: BuildComponentInput[]; dryRun: true }, signal: AbortSignal) => Promise<ApplyBuildResponse>;

export interface UsePlatformCheckArgs {
    enabled: boolean;
    idle: boolean;
    buildName: string;
    sessionId: string;
    components: ManifestComponent[];
    fetchConfigByFilename: FetchConfigByFilename;
    applyBuild: PlatformDryRun;
}

export interface UsePlatformCheckResult {
    check: PlatformCheck;
    key: string | null;
    checkAgain: () => void;
}

export const UNAVAILABLE_RETRY_BASE_MS = 15_000;
export const UNAVAILABLE_RETRY_MAX_MS = 5 * 60_000;

const IDLE: PlatformCheck = { kind: "idle" };
const CHECKING: PlatformCheck = { kind: "checking" };
const NO_QUESTION: CheckState = { check: IDLE, key: null, inputs: "" };

interface CheckState {
    check: PlatformCheck;
    key: string | null;
    inputs: string;
}

function isRemembered(check: PlatformCheck): boolean {
    return check.kind !== "unavailable";
}

interface Backoff {
    check: PlatformCheck;
    failures: number;
    retryAt: number;
}

function retryDelay(failures: number): number {
    return Math.min(UNAVAILABLE_RETRY_MAX_MS, UNAVAILABLE_RETRY_BASE_MS * 2 ** (failures - 1));
}

export function usePlatformCheck({ enabled, idle, buildName, sessionId, components, fetchConfigByFilename, applyBuild }: UsePlatformCheckArgs): UsePlatformCheckResult {
    const [state, setState] = useState<CheckState>(NO_QUESTION);
    const [asksAgain, setAsksAgain] = useState(0);
    const answers = useRef(new Map<string, PlatformCheck>());
    const backoffs = useRef(new Map<string, Backoff>());
    const answersSession = useRef(sessionId);
    const latestRequest = useRef(0);
    const currentKey = useRef<string | null>(null);
    const latest = useRef({ components, fetchConfigByFilename, applyBuild });
    latest.current = { components, fetchConfigByFilename, applyBuild };

    const askAgain = useCallback(() => setAsksAgain(n => n + 1), []);

    if (!enabled && state !== NO_QUESTION) setState(NO_QUESTION);

    const componentsSignature = JSON.stringify(components);
    const inputsSignature = `${sessionId}|${buildName}|${componentsSignature}`;

    useEffect(() => {
        if (answersSession.current !== sessionId) {
            answers.current.clear();
            backoffs.current.clear();
            answersSession.current = sessionId;
        }
        if (!enabled) currentKey.current = null;
        if (!enabled || !idle) return;

        const request = ++latestRequest.current;
        const isCurrent = () => request === latestRequest.current;
        const { components: asked, fetchConfigByFilename: fetchConfig, applyBuild: dryRun } = latest.current;
        const controller = new AbortController();
        let retryTimer: ReturnType<typeof setTimeout> | undefined;

        void (async () => {
            let inputs: BuildComponentInput[];
            try {
                inputs = await toBuildComponentInputs(asked, fetchConfig);
            } catch (err) {
                if (isCurrent()) {
                    currentKey.current = null;
                    setState({ check: { kind: "unavailable", message: getErrorMessage(err, "The build could not be read to check it.") }, key: null, inputs: inputsSignature });
                }
                return;
            }
            if (!isCurrent()) return;

            const key = JSON.stringify({ buildName, components: inputs });
            currentKey.current = key;
            const remembered = answers.current.get(key);
            if (remembered) {
                setState({ check: remembered, key, inputs: inputsSignature });
                return;
            }

            const backoff = backoffs.current.get(key);
            const wait = backoff ? backoff.retryAt - Date.now() : 0;
            if (backoff && wait > 0) {
                setState({ check: backoff.check, key, inputs: inputsSignature });
                retryTimer = setTimeout(askAgain, wait);
                return;
            }

            setState({ check: CHECKING, key, inputs: inputsSignature });
            let check: PlatformCheck;
            try {
                check = classifyDryRun(await dryRun({ buildName, components: inputs, dryRun: true }, controller.signal));
            } catch (err) {
                check = classifyDryRunError(err);
            }
            if (!isCurrent()) return;
            if (isRemembered(check)) {
                answers.current.set(key, check);
                backoffs.current.delete(key);
            } else {
                const failures = (backoff?.failures ?? 0) + 1;
                const delay = retryDelay(failures);
                backoffs.current.set(key, { check, failures, retryAt: Date.now() + delay });
                retryTimer = setTimeout(askAgain, delay);
            }
            setState({ check, key, inputs: inputsSignature });
        })();

        return () => {
            latestRequest.current = request + 1;
            controller.abort();
            clearTimeout(retryTimer);
        };
    }, [enabled, idle, buildName, sessionId, componentsSignature, inputsSignature, asksAgain, askAgain]);

    const checkAgain = useCallback(() => {
        if (currentKey.current !== null) {
            answers.current.delete(currentKey.current);
            backoffs.current.delete(currentKey.current);
        }
        setState(prev => (prev === NO_QUESTION ? prev : { ...prev, check: CHECKING }));
        askAgain();
    }, [askAgain]);

    const answersTheseInputs = state.inputs === inputsSignature;
    if (!enabled) return { check: IDLE, key: null, checkAgain };
    const shown = answersTheseInputs ? state.check : CHECKING;
    const check = shown.kind === "checking" && !idle ? IDLE : shown;
    return { check, key: answersTheseInputs ? state.key : null, checkAgain };
}
