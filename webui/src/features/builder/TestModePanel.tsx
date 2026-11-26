// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type FormEvent, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { BookOpen, Bot, FlaskConical, Globe, Loader2, Workflow } from "lucide-react";

import { useAgentCards } from "@/api/agent-cards";
import { useCreateTestAgentMutation, useTestAgentQuery } from "@/api/builder";
import { api } from "@/api/client";

import { useChatContext } from "@/providers/contexts";

import { useIsBuilderServerApplyEnabled } from "@/hooks/useIsBuilderServerApplyEnabled";

import { isOperationalStatus } from "@/common/status";

import { Button } from "@/ui/button";
import { type MessageFE } from "@/types/chat";

import { ChatArea } from "@/features/chat/ChatArea";

import { ChatProvider } from "@/providers";
import type { RuntimeStatusValue } from "@/types";

import { componentTypeLabel, isSessionComponentLive } from "./builderUtils";
import { type ArtifactVersions, isRunningStaleConfig } from "./componentDirtyState";
import { useArtifactVersions } from "./hooks/useArtifactVersions";
import { useBuilderManifest } from "./hooks/useBuilderManifest";
import { useTestPlan, useTestReport } from "./hooks/useTestArtifacts";
import { useTestSessionLifecycle } from "./hooks/useTestSessionLifecycle";
import { type TestPlan, TestPlanCard } from "./TestPlanCard";
import { TEST_REPORT_ARTIFACT_FILENAME, type TestReport, TestReportCard } from "./TestReportCard";

const PRESET_PROMPTS = ["Explain what I can do here", "Create a test for me", "Guide me through creating a test"];

export type TestableKind = "workflow" | "agent" | "skill" | "a2a_agent";

export interface TestTarget {
    type: TestableKind | (string & {});
    name: string;
}

export function isChatTestable(kind: string): kind is TestableKind {
    return kind === "workflow" || kind === "agent" || kind === "skill" || kind === "a2a_agent";
}

export function pickDefaultTestTarget(components: { type?: string; name: string; status?: string; origin?: string }[] | undefined): TestTarget | null {
    if (!components?.length) return null;
    const sessionComps = components.filter(c => c.origin !== "platform");
    const wf = sessionComps.find(c => c.type === "workflow");
    if (wf) return { type: "workflow", name: wf.name };
    const ag = sessionComps.find(c => c.type === "agent");
    if (ag) return { type: "agent", name: ag.name };
    const a2a = sessionComps.find(c => c.type === "a2a_agent");
    if (a2a) return { type: "a2a_agent", name: a2a.name };
    const sk = sessionComps.find(c => c.type === "skill");
    if (sk) return { type: "skill", name: sk.name };
    return null;
}

export function isEffectiveTargetDeployed(
    target: TestTarget | null,
    components: { id?: string; name: string; status?: string; origin?: string; type?: string; filename?: string; deployed_version?: number }[] | undefined,
    liveAgentCards: ReadonlyArray<{ name?: string; displayName?: string }> | undefined,
    edits: { versions: ArtifactVersions | undefined; serverApplyEnabled: boolean }
): boolean {
    if (!target) return false;
    const c = components?.find(comp => comp.name === target.name);
    if (c && isRunningStaleConfig(c, edits.versions, edits.serverApplyEnabled)) return false;
    if (!c) return false;
    if (c.status === "deployed" || c.origin === "platform") return true;
    return isSessionComponentLive(c, liveAgentCards);
}

export function isTestAgentHealthy(deploymentStatus: string | undefined, runtimeStatus: RuntimeStatusValue | undefined): boolean {
    return deploymentStatus === "deployed" && (isOperationalStatus(runtimeStatus) || runtimeStatus === "starting");
}

interface TestModePanelProps {
    testTargetOverride?: string | null;
    resolveAddressableName?: (target: TestTarget) => string;
    onApplyChangesToBuild?: (report: TestReport) => void | Promise<boolean>;
    onTestRunningChange?: (running: boolean) => void;
    stopTestRef?: React.MutableRefObject<(() => void) | null>;
    onLatestRunTaskIdChange?: (taskId: string | null) => void;
    selectedActivityRun?: { runNumber: number; taskId: string } | null;
    onSelectActivityRun?: (sel: { runNumber: number; taskId: string } | null) => void;
    pinnedTestSessionId?: string | null;
    onPinnedTestSessionApplied?: () => void;
}

