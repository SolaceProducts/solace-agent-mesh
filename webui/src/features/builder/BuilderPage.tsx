// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type FormEvent, useCallback, useEffect, useRef, useState } from "react";
import { AlertTriangle, MessageCircle } from "lucide-react";

import { useAgentCards } from "@/api/agent-cards";
import { useAgents } from "@/api/agents";
import { lookupTestAgentParent } from "@/api/builder";
import { api } from "@/api/client";

import { useBuilderContext, useChatContext } from "@/providers/contexts";

import { reportAndNotifyError } from "@/common/product-analytics/reportAndNotifyError";
import { reportOncePerSession } from "@/common/product-analytics/reportOncePerSession";

import { isEphemeralTestAgentName } from "@/utils/agentUtils";
import { clearPendingPrefill, stashPendingPrefill } from "@/utils/redirectPrefill";
import { SESSION_STORAGE_KEYS } from "@/utils/sessionStorageKeys";

import { Button } from "@/ui/button";
import type { MessageChip } from "@/types/chat";

import { useIsMobile } from "@/hooks";

import { BuilderActions } from "./BuilderActions";
import { BuilderArtifactPreviewModal } from "./BuilderArtifactPreviewModal";
import { BuilderCanvas } from "./BuilderCanvas";
import { BuilderChatPanel } from "./BuilderChatPanel";
import { BuilderPageHeader } from "./BuilderPageHeader";
import { useBuilderSessionRestore } from "./builderSessionRestore";
import { ComponentDetailDialog } from "./detail/ComponentDetailDialog";
import { CANVAS_PANE_SIZING } from "./hooks/builderSplitLayout";
import { manifestWarning } from "./hooks/configsWithoutManifest";
import { useArtifactVersions } from "./hooks/useArtifactVersions";
import { useAutoFullValidation } from "./hooks/useAutoFullValidation";
import { useAutoRequestManifest } from "./hooks/useAutoRequestManifest";
import { useBuildActivateBridge } from "./hooks/useBuildActivateBridge";
import { useBuilderSplitLayout } from "./hooks/useBuilderSplitLayout";
import { useComponentsMissingCredentials } from "./hooks/useComponentsMissingCredentials";
import { useDroppedResources } from "./hooks/useDroppedResources";
import { TestActivityView } from "./TestActivityView";
import { TestModeCanvasHeader } from "./TestModeCanvasHeader";
import { pickDefaultTestTarget } from "./TestModePanel";
import { useBuilderPageState } from "./useBuilderPageState";

function parseRedirectChip(chipRaw: string | null): MessageChip | null {
    if (!chipRaw) return null;
    try {
        const parsed: unknown = JSON.parse(chipRaw);
        if (parsed && typeof parsed === "object" && typeof (parsed as { label?: unknown }).label === "string") {
            const { label, emoji, kind } = parsed as { label: string; emoji?: unknown; kind?: unknown };
            return {
                label,
                ...(typeof emoji === "string" ? { emoji } : {}),
                ...(typeof kind === "string" ? { kind } : {}),
            };
        }
        console.warn("[BuilderPage] Dropping redirect chip: missing string label");
    } catch {
        console.warn("[BuilderPage] Dropping redirect chip: malformed JSON");
    }
    return null;
}

const BUILDER_SESSION_LOAD_FAILED = "Failed to load the builder session. Please try again.";

