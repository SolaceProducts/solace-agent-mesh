// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type FormEvent, useCallback, useEffect, useRef, useState } from "react";

import { useChatContext } from "@/providers/contexts";

import { useIsBuilderServerApplyEnabled } from "@/hooks/useIsBuilderServerApplyEnabled";

import { type ArtifactVersions, isRunningStaleConfig } from "../componentDirtyState";
import { type ValidationStampReading, validationStampState } from "../validationStamp";

import { useArtifactVersions } from "./useArtifactVersions";
import { useBuilderManifest } from "./useBuilderManifest";

export const MAX_AUTO_FULL_VALIDATION_ATTEMPTS = 2;
export const AUTO_FULL_VALIDATION_RETRY_MS = 15000;

export const VALID_STATUSES = new Set(["validated", "draft", "error", "deployed", "blocked"]);

const PLATFORM_ORIGIN = "platform";

export const VALID_ORIGINS = new Set(["session", "platform"]);

const NO_COMPONENTS_KEY = "no-components";

export const FULL_VALIDATION_REQUEST = "All components are validated. Please run ValidateBuildManifest with full_validation=true to verify cross-component wiring before deployment.";

interface ManifestComponentGate {
    name?: string;
    status?: string;
    origin?: string;
    type?: string;
    filename?: string;
    deployed_version?: number;
}

function effectiveGateStatus(c: ManifestComponentGate, versions: ArtifactVersions, serverApplyEnabled: boolean): string | undefined {
    if (c.status !== "deployed") return c.status;
    if (c.deployed_version === undefined) return "validated";
    return isRunningStaleConfig(c, versions, serverApplyEnabled) ? "validated" : c.status;
}

export interface UnreachableReason {
    key: string;
    message: string;
}

export function fullValidationUnreachable(components: ManifestComponentGate[]): UnreachableReason | null {
    if (components.length === 0) {
        return { key: NO_COMPONENTS_KEY, message: "The build manifest lists no components." };
    }

    const misfiled = components.find(c => c.origin !== undefined && c.origin !== "" && !VALID_ORIGINS.has(c.origin));
    if (misfiled) {
        return {
            key: `origin:${misfiled.name ?? ""}:${misfiled.origin ?? ""}`,
            message: `Component '${misfiled.name ?? ""}' has origin '${misfiled.origin ?? ""}', which is not one of: ${[...VALID_ORIGINS].join(", ")}.`,
        };
    }

    const stalled = components.find(c => c.origin !== PLATFORM_ORIGIN && c.status !== undefined && c.status !== "" && !VALID_STATUSES.has(c.status));
    if (!stalled) return null;
    return {
        key: `status:${stalled.name ?? ""}:${stalled.status ?? ""}`,
        message: `Component '${stalled.name ?? ""}' has status '${stalled.status ?? ""}', which is not one of: ${[...VALID_STATUSES].join(", ")}.`,
    };
}

type ValidationAsk = { kind: "ask"; budgetKey: string; message: string } | { kind: "retire" } | { kind: "wait" };

export function decideValidationAsk(components: ManifestComponentGate[], fingerprint: string, agentJustFinished: boolean, versions: ArtifactVersions = new Map(), serverApplyEnabled = false): ValidationAsk {
    const unreachable = fullValidationUnreachable(components);

    if (unreachable === null) {
        const sessionComponents = components.filter(c => c.origin !== PLATFORM_ORIGIN);
        const effective = sessionComponents.map(c => effectiveGateStatus(c, versions, serverApplyEnabled));
        const stillWorking = effective.some(s => s !== "validated" && s !== "deployed");
        const awaitingCheck = effective.includes("validated");
        const ready = sessionComponents.length > 0 && !stillWorking && awaitingCheck;
        return ready ? { kind: "ask", budgetKey: `ready:${fingerprint}`, message: FULL_VALIDATION_REQUEST } : { kind: "retire" };
    }

    if (unreachable.key === NO_COMPONENTS_KEY && !agentJustFinished) return { kind: "wait" };

    return {
        kind: "ask",
        budgetKey: unreachable.key,
        message: `${unreachable.message} Please call ValidateBuildManifest to check and fix the manifest.`,
    };
}

function stampFingerprint(stamp: ValidationStampReading, versions: ArtifactVersions): string {
    if (stamp.state === "unknown") return "|stamp:unknown";
    if (stamp.state !== "stale") return "";
    const changed = [...stamp.changed].sort((a, b) => a.localeCompare(b)).map(filename => `${filename}@${versions.get(filename) ?? "?"}`);
    return `|stamp:stale:${changed.join(",")}`;
}

export interface AutoFullValidationState {
    validationStuck: boolean;
    retryValidation: () => void;
}