export function TestModePanel({
    testTargetOverride = null,
    resolveAddressableName,
    onApplyChangesToBuild,
    onTestRunningChange,
    stopTestRef,
    onLatestRunTaskIdChange,
    selectedActivityRun = null,
    onSelectActivityRun,
    pinnedTestSessionId = null,
    onPinnedTestSessionApplied,
}: TestModePanelProps = {}) {
    const {
        sessionId,
        agents,
        isResponding: buildResponding,
    } = useChatContext() as unknown as {
        sessionId: string | null;
        agents: { name?: string; display_name?: string; displayName?: string }[];
        isResponding: boolean;
    };
    const { manifest } = useBuilderManifest();
    const artifactVersions = useArtifactVersions(manifest);
    const serverApplyEnabled = useIsBuilderServerApplyEnabled();
    const { data: liveAgentCards } = useAgentCards();

    const defaultTarget = useMemo(() => pickDefaultTestTarget(manifest?.components), [manifest?.components]);

    const effectiveTarget = useMemo<TestTarget | null>(() => {
        if (testTargetOverride) {
            const match = manifest?.components?.find(c => c.name === testTargetOverride);
            if (!match || !match.type) return null;
            return { type: match.type, name: testTargetOverride };
        }
        return defaultTarget;
    }, [testTargetOverride, defaultTarget, manifest?.components]);

    const addressableName = useMemo(() => {
        if (!effectiveTarget) return null;
        return resolveAddressableName?.(effectiveTarget) ?? effectiveTarget.name;
    }, [effectiveTarget, resolveAddressableName]);

    const effectiveTargetDeployed = useMemo(
        () => isEffectiveTargetDeployed(effectiveTarget, manifest?.components, liveAgentCards, { versions: artifactVersions, serverApplyEnabled }),
        [effectiveTarget, manifest?.components, liveAgentCards, artifactVersions, serverApplyEnabled]
    );

    const testAgentQuery = useTestAgentQuery(sessionId, !!sessionId);
    const createTestAgent = useCreateTestAgentMutation();
    const { isPending: createPending, mutate: createMutate, reset: createReset } = createTestAgent;

    const targetKey = (t: TestTarget) => `${t.type}::${t.name}`;
    const lastTargetFiredRef = useRef<string | null>(null);
    const [retryCount, setRetryCount] = useState(0);
    useEffect(() => {
        if (!sessionId || !effectiveTarget) return;
        if (buildResponding) return;
        if (!effectiveTargetDeployed) return;
        if (!isChatTestable(effectiveTarget.type)) return;
        if (testAgentQuery.isLoading) return;
        const existingName = testAgentQuery.data?.sourceComponentName ?? null;
        const existingKind = testAgentQuery.data?.sourceComponentKind ?? null;
        const isHealthy = isTestAgentHealthy(testAgentQuery.data?.deploymentStatus, testAgentQuery.data?.runtimeStatus?.status);
        const targetMatches = existingName === addressableName && existingKind === effectiveTarget.type;
        const needsCreate = !testAgentQuery.data || !targetMatches || !isHealthy;
        if (!needsCreate) return;
        const componentName = addressableName ?? effectiveTarget.name;
        const currentKey = `${effectiveTarget.type}::${componentName}`;
        if (lastTargetFiredRef.current === currentKey) return;
        if (createPending) return;
        lastTargetFiredRef.current = currentKey;
        createMutate({
            sessionId,
            componentName,
            componentKind: effectiveTarget.type,
        });
    }, [sessionId, effectiveTarget, addressableName, buildResponding, effectiveTargetDeployed, testAgentQuery.isLoading, testAgentQuery.data, createPending, createMutate, retryCount]);

    const lastSessionRef = useRef(sessionId);
    if (sessionId !== lastSessionRef.current) {
        lastSessionRef.current = sessionId;
        lastTargetFiredRef.current = null;
    }

    const retryCreate = useCallback(() => {
        lastTargetFiredRef.current = null;
        createReset();
        setRetryCount(n => n + 1);
    }, [createReset]);
    const targetReadyToRetry = !!effectiveTarget && isTargetReadyToRetry(effectiveTarget, addressableName, agents, manifest?.components);
    const createFailedForCurrent = createTestAgent.isError && !!effectiveTarget && isCreateFor(createTestAgent.variables, sessionId, effectiveTarget, addressableName);
    useRetryWhenTargetBecomesReady(createFailedForCurrent, targetReadyToRetry, retryCreate);

    type LiveAgent = NonNullable<NonNullable<typeof testAgentQuery.data> | NonNullable<typeof createTestAgent.data>>;
    const lastGoodAgentRef = useRef<LiveAgent | null>(null);
    const liveAgent = (testAgentQuery.data ?? createTestAgent.data) as LiveAgent | undefined;
    if (liveAgent) lastGoodAgentRef.current = liveAgent;

    if (!sessionId) {
        return (
            <TestModeShell>
                <p className="text-sm">Open a build session to enter Test mode.</p>
            </TestModeShell>
        );
    }

    if (!effectiveTarget) {
        return (
            <TestModeShell>
                <p className="text-sm">Once you&rsquo;ve built a workflow or agent, you can come back here to test it.</p>
            </TestModeShell>
        );
    }

    if (!effectiveTargetDeployed) {
        return (
            <TestModeShell>
                <p className="text-sm">
                    Deploy <span className="font-semibold">{effectiveTarget.name}</span> with <span className="font-semibold">Build &amp; Activate</span> before you can test it.
                </p>
            </TestModeShell>
        );
    }

    if (!isChatTestable(effectiveTarget.type)) {
        return (
            <TestModeShell>
                <p className="text-sm">
                    Test mode for <span className="font-semibold">{componentTypeLabel(effectiveTarget.type).toLowerCase()}</span> components is coming soon.
                </p>
                <p className="text-xs">For now, pick a workflow, agent, or skill from the canvas to drive a chat-based test.</p>
            </TestModeShell>
        );
    }

    if (createFailedForCurrent) {
        const msg = (createTestAgent.error as Error | undefined)?.message?.trim() || "Failed to create test agent.";
        return (
            <TestModeShell>
                <p className="text-sm text-(--error-wMain)">{msg}</p>
                <Button size="sm" variant="outline" onClick={retryCreate}>
                    Try again
                </Button>
            </TestModeShell>
        );
    }

    const agentMatchesTarget = (a: { sourceComponentName?: string; sourceComponentKind?: string } | null | undefined) => !!a && a.sourceComponentName === (addressableName ?? effectiveTarget.name) && a.sourceComponentKind === effectiveTarget.type;
    const cachedAgentMatchesTarget = agentMatchesTarget(lastGoodAgentRef.current);
    const testAgent = agentMatchesTarget(liveAgent) ? liveAgent : cachedAgentMatchesTarget ? lastGoodAgentRef.current : null;
    if (!testAgent) {
        return (
            <TestModeShell>
                <Loader2 className="h-5 w-5 animate-spin" />
                <p className="text-xs">Spinning up a test agent for {effectiveTarget.name}&hellip;</p>
            </TestModeShell>
        );
    }

    return (
        <TestModeChatHost
            key={targetKey(effectiveTarget)}
            builderSessionId={sessionId}
            testAgentName={testAgent.instanceName ?? testAgent.name}
            target={effectiveTarget}
            onApplyChangesToBuild={onApplyChangesToBuild}
            onTestRunningChange={onTestRunningChange}
            stopTestRef={stopTestRef}
            onLatestRunTaskIdChange={onLatestRunTaskIdChange}
            selectedActivityRun={selectedActivityRun}
            onSelectActivityRun={onSelectActivityRun}
            pinnedTestSessionId={pinnedTestSessionId}
            onPinnedTestSessionApplied={onPinnedTestSessionApplied}
        />
    );
}

