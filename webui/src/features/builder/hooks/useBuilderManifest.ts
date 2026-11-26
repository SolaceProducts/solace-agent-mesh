// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import yaml from "js-yaml";
import type { RefObject } from "react";
import { z } from "zod";

import { useChatArtifacts } from "@/api/artifacts";
import { builderKeys, getManifestContent, useManifestContentQuery, usePatchSessionNameMutation, useUploadManifestMutation } from "@/api/builder";

import { useChatContext } from "@/providers/contexts";

import { BUILD_MANIFEST_ARTIFACT_FILENAME } from "../builderArtifacts";

import { decodeBase64Content } from "./artifactUtils";

export const SAM_BUILD_MANIFEST_MIME_TYPE = "application/vnd.sam-build-manifest+yaml";

const textOrDropped = z.union([z.string(), z.any().transform(() => undefined)]).optional();

export const ManifestWorkflowNodeSchema = z
    .object({
        id: z.string(),
        type: z.string(),
        depends_on: z.array(z.string()).optional(),
        agent_name: z.string().optional(),
        workflow_name: z.string().optional(),
        tool_name: z.string().optional(),
        cases: z.array(z.object({ condition: z.string(), node: z.string() }).passthrough()).optional(),
        default: z.string().optional(),
        node: z.string().optional(),
        items: z.string().optional(),
        condition: z.string().optional(),
        max_iterations: z.number().optional(),
        delay: z.string().optional(),
        connector: textOrDropped,
    })
    .passthrough();

export type ManifestWorkflowNode = z.infer<typeof ManifestWorkflowNodeSchema>;

const ManifestComponentSchema = z
    .object({
        filename: z.string(),
        type: z.string(),
        name: z.string(),
        description: z.string().optional(),
        id: z.string().optional(),
        deployed_version: z.number().optional(),
        status: z.string().optional(),
        blocked_reason: textOrDropped,
        origin: z.string().optional(),
        depends_on: z.array(z.string()).optional(),
        connectors: z.array(z.string()).optional(),
        tools: z.array(z.string()).optional(),
        nodes: z.array(ManifestWorkflowNodeSchema).optional(),
        gateway_type: z.string().optional(),
        icon: z.string().optional(),
    })
    .passthrough();

const EnvVarSchema = z
    .object({
        name: z.string(),
        description: z.string().optional(),
        required: z.boolean().optional(),
        default: z.string().optional(),
    })
    .passthrough();

function isPlainObject(value: unknown): value is Record<string, unknown> {
    if (value === null || typeof value !== "object") return false;
    const prototype = Object.getPrototypeOf(value);
    return prototype === Object.prototype || prototype === null;
}

function withoutNullFields(value: unknown): unknown {
    if (Array.isArray(value)) return value.map(withoutNullFields);
    if (!isPlainObject(value)) return value;
    return Object.fromEntries(
        Object.entries(value)
            .filter(([, field]) => field !== null)
            .map(([key, field]) => [key, withoutNullFields(field)])
    );
}

const ValidationVersionsSchema = z.record(z.string(), z.number());

function readValidationVersions(value: unknown): Record<string, number> | undefined {
    const parsed = ValidationVersionsSchema.safeParse(value);
    return parsed.success ? parsed.data : undefined;
}

const BuildManifestShape = z
    .object({
        name: z.string(),
        display_name: z.string().optional(),
        description: z.string().optional(),
        architecture: z.string().optional(),
        full_validation_passed: z.boolean().optional(),
        full_validation_versions: z.unknown().transform(readValidationVersions).optional(),
        risks: z.array(z.string()).optional(),
        env_vars: z.array(EnvVarSchema).optional(),
        components: z.array(ManifestComponentSchema).optional(),
        loaded_for_edit: z.boolean().optional(),
    })
    .passthrough();

export const BuildManifestSchema = z.preprocess(withoutNullFields, BuildManifestShape);

export type BuildManifest = z.infer<typeof BuildManifestSchema>;

function manifestsEqual(a: unknown, b: unknown): boolean {
    if (a === b) return true;
    if (typeof a !== "object" || typeof b !== "object" || a === null || b === null) return false;
    const aArray = Array.isArray(a);
    if (aArray !== Array.isArray(b)) return false;
    if (aArray) {
        const bArr = b as unknown[];
        return (a as unknown[]).length === bArr.length && (a as unknown[]).every((v, i) => manifestsEqual(v, bArr[i]));
    }
    const aKeys = Object.keys(a as Record<string, unknown>);
    const bObj = b as Record<string, unknown>;
    return aKeys.length === Object.keys(bObj).length && aKeys.every(k => Object.prototype.hasOwnProperty.call(bObj, k) && manifestsEqual((a as Record<string, unknown>)[k], bObj[k]));
}