export function useAutoFullValidation(): AutoFullValidationState {
    const { isResponding, handleSubmit, sessionId } = useChatContext();
    const { manifest } = useBuilderManifest();
    const artifactVersions = useArtifactVersions(manifest);
    const stamp = validationStampState(manifest, artifactVersions);
    const stampState = stamp.state;
    const stampKey = stampFingerprint(stamp, artifactVersions);
    const serverApplyEnabled = useIsBuilderServerApplyEnabled();
    const wasRespondingRef = useRef(false);
    const lastCheckedVersionRef = useRef<string | null>(null);
    const lastSessionRef = useRef(sessionId);
    const sessionLoadCheckedRef = useRef(false);
    const retryTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
    const attemptsRef = useRef(0);
    const attemptsKeyRef = useRef<string | null>(null);
    const lastAskRef = useRef<{ budgetKey: string; fingerprint: string; message: string } | null>(null);
    const [validationStuck, setValidationStuck] = useState(false);

    if (sessionId !== lastSessionRef.current) {
        lastSessionRef.current = sessionId;
        lastCheckedVersionRef.current = null;
        sessionLoadCheckedRef.current = false;
        attemptsRef.current = 0;
        attemptsKeyRef.current = null;
        lastAskRef.current = null;
        setValidationStuck(false);
        if (retryTimerRef.current) {
            clearTimeout(retryTimerRef.current);
            retryTimerRef.current = null;
        }
    }

    const sendValidationRequest = useCallback(
        (message: string) => {
            const fakeEvent = new Event("submit") as unknown as FormEvent;
            handleSubmit(fakeEvent, [], message, null, null, null, null, null, { hidden: true }).catch(() => {
                lastCheckedVersionRef.current = null;
            });
        },
        [handleSubmit]
    );

    const attemptValidation = useCallback(
        (budgetKey: string, fingerprint: string, message: string) => {
            if (attemptsKeyRef.current !== budgetKey) {
                attemptsKeyRef.current = budgetKey;
                attemptsRef.current = 0;
                setValidationStuck(false);
            }
            lastAskRef.current = { budgetKey, fingerprint, message };
            if (attemptsRef.current >= MAX_AUTO_FULL_VALIDATION_ATTEMPTS) {
                setValidationStuck(true);
                return;
            }
            attemptsRef.current += 1;
            sendValidationRequest(message);

            if (retryTimerRef.current) clearTimeout(retryTimerRef.current);
            retryTimerRef.current = setTimeout(() => {
                retryTimerRef.current = null;
                if (lastCheckedVersionRef.current === fingerprint) {
                    attemptValidation(budgetKey, fingerprint, message);
                }
            }, AUTO_FULL_VALIDATION_RETRY_MS);
        },
        [sendValidationRequest]
    );

    const retryValidation = useCallback(() => {
        const ask = lastAskRef.current;
        attemptsRef.current = 0;
        attemptsKeyRef.current = null;
        setValidationStuck(false);
        if (!ask) return;
        lastCheckedVersionRef.current = ask.fingerprint;
        attemptValidation(ask.budgetKey, ask.fingerprint, ask.message);
    }, [attemptValidation]);

    useEffect(() => {
        const wasResponding = wasRespondingRef.current;
        wasRespondingRef.current = isResponding;

        if (!manifest) return;
        const components = manifest.components ?? [];

        const stateFingerprint = components.length === 0 ? NO_COMPONENTS_KEY : components.map(c => `${c.name}:${c.status}:${c.origin}:${effectiveGateStatus(c, artifactVersions, serverApplyEnabled)}`).join(",");
        const fingerprint = stateFingerprint + stampKey;

        const agentJustFinished = wasResponding && !isResponding;
        const sessionJustLoaded = !isResponding && !sessionLoadCheckedRef.current;
        const settledStateChanged = !isResponding && lastCheckedVersionRef.current !== null && fingerprint !== lastCheckedVersionRef.current;

        if (!agentJustFinished && !sessionJustLoaded && !settledStateChanged) return;
        if (sessionJustLoaded) sessionLoadCheckedRef.current = true;

        if (fingerprint === lastCheckedVersionRef.current) return;

        if (stampState === "current") {
            lastCheckedVersionRef.current = fingerprint;
            setValidationStuck(false);
            return;
        }

        if (stampState === "unknown") {
            lastCheckedVersionRef.current = fingerprint;
            return;
        }

        const decision = decideValidationAsk(components, fingerprint, agentJustFinished, artifactVersions, serverApplyEnabled);
        if (decision.kind === "wait") return;

        lastCheckedVersionRef.current = fingerprint;

        if (decision.kind === "retire") {
            if (retryTimerRef.current) {
                clearTimeout(retryTimerRef.current);
                retryTimerRef.current = null;
            }
            setValidationStuck(false);
            return;
        }

        attemptValidation(decision.budgetKey, fingerprint, decision.message);
    }, [isResponding, manifest, stampState, stampKey, attemptValidation, artifactVersions, serverApplyEnabled]);

    useEffect(() => {
        if (stampState === "current") {
            setValidationStuck(false);
            if (retryTimerRef.current) {
                clearTimeout(retryTimerRef.current);
                retryTimerRef.current = null;
            }
        }
    }, [stampState]);

    useEffect(() => {
        return () => {
            if (retryTimerRef.current) clearTimeout(retryTimerRef.current);
        };
    }, []);

    return { validationStuck, retryValidation };
}