type LiveAgentLike = { name?: string; display_name?: string; displayName?: string };

function isTargetReadyToRetry(target: TestTarget, addressableName: string | null, agents: LiveAgentLike[] | undefined, manifestComponents: { name: string; status?: string }[] | undefined): boolean {
    if (target.type === "skill") {
        const status = manifestComponents?.find(c => c.name === target.name)?.status;
        return status === "validated" || status === "deployed";
    }
    const wanted = addressableName ?? target.name;
    return (agents ?? []).some(a => a.name === wanted || a.display_name === wanted || a.displayName === wanted);
}

function isCreateFor(variables: { sessionId: string; componentName: string; componentKind: string } | undefined, sessionId: string | null, target: TestTarget, addressableName: string | null): boolean {
    return variables?.sessionId === sessionId && variables.componentName === (addressableName ?? target.name) && variables.componentKind === target.type;
}

function useRetryWhenTargetBecomesReady(failed: boolean, ready: boolean, retry: () => void) {
    const readyAtLastCheck = useRef<boolean | null>(null);
    useEffect(() => {
        if (!failed) {
            readyAtLastCheck.current = null;
            return;
        }
        const wasReady = readyAtLastCheck.current;
        readyAtLastCheck.current = ready;
        if (wasReady === false && ready) retry();
    }, [failed, ready, retry]);
}

