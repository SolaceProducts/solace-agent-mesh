// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import "./canvas/canvasStyles.css";

import { type CSSProperties, type FormEvent, Fragment, type MouseEvent as ReactMouseEvent, type MutableRefObject, type ReactNode, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { useBooleanFlagDetails } from "@openfeature/react-sdk";
import { Bot, GripVertical, Network, Workflow } from "lucide-react";

import { useAgentCards } from "@/api/agent-cards";
import type { DroppedResource, RemovalResult } from "@/api/builder/applyTypes";
import { useWorkflowsQuery } from "@/api/workflows";

import { type SidePanelTabId, useChatContext } from "@/providers/contexts";

import { useIsBuilderServerApplyEnabled } from "@/hooks/useIsBuilderServerApplyEnabled";

import { cn } from "@/utils/cnTailwind";

import { Button } from "@/ui/button";
import { type MessageFE } from "@/types/chat";

import FlowChartPanel from "@/features/activities/flowChart/FlowChartPanel";
import { FlowChartDetails } from "@/features/activities/FlowChartDetails";
import { useReconciledActivityTask } from "@/features/activities/useReconciledActivityTask";
import { UserInputMessage } from "@/features/chat/a2ui/UserInputMessage";
import { filterActivityUpdates } from "@/features/chat/activityIndicator";
import { ChatArea } from "@/features/chat/ChatArea";

import { ActivityEmptyState } from "./ActivityEmptyState";
import { reportBuilderAgentCreationStarted } from "./agentCreationAnalytics";
import { ApplyChangesCard, type ApplyChangesCardData } from "./ApplyChangesCard";
import { ApplyChangesDialog, shouldSkipApplyChangesConfirm } from "./ApplyChangesDialog";
import { BuildCompleteCTA } from "./BuildCompleteCTA";
import { BuilderActivityLayer } from "./BuilderActivityLayer";
import { BUILDER_WELCOME } from "./builderConstants";
import { BuilderTabToggle } from "./BuilderTabToggle";
import {
    buildBlockedByCredsMessage,
    collectPendingHilInputMessages,
    computeSuppressedActivityMessageId,
    detectComponentsWithMissingCredentials,
    editCompleteCtaMode,
    findLastAiMessageId,
    hasLiveBuildActivity,
    isBlockedSessionComponent,
    isBuildCompleteCtaShowing,
    isFirstNonUserMessageWithId,
    isInlineProgressActive,
    isLastNonUserMessage,
    isSaveOnlyBuild,
    resolveEffectiveManifest,
    resolvePlanAnchorId,
    resolveTestTargetName,
    shouldShowBuildCompleteCTAAddon,
    shouldShowCreationProgress,
    shouldShowCreationProgressCard,
    shouldShowEditCompleteCard,
    shouldShowEditingProgressCard,
    shouldShowPlanCard,
    validatedButLiveComponents,
} from "./builderUtils";
import { activitySuppressionFor, borrowedStepsSource, isApprovedBuildInterrupted, resumeSurface } from "./buildInterruption";
import type { ArtifactVersions } from "./componentDirtyState";
import type { DroppedView } from "./droppedResources";
import { DroppedResourcesNotice } from "./DroppedResourcesNotice";
import { type ApplyComplete, EditCompleteCard } from "./EditCompleteCard";
import { clampSplitPanelWidth, splitPanelSizing } from "./hooks/builderSplitLayout";
import { useArtifactVersions } from "./hooks/useArtifactVersions";
import type { BuildActivateFailure } from "./hooks/useBuildActivateBridge";
import { type ComponentCanvasData, useBuilderCanvasData } from "./hooks/useBuilderCanvasData";
import { type BuildManifest, type SaveManifestResult, useBuilderManifest } from "./hooks/useBuilderManifest";
import { useChatDeployTrigger } from "./hooks/useChatDeployTrigger";
import { usePersistedTaskEnd } from "./hooks/usePersistedTaskEnd";
import { useReportBuildInterrupted } from "./hooks/useReportBuildInterrupted";
import { useApplyComplete } from "./hooks/useTestArtifacts";
import { useTurnEndedAbnormally } from "./hooks/useTurnEndedAbnormally";
import { InlineCreationProgress } from "./InlineCreationProgress";
import { InlineEditingProgress } from "./InlineEditingProgress";
import { InlinePlanCard } from "./InlinePlanCard";
import { isLoadedForEdit } from "./loadedForEdit";
import { awaitsActivation, type PlatformCheckView } from "./platformCheck";
import { submitResumeBuild } from "./resumeBuild";
import { SplitResizeHandle } from "./SplitResizeHandle";
import { SwitchToBuildWhileTestingDialog } from "./SwitchToBuildWhileTestingDialog";
import { shouldSkipTestModeConfirm, SwitchToTestModeDialog } from "./SwitchToTestModeDialog";
import { isEffectiveTargetDeployed, pickDefaultTestTarget, TestModePanel } from "./TestModePanel";
import { formatRecommendationsAsBuildRequest } from "./testRecommendationsMessage";
import type { TestReport } from "./TestReportCard";
import type { BuilderActiveTab, ChatPanelMode } from "./useBuilderPageState";

function applyChangesAddon(message: MessageFE, onViewTestReport: () => void): ReactNode | null {
    const data = message.metadata?.applyChangesCard as ApplyChangesCardData | undefined;
    return data ? <ApplyChangesCard data={data} onViewTestReport={onViewTestReport} /> : null;
}

function renderPlanAddon(params: {
    loadedForEdit: boolean;
    manifest: BuildManifest | null;
    planApproved: boolean;
    planAnchorMessageId: string | null;
    isFirstNonUserWithId: (targetId: string) => boolean;
    isLastMessage: boolean;
    onApprove: () => void;
    onRequestChanges: (feedback: string) => void;
    onSaveManifest?: (updater: (prev: BuildManifest) => BuildManifest) => Promise<SaveManifestResult>;
    isResponding: boolean;
    isPlanCollapsed: boolean;
    onCollapsedChange: (collapsed: boolean) => void;
    canBuildActivate?: boolean;
    isBuildActivating?: boolean;
    onBuildActivate?: () => void;
    platformCheck?: PlatformCheckView;
    onCheckAgain?: () => void;
    onAskBuilder?: () => void;
    deployFailure?: BuildActivateFailure | null;
    onRedeploy?: () => void;
    onResumeBuild?: () => void;
    droppedNotice?: ReactNode;
}): ReactNode | null {
    const { manifest, planAnchorMessageId, isFirstNonUserWithId } = params;
    const shouldShowPlan = shouldShowPlanCard({
        loadedForEdit: params.loadedForEdit,
        hasManifest: !!manifest,
        planApproved: params.planApproved,
        hasPlanAnchor: !!planAnchorMessageId,
        isAnchored: planAnchorMessageId ? () => isFirstNonUserWithId(planAnchorMessageId) : () => false,
        isLastMessage: params.isLastMessage,
    });
    if (!shouldShowPlan || !manifest) return null;

    return (
        <InlinePlanCard
            key="plan"
            manifest={manifest}
            onApprove={params.onApprove}
            onRequestChanges={params.onRequestChanges}
            onSaveManifest={params.onSaveManifest}
            isApproved={params.planApproved}
            isResponding={params.isResponding}
            isCollapsed={params.isPlanCollapsed}
            onCollapsedChange={params.onCollapsedChange}
            canBuildActivate={params.canBuildActivate}
            isBuildActivating={params.isBuildActivating}
            onBuildActivate={params.onBuildActivate}
            platformCheck={params.platformCheck}
            onCheckAgain={params.onCheckAgain}
            onAskBuilder={params.onAskBuilder}
            deployFailure={params.deployFailure}
            onRetry={() => params.onRedeploy?.()}
            onResumeBuild={params.onResumeBuild}
            droppedNotice={params.droppedNotice}
        />
    );
}

function renderProgressAddon(params: {
    message: MessageFE;
    showCreationProgress: boolean;
    manifest: BuildManifest | null;
    isApplyingChanges: boolean;
    progressAnchorMessageId: string | null;
    isProgressCollapsed: boolean;
    isFirstNonUserWithId: (targetId: string) => boolean;
    isLastMessage: boolean;
    showThinkingContentEnabled: boolean;
    componentData: Map<string, ComponentCanvasData>;
    isResponding: boolean;
    onCollapsedChange: (collapsed: boolean) => void;
    setTaskIdInSidePanel: (taskId: string | null) => void;
    openSidePanelTab: (tab: SidePanelTabId) => void;
    isBuildInterrupted: boolean;
    borrowedSteps?: MessageFE;
    onResume?: () => void;
}): ReactNode | null {
    const { message, manifest, progressAnchorMessageId, isFirstNonUserWithId } = params;
    const shouldShowProgress = shouldShowCreationProgressCard({
        showCreationProgress: params.showCreationProgress,
        hasManifest: !!manifest,
        isApplyingChanges: params.isApplyingChanges,
        hasProgressAnchor: !!progressAnchorMessageId,
        isProgressCollapsed: params.isProgressCollapsed,
        isAnchored: progressAnchorMessageId ? () => isFirstNonUserWithId(progressAnchorMessageId) : () => false,
        isLastMessage: params.isLastMessage,
    });
    if (!shouldShowProgress || !manifest) return null;

    const activityUpdates = filterActivityUpdates((params.isLastMessage && params.borrowedSteps ? params.borrowedSteps : message).progressUpdates, params.showThinkingContentEnabled);
    const taskId = message.taskId;
    const onViewWorkflow = taskId
        ? () => {
              params.setTaskIdInSidePanel(taskId);
              params.openSidePanelTab("activity");
          }
        : undefined;
    return (
        <InlineCreationProgress
            key="progress"
            manifest={manifest}
            componentData={params.componentData}
            isResponding={params.isResponding}
            isCollapsed={params.isProgressCollapsed}
            onCollapsedChange={params.onCollapsedChange}
            activityUpdates={activityUpdates}
            activityActive={params.isLastMessage && !message.isComplete}
            onViewWorkflow={onViewWorkflow}
            isInterrupted={params.isBuildInterrupted}
            onResume={params.onResume}
        />
    );
}

function renderEditingProgressAddon(params: {
    manifest: BuildManifest | null;
    isLastMessage: boolean;
    isApplyingChanges: boolean;
    inBuildTab: boolean;
    componentData: Map<string, ComponentCanvasData>;
    isResponding: boolean;
    onSwitchToTest: () => void;
}): ReactNode | null {
    const { manifest } = params;
    if (!manifest || !shouldShowEditingProgressCard({ isLastMessage: params.isLastMessage, isApplyingChanges: params.isApplyingChanges, hasManifest: true, inBuildTab: params.inBuildTab })) {
        return null;
    }
    return <InlineEditingProgress key="editing-progress" manifest={manifest} componentData={params.componentData} isResponding={params.isResponding} onSwitchToTest={params.onSwitchToTest} />;
}

function renderHilInputAddons(msgs: MessageFE[]): ReactNode[] {
    return collectPendingHilInputMessages(msgs).map(m => <UserInputMessage key={`input-${m.metadata?.messageId}`} message={m} />);
}

function renderCompletionAddon(params: {
    planAnchorMessageId: string | null;
    isFirstNonUserWithId: (targetId: string) => boolean;
    isLastMessage: boolean;
    manifest: BuildManifest | null;
    applyComplete: ApplyComplete | null;
    deployFailure?: BuildActivateFailure | null;
    loadedForEdit: boolean;
    inBuildTab: boolean;
    isApplyingChanges: boolean;
    isResponding: boolean;
    canBuildActivate?: boolean;
    isBuildActivating?: boolean;
    onBuildActivate?: () => void;
    platformCheck?: PlatformCheckView;
    onCheckAgain?: () => void;
    onAskBuilder?: () => void;
    onSwitchToTest: () => void;
    onAddToAgent: (text: string) => void;
    planApproved: boolean;
    deployEdits: { versions: ArtifactVersions; serverApplyEnabled: boolean };
    droppedNotice?: ReactNode;
}): ReactNode | null {
    const { applyComplete, manifest } = params;

    const buildBlocked = !!manifest?.components?.some(isBlockedSessionComponent);
    if (buildBlocked && !params.planApproved) return null;

    const buildCompleteAnchor = params.planAnchorMessageId ? params.isFirstNonUserWithId(params.planAnchorMessageId) : params.isLastMessage;

    if (shouldShowEditCompleteCard({ hasApplyComplete: !!applyComplete && !buildBlocked, anchored: buildCompleteAnchor, inBuildTab: params.inBuildTab })) {
        return (
            <Fragment key="edit-complete">
                <EditCompleteCard
                    applyComplete={applyComplete!}
                    onSwitchToTest={params.onSwitchToTest}
                    ctaMode={editCompleteCtaMode({ pendingDeploy: awaitsActivation(params.canBuildActivate, params.platformCheck) })}
                    onBuildActivate={params.onBuildActivate}
                    isBuildActivating={params.isBuildActivating}
                    canBuildActivate={params.canBuildActivate}
                    platformCheck={params.platformCheck}
                    onCheckAgain={params.onCheckAgain}
                    onAskBuilder={params.onAskBuilder}
                />
                {params.droppedNotice}
            </Fragment>
        );
    }

    if (
        manifest &&
        shouldShowBuildCompleteCTAAddon({
            hasManifest: true,
            hasDeployFailure: !!params.deployFailure,
            loadedForEdit: params.loadedForEdit,
            hasApplyComplete: !!applyComplete && !buildBlocked,
            anchored: buildCompleteAnchor,
            inBuildTab: params.inBuildTab,
            isApplyingChanges: params.isApplyingChanges,
            isResponding: params.isResponding,
        })
    ) {
        return (
            <Fragment key="build-complete">
                <BuildCompleteCTA manifest={manifest} deployEdits={params.deployEdits} onSwitchToTest={params.onSwitchToTest} onAddToAgent={params.onAddToAgent} />
                {params.droppedNotice}
            </Fragment>
        );
    }

    return null;
}

interface BuilderChatPanelProps {
    mode: ChatPanelMode;
    onModeChange: (mode: ChatPanelMode) => void;
    onPlanApproved?: () => void;
    builderActiveTab: BuilderActiveTab;
    onBuilderActiveTabChange: (tab: BuilderActiveTab) => void;
    testTargetName: string | null;
    testRunning: boolean;
    onTestRunningChange: (running: boolean) => void;
    stopTestRef: MutableRefObject<(() => void) | null>;
    onLatestRunTaskIdChange: (taskId: string | null) => void;
    selectedTestActivityRun: { runNumber: number; taskId: string } | null;
    onSelectTestActivityRun: (sel: { runNumber: number; taskId: string } | null) => void;
    pinnedTestSessionId?: string | null;
    onPinnedTestSessionApplied?: () => void;
    isApplyingChanges: boolean;
    onApplyingChangesChange: (applying: boolean) => void;
    componentsMissingCreds?: ReadonlyMap<string, number>;
    canBuildActivate?: boolean;
    isBuildActivating?: boolean;
    onBuildActivate?: () => void;
    platformCheck?: PlatformCheckView;
    onCheckAgain?: () => void;
    onAskBuilder?: () => void;
    deployFailure?: BuildActivateFailure | null;
    onRedeploy?: () => void;
    dropped?: DroppedView;
    onRemoveDropped?: (resources: readonly DroppedResource[]) => Promise<RemovalResult[]>;
    onBuildInterruptedChange?: (interrupted: boolean) => void;
}

function platformCheckForPlanCard(platformCheck: PlatformCheckView | undefined, buildCompleteCtaShowing: boolean): PlatformCheckView | undefined {
    return buildCompleteCtaShowing ? undefined : platformCheck;
}

const FLOAT_MIN_W = 320;
const FLOAT_MIN_H = 300;

export function BuilderChatPanel({
    mode,
    onModeChange,
    onPlanApproved,
    builderActiveTab,
    onBuilderActiveTabChange,
    testTargetName,
    testRunning,
    onTestRunningChange,
    stopTestRef,
    onLatestRunTaskIdChange,
    selectedTestActivityRun,
    onSelectTestActivityRun,
    pinnedTestSessionId,
    onPinnedTestSessionApplied,
    componentsMissingCreds,
    isApplyingChanges: applyInFlight,
    onApplyingChangesChange: setApplyInFlight,
    canBuildActivate,
    isBuildActivating,
    onBuildActivate,
    platformCheck,
    onCheckAgain,
    onAskBuilder,
    deployFailure,
    onRedeploy,
    dropped,
    onRemoveDropped,
    onBuildInterruptedChange,
}: Readonly<BuilderChatPanelProps>) {
    const [activityOpen, setActivityOpen] = useState(false);
    const [showSwitchDialog, setShowSwitchDialog] = useState(false);
    const [showSwitchBuildDialog, setShowSwitchBuildDialog] = useState(false);
    const [pendingApplyReport, setPendingApplyReport] = useState<TestReport | null>(null);

    const requestSwitchToBuild = useCallback(() => {
        if (builderActiveTab === "build") return;
        if (testRunning) {
            setShowSwitchBuildDialog(true);
            return;
        }
        onBuilderActiveTabChange("build");
    }, [builderActiveTab, testRunning, onBuilderActiveTabChange]);

    const confirmSwitchToBuild = useCallback(() => {
        const stop = stopTestRef.current;
        if (stop) {
            try {
                stop();
            } catch {}
        }
        setShowSwitchBuildDialog(false);
        onBuilderActiveTabChange("build");
    }, [stopTestRef, onBuilderActiveTabChange]);

    const { manifest, saveManifest } = useBuilderManifest();
    const artifactVersions = useArtifactVersions(manifest);
    const serverApplyEnabled = useIsBuilderServerApplyEnabled();
    const { componentData } = useBuilderCanvasData();
    const { agents, handleSubmit, sessionId, isResponding, isCancelling, currentTaskId, messages, activeSidePanelTab, setActiveSidePanelTab, setTaskIdInSidePanel, openSidePanelTab, displayError } = useChatContext();
    const { value: showThinkingContentEnabled } = useBooleanFlagDetails("show_thinking_content", false);
    const { data: liveAgentCards, isLoading: agentsLoading } = useAgentCards();

    useChatDeployTrigger({ sessionId, messages, canBuildActivate: !!canBuildActivate, saveOnlyBuild: isSaveOnlyBuild(manifest?.components), isBuildActivating: !!isBuildActivating, onBuildActivate });

    const defaultTestTarget = useMemo(() => pickDefaultTestTarget(manifest?.components), [manifest?.components]);
    const hasTestTarget = !!defaultTestTarget;
    const canTest = useMemo(
        () => isEffectiveTargetDeployed(defaultTestTarget, manifest?.components, liveAgentCards, { versions: artifactVersions, serverApplyEnabled }),
        [defaultTestTarget, manifest?.components, liveAgentCards, artifactVersions, serverApplyEnabled]
    );

    const requestSwitchToTest = useCallback(() => {
        if (builderActiveTab === "test") return;
        if (!canTest) return;
        const missing = componentsMissingCreds ?? detectComponentsWithMissingCredentials(manifest, componentData);
        if (missing.size > 0) {
            displayError({
                title: "Credentials required",
                error: buildBlockedByCredsMessage(missing, "testing"),
                errorSource: "client",
            });
            return;
        }
        if (shouldSkipTestModeConfirm()) {
            onBuilderActiveTabChange("test");
        } else {
            setShowSwitchDialog(true);
        }
    }, [builderActiveTab, canTest, componentsMissingCreds, manifest, componentData, displayError, onBuilderActiveTabChange]);

    const requestAddToAgent = useCallback(
        (text: string) => {
            handleSubmit({ preventDefault: () => {} } as FormEvent, null, text, null, null, null, null, null);
        },
        [handleSubmit]
    );
    const { applyComplete, runNumber: applyCompleteVersion } = useApplyComplete();
    const applyStartVersionRef = useRef<number | null>(null);

    const applyTestRecommendationsImmediately = useCallback(
        (report: TestReport) => {
            const text = formatRecommendationsAsBuildRequest(report);
            const cardData: ApplyChangesCardData = {
                componentName: report.componentName,
                componentKind: report.componentKind,
                items: report.recommendations.map(r => ({ priority: r.priority, area: r.area, finding: r.finding })),
            };
            onBuilderActiveTabChange("build");
            applyStartVersionRef.current = applyCompleteVersion;
            setApplyInFlight(true);
            handleSubmit({ preventDefault: () => {} } as FormEvent, null, text, null, null, null, null, null, { hidden: true, applyChangesCard: cardData });
        },
        [handleSubmit, onBuilderActiveTabChange, applyCompleteVersion, setApplyInFlight]
    );

    const pendingApplyResolverRef = useRef<((confirmed: boolean) => void) | null>(null);
    const handleApplyTestRecommendations = useCallback(
        (report: TestReport): Promise<boolean> => {
            if (shouldSkipApplyChangesConfirm()) {
                applyTestRecommendationsImmediately(report);
                return Promise.resolve(true);
            }
            pendingApplyResolverRef.current?.(false);
            return new Promise<boolean>(resolve => {
                pendingApplyResolverRef.current = resolve;
                setPendingApplyReport(report);
            });
        },
        [applyTestRecommendationsImmediately]
    );
    const [planApproved, setPlanApproved] = useState(() => {
        if (!manifest?.components) return false;
        return manifest.components.some(c => c.origin !== "platform" && (c.status === "validated" || c.status === "deployed"));
    });
    const hasRehydratedApproval = useRef(false);
    useEffect(() => {
        if (hasRehydratedApproval.current || !manifest?.components || planApproved) return;
        const wasApproved = manifest.components.some(c => c.origin !== "platform" && (c.status === "validated" || c.status === "deployed"));
        if (wasApproved) {
            hasRehydratedApproval.current = true;
            setPlanApproved(true);
        }
    }, [manifest, planApproved]);
    const sawNonActivityTab = useRef(false);
    useEffect(() => {
        if (activeSidePanelTab !== "activity") {
            sawNonActivityTab.current = true;
            return;
        }
        if (!sawNonActivityTab.current) {
            setActiveSidePanelTab("files");
            return;
        }
        setActivityOpen(true);
        setActiveSidePanelTab("files");
    }, [activeSidePanelTab, setActiveSidePanelTab]);

    const [isPlanCollapsed, setIsPlanCollapsed] = useState(false);
    const [isProgressCollapsed, setIsProgressCollapsed] = useState(false);
    const builderAvailable = agents?.some((a: { name?: string }) => a.name === "Builder") ?? false;
    const messagesRef = useRef(messages);
    messagesRef.current = messages;
    const componentDataRef = useRef(componentData);
    componentDataRef.current = componentData;
    const isRespondingRef = useRef(isResponding);
    isRespondingRef.current = isResponding;
    const panelRef = useRef<HTMLDivElement>(null);
    const [isCompact, setIsCompact] = useState(false);

    const { data: platformWorkflows } = useWorkflowsQuery();
    const storedWorkflowNames = useMemo(() => (platformWorkflows ? new Set(platformWorkflows.map(w => w.name)) : undefined), [platformWorkflows]);
    const resolveAddressableTestName = useCallback((target: { type: string; name: string }) => resolveTestTargetName(target.type, componentData.get(target.name)?.yamlText, target.name, storedWorkflowNames), [componentData, storedWorkflowNames]);

    const approvalMessageIdRef = useRef<string | null>(null);
    const hasReportedRehydratedCreation = useRef(false);

    const lastSessionRef = useRef(sessionId);
    if (sessionId !== lastSessionRef.current) {
        lastSessionRef.current = sessionId;
        setPlanApproved(false);
        hasRehydratedApproval.current = false;
        hasReportedRehydratedCreation.current = false;
        setIsPlanCollapsed(false);
        setIsProgressCollapsed(false);
        approvalMessageIdRef.current = null;
    }

    useEffect(() => {
        if (!manifest?.components) return;
        const sessionComponents = manifest.components.filter(c => c.origin !== "platform");
        const hasValidatedOrDeployed = sessionComponents.some(c => c.status === "validated" || c.status === "deployed");
        const hasGeneratedContent = sessionComponents.some(c => componentData.get(c.name)?.yamlText);
        const wasApprovedInStorage = (() => {
            try {
                return sessionStorage.getItem(`sam_builder_approved_${sessionId}`) === "1";
            } catch {
                return false;
            }
        })();
        const hasApprovalMessage = messages?.some((m: MessageFE) => {
            if (!m.isUser || !m.parts) return false;
            return m.parts.some((p: { kind: string; text?: string }) => p.kind === "text" && typeof p.text === "string" && p.text.startsWith("Build plan approved"));
        });
        const wasApproved = hasValidatedOrDeployed || hasGeneratedContent || wasApprovedInStorage || hasApprovalMessage;
        if (!wasApproved) return;
        if (!planApproved) {
            setPlanApproved(true);
            setIsPlanCollapsed(true);
        }
        if (!hasReportedRehydratedCreation.current) {
            hasReportedRehydratedCreation.current = true;
            reportBuilderAgentCreationStarted(manifest.components, sessionId);
        }
    }, [manifest, planApproved, componentData, messages, sessionId]);

    const [panelWidth, setPanelWidth] = useState(480);
    const isResizingRef = useRef(false);

    const [floatPos, setFloatPos] = useState({ x: 16, top: 0 });
    const [floatSize, setFloatSize] = useState({ width: 560, height: 0 });
    const [floatInitialized, setFloatInitialized] = useState(false);
    const dragState = useRef<{ startX: number; startY: number; startPosX: number; startPosTop: number } | null>(null);
    const resizeState = useRef<{ edge: string; startX: number; startY: number; startW: number; startH: number; startPosX: number; startPosTop: number } | null>(null);

    useEffect(() => {
        const panel = panelRef.current;
        if (!panel) return;
        const observer = new ResizeObserver(entries => {
            for (const entry of entries) {
                setIsCompact(entry.contentRect.width < 450);
            }
        });
        observer.observe(panel);
        return () => observer.disconnect();
    }, []);

    useLayoutEffect(() => {
        if (mode !== "floating" || floatInitialized) return;
        const panel = panelRef.current;
        const container = panel?.offsetParent as HTMLElement | null;
        if (!panel || !container) return;
        const h = Math.min(container.clientHeight * 0.75, container.clientHeight - 32);
        const top = container.clientHeight - h - 16;
        setFloatSize(prev => ({ ...prev, height: Math.max(FLOAT_MIN_H, h) }));
        setFloatPos({ x: 16, top: Math.max(0, top) });
        setFloatInitialized(true);
    }, [mode, floatInitialized]);

    const handleDragStart = useCallback(
        (e: ReactMouseEvent) => {
            if ((e.target as HTMLElement).closest("button, [role='tab']")) return;
            e.preventDefault();
            dragState.current = { startX: e.clientX, startY: e.clientY, startPosX: floatPos.x, startPosTop: floatPos.top };

            const handleDragMove = (moveEvent: MouseEvent) => {
                if (!dragState.current) return;
                const dx = moveEvent.clientX - dragState.current.startX;
                const dy = moveEvent.clientY - dragState.current.startY;
                setFloatPos({
                    x: Math.max(0, dragState.current.startPosX + dx),
                    top: Math.max(0, dragState.current.startPosTop + dy),
                });
            };
            const handleDragEnd = () => {
                dragState.current = null;
                window.removeEventListener("mousemove", handleDragMove);
                window.removeEventListener("mouseup", handleDragEnd);
            };
            window.addEventListener("mousemove", handleDragMove);
            window.addEventListener("mouseup", handleDragEnd);
        },
        [floatPos]
    );

    const handleFloatResizeStart = useCallback(
        (edge: string, e: ReactMouseEvent) => {
            e.preventDefault();
            e.stopPropagation();
            resizeState.current = {
                edge,
                startX: e.clientX,
                startY: e.clientY,
                startW: floatSize.width,
                startH: floatSize.height,
                startPosX: floatPos.x,
                startPosTop: floatPos.top,
            };

            const handleResizeMove = (moveEvent: MouseEvent) => {
                const rs = resizeState.current;
                if (!rs) return;
                const dx = moveEvent.clientX - rs.startX;
                const dy = moveEvent.clientY - rs.startY;
                let newW = rs.startW,
                    newH = rs.startH,
                    newX = rs.startPosX,
                    newTop = rs.startPosTop;
                if (rs.edge.includes("r")) newW = Math.max(FLOAT_MIN_W, rs.startW + dx);
                if (rs.edge.includes("l")) {
                    newW = Math.max(FLOAT_MIN_W, rs.startW - dx);
                    newX = rs.startPosX + (rs.startW - newW);
                }
                if (rs.edge.includes("b")) newH = Math.max(FLOAT_MIN_H, rs.startH + dy);
                if (rs.edge.includes("t")) {
                    newH = Math.max(FLOAT_MIN_H, rs.startH - dy);
                    newTop = rs.startPosTop + (rs.startH - newH);
                }
                setFloatSize({ width: newW, height: newH });
                setFloatPos({ x: Math.max(0, newX), top: Math.max(0, newTop) });
            };
            const handleResizeEnd = () => {
                resizeState.current = null;
                window.removeEventListener("mousemove", handleResizeMove);
                window.removeEventListener("mouseup", handleResizeEnd);
            };
            window.addEventListener("mousemove", handleResizeMove);
            window.addEventListener("mouseup", handleResizeEnd);
        },
        [floatSize, floatPos]
    );

    const handleSplitResizeStart = useCallback(
        (e: ReactMouseEvent) => {
            e.preventDefault();
            isResizingRef.current = true;
            const startX = e.clientX;
            const panel = panelRef.current;
            const startWidth = panel?.getBoundingClientRect().width ?? panelWidth;
            const rowWidth = panel?.offsetParent?.getBoundingClientRect().width ?? globalThis.innerWidth;

            const handleMouseMove = (moveEvent: MouseEvent) => {
                const delta = moveEvent.clientX - startX;
                setPanelWidth(clampSplitPanelWidth(startWidth + delta, rowWidth, globalThis.innerWidth));
            };
            const handleMouseUp = () => {
                isResizingRef.current = false;
                document.removeEventListener("mousemove", handleMouseMove);
                document.removeEventListener("mouseup", handleMouseUp);
                document.body.style.cursor = "";
                document.body.style.userSelect = "";
            };
            document.body.style.cursor = "col-resize";
            document.body.style.userSelect = "none";
            document.addEventListener("mousemove", handleMouseMove);
            document.addEventListener("mouseup", handleMouseUp);
        },
        [panelWidth]
    );

    const handleApprove = useCallback(() => {
        setPlanApproved(true);
        try {
            sessionStorage.setItem(`sam_builder_approved_${sessionId}`, "1");
        } catch {}
        reportBuilderAgentCreationStarted(manifest?.components, sessionId);
        if (mode === "fullscreen") onModeChange("split");

        const draftComponents = manifest?.components?.filter(c => c.status === "draft" && c.origin !== "platform").map(c => `${c.name} (${c.type})`) ?? [];
        const approvalMessage = draftComponents.length > 0 ? `Build plan approved, proceed with generation. Components still needing configs: ${draftComponents.join(", ")}.` : "Build plan approved, proceed with generation.";

        const msgs = messagesRef.current;
        if (msgs.length > 0) {
            const lastMsg = msgs[msgs.length - 1];
            approvalMessageIdRef.current = lastMsg.metadata?.messageId ?? null;
        }

        handleSubmit({ preventDefault: () => {} } as FormEvent, null, approvalMessage, null, null, null, null, null, { hidden: true });
        onPlanApproved?.();
    }, [handleSubmit, mode, onModeChange, onPlanApproved, manifest, sessionId]);

    const handleRequestChanges = useCallback(
        (feedback: string) => {
            if (mode === "fullscreen") onModeChange("split");
            const message = `I have some feedback on the build plan:\n\n${feedback}`;
            handleSubmit({ preventDefault: () => {} } as FormEvent, null, message, null, null, null, null, null, { hidden: true });
        },
        [handleSubmit, mode, onModeChange]
    );

    const builderMessageFilter = useCallback((message: MessageFE, index: number) => {
        if (index !== 0) return true;
        if (message.isUser) return true;
        if (message.taskId) return true;
        if (message.isStatusBubble) return true;
        return false;
    }, []);

    const approvedManifestRef = useRef(manifest);
    if (manifest) approvedManifestRef.current = manifest;
    const effectiveManifest = resolveEffectiveManifest(manifest, planApproved, approvedManifestRef.current);

    const showCreationProgress = shouldShowCreationProgress(planApproved, effectiveManifest);

    const loadedForEdit = isLoadedForEdit(effectiveManifest, artifactVersions);
    const deployEdits = { versions: artifactVersions, serverApplyEnabled };
    const buildCompleteCtaShowing = isBuildCompleteCtaShowing(effectiveManifest?.components, deployEdits) && !loadedForEdit;
    const planCanBuildActivate = canBuildActivate && !buildCompleteCtaShowing;
    const planPlatformCheck = platformCheckForPlanCard(platformCheck, buildCompleteCtaShowing);
    const planHasBuildToActivate = awaitsActivation(planCanBuildActivate, planPlatformCheck);
    const planCardShowsDropped = !planApproved || planHasBuildToActivate;
    const droppedNotice = useMemo(() => (dropped && onRemoveDropped ? <DroppedResourcesNotice view={dropped} onRemove={onRemoveDropped} /> : null), [dropped, onRemoveDropped]);

    const lastAiMessageId = findLastAiMessageId(messages);

    useEffect(() => {
        if (!applyInFlight) return;
        if (applyCompleteVersion === null) return;
        const startV = applyStartVersionRef.current;
        if (startV === null || applyCompleteVersion > startV) {
            setApplyInFlight(false);
            applyStartVersionRef.current = null;
        }
    }, [applyInFlight, applyCompleteVersion, setApplyInFlight]);
    const wasRespondingRef = useRef(false);
    useEffect(() => {
        if (!applyInFlight) return;
        const wasResponding = wasRespondingRef.current;
        wasRespondingRef.current = isResponding;
        if (wasResponding && !isResponding) {
            const t = setTimeout(() => setApplyInFlight(false), 250);
            return () => clearTimeout(t);
        }
    }, [applyInFlight, isResponding, setApplyInFlight]);
    const isApplyingChanges = applyInFlight;
    const endedAbnormallyOnRecord = usePersistedTaskEnd({ messages, enabled: planApproved && !isResponding && !isCancelling });
    const turnEndedAbnormally = useTurnEndedAbnormally({ messages, isResponding, isCancelling, currentTaskId, sessionId, endedAbnormallyOnRecord });
    const isBuildInterrupted = isApprovedBuildInterrupted({ planApproved, isApplyingChanges, isResponding, isCancelling, turnEndedAbnormally, hasDeployFailure: !!deployFailure, isBuildActivating: !!isBuildActivating, manifest: effectiveManifest });
    useReportBuildInterrupted(isBuildInterrupted, onBuildInterruptedChange);
    const resumeOn = resumeSurface({ hostInterrupted: isBuildInterrupted, isCancelling });
    const planCardOwnsResume = resumeOn === "plan-card";
    const handleResumeBuild = useCallback(() => submitResumeBuild(handleSubmit), [handleSubmit]);

    const lastMessage = messages.at(-1);
    const currentTurnHasActivity = hasLiveBuildActivity(lastMessage, componentData, showThinkingContentEnabled);
    const inlineProgressActive = isInlineProgressActive({
        isResponding,
        showCreationProgress,
        isApplyingChanges,
        currentTurnHasActivity,
        inBuildTab: builderActiveTab === "build",
    });

    const liveAgentFlipSignatureRef = useRef<string | null>(null);
    useEffect(() => {
        if (!manifest?.components?.length) return;
        if (!agents?.length) return;
        const stale = validatedButLiveComponents(manifest.components, agents);
        if (stale.length === 0) return;
        const signature = stale
            .map(c => c.name)
            .sort()
            .join(",");
        if (liveAgentFlipSignatureRef.current === signature) return;
        liveAgentFlipSignatureRef.current = signature;
        console.info("[builder] live-agent flip: flipping validated→deployed for running agents", {
            stale: stale.map(c => c.name),
        });
        void saveManifest(prev => ({
            ...prev,
            components: (prev.components ?? []).map(c => (stale.some(s => s.name === c.name) ? { ...c, status: "deployed" } : c)),
        })).then(result => {
            if (!result.ok) liveAgentFlipSignatureRef.current = null;
        });
    }, [manifest, agents, saveManifest]);

    const planAnchorMessageId = resolvePlanAnchorId(messages);

    const progressAnchorMessageId = approvalMessageIdRef.current || planAnchorMessageId;

    const borrowedSteps = borrowedStepsSource(messages, { isInterrupted: isBuildInterrupted, isTimelineCollapsed: isProgressCollapsed });
    const suppressActivityMsgId = activitySuppressionFor(
        computeSuppressedActivityMessageId({
            showCreationProgress,
            isApplyingChanges,
            isProgressCollapsed,
            progressAnchorMessageId,
            lastAiMessageId,
        }),
        borrowedSteps
    );

    const renderMessageAddon = useCallback(
        (message: MessageFE, _index: number) => {
            const messageId = message.metadata?.messageId;
            if (!messageId || message.isUser) return null;

            const msgs = messagesRef.current;

            const isLastMessage = isLastNonUserMessage(msgs, message);
            const isFirstNonUserWithId = (targetId: string) => isFirstNonUserMessageWithId(msgs, message, targetId);

            const addons: ReactNode[] = [];

            const planAddon = renderPlanAddon({
                loadedForEdit,
                manifest: effectiveManifest,
                planApproved,
                planAnchorMessageId,
                isFirstNonUserWithId,
                isLastMessage,
                onApprove: handleApprove,
                onRequestChanges: handleRequestChanges,
                onSaveManifest: saveManifest,
                isResponding: isRespondingRef.current,
                isPlanCollapsed,
                onCollapsedChange: setIsPlanCollapsed,
                canBuildActivate: planCanBuildActivate,
                isBuildActivating,
                onBuildActivate,
                platformCheck: planPlatformCheck,
                onCheckAgain,
                onAskBuilder,
                deployFailure,
                onRedeploy,
                onResumeBuild: planCardOwnsResume ? handleResumeBuild : undefined,
                droppedNotice,
            });
            if (planAddon) addons.push(planAddon);

            const progressAddon = renderProgressAddon({
                message,
                showCreationProgress: !!showCreationProgress,
                manifest: effectiveManifest,
                isApplyingChanges,
                progressAnchorMessageId,
                isProgressCollapsed,
                isFirstNonUserWithId,
                isLastMessage,
                showThinkingContentEnabled,
                componentData: componentDataRef.current,
                isResponding: isRespondingRef.current,
                onCollapsedChange: setIsProgressCollapsed,
                setTaskIdInSidePanel,
                openSidePanelTab,
                isBuildInterrupted,
                borrowedSteps,
                onResume: resumeOn === "timeline" ? handleResumeBuild : undefined,
            });
            if (progressAddon) addons.push(progressAddon);

            const editingProgressAddon = renderEditingProgressAddon({
                manifest: effectiveManifest,
                isLastMessage,
                isApplyingChanges,
                inBuildTab: builderActiveTab === "build",
                componentData: componentDataRef.current,
                isResponding: isRespondingRef.current,
                onSwitchToTest: requestSwitchToTest,
            });
            if (editingProgressAddon) addons.push(editingProgressAddon);

            if (isLastMessage) addons.push(...renderHilInputAddons(msgs));

            const completionAddon = renderCompletionAddon({
                planAnchorMessageId,
                isFirstNonUserWithId,
                isLastMessage,
                manifest: effectiveManifest,
                applyComplete,
                deployFailure,
                loadedForEdit,
                inBuildTab: builderActiveTab === "build",
                isApplyingChanges,
                isResponding,
                canBuildActivate,
                isBuildActivating,
                onBuildActivate,
                platformCheck,
                onCheckAgain,
                onAskBuilder,
                onSwitchToTest: requestSwitchToTest,
                onAddToAgent: requestAddToAgent,
                planApproved,
                deployEdits,
                droppedNotice: planCardShowsDropped ? null : droppedNotice,
            });
            if (completionAddon) addons.push(completionAddon);

            return addons.length > 0 ? <>{addons}</> : null;
        },
        // eslint-disable-next-line react-hooks/exhaustive-deps
        [
            manifest,
            effectiveManifest,
            handleApprove,
            handleRequestChanges,
            saveManifest,
            planApproved,
            isPlanCollapsed,
            isProgressCollapsed,
            showCreationProgress,
            planAnchorMessageId,
            progressAnchorMessageId,
            builderActiveTab,
            requestSwitchToTest,
            requestAddToAgent,
            applyComplete,
            isApplyingChanges,
            isResponding,
            showThinkingContentEnabled,
            setTaskIdInSidePanel,
            openSidePanelTab,
            planCanBuildActivate,
            planPlatformCheck,
            platformCheck,
            onCheckAgain,
            onAskBuilder,
            isBuildActivating,
            onBuildActivate,
            deployFailure,
            onRedeploy,
            isBuildInterrupted,
            borrowedSteps,
            planCardOwnsResume,
            isCancelling,
            handleResumeBuild,
            droppedNotice,
        ]
    );

    const renderMessageAddonWithApply = useCallback(
        (message: MessageFE, index: number) => {
            if (message.isUser) {
                const card = applyChangesAddon(message, () => onBuilderActiveTabChange("test"));
                if (card) return card;
            }
            return renderMessageAddon(message, index);
        },
        [renderMessageAddon, onBuilderActiveTabChange]
    );

    const isFloating = mode === "floating";

    const containerClassName = cn("bg-(--background-w10) flex min-w-0 flex-col overflow-hidden", mode === "fullscreen" && "h-full w-full", mode === "split" && "relative h-full border-r", isFloating && "absolute z-30 rounded-lg border shadow-xl");

    const splitShadow = "3px 0 14px -2px rgba(0,0,0,0.2)";
    const floatingStyle: CSSProperties | undefined = isFloating ? { left: floatPos.x, top: floatPos.top, width: floatSize.width, height: floatSize.height || "75vh" } : undefined;
    const containerStyle: CSSProperties | undefined = mode === "split" ? { ...splitPanelSizing(panelWidth), boxShadow: splitShadow } : floatingStyle;

    return (
        <div ref={panelRef} data-testid="builder-chat-panel" className={containerClassName} style={containerStyle}>
            {isFloating && (
                <>
                    <div className="absolute inset-x-1 top-0 z-40 h-1.5 cursor-ns-resize" onMouseDown={e => handleFloatResizeStart("t", e)} />
                    <div className="absolute inset-x-1 bottom-0 z-40 h-1.5 cursor-ns-resize" onMouseDown={e => handleFloatResizeStart("b", e)} />
                    <div className="absolute inset-y-1 left-0 z-40 w-1.5 cursor-ew-resize" onMouseDown={e => handleFloatResizeStart("l", e)} />
                    <div className="absolute inset-y-1 right-0 z-40 w-1.5 cursor-ew-resize" onMouseDown={e => handleFloatResizeStart("r", e)} />
                    <div className="absolute top-0 left-0 z-50 h-3 w-3 cursor-nwse-resize" onMouseDown={e => handleFloatResizeStart("tl", e)} />
                    <div className="absolute top-0 right-0 z-50 h-3 w-3 cursor-nesw-resize" onMouseDown={e => handleFloatResizeStart("tr", e)} />
                    <div className="absolute bottom-0 left-0 z-50 h-3 w-3 cursor-nesw-resize" onMouseDown={e => handleFloatResizeStart("bl", e)} />
                    <div className="absolute right-0 bottom-0 z-50 h-3 w-3 cursor-nwse-resize" onMouseDown={e => handleFloatResizeStart("br", e)} />
                </>
            )}

            <div className="flex h-full flex-col">
                <div className={cn("relative flex items-center justify-center gap-1 px-2 pt-5 pb-2", isFloating && "cursor-grab active:cursor-grabbing")} onMouseDown={isFloating ? handleDragStart : undefined}>
                    {isFloating && <GripVertical className="absolute top-1/2 left-2 h-3.5 w-3.5 shrink-0 -translate-y-1/2" />}
                    {!activityOpen && <BuilderTabToggle builderActiveTab={builderActiveTab} canTest={canTest} hasTestTarget={hasTestTarget} isCompact={isCompact} onRequestTest={requestSwitchToTest} onRequestBuild={requestSwitchToBuild} />}
                    <Button variant="ghost" size="sm" className="h-7 w-7 shrink-0 p-0" tooltip="Activity" onClick={() => setActivityOpen(true)}>
                        <Network className="h-3.5 w-3.5" />
                    </Button>
                </div>

                <div className="relative min-h-0 min-w-0 flex-1 overflow-hidden">
                    <div className={cn("h-full", builderActiveTab !== "build" && "hidden")}>
                        {builderAvailable || agentsLoading ? (
                            <ChatArea
                                compact
                                allowAgentSelection={false}
                                welcomeOverride={BUILDER_WELCOME}
                                renderMessageAddon={renderMessageAddonWithApply}
                                messageFilter={builderMessageFilter}
                                hideInlineActivityForMessageId={suppressActivityMsgId}
                                suppressLoadingIndicator={inlineProgressActive}
                            />
                        ) : (
                            <div className="flex h-full items-center justify-center p-4">
                                <div className="text-center">
                                    <Bot className="mx-auto mb-3 h-10 w-10" />
                                    <p className="text-sm font-medium">Builder agent is not available</p>
                                    <p className="mt-1 text-xs">The Builder agent must be running to use the build chat. Check that the Builder agent is deployed and discoverable.</p>
                                </div>
                            </div>
                        )}
                    </div>
                    <div className={cn("h-full", builderActiveTab !== "test" && "hidden")}>
                        <TestModePanel
                            testTargetOverride={testTargetName}
                            resolveAddressableName={resolveAddressableTestName}
                            onApplyChangesToBuild={handleApplyTestRecommendations}
                            onTestRunningChange={onTestRunningChange}
                            stopTestRef={stopTestRef}
                            onLatestRunTaskIdChange={onLatestRunTaskIdChange}
                            selectedActivityRun={selectedTestActivityRun}
                            onSelectActivityRun={onSelectTestActivityRun}
                            pinnedTestSessionId={pinnedTestSessionId}
                            onPinnedTestSessionApplied={onPinnedTestSessionApplied}
                        />
                    </div>
                    {activityOpen && (
                        <BuilderActivityLayer onBack={() => setActivityOpen(false)}>
                            <ActivityTabContent />
                        </BuilderActivityLayer>
                    )}
                </div>
            </div>

            {mode === "split" && <SplitResizeHandle panelRef={panelRef} onDragStart={handleSplitResizeStart} onWidthChange={setPanelWidth} />}

            <SwitchToTestModeDialog
                open={showSwitchDialog}
                onCancel={() => setShowSwitchDialog(false)}
                onConfirm={() => {
                    setShowSwitchDialog(false);
                    onBuilderActiveTabChange("test");
                }}
            />

            <SwitchToBuildWhileTestingDialog open={showSwitchBuildDialog} onCancel={() => setShowSwitchBuildDialog(false)} onConfirm={confirmSwitchToBuild} />

            <ApplyChangesDialog
                open={pendingApplyReport !== null}
                onCancel={() => {
                    setPendingApplyReport(null);
                    pendingApplyResolverRef.current?.(false);
                    pendingApplyResolverRef.current = null;
                }}
                onConfirm={() => {
                    const report = pendingApplyReport;
                    setPendingApplyReport(null);
                    if (report) applyTestRecommendationsImmediately(report);
                    pendingApplyResolverRef.current?.(true);
                    pendingApplyResolverRef.current = null;
                }}
            />
        </div>
    );
}

function ActivityTabContent() {
    const { taskIdInSidePanel, activityEventTasks, isResponding, currentTaskId } = useChatContext();
    const { visualizedTask, isLoadingTask } = useReconciledActivityTask(taskIdInSidePanel ?? null, activityEventTasks, isResponding && currentTaskId === taskIdInSidePanel);

    if (isLoadingTask) {
        return <ActivityEmptyState icon={Workflow} message="Loading activity data..." />;
    }

    if (!taskIdInSidePanel) {
        return <ActivityEmptyState icon={Workflow} message="No task selected to display" />;
    }

    if (!visualizedTask) {
        return <ActivityEmptyState icon={Workflow} message="No activity data available for the selected task" />;
    }

    return (
        <div className="flex h-full flex-col">
            <FlowChartDetails task={visualizedTask} />
            <FlowChartPanel processedSteps={visualizedTask.steps || []} isRightPanelVisible={false} />
        </div>
    );
}
