// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useQueries } from "@tanstack/react-query";
import yaml from "js-yaml";

import { useChatArtifacts } from "@/api/artifacts";
import { builderKeys, getManifestContent } from "@/api/builder";
import type { AuthConfig, CreateRemoteAgentRequest } from "@/api/remote-agents";
import { useRemoteAgents } from "@/api/remote-agents";

import { useChatContext } from "@/providers/contexts";

import { workflowBody, type WorkflowConfig } from "@/utils/agentUtils";

import { type ArtifactInfo } from "@/types/chat";

import type { ConfigType, ConnectorMetadata, GatewayMetadata, SkillDiagramConfig } from "../builderUtils";
import {
    detectConfigType,
    extractA2AAgentMetadata,
    extractAgentDiagramConfig,
    extractConnectorMetadata,
    extractGatewayMetadata,
    extractSkillConfig,
    extractWorkflowConfig,
    isBlockedSessionComponent,
    isBuilderConfigMime,
    normalizeComponentName,
} from "../builderUtils";
import { type AgentDiagramConfig } from "../types";
import { validationStampState } from "../validationStamp";

import { resolveArtifactContent } from "./artifactUtils";
import { useArtifactVersions } from "./useArtifactVersions";
import type { BuildManifest } from "./useBuilderManifest";
import { useBuilderManifest } from "./useBuilderManifest";

export interface WorkflowNodeSummary {
    total: number;
    agents: string[];
    types: Record<string, number>;
}

export interface DependencyProgress {
    name: string;
    type: ConfigType;
    status: "generating" | "queued" | "done" | "waiting";
    icon?: string;
}

export interface ComponentCanvasData {
    id?: string;
    name: string;
    filename: string;
    type: ConfigType;
    status?: string;
    origin?: string;
    dependsOn?: string[];
    yamlText: string | null;
    isLoading: boolean;
    isGenerating: boolean;
    isQueued: boolean;
    isWaitingForInput: boolean;
    isBuildingDeps: boolean;
    isBuildFinalizing?: boolean;
    isBuilderResponding: boolean;
    changedSinceValidation?: boolean;
    dependencyProgress: DependencyProgress[];
    agentConfig?: AgentDiagramConfig | null;
    skillConfig?: SkillDiagramConfig | null;
    gatewayMetadata?: GatewayMetadata | null;
    connectorMetadata?: ConnectorMetadata | null;
    a2aAgentMetadata?: CreateRemoteAgentRequest | null;
    workflowConfig?: WorkflowConfig | null;
    workflowNodeSummary?: WorkflowNodeSummary | null;
}

export function arrEq<T>(a: readonly T[] | undefined, b: readonly T[] | undefined): boolean {
    if (a === b) return true;
    if (!a || !b) return false;
    if (a.length !== b.length) return false;
    for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) return false;
    return true;
}

export function depProgressEq(a: readonly DependencyProgress[], b: readonly DependencyProgress[]): boolean {
    if (a === b) return true;
    if (a.length !== b.length) return false;
    for (let i = 0; i < a.length; i++) {
        const x = a[i],
            y = b[i];
        if (x.name !== y.name || x.type !== y.type || x.status !== y.status || x.icon !== y.icon) return false;
    }
    return true;
}

export function entryEq(cached: ComponentCanvasData, cand: ComponentCanvasData): boolean {
    return (
        cached.id === cand.id &&
        cached.name === cand.name &&
        cached.filename === cand.filename &&
        cached.type === cand.type &&
        cached.status === cand.status &&
        cached.origin === cand.origin &&
        cached.yamlText === cand.yamlText &&
        cached.isLoading === cand.isLoading &&
        cached.isGenerating === cand.isGenerating &&
        cached.isQueued === cand.isQueued &&
        cached.isWaitingForInput === cand.isWaitingForInput &&
        cached.isBuildingDeps === cand.isBuildingDeps &&
        (cached.isBuildFinalizing ?? false) === (cand.isBuildFinalizing ?? false) &&
        (cached.changedSinceValidation ?? false) === (cand.changedSinceValidation ?? false) &&
        cached.isBuilderResponding === cand.isBuilderResponding &&
        arrEq(cached.dependsOn, cand.dependsOn) &&
        depProgressEq(cached.dependencyProgress, cand.dependencyProgress) &&
        cached.agentConfig === cand.agentConfig &&
        cached.skillConfig === cand.skillConfig &&
        cached.gatewayMetadata === cand.gatewayMetadata &&
        cached.connectorMetadata === cand.connectorMetadata &&
        cached.a2aAgentMetadata === cand.a2aAgentMetadata &&
        cached.workflowConfig === cand.workflowConfig &&
        cached.workflowNodeSummary === cand.workflowNodeSummary
    );
}