function TestModeShell({ children }: { children: React.ReactNode }) {
    return (
        <div className="flex h-full flex-col items-center justify-start gap-3 p-8 text-center">
            <div className="flex h-12 w-12 items-center justify-center rounded-full bg-(--primary-w10)">
                <FlaskConical className="h-6 w-6 text-(--primary-wMain)" />
            </div>
            <h2 className="text-lg font-semibold">Welcome to Test Mode!</h2>
            <p className="max-w-md text-sm leading-relaxed">This is where you can play around with the objects that were created to make sure they work the way you want. How do you want to get started?</p>
            {children}
        </div>
    );
}

function TestModeChatHost({
    builderSessionId,
    testAgentName,
    target,
    onApplyChangesToBuild,
    onTestRunningChange,
    stopTestRef,
    onLatestRunTaskIdChange,
    selectedActivityRun,
    onSelectActivityRun,
    pinnedTestSessionId,
    onPinnedTestSessionApplied,
}: {
    builderSessionId: string;
    testAgentName: string;
    target: TestTarget;
    onApplyChangesToBuild?: (report: TestReport) => void | Promise<boolean>;
    onTestRunningChange?: (running: boolean) => void;
    stopTestRef?: React.MutableRefObject<(() => void) | null>;
    onLatestRunTaskIdChange?: (taskId: string | null) => void;
    selectedActivityRun?: { runNumber: number; taskId: string } | null;
    onSelectActivityRun?: (sel: { runNumber: number; taskId: string } | null) => void;
    pinnedTestSessionId?: string | null;
    onPinnedTestSessionApplied?: () => void;
}) {
    const { testSessionId, sessionError, retry } = useTestSessionLifecycle({
        builderSessionId,
        testAgentName,
        targetName: target.name,
        pinnedTestSessionId,
        onPinnedTestSessionApplied,
    });

    if (sessionError) {
        return (
            <TestModeShell>
                <p className="text-sm text-(--error-wMain)">{sessionError}</p>
                <Button size="sm" variant="outline" onClick={retry}>
                    Try again
                </Button>
            </TestModeShell>
        );
    }

    if (!testSessionId) {
        return (
            <TestModeShell>
                <Loader2 className="h-5 w-5 animate-spin" />
            </TestModeShell>
        );
    }

    return (
        <ChatProvider key={testSessionId} disableBuilderBridge>
            <TestModeSessionInitializer
                builderSessionId={builderSessionId}
                testSessionId={testSessionId}
                testAgentName={testAgentName}
                target={target}
                onApplyChangesToBuild={onApplyChangesToBuild}
                onTestRunningChange={onTestRunningChange}
                stopTestRef={stopTestRef}
                onLatestRunTaskIdChange={onLatestRunTaskIdChange}
                selectedActivityRun={selectedActivityRun}
                onSelectActivityRun={onSelectActivityRun}
            />
        </ChatProvider>
    );
}