export function BuilderPage() {
    const state = useBuilderPageState();
    const { refetch: userAgentsRefetch } = useAgents();
    const { refetch: yamlAgentCardsRefetch } = useAgentCards();
    const { setBuilderMode, setSelectedBuilderNode } = useBuilderContext();

    useEffect(() => {
        reportOncePerSession("builder:opened");
        setBuilderMode(true);
        return () => {
            setBuilderMode(false);
            setSelectedBuilderNode(null);
        };
    }, [setBuilderMode, setSelectedBuilderNode]);

    const componentDataRef = useRef(state.componentData);
    componentDataRef.current = state.componentData;
    useEffect(() => {
        if (state.selectedNodeId) {
            const data = componentDataRef.current.get(state.selectedNodeId);
            setSelectedBuilderNode({ type: data?.type ?? "unknown", name: state.selectedNodeId });
        } else {
            setSelectedBuilderNode(null);
        }
    }, [state.selectedNodeId, setSelectedBuilderNode]);

    const { sessionId: currentChatSessionId, handleSwitchSession, handleSubmit, setSelectedAgentName, addNotification } = useChatContext();
    const { isLoading: agentsLoading } = useAgentCards();
    const redirectAppliedRef = useRef(false);
    useBuilderSessionRestore(agentsLoading, redirectAppliedRef);
    const pendingArtifactsRef = useRef(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_PENDING_ARTIFACTS));

    const pendingBuilderSessionRef = useRef(sessionStorage.getItem("sam_agent_session_redirect"));
    useEffect(() => {
        if (!pendingBuilderSessionRef.current || agentsLoading) return;
        const sid = pendingBuilderSessionRef.current;
        pendingBuilderSessionRef.current = null;
        sessionStorage.removeItem("sam_agent_session_redirect");
        redirectAppliedRef.current = true;
        handleSwitchSession(sid);
    }, [agentsLoading, handleSwitchSession]);

    const [pinnedTestSessionId, setPinnedTestSessionId] = useState<string | null>(null);
    const pendingTestPinTargetRef = useRef<{ builderSessionId: string; componentName: string } | null>(null);

    const restorePinnedTestSession = useCallback(
        async (testSid: string) => {
            try {
                redirectAppliedRef.current = true;
                const sessRes = (await api.webui.get(`/api/v1/sessions/${testSid}`)) as { agentId?: string } | null;
                const agentName = sessRes?.agentId;
                if (!agentName) return;
                const parent = await lookupTestAgentParent(agentName);
                if (!parent) return;
                pendingTestPinTargetRef.current = {
                    builderSessionId: parent.builderSessionId,
                    componentName: parent.componentName,
                };
                setPinnedTestSessionId(testSid);
                await handleSwitchSession(parent.builderSessionId);
            } catch (err) {
                console.warn("[builder] test-session restore failed", err);
                pendingTestPinTargetRef.current = null;
                setPinnedTestSessionId(null);
            }
        },
        [handleSwitchSession]
    );

    const pendingTestSessionPinRef = useRef(sessionStorage.getItem("sam_test_session_pin"));
    useEffect(() => {
        if (!pendingTestSessionPinRef.current || agentsLoading) return;
        const testSid = pendingTestSessionPinRef.current;
        pendingTestSessionPinRef.current = null;
        sessionStorage.removeItem("sam_test_session_pin");
        void restorePinnedTestSession(testSid);
    }, [agentsLoading, restorePinnedTestSession]);

    useEffect(() => {
        const target = pendingTestPinTargetRef.current;
        if (!target) return;
        if (currentChatSessionId !== target.builderSessionId) return;
        pendingTestPinTargetRef.current = null;
        state.setTestTargetName(target.componentName);
        state.setBuilderActiveTab("test");
    }, [currentChatSessionId, state]);

    useEffect(() => {
        const handler = (e: Event) => {
            const name = (e as CustomEvent).detail?.name;
            if (typeof name === "string" && name) state.handleNodeDoubleClick(name);
        };
        window.addEventListener("builder-open-component-detail", handler);
        return () => window.removeEventListener("builder-open-component-detail", handler);
    }, [state]);

    useEffect(() => {
        const checkRedirect = () => {
            setTimeout(() => {
                const pinSid = sessionStorage.getItem("sam_test_session_pin");
                if (pinSid) {
                    sessionStorage.removeItem("sam_test_session_pin");
                    void restorePinnedTestSession(pinSid);
                    return;
                }
                const sid = sessionStorage.getItem("sam_agent_session_redirect");
                if (sid) {
                    sessionStorage.removeItem("sam_agent_session_redirect");
                    handleSwitchSession(sid);
                }
            }, 0);
        };
        const handleAgentNav = (e: Event) => {
            const detail = (e as CustomEvent).detail as { sessionId: string; agentId?: string; isEphemeralTest?: boolean };
            if (detail.isEphemeralTest === true || isEphemeralTestAgentName(detail.agentId)) {
                void restorePinnedTestSession(detail.sessionId);
            }
        };
        document.addEventListener("click", checkRedirect);
        window.addEventListener("agent-session-navigate", handleAgentNav);
        return () => {
            document.removeEventListener("click", checkRedirect);
            window.removeEventListener("agent-session-navigate", handleAgentNav);
        };
    }, [handleSwitchSession, restorePinnedTestSession]);

    useEffect(() => {
        if (redirectAppliedRef.current || agentsLoading) return;

        const redirectContext = sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_CONTEXT);
        if (!redirectContext) return;

        redirectAppliedRef.current = true;
        sessionStorage.removeItem(SESSION_STORAGE_KEYS.REDIRECT_CONTEXT);

        const redirectSessionId = sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_SESSION_ID);
        const autoSubmit = sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_AUTO_SUBMIT) === "true";
        const displayHtml = sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_DISPLAY_HTML);
        const chipRaw = sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_CHIP);
        const pendingArtifactsRaw = pendingArtifactsRef.current;
        sessionStorage.removeItem(SESSION_STORAGE_KEYS.REDIRECT_SESSION_ID);
        sessionStorage.removeItem(SESSION_STORAGE_KEYS.REDIRECT_AUTO_SUBMIT);
        sessionStorage.removeItem(SESSION_STORAGE_KEYS.REDIRECT_DISPLAY_HTML);
        sessionStorage.removeItem(SESSION_STORAGE_KEYS.REDIRECT_CHIP);
        sessionStorage.removeItem(SESSION_STORAGE_KEYS.REDIRECT_PENDING_ARTIFACTS);

        const chip = parseRedirectChip(chipRaw);

        const applyArtifacts = async (sessionId: string) => {
            if (!pendingArtifactsRaw) return;
            let pending: { destSessionId?: string; sourceSessionId: string; filenames: string[] };
            try {
                pending = JSON.parse(pendingArtifactsRaw);
            } catch {
                console.warn("[BuilderPage] Dropping pending redirect artifacts: malformed JSON");
                addNotification("Could not carry over the attached artifacts.", "warning");
                return;
            }
            if (!pending?.sourceSessionId || !Array.isArray(pending.filenames)) {
                console.warn("[BuilderPage] Dropping pending redirect artifacts: missing sourceSessionId or filenames");
                addNotification("Could not carry over the attached artifacts.", "warning");
                return;
            }
            if (pending.destSessionId && pending.destSessionId !== sessionId) return;
            await api.webui.post("/api/v1/sessions", { id: sessionId });
            await api.webui.post(`/api/v1/sessions/${sessionId}/artifacts/copy`, {
                sourceSessionId: pending.sourceSessionId,
                filenames: pending.filenames,
            });
        };

        if (autoSubmit) {
            (async () => {
                try {
                    if (redirectSessionId) {
                        await applyArtifacts(redirectSessionId);
                        await handleSwitchSession(redirectSessionId);
                    }
                    setSelectedAgentName("Builder");
                    const fakeEvent = { preventDefault: () => {} } as FormEvent;
                    await handleSubmit(fakeEvent, null, redirectContext, redirectSessionId, displayHtml, null, null, null, { agent_name: "Builder", ...(chip ? { chip } : {}) });
                } catch (err) {
                    console.error("[BuilderPage] Builder redirect auto-submit failed:", err);
                    reportAndNotifyError(addNotification, BUILDER_SESSION_LOAD_FAILED, "api");
                }
            })();
            return;
        }

        stashPendingPrefill(redirectContext, redirectSessionId);
        if (redirectSessionId) {
            (async () => {
                try {
                    await applyArtifacts(redirectSessionId);
                    const landed = await handleSwitchSession(redirectSessionId);
                    if (!landed) clearPendingPrefill();
                } catch (err) {
                    clearPendingPrefill();
                    console.error("[BuilderPage] Builder redirect session restore failed:", err);
                    reportAndNotifyError(addNotification, BUILDER_SESSION_LOAD_FAILED, "api");
                }
            })();
        }
    }, [agentsLoading, handleSwitchSession, handleSubmit, setSelectedAgentName, addNotification]);

    const { stuck: buildStuckWithoutManifest, unreadable: manifestUnreadable, requestAgain: requestManifestAgain } = useAutoRequestManifest(state.configCount, state.manifest);
    const { validationStuck, retryValidation } = useAutoFullValidation();

    const manifestReady = state.manifest.manifest?.full_validation_passed === true;
    const hasActiveConfig = state.activeYaml && (state.activeType === "agent" || state.activeType === "workflow" || state.activeType === "connector" || state.activeType === "skill");
    const showActions = hasActiveConfig || manifestReady;
    const componentsMissingCreds = useComponentsMissingCredentials(state.manifest.manifest, state.componentData);

    const { buildActivate, platformCheck, deployFailure, handleBuildActivateApiChange, handleBuildActivate, handleRedeploy, handleCheckAgain, handleAskBuilder } = useBuildActivateBridge();
    const artifactVersions = useArtifactVersions(state.manifest.manifest);
    const { view: dropped, remove: removeDropped } = useDroppedResources({ manifest: state.manifest.manifest, fetchConfigByFilename: state.fetchConfigByFilename, versions: artifactVersions });
    const { chatPanelMode, setChatPanelMode, preMinimizeMode, hasCanvasContent, isSessionTransitioning } = state;
    const isMobile = useIsMobile();
    const {
        rowRef,
        effectiveMode: effectiveChatPanelMode,
        canvasViewToggle,
    } = useBuilderSplitLayout({
        sessionId: currentChatSessionId,
        chatPanelMode,
        isMobile,
        isSessionTransitioning,
        hasCanvasContent,
    });
    const isEmpty = isSessionTransitioning || (!state.config.configYaml && !hasCanvasContent);

    const testHeaderTarget = (() => {
        if (state.builderActiveTab !== "test") return null;
        const comps = state.manifest.manifest?.components ?? [];
        const sessionComps = comps.filter(c => c.origin !== "platform");
        if (state.testTargetName) {
            const m = comps.find(c => c.name === state.testTargetName);
            return {
                name: state.testTargetName,
                kind: m?.type === "workflow" ? ("workflow" as const) : ("agent" as const),
            };
        }
        const wf = sessionComps.find(c => c.type === "workflow");
        if (wf) return { name: wf.name, kind: "workflow" as const };
        const ag = sessionComps.find(c => c.type === "agent");
        if (ag) return { name: ag.name, kind: "agent" as const };
        return null;
    })();

    const effectiveTestTargetName = state.builderActiveTab === "test" ? (state.testTargetName ?? pickDefaultTestTarget(state.manifest.manifest?.components)?.name ?? null) : null;

    const showTestHeader = !!testHeaderTarget && state.testRunning && !state.selectedTestActivityRun;
    const showTestActivityOverlay = state.builderActiveTab === "test" && (state.testRunning || !!state.selectedTestActivityRun);
    const canvasWithActions = (
        <div data-testid="builder-canvas-pane" className="relative flex min-h-0 w-full flex-1 flex-col" style={CANVAS_PANE_SIZING}>
            {showTestHeader && (
                <TestModeCanvasHeader
                    testTargetName={testHeaderTarget!.name}
                    testTargetKind={testHeaderTarget!.kind}
                    running={state.testRunning}
                    onStop={() => {
                        const stop = state.stopTestRef.current;
                        if (stop) {
                            try {
                                stop();
                            } catch {}
                        }
                    }}
                />
            )}
            <div className="relative min-h-0 flex-1">
                <BuilderCanvas
                    manifest={state.manifest.manifest}
                    componentData={state.componentData}
                    selectedNodeId={state.selectedNodeId}
                    onNodeSelect={nodeId => {
                        if (nodeId) state.handleNodeDoubleClick(nodeId);
                    }}
                    onNodeDoubleClick={state.handleNodeDoubleClick}
                    isEmpty={isEmpty}
                    isLoading={state.config.isLoading}
                    isVisible={effectiveChatPanelMode !== "fullscreen"}
                    builderActiveTab={state.builderActiveTab}
                    testTargetName={effectiveTestTargetName}
                    onSelectTestTarget={state.setTestTargetName}
                    isApplyingChanges={state.isApplyingChanges}
                    buildInterrupted={state.buildInterrupted}
                    hideControls={showTestActivityOverlay}
                />
                {validationStuck && !manifestUnreadable && (
                    <div role="alert" className="absolute top-4 left-1/2 z-30 flex max-w-md -translate-x-1/2 items-center gap-3 rounded-lg border border-(--warning-wMain) bg-(--warning-w10) px-4 py-2.5 shadow-md">
                        <AlertTriangle className="size-4 shrink-0 text-(--warning-wMain)" />
                        <span className="text-sm text-(--secondary-text-wMain)">Couldn't confirm full validation — deploy stays disabled until it succeeds.</span>
                        <Button variant="ghost" size="sm" onClick={retryValidation}>
                            Re-run validation
                        </Button>
                    </div>
                )}
                {showTestActivityOverlay && (
                    <TestActivityView
                        taskId={state.selectedTestActivityRun ? state.selectedTestActivityRun.taskId : (state.latestTestRunTaskId ?? null)}
                        runNumber={state.selectedTestActivityRun?.runNumber ?? null}
                        isLive={state.testRunning && !state.selectedTestActivityRun}
                        onClose={() => state.setSelectedTestActivityRun(null)}
                    />
                )}
            </div>
            {showActions && (
                <div className="absolute right-4 bottom-4 z-30">
                    <BuilderActions
                        configYaml={state.activeYaml ?? ""}
                        configType={state.activeType}
                        manifest={state.manifest.manifest}
                        componentsMissingCreds={componentsMissingCreds}
                        onBuildActivateApiChange={handleBuildActivateApiChange}
                        saveManifest={state.manifest.saveManifest}
                        fetchConfigByFilename={state.fetchConfigByFilename}
                        userAgentsRefetch={async () => {
                            await userAgentsRefetch();
                        }}
                        yamlAgentCardsRefetch={yamlAgentCardsRefetch}
                        autoSave={state.pendingAutoCreate}
                        onAutoSaveComplete={() => state.setPendingAutoCreate(false)}
                    />
                </div>
            )}
        </div>
    );

    return (
        <div className="flex h-full flex-col">
            <BuilderPageHeader state={state} canvasViewToggle={canvasViewToggle} />

            <div ref={rowRef} className="relative flex min-h-0 flex-1">
                <div data-testid="builder-chat-wrapper" className={effectiveChatPanelMode === "minimized" ? "hidden" : "contents"}>
                    <BuilderChatPanel
                        mode={effectiveChatPanelMode}
                        onModeChange={setChatPanelMode}
                        onPlanApproved={() => state.setPendingAutoCreate(true)}
                        builderActiveTab={state.builderActiveTab}
                        onBuilderActiveTabChange={state.setBuilderActiveTab}
                        testTargetName={state.testTargetName}
                        testRunning={state.testRunning}
                        onTestRunningChange={state.setTestRunning}
                        stopTestRef={state.stopTestRef}
                        onLatestRunTaskIdChange={state.setLatestTestRunTaskId}
                        selectedTestActivityRun={state.selectedTestActivityRun}
                        onSelectTestActivityRun={state.setSelectedTestActivityRun}
                        pinnedTestSessionId={pinnedTestSessionId}
                        onPinnedTestSessionApplied={() => setPinnedTestSessionId(null)}
                        isApplyingChanges={state.isApplyingChanges}
                        onApplyingChangesChange={state.setIsApplyingChanges}
                        onBuildInterruptedChange={state.setHostBuildInterrupted}
                        componentsMissingCreds={componentsMissingCreds}
                        canBuildActivate={buildActivate.can}
                        isBuildActivating={buildActivate.busy}
                        onBuildActivate={handleBuildActivate}
                        platformCheck={platformCheck}
                        onCheckAgain={handleCheckAgain}
                        onAskBuilder={handleAskBuilder}
                        deployFailure={deployFailure}
                        onRedeploy={handleRedeploy}
                        dropped={dropped}
                        onRemoveDropped={removeDropped}
                    />
                </div>

                {(buildStuckWithoutManifest || manifestUnreadable) && (
                    <div role="alert" className="absolute top-4 left-1/2 z-40 flex max-w-md -translate-x-1/2 items-center gap-3 rounded-lg border border-(--warning-wMain) bg-(--warning-w10) px-4 py-2.5 shadow-md">
                        <AlertTriangle className="size-4 shrink-0 text-(--warning-wMain)" />
                        <span className="text-sm text-(--secondary-text-wMain)">{manifestWarning(manifestUnreadable, !!state.manifest.manifest)}</span>
                        <Button variant="ghost" size="sm" onClick={requestManifestAgain}>
                            Ask the builder
                        </Button>
                    </div>
                )}

                <div className={effectiveChatPanelMode === "fullscreen" ? "hidden" : "contents"}>{canvasWithActions}</div>

                {effectiveChatPanelMode === "minimized" && !canvasViewToggle && (
                    <Button
                        size="icon"
                        onClick={() => setChatPanelMode(preMinimizeMode === "minimized" || preMinimizeMode === "fullscreen" ? "split" : preMinimizeMode)}
                        className="absolute bottom-4 left-4 z-20 flex h-12 w-12 items-center justify-center rounded-full shadow-lg transition-transform hover:scale-105"
                        style={{ backgroundColor: "var(--brand-wMain)", color: "white" }}
                        title="Open chat (⌘J)"
                        aria-label="Open chat"
                    >
                        <MessageCircle className="h-5 w-5" />
                    </Button>
                )}
            </div>

            <BuilderArtifactPreviewModal />

            {state.detailDialogOpen && state.selectedComponentData && (
                <ComponentDetailDialog
                    data={state.selectedComponentData}
                    isOpen={state.detailDialogOpen}
                    onClose={() => {
                        state.setDetailDialogOpen(false);
                        state.setSelectedNodeId(null);
                    }}
                    manifest={state.manifest.manifest}
                    componentData={state.componentData}
                    workflowRole={state.selectedWorkflowRole}
                />
            )}
        </div>
    );
}