export interface BuildContext {
    readonly resolvedConfigs: Map<string, string>;
    readonly loadingSet: Set<string>;
    readonly generatingFilenames: Set<string>;
    readonly isResponding: boolean;
    readonly buildFinalizing: boolean;
    readonly hasAnyGenerating: boolean;
    readonly changedSinceValidation: ReadonlySet<string>;
    readonly pendingInputComponentName: string | null;
    readonly createdNames: Set<string>;
    readonly a2aFallbackByName: ReadonlyMap<string, CreateRemoteAgentRequest>;
}

export type ManifestComponent = NonNullable<BuildManifest["components"]>[number];

export function isComponentQueued(comp: ManifestComponent, yamlText: string | null, isGenerating: boolean, ctx: BuildContext): boolean {
    if (!ctx.isResponding || !ctx.hasAnyGenerating || isGenerating) return false;
    if (comp.status === "validated" || comp.status === "deployed" || comp.status === "error") return false;
    if (comp.origin === "platform") return false;
    if (yamlText) return false;
    return !ctx.createdNames.has(comp.name);
}

export function isComponentWaitingForInput(comp: ManifestComponent, pendingName: string | null): boolean {
    if (isBlockedSessionComponent(comp)) return true;
    if (pendingName === null || comp.name == null) return false;
    return comp.name === pendingName || comp.name.toLowerCase() === pendingName.toLowerCase();
}

export function parseWorkflowNodeSummary(yamlText: string): WorkflowNodeSummary | null {
    try {
        const doc = yaml.load(yamlText, { json: true }) as Record<string, unknown> | null;
        const apps = doc?.apps as Array<Record<string, unknown>> | undefined;
        const appConfig = apps?.[0]?.app_config as Record<string, unknown> | undefined;
        const nodes = workflowBody(appConfig).nodes as Array<Record<string, unknown>> | undefined;
        if (!Array.isArray(nodes)) return null;
        const types: Record<string, number> = {};
        const agents: string[] = [];
        for (const node of nodes) {
            const t = (node.type as string) || "unknown";
            types[t] = (types[t] || 0) + 1;
            if (t === "agent" && node.agent_name) agents.push(node.agent_name as string);
        }
        return { total: nodes.length, agents, types };
    } catch {
        return null;
    }
}

export function parseTypeMetadata(type: ConfigType, yamlText: string): Partial<ComponentCanvasData> {
    switch (type) {
        case "agent":
            return { agentConfig: extractAgentDiagramConfig(yamlText) };
        case "skill":
            return { skillConfig: extractSkillConfig(yamlText) };
        case "gateway":
            return { gatewayMetadata: extractGatewayMetadata(yamlText) };
        case "connector":
            return { connectorMetadata: extractConnectorMetadata(yamlText) };
        case "a2a_agent":
            return { a2aAgentMetadata: extractA2AAgentMetadata(yamlText) };
        case "workflow": {
            const result: Partial<ComponentCanvasData> = { workflowConfig: extractWorkflowConfig(yamlText) };
            const summary = parseWorkflowNodeSummary(yamlText);
            if (summary) result.workflowNodeSummary = summary;
            return result;
        }
        default:
            return {};
    }
}