function TestModeSessionInitializer({
    builderSessionId,
    testSessionId,
    testAgentName,
    target,
    onApplyChangesToBuild,
    onTestRunningChange,
    stopTestRef,
    onLatestRunTaskIdChange,
    selectedActivityRun,
    onSelectActivityRun,
}: {
    builderSessionId: string;
    testSessionId: string;
    testAgentName: string;
    target: TestTarget;
    onApplyChangesToBuild?: (report: TestReport) => void | Promise<boolean>;
    onTestRunningChange?: (running: boolean) => void;
    stopTestRef?: React.MutableRefObject<(() => void) | null>;
    onLatestRunTaskIdChange?: (taskId: string | null) => void;
    selectedActivityRun?: { runNumber: number; taskId: string } | null;
    onSelectActivityRun?: (sel: { runNumber: number; taskId: string } | null) => void;
}) {
    const { handleSwitchSession, sessionId, setSelectedAgentName, selectedAgentName, agents } = useChatContext() as unknown as {
        handleSwitchSession: (id: string) => Promise<void>;
        sessionId: string | null;
        setSelectedAgentName: (name: string) => void;
        selectedAgentName: string;
        agents?: { name?: string }[];
    };
    const switchedRef = useRef(false);

    useEffect(() => {
        if (switchedRef.current) return;
        if (sessionId === testSessionId) {
            switchedRef.current = true;
            return;
        }
        switchedRef.current = true;
        handleSwitchSession(testSessionId).catch(() => {});
    }, [testSessionId, sessionId, handleSwitchSession]);

    useEffect(() => {
        if (!agents?.length) return;
        if (selectedAgentName === testAgentName) return;
        const exact = agents.find(a => a.name === testAgentName);
        if (exact?.name) {
            setSelectedAgentName(exact.name);
        } else {
            setSelectedAgentName(testAgentName);
        }
    }, [agents, testAgentName, selectedAgentName, setSelectedAgentName]);

    if (sessionId !== testSessionId) {
        return (
            <TestModeShell>
                <Loader2 className="h-5 w-5 animate-spin" />
            </TestModeShell>
        );
    }

    return (
        <TestModeChat
            builderSessionId={builderSessionId}
            testAgentName={testAgentName}
            target={target}
            onApplyChangesToBuild={onApplyChangesToBuild}
            onTestRunningChange={onTestRunningChange}
            stopTestRef={stopTestRef}
            onLatestRunTaskIdChange={onLatestRunTaskIdChange}
            selectedActivityRun={selectedActivityRun}
            onSelectActivityRun={onSelectActivityRun}
        />
    );
}

