// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import yaml from "js-yaml";

import { useChatArtifacts } from "@/api/artifacts";
import { builderKeys } from "@/api/builder";

import { useChatContext } from "@/providers/contexts";

import { workflowBody } from "@/utils/agentUtils";

import type { ConfigType } from "./builderUtils";
import { detectConfigType, extractConfigName, isBuilderConfigMime, SAM_CONNECTOR_CONFIG_MIME_TYPE } from "./builderUtils";
import { groupComponents } from "./canvas";
import { fetchArtifactAtVersion, fetchArtifactDirect, resolveArtifactContent } from "./hooks/artifactUtils";
import type { ComponentCanvasData } from "./hooks/useBuilderCanvasData";
import { useBuilderCanvasData } from "./hooks/useBuilderCanvasData";
import type { BuilderConfigState } from "./hooks/useBuilderConfig";
import { useBuilderConfig } from "./hooks/useBuilderConfig";
import type { BuilderManifestState } from "./hooks/useBuilderManifest";
import { useBuilderManifest } from "./hooks/useBuilderManifest";
import { usePendingAutoCreate } from "./hooks/usePendingAutoCreate";
import { type ConfigReadOptions, type FetchConfigByFilename } from "./serverApply";
import { isBuildInterruptedOnCanvas } from "./workflowChildStatus";

export type ChatPanelMode = "fullscreen" | "split" | "floating" | "minimized";

export interface BuilderPageState {
    config: BuilderConfigState;
    manifest: BuilderManifestState;
    componentData: Map<string, ComponentCanvasData>;
    selectedNodeId: string | null;
    setSelectedNodeId: (id: string | null) => void;
    handleNodeDoubleClick: (nodeId: string) => void;
    activeYaml: string | null;
    activeType: ConfigType;
    rootName: string;
    configType: ConfigType;
    chatPanelMode: ChatPanelMode;
    setChatPanelMode: (mode: ChatPanelMode) => void;
    preMinimizeMode: ChatPanelMode;
    hasCanvasContent: boolean;
    configCount: number;
    handleConnectorUpdated: (updatedJson: string) => void;
    findConfigByAgentName: (agentName: string) => Promise<string | null>;
    fetchConfigByFilename: FetchConfigByFilename;
    detailDialogOpen: boolean;
    setDetailDialogOpen: (open: boolean) => void;
    selectedComponentData: ComponentCanvasData | null;
    selectedWorkflowRole: { workflowName: string; node: Record<string, unknown> | null } | null;
    pendingAutoCreate: boolean;
    setPendingAutoCreate: (pending: boolean) => void;
    isSessionTransitioning: boolean;

    builderActiveTab: BuilderActiveTab;
    setBuilderActiveTab: (tab: BuilderActiveTab) => void;

    testTargetName: string | null;
    setTestTargetName: (name: string | null) => void;

    testRunning: boolean;
    setTestRunning: (running: boolean) => void;

    stopTestRef: React.MutableRefObject<(() => void) | null>;

    latestTestRunTaskId: string | null;
    setLatestTestRunTaskId: (id: string | null) => void;

    selectedTestActivityRun: { runNumber: number; taskId: string } | null;
    setSelectedTestActivityRun: (sel: { runNumber: number; taskId: string } | null) => void;

    isApplyingChanges: boolean;
    setIsApplyingChanges: (applying: boolean) => void;

    buildInterrupted: boolean;
    setHostBuildInterrupted: (interrupted: boolean) => void;
}

export type BuilderActiveTab = "build" | "test";