export function buildCandidateEntry(comp: ManifestComponent, ctx: BuildContext, cached: ComponentCanvasData | undefined): ComponentCanvasData {
    const yamlText = ctx.resolvedConfigs.get(comp.filename) ?? null;
    const type = (comp.type as ConfigType) || (yamlText ? detectConfigType(yamlText) : "unknown");
    const isGenerating = ctx.generatingFilenames.has(comp.filename);
    const isWaitingForInput = isComponentWaitingForInput(comp, ctx.pendingInputComponentName);
    const isQueued = isComponentQueued(comp, yamlText, isGenerating, ctx);
    const finalizingEligible = comp.status === "validated" || ctx.createdNames.has(comp.name);
    const isBuildFinalizing = ctx.buildFinalizing && finalizingEligible && comp.status !== "deployed" && comp.status !== "error" && comp.origin !== "platform";
    const reuseParsed = cached && cached.name === comp.name && cached.yamlText === yamlText && cached.type === type;
    let parsed: Partial<ComponentCanvasData> = {};
    if (reuseParsed) {
        parsed = {
            agentConfig: cached.agentConfig,
            skillConfig: cached.skillConfig,
            gatewayMetadata: cached.gatewayMetadata,
            connectorMetadata: cached.connectorMetadata,
            a2aAgentMetadata: cached.a2aAgentMetadata,
            workflowConfig: cached.workflowConfig,
            workflowNodeSummary: cached.workflowNodeSummary,
        };
    } else if (yamlText) {
        parsed = parseTypeMetadata(type, yamlText);
    }

    if (type === "a2a_agent" && !yamlText && !parsed.a2aAgentMetadata) {
        const fallback = ctx.a2aFallbackByName.get(comp.name) ?? ctx.a2aFallbackByName.get(normalizeComponentName(comp.name));
        if (fallback) parsed = { ...parsed, a2aAgentMetadata: fallback };
    }

    return {
        id: comp.id,
        name: comp.name,
        filename: comp.filename,
        type,
        status: comp.status,
        origin: comp.origin,
        dependsOn: comp.depends_on,
        yamlText,
        isLoading: ctx.loadingSet.has(comp.filename),
        isGenerating: isWaitingForInput ? false : isGenerating,
        isQueued: isWaitingForInput ? false : isQueued,
        isWaitingForInput,
        isBuildFinalizing,
        changedSinceValidation: ctx.changedSinceValidation.has(comp.filename),
        isBuilderResponding: ctx.isResponding,
        isBuildingDeps: false,
        dependencyProgress: [],
        ...parsed,
    };
}

export function depProgressStatus(depData: ComponentCanvasData): DependencyProgress["status"] {
    if (depData.isWaitingForInput) return "waiting";
    if (depData.isGenerating) return "generating";
    if (depData.status === "validated" || depData.status === "deployed") return "done";
    return "queued";
}

export function computeDependencyProgress(comp: ManifestComponent, result: Map<string, ComponentCanvasData>, manifest: BuildManifest): DependencyProgress[] {
    const depNames = new Set([...(comp.depends_on ?? []), ...(comp.connectors ?? [])]);
    if (depNames.size === 0) return [];

    const deps: DependencyProgress[] = [];
    for (const depName of depNames) {
        const depData = result.get(depName);
        if (!depData || depData.origin === "platform") continue;
        const depComp = manifest.components?.find(c => c.name === depName);
        deps.push({
            name: depName,
            type: depData.type,
            status: depProgressStatus(depData),
            icon: depComp?.icon,
        });
    }
    return deps;
}

export function eligibleForDepProgress(entry: ComponentCanvasData): boolean {
    return entry.isQueued && (entry.type === "agent" || entry.type === "workflow");
}

function preserveStableRef(result: Map<string, ComponentCanvasData>, prevEntries: Map<string, ComponentCanvasData>): void {
    for (const [name, cand] of result) {
        const cached = prevEntries.get(cand.filename);
        if (cached && entryEq(cached, cand)) result.set(name, cached);
    }
}

function refreshCache(result: Map<string, ComponentCanvasData>, prevEntries: Map<string, ComponentCanvasData>, seenFilenames: Set<string>): void {
    for (const filename of [...prevEntries.keys()]) {
        if (!seenFilenames.has(filename)) prevEntries.delete(filename);
    }
    for (const entry of result.values()) prevEntries.set(entry.filename, entry);
}