function voidUploadEvidenceOnForeignWrite(lastUploadedRef: RefObject<BuildManifest | null>, parsedManifest: BuildManifest | null, holdDuringRewrite: boolean): void {
    if (!parsedManifest || holdDuringRewrite || !lastUploadedRef.current) return;
    if (!manifestsEqual(lastUploadedRef.current, parsedManifest)) lastUploadedRef.current = null;
}

type ManifestComponent = NonNullable<BuildManifest["components"]>[number];

const PLATFORM_ID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export const platformId = (component: ManifestComponent): string | undefined => (typeof component.id === "string" && PLATFORM_ID_PATTERN.test(component.id) ? component.id : undefined);

export function isSameComponent(a: ManifestComponent, b: ManifestComponent): boolean {
    const idA = platformId(a);
    const idB = platformId(b);
    if (idA && idB) return idA === idB;
    return a.type === b.type && a.name === b.name;
}

type StoredManifest = { kind: "absent" } | { kind: "failed" } | { kind: "unreadable" } | { kind: "ok"; manifest: BuildManifest };

async function readStoredManifest(sessionId: string, filename: string): Promise<StoredManifest> {
    let stored: string | null;
    try {
        stored = await getManifestContent(sessionId, filename, "latest");
    } catch {
        console.warn("[builder] saveManifest: could not read the stored manifest before writing; writing from the page's copy", { sessionId, filename });
        return { kind: "failed" };
    }
    if (!stored) return { kind: "absent" };
    try {
        const parsed = BuildManifestSchema.safeParse(yaml.load(stored, { json: true }));
        return parsed.success ? { kind: "ok", manifest: parsed.data } : { kind: "unreadable" };
    } catch {
        return { kind: "unreadable" };
    }
}

function componentsWrittenSince(current: BuildManifest, next: BuildManifest, stored: BuildManifest): { unseen: ManifestComponent[]; shadowed: ManifestComponent[] } {
    const known = [...(current.components ?? []), ...(next.components ?? [])];
    const notKnown = (stored.components ?? []).filter(component => !known.some(candidate => isSameComponent(candidate, component)));
    const nameHeld = (component: ManifestComponent) => known.some(candidate => candidate.name === component.name);
    return { unseen: notKnown.filter(component => !nameHeld(component)), shadowed: notKnown.filter(nameHeld) };
}

async function keepComponentsWrittenSince(sessionId: string, filename: string, current: BuildManifest, next: BuildManifest): Promise<BuildManifest | null> {
    const stored = await readStoredManifest(sessionId, filename);
    if (stored.kind === "unreadable") return null;
    if (stored.kind === "absent") {
        console.warn("[builder] saveManifest: the store returned no manifest; writing from the page's copy", { sessionId, filename });
    }
    const { unseen, shadowed } = stored.kind === "ok" ? componentsWrittenSince(current, next, stored.manifest) : { unseen: [], shadowed: [] };
    if (shadowed.length > 0) {
        console.warn("[builder] saveManifest: not keeping stored components whose names the page already holds", { sessionId, filename, components: shadowed.map(c => c.name) });
    }
    if (unseen.length === 0) return next;
    console.warn("[builder] saveManifest: keeping components written since this manifest was read", { sessionId, filename, components: unseen.map(c => c.name) });
    return { ...next, components: [...(next.components ?? []), ...unseen] };
}

function logUnreadableRefusalOnce(loggedRef: RefObject<boolean>, sessionId: string, filename: string): void {
    if (loggedRef.current) return;
    loggedRef.current = true;
    console.warn("[builder] saveManifest refused: the latest stored manifest cannot be read", { sessionId, filename });
}

export type SaveManifestResult = { ok: true } | { ok: false; reason: "unreadable" | "failed" | "session_changed" };

export interface SaveManifestOptions {
    forSession?: string;
}

const SAVED: SaveManifestResult = { ok: true };
const FAILED: SaveManifestResult = { ok: false, reason: "failed" };
const REFUSED_UNREADABLE: SaveManifestResult = { ok: false, reason: "unreadable" };
const SESSION_CHANGED: SaveManifestResult = { ok: false, reason: "session_changed" };

export interface BuilderManifestState {
    manifest: BuildManifest | null;
    isLoading: boolean;
    isSettled: boolean;
    isUnreadable: boolean;
    saveManifest: (updater: (prev: BuildManifest) => BuildManifest, options?: SaveManifestOptions) => Promise<SaveManifestResult>;
}