function TestModeChat({
    builderSessionId,
    testAgentName,
    target,
    onApplyChangesToBuild,
    onTestRunningChange,
    stopTestRef,
    onLatestRunTaskIdChange,
    selectedActivityRun,
    onSelectActivityRun,
}: {
    builderSessionId: string;
    testAgentName: string;
    target: TestTarget;
    onApplyChangesToBuild?: (report: TestReport) => void | Promise<boolean>;
    onTestRunningChange?: (running: boolean) => void;
    stopTestRef?: React.MutableRefObject<(() => void) | null>;
    onLatestRunTaskIdChange?: (taskId: string | null) => void;
    selectedActivityRun?: { runNumber: number; taskId: string } | null;
    onSelectActivityRun?: (sel: { runNumber: number; taskId: string } | null) => void;
}) {
    const {
        messages,
        handleSubmit,
        isResponding,
        handleCancel,
        sessionId: testSessionId,
    } = useChatContext() as unknown as {
        messages: MessageFE[];
        handleSubmit: (...args: unknown[]) => void;
        isResponding: boolean;
        handleCancel: () => void;
        sessionId: string | null;
    };

    useEffect(() => {
        if (!stopTestRef) return;
        stopTestRef.current = handleCancel;
        return () => {
            if (stopTestRef.current === handleCancel) stopTestRef.current = null;
        };
    }, [handleCancel, stopTestRef]);
    const realMessageCount = useMemo(() => messages.filter((m: MessageFE) => !m.isStatusBubble).length, [messages]);
    const hasConversation = realMessageCount > 0;

    const { plan: testPlan, runNumber: planRunNumber } = useTestPlan();
    const { report: testReport, runNumber: reportRunNumber } = useTestReport();

    const latestRunTaskId = useMemo(() => {
        for (let i = messages.length - 1; i >= 0; i--) {
            const m = messages[i];
            if (m.isUser && m.taskId) return m.taskId as string;
        }
        for (let i = messages.length - 1; i >= 0; i--) {
            const m = messages[i];
            if (!m.isUser && m.taskId) return m.taskId as string;
        }
        return null;
    }, [messages]);
    useEffect(() => {
        onLatestRunTaskIdChange?.(latestRunTaskId);
    }, [latestRunTaskId, onLatestRunTaskIdChange]);

    const runTaskIdSnapshotRef = useRef<string | null>(null);

    interface RunRecord {
        runNumber: number;
        report: TestReport;
        anchorMessageId: string | null;
        taskId: string | null;
    }
    const [runHistory, setRunHistory] = useState<RunRecord[]>([]);
    useEffect(() => {
        if (!testReport || reportRunNumber === null) return;
        setRunHistory(prev => {
            if (prev.some(r => r.runNumber === reportRunNumber)) return prev;
            let anchor: string | null = null;
            for (let i = messages.length - 1; i >= 0; i--) {
                const m = messages[i];
                if (!m.isUser && m.metadata?.messageId) {
                    anchor = m.metadata.messageId as string;
                    break;
                }
            }
            const taskIdForRun = runTaskIdSnapshotRef.current ?? latestRunTaskId ?? testReport.taskId ?? null;
            return [
                ...prev,
                {
                    runNumber: reportRunNumber,
                    report: testReport,
                    anchorMessageId: anchor,
                    taskId: taskIdForRun,
                },
            ].sort((a, b) => a.runNumber - b.runNumber);
        });
    }, [testReport, reportRunNumber, latestRunTaskId, messages]);

    useEffect(() => {
        setRunHistory([]);
    }, [testAgentName]);

    const hydratedRef = useRef<string | null>(null);
    useEffect(() => {
        if (!testSessionId) return;
        if (hydratedRef.current === testSessionId) return;
        hydratedRef.current = testSessionId;
        let cancelled = false;
        (async () => {
            try {
                const versionsRes = await api.webui.get<{ data: number[] }>(`/api/v1/artifacts/${testSessionId}/${encodeURIComponent(TEST_REPORT_ARTIFACT_FILENAME)}/versions`);
                const versions = versionsRes?.data ?? [];
                if (cancelled || versions.length === 0) return;
                const sorted = [...versions].sort((a, b) => a - b);
                const fetched: { v: number; report: TestReport }[] = [];
                for (const v of sorted) {
                    const res = await api.webui.get(`/api/v1/artifacts/${testSessionId}/${encodeURIComponent(TEST_REPORT_ARTIFACT_FILENAME)}/versions/${v}`, { fullResponse: true });
                    if (cancelled) return;
                    if (!res?.ok) continue;
                    const text = await res.text();
                    try {
                        const report = JSON.parse(text) as TestReport;
                        fetched.push({ v, report });
                    } catch {}
                }
                if (cancelled || fetched.length === 0) return;
                setRunHistory(prev => {
                    const known = new Set(prev.map(r => r.runNumber));
                    const additions: RunRecord[] = [];
                    for (const { v, report } of fetched) {
                        const runNumber = v + 1;
                        if (known.has(runNumber)) continue;
                        additions.push({
                            runNumber,
                            report,
                            anchorMessageId: null,
                            taskId: report.taskId ?? null,
                        });
                    }
                    if (additions.length === 0) return prev;
                    return [...prev, ...additions].sort((a, b) => a.runNumber - b.runNumber);
                });
            } catch (err) {
                console.warn("[TestModePanel] failed to hydrate runHistory", err);
            }
        })();
        return () => {
            cancelled = true;
        };
    }, [testSessionId]);

    const lastPinnedRunRef = useRef<number | null>(null);
    useEffect(() => {
        if (runHistory.length === 0) return;
        const latest = runHistory[runHistory.length - 1];
        if (!latest.taskId) return;
        if (lastPinnedRunRef.current === latest.runNumber) return;
        lastPinnedRunRef.current = latest.runNumber;
        onSelectActivityRun?.({ runNumber: latest.runNumber, taskId: latest.taskId });
    }, [runHistory, onSelectActivityRun]);

    const planForRender = useMemo<TestPlan | null>(() => {
        if (!testPlan) return null;
        const planV = planRunNumber ?? 0;
        const reportV = reportRunNumber ?? 0;
        if (testReport && testReport.componentName === testPlan.componentName && reportV >= planV) {
            return null;
        }
        return testPlan;
    }, [testPlan, testReport, planRunNumber, reportRunNumber]);

    const [running, setRunning] = useState(false);
    useEffect(() => {
        if (!isResponding && running) setRunning(false);
    }, [isResponding, running]);
    useEffect(() => {
        onTestRunningChange?.(running);
    }, [running, onTestRunningChange]);

    useEffect(() => {
        if (running) {
            onSelectActivityRun?.(null);
            runTaskIdSnapshotRef.current = null;
        }
    }, [running, onSelectActivityRun]);

    useEffect(() => {
        if (running && latestRunTaskId) {
            runTaskIdSnapshotRef.current = latestRunTaskId;
        }
    }, [running, latestRunTaskId]);

    const sendMessage = useCallback(
        (text: string) => {
            if (isResponding) return;
            const fakeEvent = { preventDefault: () => {} } as unknown as FormEvent;
            handleSubmit(fakeEvent, null, text);
        },
        [handleSubmit, isResponding]
    );

    const handleStartTest = useCallback(
        (edited: TestPlan) => {
            if (isResponding) return;
            const payload = JSON.stringify(
                {
                    sampleInput: edited.sampleInput,
                    expectedOutput: edited.expectedOutput,
                },
                null,
                2
            );
            const text = `Run this test now. Use the plan I've reviewed:\n\n\`\`\`json\n${payload}\n\`\`\`\n\nInvoke the wrapped component with sampleInput, capture the actual output, then call sam-test-harness__generate_recommendations.`;
            setRunning(true);
            const fakeEvent = { preventDefault: () => {} } as unknown as FormEvent;
            handleSubmit(fakeEvent, null, text, null, null, null, null, null, { hidden: true });
        },
        [isResponding, handleSubmit]
    );

    const handleCancelPlan = useCallback(() => {
        if (isResponding) return;
        const fakeEvent = { preventDefault: () => {} } as unknown as FormEvent;
        handleSubmit(fakeEvent, null, "I'd like a different test plan — please ask me what to change or generate a new one.", null, null, null, null, null, { hidden: true });
    }, [handleSubmit, isResponding]);

    const appliedRunsStorageKey = `sam_test_applied::${builderSessionId}::${testAgentName}`;
    const [appliedRunNumbers, setAppliedRunNumbersRaw] = useState<Set<number>>(() => {
        try {
            const raw = localStorage.getItem(appliedRunsStorageKey);
            if (!raw) return new Set();
            const parsed = JSON.parse(raw);
            return Array.isArray(parsed) ? new Set<number>(parsed.filter((n): n is number => typeof n === "number")) : new Set();
        } catch {
            return new Set();
        }
    });
    const setAppliedRunNumbers: typeof setAppliedRunNumbersRaw = updater => {
        setAppliedRunNumbersRaw(prev => {
            const next = typeof updater === "function" ? (updater as (p: Set<number>) => Set<number>)(prev) : updater;
            try {
                localStorage.setItem(appliedRunsStorageKey, JSON.stringify([...next]));
            } catch {}
            return next;
        });
    };
    useEffect(() => {
        setAppliedRunNumbersRaw(() => {
            try {
                const raw = localStorage.getItem(appliedRunsStorageKey);
                if (!raw) return new Set<number>();
                const parsed = JSON.parse(raw);
                return Array.isArray(parsed) ? new Set<number>(parsed.filter((n): n is number => typeof n === "number")) : new Set<number>();
            } catch {
                return new Set<number>();
            }
        });
    }, [appliedRunsStorageKey]);

    const handleApplyChanges = useCallback(
        async (report: TestReport, runNumber: number) => {
            if (onApplyChangesToBuild) {
                const result = onApplyChangesToBuild(report);
                const confirmed = result instanceof Promise ? await result : true;
                if (!confirmed) return;
                setAppliedRunNumbers(prev => {
                    if (prev.has(runNumber)) return prev;
                    const next = new Set(prev);
                    next.add(runNumber);
                    return next;
                });
                return;
            }
            sendMessage("I'd like to apply these recommendations to the build. Please summarise them so I can paste them into Build mode.");
            setAppliedRunNumbers(prev => {
                if (prev.has(runNumber)) return prev;
                const next = new Set(prev);
                next.add(runNumber);
                return next;
            });
        },
        [onApplyChangesToBuild, sendMessage]
    );

    const handleContinueTesting = useCallback(() => {
        if (isResponding) return;
        onSelectActivityRun?.(null);
        const fakeEvent = { preventDefault: () => {} } as unknown as FormEvent;
        handleSubmit(fakeEvent, null, "I'd like to run another test. Ask me one short question about what I'd like to focus on or test next, then wait for my answer. Don't list test ideas or generate a plan yet.", null, null, null, null, null, {
            hidden: true,
        });
    }, [onSelectActivityRun, handleSubmit, isResponding]);

    const handleViewActivity = useCallback(
        (runRecord: RunRecord) => {
            if (!runRecord.taskId) return;
            onSelectActivityRun?.({ runNumber: runRecord.runNumber, taskId: runRecord.taskId });
        },
        [onSelectActivityRun]
    );

    const sendPreset = (prompt: string) => sendMessage(prompt);

    const renderMessageAddon = useCallback(
        (message: MessageFE) => {
            if (message.isUser) return null;
            const last = lastNonUserMessage(messages);
            const messageId = message.metadata?.messageId as string | undefined;
            const isLast = !!last && message === last;
            const addons: React.ReactNode[] = [];

            const knownIds = new Set<string>();
            for (const m of messages) {
                if (m.metadata?.messageId) knownIds.add(m.metadata.messageId as string);
            }
            for (const run of runHistory) {
                const anchor = run.anchorMessageId && knownIds.has(run.anchorMessageId) ? run.anchorMessageId : null;
                const attachHere = anchor ? messageId === anchor : isLast;
                if (!attachHere) continue;
                addons.push(
                    <TestReportCard
                        key={`test-report-${run.runNumber}`}
                        report={run.report}
                        runNumber={run.runNumber}
                        onApplyChanges={report => handleApplyChanges(report, run.runNumber)}
                        onContinueTesting={handleContinueTesting}
                        onViewActivity={run.taskId ? () => handleViewActivity(run) : undefined}
                        isActivityShown={!!selectedActivityRun && selectedActivityRun.runNumber === run.runNumber}
                        alreadyApplied={appliedRunNumbers.has(run.runNumber)}
                    />
                );
            }

            if (isLast && planForRender) {
                addons.push(<TestPlanCard key="test-plan" plan={planForRender} runNumber={planRunNumber} onStartTest={handleStartTest} onCancel={handleCancelPlan} busy={running} />);
            }
            return addons.length > 0 ? <>{addons}</> : null;
        },
        [messages, planForRender, planRunNumber, runHistory, running, handleStartTest, handleCancelPlan, handleApplyChanges, handleContinueTesting, handleViewActivity, selectedActivityRun, appliedRunNumbers]
    );

    return (
        <div className="flex h-full flex-col">
            {!hasConversation && (
                <div className="flex flex-col items-center gap-3 px-6 py-8 text-center">
                    <div className="flex h-12 w-12 items-center justify-center rounded-full bg-(--primary-w10)">
                        <FlaskConical className="h-6 w-6 text-(--primary-wMain)" />
                    </div>
                    <h2 className="text-lg font-semibold">Welcome to Test Mode!</h2>
                    <p className="max-w-md text-sm leading-relaxed">This is where you can play around with the objects that were created to make sure they work the way you want. How do you want to get started?</p>
                    <div className="mt-2 flex flex-col items-center gap-2">
                        {PRESET_PROMPTS.map(p => (
                            <Button key={p} variant="outline" size="sm" disabled={isResponding} onClick={() => sendPreset(p)}>
                                {p}
                            </Button>
                        ))}
                    </div>
                </div>
            )}

            <div className="min-h-0 flex-1 overflow-hidden">
                <ChatArea compact allowAgentSelection={false} hideWelcomeScreen renderMessageAddon={renderMessageAddon} inputFormHeader={<TestingInputBanner target={target} />} />
            </div>
        </div>
    );
}

function TestingInputBanner({ target }: { target: TestTarget }) {
    const iconByKind: Record<string, typeof Bot> = { workflow: Workflow, skill: BookOpen, a2a_agent: Globe };
    const Icon = iconByKind[target.type] ?? Bot;
    return (
        <div className="mb-2 flex items-center gap-2 text-sm">
            <span>Testing:</span>
            <Icon className="h-4 w-4 shrink-0 text-(--success-wMain)" />
            <span className="font-semibold">{target.name}</span>
        </div>
    );
}

function lastNonUserMessage(messages: MessageFE[]): MessageFE | null {
    for (let i = messages.length - 1; i >= 0; i--) {
        const m = messages[i];
        if (!m.isUser && !m.isStatusBubble) return m;
    }
    return null;
}