function buildComponentData(
    manifest: BuildManifest,
    resolvedConfigs: Map<string, string>,
    loadingSet: Set<string>,
    generatingFilenames: Set<string>,
    isResponding: boolean,
    buildFinalizing: boolean,
    hasAnyGenerating: boolean,
    changedSinceValidation: ReadonlySet<string>,
    pendingInputComponentName: string | null,
    createdNames: Set<string>,
    a2aFallbackByName: ReadonlyMap<string, CreateRemoteAgentRequest>,
    prevEntries: Map<string, ComponentCanvasData>
): Map<string, ComponentCanvasData> {
    const ctx: BuildContext = {
        resolvedConfigs,
        loadingSet,
        generatingFilenames,
        isResponding,
        buildFinalizing,
        hasAnyGenerating,
        changedSinceValidation,
        pendingInputComponentName,
        createdNames,
        a2aFallbackByName,
    };
    const components = manifest.components ?? [];
    const result = new Map<string, ComponentCanvasData>();
    const seenFilenames = new Set<string>();

    for (const comp of components) {
        const entry = buildCandidateEntry(comp, ctx, prevEntries.get(comp.filename));
        result.set(comp.name, entry);
        seenFilenames.add(comp.filename);
    }

    for (const comp of components) {
        const entry = result.get(comp.name);
        if (!entry || !eligibleForDepProgress(entry)) continue;
        const deps = computeDependencyProgress(comp, result, manifest);
        if (deps.some(d => d.status === "generating" || d.status === "waiting")) {
            entry.isBuildingDeps = true;
            entry.dependencyProgress = deps;
        }
    }

    preserveStableRef(result, prevEntries);
    refreshCache(result, prevEntries, seenFilenames);

    return result;
}

export function hasGenerationStarted(manifest: BuildManifest | null, sessionId: string): boolean {
    try {
        if (sessionStorage.getItem(`sam_builder_approved_${sessionId}`) === "1") return true;
    } catch {}
    return (manifest?.components ?? []).some(c => c.origin !== "platform" && (c.status === "validated" || c.status === "deployed" || c.status === "error"));
}

export function deriveGeneratingFilenames(configArtifacts: ArtifactInfo[], manifest: BuildManifest | null, isResponding: boolean, generationStarted: boolean): Set<string> {
    const generating = new Set<string>();
    if (!manifest?.components) return generating;

    if (isResponding) {
        for (const artifact of configArtifacts) {
            if (artifact.accumulatedContent) {
                const comp = manifest.components.find(c => c.filename === artifact.filename);
                if (comp && comp.origin !== "platform") {
                    generating.add(artifact.filename);
                }
            }
        }
    }

    if (generating.size === 0 && isResponding && generationStarted) {
        const firstDraft = manifest.components.find(c => c.status === "draft" && c.origin !== "platform" && c.filename);
        if (firstDraft) {
            generating.add(firstDraft.filename);
        }
    }

    return generating;
}

type ConfigFetchResult = { filename: string; content: string | null; version?: number; recordVersionOnFailure?: boolean };

function applyFetchResults(prev: Map<string, string>, results: ConfigFetchResult[], fetching: Set<string>, resolvedVersions?: Map<string, number>): Map<string, string> {
    let changed = false;
    const next = new Map(prev);
    for (const { filename, content, version, recordVersionOnFailure } of results) {
        if (content && next.get(filename) !== content) {
            next.set(filename, content);
            changed = true;
        }
        if (version != null && (content || recordVersionOnFailure)) resolvedVersions?.set(filename, version);
        fetching.delete(filename);
    }
    return changed ? next : prev;
}

type ConfigResolveDecision = { kind: "skip" } | { kind: "stream"; content: string } | { kind: "fetch"; artifact: ArtifactInfo; versionAdvanced: boolean };

const SKIP_RESOLVE: ConfigResolveDecision = { kind: "skip" };

function decideConfigResolve(artifact: ArtifactInfo | undefined, existing: string | undefined, pending: string | undefined, recordedVersion: number | undefined, inFlight: boolean): ConfigResolveDecision {
    if (!artifact) return SKIP_RESOLVE;

    if (artifact.accumulatedContent) {
        const unseen = artifact.accumulatedContent !== existing && artifact.accumulatedContent !== pending;
        return unseen ? { kind: "stream", content: artifact.accumulatedContent } : SKIP_RESOLVE;
    }

    const listedVersion = artifact.version;
    const settled = listedVersion != null && artifact.creationStatus !== "in-progress";
    const versionAdvanced = existing !== undefined && settled && recordedVersion !== listedVersion;

    if (existing && !versionAdvanced) return SKIP_RESOLVE;
    if (inFlight) return SKIP_RESOLVE;
    return { kind: "fetch", artifact, versionAdvanced };
}