export function useBuilderPageState(): BuilderPageState {
    const config = useBuilderConfig();
    const manifest = useBuilderManifest();
    const { componentData } = useBuilderCanvasData();
    const { downloadAndResolveArtifact, sessionId, uploadArtifactFile } = useChatContext();
    const { data: artifacts } = useChatArtifacts();
    const queryClient = useQueryClient();

    const [selectedNodeId, setSelectedNodeId] = useState<string | null>(null);
    const [detailDialogOpen, setDetailDialogOpen] = useState(false);
    const [selectedWorkflowRole, setSelectedWorkflowRole] = useState<{ workflowName: string; node: Record<string, unknown> | null } | null>(null);
    const [pendingAutoCreate, setPendingAutoCreate] = usePendingAutoCreate(sessionId);

    const [builderActiveTab, setBuilderActiveTab] = useState<BuilderActiveTab>("build");
    const [testTargetName, setTestTargetName] = useState<string | null>(null);
    const [testRunning, setTestRunning] = useState(false);
    const stopTestRef = useRef<(() => void) | null>(null);
    const [latestTestRunTaskId, setLatestTestRunTaskId] = useState<string | null>(null);
    const [selectedTestActivityRun, setSelectedTestActivityRun] = useState<{
        runNumber: number;
        taskId: string;
    } | null>(null);
    const [isApplyingChanges, setIsApplyingChanges] = useState(false);
    const [hostBuildInterrupted, setHostBuildInterrupted] = useState(false);
    const buildInterrupted = useMemo(() => isBuildInterruptedOnCanvas(hostBuildInterrupted, componentData.values()), [hostBuildInterrupted, componentData]);

    const [chatPanelMode, setChatPanelModeRaw] = useState<ChatPanelMode>("fullscreen");
    const preMinimizeModeRef = useRef<ChatPanelMode>("split");

    const setChatPanelMode = useCallback((mode: ChatPanelMode) => {
        setChatPanelModeRaw(prev => {
            if (prev !== "minimized") {
                preMinimizeModeRef.current = prev;
            }
            return mode;
        });
    }, []);

    const hasAutoTransitioned = useRef(false);

    const lastSessionRef = useRef(sessionId);
    const artifactsSettledRef = useRef(true);
    const needsSessionReset = sessionId !== lastSessionRef.current;
    if (needsSessionReset) {
        lastSessionRef.current = sessionId;
        setSelectedNodeId(null);
        setSelectedWorkflowRole(null);
        setDetailDialogOpen(false);
        setChatPanelModeRaw("fullscreen");
        setBuilderActiveTab("build");
        setTestTargetName(null);
        setTestRunning(false);
        stopTestRef.current = null;
        setLatestTestRunTaskId(null);
        setSelectedTestActivityRun(null);
        setIsApplyingChanges(false);
        setHostBuildInterrupted(false);
        hasAutoTransitioned.current = false;
        artifactsSettledRef.current = false;
    }

    const configArtifacts = useMemo(() => artifacts?.filter((a: { mimeType: string }) => isBuilderConfigMime(a.mimeType)) ?? [], [artifacts]);

    const configsSeenRef = useRef(0);
    if (needsSessionReset) configsSeenRef.current = 0;
    if (configArtifacts.length > configsSeenRef.current) configsSeenRef.current = configArtifacts.length;
    const configCount = configsSeenRef.current;

    const renderableGroupCount = useMemo(() => groupComponents(manifest.manifest, componentData).length, [manifest.manifest, componentData]);
    const hasCanvasContent = configArtifacts.length > 0 || renderableGroupCount > 0;

    if (!needsSessionReset && !artifactsSettledRef.current && ((configArtifacts.length === 0 && !manifest.manifest) || manifest.manifest)) {
        artifactsSettledRef.current = true;
    }

    useEffect(() => {
        if (!artifactsSettledRef.current) return;
        const hasContent = configArtifacts.length > 0 || renderableGroupCount > 0;
        if (chatPanelMode === "fullscreen" && hasContent && !hasAutoTransitioned.current) {
            hasAutoTransitioned.current = true;
            setChatPanelMode("split");
        }
    }, [chatPanelMode, configArtifacts.length, renderableGroupCount, setChatPanelMode]);

    const fetchConfigByFilename = useCallback(
        async (filename: string, options?: ConfigReadOptions): Promise<string | null> => {
            if (options?.version !== undefined) return sessionId ? fetchArtifactAtVersion(sessionId, filename, options.version) : null;
            const artifact = configArtifacts.find((a: { filename: string }) => a.filename === filename);
            if (!artifact) {
                return sessionId ? fetchArtifactDirect(sessionId, filename) : null;
            }
            return resolveArtifactContent(artifact, downloadAndResolveArtifact, sessionId, options);
        },
        [configArtifacts, downloadAndResolveArtifact, sessionId]
    );

    const findConfigByAgentName = useCallback(
        async (agentName: string): Promise<string | null> => {
            const component = manifest.manifest?.components?.find(c => c.name === agentName || c.name.toLowerCase() === agentName.toLowerCase());
            if (component) {
                const yaml = await fetchConfigByFilename(component.filename);
                if (yaml) return yaml;
            }

            for (const artifact of configArtifacts) {
                const yaml = await resolveArtifactContent(artifact, downloadAndResolveArtifact, sessionId);
                if (yaml) {
                    const name = extractConfigName(yaml);
                    if (name === agentName || name.toLowerCase() === agentName.toLowerCase()) {
                        return yaml;
                    }
                }
            }

            return null;
        },
        [manifest.manifest, configArtifacts, fetchConfigByFilename, downloadAndResolveArtifact]
    );

    const handleNodeDoubleClick = useCallback(
        (nodeId: string) => {
            let resolvedId = nodeId;
            let roleContext: { workflowName: string; node: Record<string, unknown> | null } | null = null;

            if (nodeId.startsWith("wfp:")) {
                resolvedId = nodeId.split(":").pop() ?? nodeId;
            } else if (nodeId.startsWith("wf:")) {
                const parts = nodeId.split(":");
                const wfName = parts[1];
                const nodeKey = parts.slice(2).join(":");
                if (nodeKey === "__start__" && wfName) {
                    resolvedId = wfName;
                } else if (wfName) {
                    const wfComp = manifest.manifest?.components?.find(c => c.name === wfName);
                    const matchedNode = wfComp?.nodes?.find(n => n.id === nodeKey);
                    if (matchedNode?.agent_name) {
                        resolvedId = matchedNode.agent_name;
                        const wfYaml = componentData.get(wfName)?.yamlText ?? null;
                        let fullNode: Record<string, unknown> | null = null;
                        if (wfYaml) {
                            try {
                                const parsed = yaml.load(wfYaml, { json: true }) as Record<string, unknown>;
                                const apps = parsed?.apps as Array<Record<string, unknown>> | undefined;
                                const appConfig = apps?.[0]?.app_config as Record<string, unknown> | undefined;
                                const nodes = (workflowBody(appConfig).nodes as Array<Record<string, unknown>> | undefined) ?? [];
                                fullNode = nodes.find(n => n.id === nodeKey) ?? null;
                            } catch {
                                fullNode = null;
                            }
                        }
                        roleContext = {
                            workflowName: wfName,
                            node: fullNode ?? (matchedNode as unknown as Record<string, unknown>),
                        };
                    } else {
                        return;
                    }
                } else {
                    return;
                }
            }
            if (nodeId.startsWith("__")) return;

            const compData = componentData.get(resolvedId);
            if (!compData) return;
            if (!compData.yamlText && !(compData.origin === "platform" && compData.type === "agent")) return;

            setSelectedNodeId(resolvedId);
            setSelectedWorkflowRole(roleContext);
            setDetailDialogOpen(true);
        },
        [componentData, manifest.manifest]
    );

    const handleConnectorUpdated = useCallback(
        (updatedJson: string) => {
            let filename: string | null = null;
            if (selectedNodeId) {
                const data = componentData.get(selectedNodeId);
                if (data) filename = data.filename;
            }
            if (!filename && config.artifactInfo?.filename) {
                filename = config.artifactInfo.filename;
            }

            if (filename) {
                const targetFilename = filename;
                const file = new File([updatedJson], targetFilename, { type: SAM_CONNECTOR_CONFIG_MIME_TYPE });
                uploadArtifactFile(file, undefined, undefined, true)
                    .then(() => (sessionId ? queryClient.invalidateQueries({ queryKey: [...builderKeys.manifests(), "content", sessionId, targetFilename] }) : undefined))
                    .catch(() => {});
            }
        },
        [selectedNodeId, componentData, config.artifactInfo, uploadArtifactFile, sessionId, queryClient]
    );

    const { configYaml, configType } = config;

    const selectedComponentData = selectedNodeId ? (componentData.get(selectedNodeId) ?? null) : null;

    const selectedData = selectedComponentData;
    const activeYaml = selectedData?.yamlText ?? configYaml;
    const activeType = selectedData?.type ?? (configYaml ? detectConfigType(configYaml) : configType);

    const derivedRootName = manifest.manifest?.display_name || manifest.manifest?.name || (configYaml ? extractConfigName(configYaml) : "New Build");
    const rootName = !artifactsSettledRef.current ? "New Build" : derivedRootName;

    return {
        config,
        manifest,
        componentData,
        selectedNodeId,
        setSelectedNodeId,
        handleNodeDoubleClick,
        activeYaml,
        activeType,
        rootName,
        configType,
        chatPanelMode,
        setChatPanelMode,
        preMinimizeMode: preMinimizeModeRef.current,
        hasCanvasContent,
        configCount,
        handleConnectorUpdated,
        findConfigByAgentName,
        fetchConfigByFilename,
        detailDialogOpen,
        setDetailDialogOpen,
        selectedComponentData,
        selectedWorkflowRole,
        pendingAutoCreate,
        setPendingAutoCreate,
        isSessionTransitioning: !artifactsSettledRef.current,
        builderActiveTab,
        setBuilderActiveTab,
        testTargetName,
        setTestTargetName,
        testRunning,
        setTestRunning,
        stopTestRef,
        latestTestRunTaskId,
        setLatestTestRunTaskId,
        selectedTestActivityRun,
        setSelectedTestActivityRun,
        isApplyingChanges,
        setIsApplyingChanges,
        buildInterrupted,
        setHostBuildInterrupted,
    };
}