export function useBuilderManifest(): BuilderManifestState {
    const { downloadAndResolveArtifact, sessionId } = useChatContext();
    const { data: artifacts } = useChatArtifacts();
    const [rawYaml, setRawYaml] = useState<string | null>(null);
    const [isLoading, setIsLoading] = useState(false);
    const fetchingRef = useRef(false);
    const lastVersionRef = useRef<number | undefined>(undefined);
    const adoptedAccumulatedRef = useRef<string | null>(null);
    const lastUploadedRef = useRef<BuildManifest | null>(null);
    const lastSyncedNameRef = useRef<string | null>(null);
    const uploadManifestMutation = useUploadManifestMutation();
    const patchSessionNameMutation = usePatchSessionNameMutation();
    const queryClient = useQueryClient();
    const cachedManifestRef = useRef<BuildManifest | null>(null);
    const hadCompleteManifestRef = useRef(false);

    const lastSessionRef = useRef(sessionId);
    const sessionJustChangedRef = useRef(false);
    let sessionJustChangedThisRender = false;
    if (sessionId !== lastSessionRef.current) {
        lastSessionRef.current = sessionId;
        setRawYaml(null);
        setIsLoading(false);
        fetchingRef.current = false;
        lastVersionRef.current = undefined;
        lastSyncedNameRef.current = null;
        cachedManifestRef.current = null;
        adoptedAccumulatedRef.current = null;
        lastUploadedRef.current = null;
        hadCompleteManifestRef.current = false;
        sessionJustChangedRef.current = true;
        sessionJustChangedThisRender = true;
    }

    const manifestArtifact = useMemo(() => {
        if (!artifacts || artifacts.length === 0) return null;
        for (let i = artifacts.length - 1; i >= 0; i--) {
            if (artifacts[i].mimeType === SAM_BUILD_MANIFEST_MIME_TYPE) {
                return artifacts[i];
            }
        }
        return null;
    }, [artifacts]);

    const manifestFilename = manifestArtifact?.filename;
    const manifestVersion = manifestArtifact?.version;
    const manifestAccumulatedContent = manifestArtifact?.accumulatedContent;
    const manifestUnsettled = manifestArtifact?.creationStatus === "in-progress" || manifestArtifact?.creationStatus === "failed";

    const resolvedManifestFilename = manifestFilename ?? BUILD_MANIFEST_ARTIFACT_FILENAME;

    const latestContentQuery = useManifestContentQuery(sessionId ?? undefined, resolvedManifestFilename, "latest", !!sessionId && !manifestAccumulatedContent);
    const latestContent = latestContentQuery.data;
    useEffect(() => {
        if (sessionJustChangedRef.current || manifestAccumulatedContent) return;
        if (latestContent && latestContent !== rawYaml) {
            setRawYaml(latestContent);
            setIsLoading(false);
            fetchingRef.current = false;
        }
    }, [latestContent, manifestAccumulatedContent, rawYaml]);

    const [retrigger, setRetrigger] = useState(0);

    const adoptStreamedSnapshot = useCallback((content: string) => {
        if (content === adoptedAccumulatedRef.current) return;
        adoptedAccumulatedRef.current = content;
        setRawYaml(content);
    }, []);

    useEffect(() => {
        if (sessionJustChangedRef.current) {
            sessionJustChangedRef.current = false;
            setRetrigger(n => n + 1);
            return;
        }

        if (!manifestFilename) {
            if (latestContent && latestContent !== rawYaml) {
                setRawYaml(latestContent);
            } else if (!cachedManifestRef.current && !latestContent) {
                setRawYaml(null);
            }
            setIsLoading(false);
            return;
        }

        const versionChanged = manifestVersion !== undefined && lastVersionRef.current !== undefined && manifestVersion !== lastVersionRef.current;

        if (versionChanged) {
            lastVersionRef.current = manifestVersion;
            setRawYaml(null);
            adoptedAccumulatedRef.current = null;
            fetchingRef.current = false;
        } else {
            lastVersionRef.current = manifestVersion;
        }

        if (manifestAccumulatedContent) {
            adoptStreamedSnapshot(manifestAccumulatedContent);
            setIsLoading(false);
            fetchingRef.current = false;
            return;
        }

        if (rawYaml && !fetchingRef.current) {
            setIsLoading(false);
            return;
        }

        if (fetchingRef.current) return;
        fetchingRef.current = true;
        setIsLoading(true);

        let cancelled = false;
        let retryTimer: ReturnType<typeof setTimeout> | null = null;
        const capturedSession = lastSessionRef.current;
        const filename = manifestFilename;

        const fetchManifestDirect = (): Promise<string | null> =>
            queryClient.fetchQuery({
                queryKey: builderKeys.manifestContent(capturedSession ?? "", filename, "latest"),
                queryFn: () => getManifestContent(capturedSession ?? "", filename, "latest"),
            });

        const attemptFetch = (retries: number) => {
            if (cancelled) return;
            downloadAndResolveArtifact(filename)
                .then(async (fileAttachment: { content?: string } | null) => {
                    if (cancelled || lastSessionRef.current !== capturedSession) return;
                    if (fileAttachment?.content) {
                        try {
                            setRawYaml(decodeBase64Content(fileAttachment.content));
                        } catch {
                            setRawYaml(fileAttachment.content);
                        }
                        setIsLoading(false);
                        fetchingRef.current = false;
                        return;
                    }
                    const text = await fetchManifestDirect();
                    if (cancelled || lastSessionRef.current !== capturedSession) return;
                    if (text) {
                        setRawYaml(text);
                        setIsLoading(false);
                        fetchingRef.current = false;
                    } else if (retries > 0) {
                        retryTimer = setTimeout(() => attemptFetch(retries - 1), 500);
                    } else {
                        setIsLoading(false);
                        fetchingRef.current = false;
                    }
                })
                .catch((err: unknown) => {
                    if (cancelled || lastSessionRef.current !== capturedSession) return;
                    console.error("Failed to fetch build manifest artifact:", err);
                    if (retries > 0) {
                        retryTimer = setTimeout(() => attemptFetch(retries - 1), 500);
                    } else {
                        setIsLoading(false);
                        fetchingRef.current = false;
                    }
                });
        };
        attemptFetch(3);

        return () => {
            cancelled = true;
            if (retryTimer) clearTimeout(retryTimer);
            fetchingRef.current = false;
        };
    }, [manifestFilename, manifestVersion, manifestAccumulatedContent, adoptStreamedSnapshot, latestContent, downloadAndResolveArtifact, rawYaml, retrigger]);

    const parsedManifest = useMemo((): BuildManifest | null => {
        if (!rawYaml) return null;
        try {
            const doc = yaml.load(rawYaml, { json: true });
            const parsed = BuildManifestSchema.safeParse(doc);
            if (!parsed.success) {
                console.warn("[useBuilderManifest] manifest schema validation failed", parsed.error.issues);
                return null;
            }
            return parsed.data;
        } catch (err) {
            console.warn("[useBuilderManifest] YAML parse failed", err);
            return null;
        }
    }, [rawYaml]);

    if (parsedManifest && !manifestUnsettled && !sessionJustChangedThisRender) hadCompleteManifestRef.current = true;

    const holdDuringRewrite = manifestUnsettled && hadCompleteManifestRef.current && !sessionJustChangedThisRender && !!cachedManifestRef.current;

    voidUploadEvidenceOnForeignWrite(lastUploadedRef, parsedManifest, holdDuringRewrite);

    if (parsedManifest && !sessionJustChangedThisRender && !holdDuringRewrite && (!cachedManifestRef.current || !manifestsEqual(cachedManifestRef.current, parsedManifest))) {
        cachedManifestRef.current = parsedManifest;
    }
    const manifest = sessionJustChangedThisRender ? null : holdDuringRewrite ? cachedManifestRef.current : (cachedManifestRef.current ?? parsedManifest);

    useEffect(() => {
        const manifestName = manifest?.display_name || manifest?.name;
        if (!manifestName || !sessionId) return;
        if (manifestName === lastSyncedNameRef.current) return;
        const pendingTarget = sessionStorage.getItem("sam_redirect_session_id") || sessionStorage.getItem("sam_agent_session_redirect");
        if (pendingTarget && sessionId !== pendingTarget) return;
        lastSyncedNameRef.current = manifestName;
        patchSessionNameMutation
            .mutateAsync({ sessionId, name: manifestName, agentId: "Builder" })
            .then(() => {
                window.dispatchEvent(new CustomEvent("new-chat-session"));
            })
            .catch((err: unknown) => {
                console.error("Failed to sync session name from manifest:", err);
            });
    }, [manifest?.display_name, manifest?.name, sessionId, patchSessionNameMutation]);

    const manifestBytesUnreadable = !parsedManifest && !!rawYaml;
    const bytesUnreadableRef = useRef(false);
    bytesUnreadableRef.current = manifestBytesUnreadable;
    const refusalLoggedRef = useRef(false);
    const storedRefusalLoggedRef = useRef(false);
    const uploadRef = useRef(uploadManifestMutation);
    uploadRef.current = uploadManifestMutation;

    const parsedManifestRef = useRef(parsedManifest);
    parsedManifestRef.current = parsedManifest;
    const sessionIdRef = useRef(sessionId);
    sessionIdRef.current = sessionId;

    const saveManifestNow = useCallback(
        async (updater: (prev: BuildManifest) => BuildManifest): Promise<SaveManifestResult> => {
            const current = holdDuringRewrite ? cachedManifestRef.current : (parsedManifestRef.current ?? cachedManifestRef.current);
            const filename = manifestFilename ?? BUILD_MANIFEST_ARTIFACT_FILENAME;
            if (!holdDuringRewrite && bytesUnreadableRef.current) {
                if (!refusalLoggedRef.current) {
                    refusalLoggedRef.current = true;
                    console.warn("[builder] saveManifest refused: the stored manifest cannot be read", { sessionId, filename });
                }
                return REFUSED_UNREADABLE;
            }
            refusalLoggedRef.current = false;
            if (!current || !sessionId) {
                console.warn("[builder] saveManifest preconditions failed", {
                    hasCurrent: !!current,
                    filename,
                    sessionId,
                });
                return FAILED;
            }
            try {
                let next = updater(current);
                if (manifestsEqual(current, next)) {
                    console.debug("[builder] saveManifest: no-op, manifest already matches", { sessionId, filename });
                    return SAVED;
                }
                if (manifestsEqual(lastUploadedRef.current, next)) {
                    console.debug("[builder] saveManifest: no-op, manifest already uploaded", { sessionId, filename });
                    return SAVED;
                }
                const reconciled = await keepComponentsWrittenSince(sessionId, filename, current, next);
                if (!reconciled) {
                    logUnreadableRefusalOnce(storedRefusalLoggedRef, sessionId, filename);
                    return REFUSED_UNREADABLE;
                }
                storedRefusalLoggedRef.current = false;
                next = reconciled;
                const newYaml = yaml.dump(next, { lineWidth: 120, noRefs: true });
                console.debug("[builder] saveManifest uploading", {
                    sessionId,
                    filename,
                    yamlBytes: newYaml.length,
                });
                const { ok } = await uploadRef.current.mutateAsync({
                    sessionId,
                    filename,
                    yamlText: newYaml,
                    mimeType: SAM_BUILD_MANIFEST_MIME_TYPE,
                });
                if (!ok) {
                    console.warn("[builder] saveManifest upload returned !ok");
                    return FAILED;
                }
                queryClient.setQueryData(builderKeys.manifestContent(sessionId, filename, "latest"), newYaml);
                if (sessionIdRef.current !== sessionId) return SAVED;
                setRawYaml(newYaml);
                cachedManifestRef.current = next;
                lastUploadedRef.current = next;
                parsedManifestRef.current = next;
                return SAVED;
            } catch (err) {
                console.error("[builder] saveManifest threw", err);
                return FAILED;
            }
        },
        [holdDuringRewrite, manifestFilename, sessionId, queryClient]
    );

    const saveNowRef = useRef(saveManifestNow);
    saveNowRef.current = saveManifestNow;
    const writeQueueRef = useRef<Promise<unknown>>(Promise.resolve());
    const saveManifest = useCallback((updater: (prev: BuildManifest) => BuildManifest, options?: SaveManifestOptions): Promise<SaveManifestResult> => {
        const queuedFor = options?.forSession ?? sessionIdRef.current;
        const write = writeQueueRef.current.then(() => {
            if (sessionIdRef.current === queuedFor) return saveNowRef.current(updater);
            console.warn("[builder] saveManifest dropped: the session changed before the write ran", { queuedFor, now: sessionIdRef.current });
            return SESSION_CHANGED;
        });
        writeQueueRef.current = write.catch(() => undefined);
        return write;
    }, []);

    const contentQuerySettled = latestContentQuery.isFetched && !latestContentQuery.isFetching;
    const isSettled = !!sessionId && !sessionJustChangedThisRender && !isLoading && contentQuerySettled;

    const isUnreadable = manifestBytesUnreadable && !sessionJustChangedThisRender;

    return useMemo(() => ({ manifest, isLoading, isSettled, isUnreadable, saveManifest }), [manifest, isLoading, isSettled, isUnreadable, saveManifest]);
}