export function useBuilderCanvasData(): {
    componentData: Map<string, ComponentCanvasData>;
    manifest: BuildManifest | null;
    generatingFilename: string | null;
    generatingFilenames: Set<string>;
    isGeneratingConfigs: boolean;
    hasPendingUserInput: boolean;
} {
    const { manifest: rawManifest } = useBuilderManifest();

    const cachedManifestRef = useRef<BuildManifest | null>(null);
    if (rawManifest) cachedManifestRef.current = rawManifest;
    const manifest = rawManifest ?? cachedManifestRef.current;
    const { downloadAndResolveArtifact, sessionId, isResponding, messages } = useChatContext();
    const { data: artifacts } = useChatArtifacts();
    const { data: remoteAgents } = useRemoteAgents();
    const [resolvedConfigs, setResolvedConfigs] = useState<Map<string, string>>(new Map());
    const [loadingSet, setLoadingSet] = useState<Set<string>>(new Set());
    const fetchingRef = useRef<Set<string>>(new Set());
    const resolvedVersionsRef = useRef<Map<string, number>>(new Map());
    const [fetchEpoch, setFetchEpoch] = useState(0);
    const lastSessionRef = useRef(sessionId);

    const pendingStreamingRef = useRef<Map<string, string>>(new Map());
    const flushTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

    const flushPendingStreaming = useCallback(() => {
        if (flushTimerRef.current) {
            clearTimeout(flushTimerRef.current);
            flushTimerRef.current = null;
        }
        if (pendingStreamingRef.current.size === 0) return;
        const pending = pendingStreamingRef.current;
        pendingStreamingRef.current = new Map();
        setResolvedConfigs(prev => {
            let changed = false;
            const next = new Map(prev);
            for (const [filename, content] of pending) {
                if (next.get(filename) !== content) {
                    next.set(filename, content);
                    changed = true;
                }
            }
            return changed ? next : prev;
        });
    }, []);

    const enqueueStreamingUpdate = useCallback(
        (filename: string, content: string, throttle: boolean) => {
            pendingStreamingRef.current.set(filename, content);
            if (!throttle) {
                flushPendingStreaming();
                return;
            }
            if (flushTimerRef.current === null) {
                flushTimerRef.current = setTimeout(() => {
                    flushTimerRef.current = null;
                    flushPendingStreaming();
                }, 250);
            }
        },
        [flushPendingStreaming]
    );

    const sessionJustChangedRef = useRef(false);
    if (sessionId !== lastSessionRef.current) {
        lastSessionRef.current = sessionId;
        setResolvedConfigs(new Map());
        setLoadingSet(new Set());
        fetchingRef.current = new Set();
        resolvedVersionsRef.current = new Map();
        cachedManifestRef.current = null;
        sessionJustChangedRef.current = true;
        pendingStreamingRef.current = new Map();
        if (flushTimerRef.current) {
            clearTimeout(flushTimerRef.current);
            flushTimerRef.current = null;
        }
    }

    useEffect(() => {
        if (!isResponding) flushPendingStreaming();
        return () => {
            if (flushTimerRef.current) {
                clearTimeout(flushTimerRef.current);
                flushTimerRef.current = null;
            }
        };
    }, [isResponding, flushPendingStreaming]);

    const configArtifacts = useMemo(() => artifacts?.filter((a: { mimeType: string }) => isBuilderConfigMime(a.mimeType)) ?? [], [artifacts]);

    const versionsKey = useMemo(() => configArtifacts.map((a: ArtifactInfo) => `${a.filename}:${a.version ?? 0}`).join(","), [configArtifacts]);

    const generatingFilenames = useMemo(() => deriveGeneratingFilenames(configArtifacts, manifest, isResponding, hasGenerationStarted(manifest, sessionId)), [configArtifacts, manifest, isResponding, sessionId]);

    const generatingFilename = useMemo(() => {
        const first = generatingFilenames.values().next();
        return first.done ? null : first.value;
    }, [generatingFilenames]);

    const isGeneratingConfigs = generatingFilenames.size > 0;

    useEffect(() => {
        if (sessionJustChangedRef.current) {
            sessionJustChangedRef.current = false;
            return;
        }

        if (!manifest?.components) return;

        const toFetch: Array<{ comp: NonNullable<BuildManifest["components"]>[number]; artifact: ArtifactInfo; versionAdvanced: boolean }> = [];

        for (const comp of manifest.components) {
            const decision = decideConfigResolve(
                configArtifacts.find((a: { filename: string }) => a.filename === comp.filename),
                resolvedConfigs.get(comp.filename),
                pendingStreamingRef.current.get(comp.filename),
                resolvedVersionsRef.current.get(comp.filename),
                fetchingRef.current.has(comp.filename)
            );
            if (decision.kind === "stream") {
                enqueueStreamingUpdate(comp.filename, decision.content, isResponding);
            } else if (decision.kind === "fetch") {
                toFetch.push({ comp, artifact: decision.artifact, versionAdvanced: decision.versionAdvanced });
            }
        }

        if (toFetch.length === 0) return;

        const filenames = toFetch.map(t => t.comp.filename);
        const firstLoads = toFetch.filter(t => !t.versionAdvanced).map(t => t.comp.filename);
        if (firstLoads.length > 0) {
            setLoadingSet(prev => {
                const next = new Set(prev);
                firstLoads.forEach(f => next.add(f));
                return next;
            });
        }
        filenames.forEach(f => fetchingRef.current.add(f));

        const launchSessionId = sessionId;
        Promise.all(
            toFetch.map(async ({ comp, artifact, versionAdvanced }) => {
                const content = await resolveArtifactContent(artifact, downloadAndResolveArtifact, sessionId);
                return { filename: comp.filename, content, version: artifact.version, recordVersionOnFailure: versionAdvanced };
            })
        ).then(results => {
            if (lastSessionRef.current !== launchSessionId) return;
            setResolvedConfigs(prev => applyFetchResults(prev, results, fetchingRef.current, resolvedVersionsRef.current));
            if (results.some(r => r.recordVersionOnFailure)) setFetchEpoch(n => n + 1);
            setLoadingSet(prev => {
                const next = new Set(prev);
                results.forEach(({ filename }) => next.delete(filename));
                return next;
            });
        });
    }, [manifest, configArtifacts, versionsKey, resolvedConfigs, downloadAndResolveArtifact, enqueueStreamingUpdate, isResponding, sessionId, fetchEpoch]);

    const directFetchFilenames = useMemo(() => {
        if (!sessionId || !manifest?.components) return [] as string[];
        const inList = new Set(configArtifacts.map((a: ArtifactInfo) => a.filename));
        return manifest.components.filter(c => c.origin !== "platform" && c.status !== "draft" && !isBlockedSessionComponent(c) && !inList.has(c.filename)).map(c => c.filename);
    }, [manifest, configArtifacts, sessionId]);

    const directConfigQueries = useQueries({
        queries: directFetchFilenames.map(filename => ({
            queryKey: builderKeys.manifestContent(sessionId, filename, "latest"),
            queryFn: () => getManifestContent(sessionId, filename, "latest"),
            enabled: !!sessionId && !isResponding,
        })),
    });

    const directConfigContent = useMemo(() => {
        const m = new Map<string, string>();
        directFetchFilenames.forEach((filename, i) => {
            const data = directConfigQueries[i]?.data;
            if (typeof data === "string") m.set(filename, data);
        });
        return m;
    }, [directFetchFilenames, directConfigQueries]);

    const directLoadingFilenames = useMemo(() => {
        const s = new Set<string>();
        directFetchFilenames.forEach((filename, i) => {
            if (directConfigQueries[i]?.isLoading) s.add(filename);
        });
        return s;
    }, [directFetchFilenames, directConfigQueries]);

    useEffect(() => {
        if (directConfigContent.size === 0) return;
        setResolvedConfigs(prev => {
            let changed = false;
            const next = new Map(prev);
            for (const [filename, content] of directConfigContent) {
                if (next.get(filename) !== content) {
                    next.set(filename, content);
                    changed = true;
                }
            }
            return changed ? next : prev;
        });
    }, [directConfigContent]);

    const resolvedFilenamesKey = useMemo(() => [...resolvedConfigs.keys()].join(","), [resolvedConfigs]);
    const resolvedConfigsRef = useRef(resolvedConfigs);
    resolvedConfigsRef.current = resolvedConfigs;

    const pendingInputComponentName = useMemo(() => {
        if (!messages || !manifest?.components) return null;
        for (let i = messages.length - 1; i >= 0; i--) {
            const msg = messages[i] as {
                userInputRequest?: {
                    responded?: boolean;
                    timedOut?: boolean;
                    cancelled?: boolean;
                    surface?: { dataModel?: Record<string, unknown> };
                };
            };
            const hil = msg.userInputRequest;
            if (!hil || hil.responded || hil.timedOut || hil.cancelled) continue;

            const componentName = hil.surface?.dataModel?.component_name;
            if (typeof componentName === "string" && componentName) {
                const match = manifest.components.find(c => c.name === componentName);
                if (match) return match.name;
            }

            const draftConnectors = manifest.components.filter(c => c.type === "connector" && c.status === "draft" && c.origin !== "platform");
            const withContent = draftConnectors.filter(c => resolvedConfigsRef.current.has(c.filename));
            if (withContent.length > 0) return withContent[withContent.length - 1].name;
            if (draftConnectors.length > 0) return draftConnectors[draftConnectors.length - 1].name;
            return null;
        }
        return null;
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [messages, manifest, resolvedFilenamesKey]);

    const hasPendingUserInput = pendingInputComponentName !== null;

    const createdNamesRef = useRef<Set<string>>(new Set());
    if (sessionJustChangedRef.current) {
        createdNamesRef.current = new Set();
    }
    for (const comp of manifest?.components ?? []) {
        const hasContent = !!resolvedConfigs.get(comp.filename) || comp.status === "validated" || comp.status === "deployed";
        if (hasContent) createdNamesRef.current.add(comp.name);
    }

    const componentEntryCacheRef = useRef<Map<string, ComponentCanvasData>>(new Map());
    if (sessionJustChangedRef.current) {
        componentEntryCacheRef.current = new Map();
    }

    const a2aFallbackByName = useMemo(() => {
        const map = new Map<string, CreateRemoteAgentRequest>();
        for (const ra of remoteAgents ?? []) {
            const entry: CreateRemoteAgentRequest = {
                agentUrl: ra.agentUrl,
                name: ra.name,
                agentCardLocation: ra.agentCardLocation,
                agentAuthConfig: { type: ra.agentAuthType || "none" } as AuthConfig,
                agentCardUrl: ra.agentCardUrl,
                description: ra.description,
            };
            for (const key of [ra.name, ra.instanceName]) {
                if (!key) continue;
                map.set(key, entry);
                map.set(normalizeComponentName(key), entry);
            }
        }
        return map;
    }, [remoteAgents]);

    const buildFinalizing = isResponding && manifest?.full_validation_passed !== true;

    const artifactVersions = useArtifactVersions(manifest);
    const changedKey = [...validationStampState(manifest, artifactVersions).changed].sort((a, b) => a.localeCompare(b)).join("\n");
    const changedSinceValidation = useMemo(() => new Set(changedKey === "" ? [] : changedKey.split("\n")), [changedKey]);

    const componentData = useMemo(() => {
        if (!manifest) return new Map<string, ComponentCanvasData>();
        const mergedLoading = directLoadingFilenames.size === 0 ? loadingSet : new Set([...loadingSet, ...directLoadingFilenames]);
        return buildComponentData(
            manifest,
            resolvedConfigs,
            mergedLoading,
            generatingFilenames,
            isResponding,
            buildFinalizing,
            isGeneratingConfigs,
            changedSinceValidation,
            pendingInputComponentName,
            createdNamesRef.current,
            a2aFallbackByName,
            componentEntryCacheRef.current
        );
    }, [manifest, resolvedConfigs, loadingSet, directLoadingFilenames, generatingFilenames, isResponding, buildFinalizing, isGeneratingConfigs, changedSinceValidation, pendingInputComponentName, a2aFallbackByName]);

    return { componentData, manifest, generatingFilename, generatingFilenames, isGeneratingConfigs, hasPendingUserInput };
}
