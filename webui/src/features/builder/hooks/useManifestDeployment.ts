// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type MutableRefObject, useCallback, useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";

import { useAgentCards } from "@/api/agent-cards";
import { useAgentDeploymentMutation } from "@/api/agent-deployments";
import { builderKeys, listManifestVersions } from "@/api/builder";
import { useGatewayDeploymentMutation } from "@/api/gateways";

import { useChatContext } from "@/providers/contexts";

import { useIsBuilderServerApplyEnabled } from "@/hooks/useIsBuilderServerApplyEnabled";

import { reportAndNotifyError } from "@/common/product-analytics/reportAndNotifyError";

import { getErrorMessage } from "@/utils/api";

import { buildBlockedByCredsMessage, fetchPlatformDeploymentLookup, isComponentLiveOnMesh, isSessionComponentLive, normalizeComponentName, operationNoun, type PlatformLookupEntry } from "../builderUtils";
import { type ArtifactVersions, componentFreshness, hasUndeployedEdits, hasUnsavedSaveOnlyEdits, isComponentDirty, isRunningStaleConfig, isSaveOnlyType, versionToRecord } from "../componentDirtyState";
import { dropLoadedForEdit } from "../loadedForEdit";

import { useArtifactVersions } from "./useArtifactVersions";
import type { BuildManifest, SaveManifestOptions, SaveManifestResult } from "./useBuilderManifest";
import type { DeployManifestOptions, ReportErrorToAgentFn, SaveResult } from "./useSaveOrchestration";
import { useWorkflowDeployment } from "./useWorkflowDeployment";

export function shouldDowngradeToValidated(args: { status?: string; platformStatus?: string; kindFetched: boolean; savedThisSession: boolean }): boolean {
    if (args.status !== "deployed") return false;
    if (args.savedThisSession) return false;
    if (!args.kindFetched) return false;
    return args.platformStatus !== "deployed";
}

export function shouldDowngradeComponent(args: { status?: string; platformStatus?: string; kindFetched: boolean; savedThisSession: boolean; liveOnMesh: boolean }): boolean {
    if (args.liveOnMesh) return false;
    return shouldDowngradeToValidated(args);
}

export function shouldUpgradeToDeployed(args: { status?: string; platformStatus?: string; runningStaleConfig: boolean }): boolean {
    if (args.status !== "validated") return false;
    if (args.platformStatus !== "deployed") return false;
    return !args.runningStaleConfig;
}

export function reconcileComponentStatuses(upgrade: Set<string>, downgrade: Set<string>) {
    return (prev: BuildManifest): BuildManifest => ({
        ...prev,
        components: prev.components?.map(c => {
            if (upgrade.has(c.name) && c.status === "validated") return { ...c, status: "deployed" };
            if (downgrade.has(c.name) && c.status === "deployed") return { ...c, status: "validated" };
            return c;
        }),
    });
}

function namesIn(ref: MutableRefObject<string[]> | undefined): ReadonlySet<string> {
    return new Set(ref?.current ?? []);
}

function takeProof(ref: MutableRefObject<string[]> | undefined): ReadonlySet<string> {
    const names = namesIn(ref);
    if (ref) ref.current = [];
    return names;
}

export function withoutVersionsFor(versions: ArtifactVersions, components: ReadonlyArray<{ name: string; filename?: string }>, names: ReadonlySet<string>): ArtifactVersions {
    if (names.size === 0) return versions;
    const out = new Map(versions);
    for (const c of components) {
        if (names.has(c.name) && c.filename) out.delete(c.filename);
    }
    return out;
}

export function markComponentsDeployed(entries: ReadonlyArray<{ name: string; id?: string; type?: string }>, versions?: ArtifactVersions) {
    const stamp = stampDeployed(entries, versions);
    return (prev: BuildManifest): BuildManifest => dropLoadedForEdit(stamp(prev));
}

function stampDeployed(entries: ReadonlyArray<{ name: string; id?: string; type?: string }>, versions?: ArtifactVersions) {
    return (prev: BuildManifest): BuildManifest => ({
        ...prev,
        components: prev.components?.map(c => {
            const entry = entries.find(e => e.name === c.name && (e.type === undefined || e.type === c.type));
            if (!entry) return c;
            return { ...c, status: "deployed", ...(entry.id ? { id: entry.id } : {}), ...deployedVersionPatch(c, versions) };
        }),
    });
}

function deployedVersionPatch(comp: { filename?: string; origin?: string }, versions?: ArtifactVersions): { deployed_version?: number } {
    const version = versionToRecord(comp, versions);
    return version === undefined ? {} : { deployed_version: version };
}

export async function resolveDeployedVersions(
    components: ReadonlyArray<{ name: string; filename?: string; origin?: string }>,
    deployedNames: ReadonlySet<string>,
    base: ArtifactVersions,
    fetchVersions: (filename: string) => Promise<number[]>
): Promise<ArtifactVersions> {
    const filenames = [...new Set(components.filter(c => deployedNames.has(c.name) && c.origin !== "platform" && !!c.filename).map(c => c.filename as string))];
    if (filenames.length === 0) return base;
    const fetched = await Promise.all(
        filenames.map(async (filename): Promise<readonly [string, number] | null> => {
            try {
                const versions = await fetchVersions(filename);
                return versions.length > 0 ? [filename, Math.max(...versions)] : null;
            } catch (err) {
                console.warn("[builder] deployed_version lookup failed", { filename, error: getErrorMessage(err) });
                return null;
            }
        })
    );
    const resolved = new Map(base);
    for (const entry of fetched) {
        if (entry) resolved.set(entry[0], entry[1]);
    }
    return resolved;
}

export function recordComponentIds(entries: ReadonlyArray<{ name: string; id: string; type?: string }>) {
    return (prev: BuildManifest): BuildManifest => ({
        ...prev,
        components: prev.components?.map(c => {
            const entry = entries.find(e => e.name === c.name && (e.type === undefined || e.type === c.type));
            return entry ? { ...c, id: entry.id } : c;
        }),
    });
}

export type PlatformRecord = Pick<PlatformLookupEntry, "id" | "deploymentStatus" | "name" | "displayName">;
export type PlatformLookups = {
    agentsByName: Map<string, PlatformRecord>;
    workflowsByName: Map<string, PlatformRecord>;
    gatewaysByName: Map<string, PlatformRecord>;
    remoteAgentsByName: Map<string, PlatformRecord>;
};

function lookupTableForType(type: string | undefined, tables: PlatformLookups): Map<string, PlatformRecord> {
    if (type === "workflow") return tables.workflowsByName;
    if (type === "gateway") return tables.gatewaysByName;
    if (type === "a2a_agent") return tables.remoteAgentsByName;
    return tables.agentsByName;
}

export function foldDeploymentStatuses(maps: Array<Map<string, PlatformRecord>>): Map<string, string> {
    const out = new Map<string, string>();
    for (const m of maps) {
        for (const [name, entry] of m) {
            if (entry.deploymentStatus) out.set(name, entry.deploymentStatus);
        }
    }
    return out;
}

export function foldDeploymentStatusesById(maps: Array<Map<string, PlatformRecord>>): Map<string, string> {
    const out = new Map<string, string>();
    for (const m of maps) {
        for (const entry of m.values()) {
            if (entry.id && entry.deploymentStatus) out.set(entry.id, entry.deploymentStatus);
        }
    }
    return out;
}

export function ownPlatformStatus(comp: { id?: string }, statusById: ReadonlyMap<string, string>): string | undefined {
    return comp.id ? statusById.get(comp.id) : undefined;
}

export function platformStatusOfComponent(comp: { id?: string; name: string }, statusById: ReadonlyMap<string, string>, statusByName: ReadonlyMap<string, string>): string | undefined {
    if (comp.id) return statusById.get(comp.id);
    return statusByName.get(comp.name) ?? statusByName.get(normalizeComponentName(comp.name));
}

export function liveShieldsFromDowngrade(comp: { id?: string; name: string; type?: string }, cards: ReadonlyArray<{ name?: string; displayName?: string }> | undefined): boolean {
    return comp.id ? isSessionComponentLive(comp, cards) : isComponentLiveOnMesh(comp.name, cards);
}

type ReconcilableComponent = NonNullable<BuildManifest["components"]>[number];

export interface FetchedKinds {
    agent: boolean;
    workflow: boolean;
    gateway: boolean;
    a2aAgent: boolean;
}

function fetchedForComponentType(type: string | undefined, fetched: FetchedKinds): boolean {
    if (type === "workflow") return fetched.workflow;
    if (type === "gateway") return fetched.gateway;
    if (type === "a2a_agent") return fetched.a2aAgent;
    return fetched.agent;
}

export interface ReconciliationInputs {
    components: readonly ReconcilableComponent[];
    statusById: ReadonlyMap<string, string>;
    statusByName: ReadonlyMap<string, string>;
    fetchedKinds: FetchedKinds;
    savedNames: ReadonlySet<string>;
    liveCards: ReadonlyArray<{ name?: string; displayName?: string }> | undefined;
    isRunningStale: (component: ReconcilableComponent) => boolean;
}

export function planStatusReconciliation(inputs: ReconciliationInputs): { upgrade: ReconcilableComponent[]; downgrade: ReconcilableComponent[] } {
    const reconcilable = (c: ReconcilableComponent) => c.origin !== "platform" && c.type !== "skill" && c.type !== "connector";
    const upgrade = inputs.components.filter(c => reconcilable(c) && shouldUpgradeToDeployed({ status: c.status, platformStatus: ownPlatformStatus(c, inputs.statusById), runningStaleConfig: inputs.isRunningStale(c) }));
    const downgrade = inputs.components.filter(
        c =>
            reconcilable(c) &&
            shouldDowngradeComponent({
                status: c.status,
                platformStatus: platformStatusOfComponent(c, inputs.statusById, inputs.statusByName),
                kindFetched: fetchedForComponentType(c.type, inputs.fetchedKinds),
                savedThisSession: inputs.savedNames.has(c.name),
                liveOnMesh: liveShieldsFromDowngrade(c, inputs.liveCards),
            })
    );
    return { upgrade, downgrade };
}

function resolvePlatformRecord(comp: { id?: string; name: string; type?: string }, tables: PlatformLookups, saved: ReadonlyMap<string, SaveResult>): PlatformRecord | undefined {
    const fromSave = saved.get(comp.name);
    const id = fromSave && fromSave.type === comp.type ? fromSave.id : comp.id;
    if (!id) return undefined;
    return [...new Set(lookupTableForType(comp.type, tables).values())].find(record => record.id === id);
}

export interface DeployMutators {
    deployWorkflow: (args: { workflowId: string; action: "deploy" }) => Promise<unknown>;
    gatewayDeployment: { mutateAsync: (args: { gatewayId: string; action: "deploy" }) => Promise<unknown> };
    agentDeployment: { mutateAsync: (args: { agentId: string; action: "deploy"; skipIfCurrent?: boolean }) => Promise<unknown> };
}

async function deployComponentByType(type: string | undefined, id: string, mutators: DeployMutators): Promise<void> {
    if (type === "workflow") {
        await mutators.deployWorkflow({ workflowId: id, action: "deploy" });
    } else if (type === "gateway") {
        await mutators.gatewayDeployment.mutateAsync({ gatewayId: id, action: "deploy" });
    } else {
        await mutators.agentDeployment.mutateAsync({ agentId: id, action: "deploy", skipIfCurrent: true });
    }
}

interface UseManifestDeploymentParams {
    manifest?: BuildManifest | null;
    componentsMissingCreds?: ReadonlyMap<string, number>;
    saveManifest: (updater: (prev: BuildManifest) => BuildManifest, options?: SaveManifestOptions) => Promise<SaveManifestResult>;
    savedComponents: Map<string, SaveResult>;
    changedOnPlatformRef?: MutableRefObject<string[] | undefined>;
    matchedOnPlatformRef?: MutableRefObject<string[]>;
    savedResultsRef?: MutableRefObject<ReadonlyMap<string, SaveResult>>;
    declinedOnPlatformRef?: MutableRefObject<string[]>;
    allSaved: boolean;
    autoSavedInThisFlowRef: MutableRefObject<boolean>;
    userAgentsRefetch?: () => Promise<void>;
    yamlAgentCardsRefetch?: () => void;
    reportErrorToAgent: ReportErrorToAgentFn;
    setError: (msg: string | null) => void;
    setBlockedError: (msg: string) => void;
    setDeployOutcomeError: (msg: string, failedNames: readonly string[]) => void;
    clearUnlessDeployOutcome: () => void;
    autoDeployTriggeredRef: MutableRefObject<boolean>;
    manualDeployRef?: MutableRefObject<boolean>;
    bridged?: boolean;
}

export interface UseManifestDeploymentResult {
    isDeploying: boolean;
    isDeployingManifest: boolean;
    hasUndeployed: boolean;
    hasUndeployedSessionComponents: boolean;
    handleDeploy: () => Promise<void>;
    handleDeployManifest: (changedOnPlatform?: readonly string[], options?: DeployManifestOptions) => Promise<void>;
    autoDeployTriggeredRef: MutableRefObject<boolean>;
}

function notifyUndeployable(addNotification: Parameters<typeof reportAndNotifyError>[0], missing: readonly string[], foreign: readonly string[]) {
    if (missing.length > 0) reportAndNotifyError(addNotification, `Not found on platform: ${missing.join(", ")}. Save the build first.`, "api");
    if (foreign.length > 0) reportAndNotifyError(addNotification, foreignOnPlatformMessage(foreign), "api");
}

export function foreignOnPlatformMessage(names: readonly string[]): string {
    const plural = names.length > 1;
    return `${names.join(", ")} already ${plural ? "exist" : "exists"} on the platform and this build could not confirm ${plural ? "they are" : "it is"} its own; open ${plural ? "them" : "it"} from the list to keep working on ${plural ? "them" : "it"}, or rename ${plural ? "these" : "this one"} and build again.`;
}

export function flaggedSkippedMessage(names: readonly string[]): string {
    return `Not deployed, flagged with an error by the builder: ${names.join(", ")}.`;
}

export function deployOutcomeErrorMessage(missing: readonly string[], failed: readonly { name: string; error: string }[], foreign: readonly string[]): string | null {
    const parts: string[] = [];
    if (failed.length > 0) {
        const detail = failed.map(f => `${f.name} (${f.error})`).join("; ");
        parts.push(`Deploy failed: ${detail}`);
    }
    if (missing.length > 0) parts.push(`Not found on platform: ${missing.join(", ")}. Save the build first.`);
    if (foreign.length > 0) parts.push(foreignOnPlatformMessage(foreign));
    return parts.length > 0 ? parts.join(" ") : null;
}

export function hasSaveableSessionComponent(
    manifest: BuildManifest | undefined | null,
    platformDeploymentStatus: Map<string, string>,
    serverApplyEnabled: boolean,
    versions?: ArtifactVersions,
    platformStatusById?: ReadonlyMap<string, string>
): boolean {
    return !!manifest?.components?.some(c => {
        if (c.origin === "platform") return false;
        if (isSaveOnlyType(c.type)) return hasUnsavedSaveOnlyEdits(c, versions, serverApplyEnabled);
        if (isFlaggedError(c)) return false;
        if (c.status !== "deployed") return true;
        const status = platformStatusById ? platformStatusOfComponent(c, platformStatusById, platformDeploymentStatus) : (platformDeploymentStatus.get(c.name) ?? platformDeploymentStatus.get(normalizeComponentName(c.name)));
        return status !== "deployed" || hasUndeployedEdits(c, versions, serverApplyEnabled);
    });
}

export function expandConnectorsToConsumers(changed: readonly string[], components: readonly NonNullable<BuildManifest["components"]>[number][]): string[] {
    const changedSet = new Set(changed.map(normalizeComponentName));
    const isChangedConnector = (name: string) => {
        const normalized = normalizeComponentName(name);
        return changedSet.has(normalized) && components.some(c => c.type === "connector" && normalizeComponentName(c.name) === normalized);
    };

    const out = [...changed];
    for (const comp of components) {
        if (comp.type !== "agent") continue;
        const binds = [...(comp.connectors ?? []), ...(comp.depends_on ?? [])];
        if (binds.some(isChangedConnector)) out.push(comp.name);
    }
    return out;
}

export function needsDeploy(deploymentStatus: string | undefined, componentName: string, changedOnPlatform: ReadonlySet<string>): boolean {
    return deploymentStatus !== "deployed" || isNamedIn(changedOnPlatform, componentName);
}

function isNamedIn(names: ReadonlySet<string>, componentName: string): boolean {
    if (names.has(componentName)) return true;
    const normalized = normalizeComponentName(componentName);
    for (const name of names) {
        if (normalizeComponentName(name) === normalized) return true;
    }
    return false;
}

type ManifestComponents = readonly NonNullable<BuildManifest["components"]>[number][];

function isFlaggedError(component: { status?: string }): boolean {
    return component.status === "error";
}

function isResultFlaggedError(result: SaveResult, components: ManifestComponents): boolean {
    const name = normalizeComponentName(result.name);
    return components.some(c => c.type === result.type && normalizeComponentName(c.name) === name && isFlaggedError(c));
}

function planInFlowDeploy(saved: ReadonlyMap<string, SaveResult>, changed: ReadonlySet<string> | undefined, components: ManifestComponents): { toDeploy: SaveResult[]; skippedAsFlagged: string[] } {
    const changedResults = [...saved.values()].filter(result => !changed || changed.has(normalizeComponentName(result.name)));
    const skipped = changedResults.filter(result => isResultFlaggedError(result, components));
    return { toDeploy: changedResults.filter(result => !skipped.includes(result)), skippedAsFlagged: skipped.map(result => result.name) };
}

function notifyFlaggedSkipped(addNotification: ReturnType<typeof useChatContext>["addNotification"], names: readonly string[]) {
    if (names.length > 0) addNotification(flaggedSkippedMessage(names), "warning");
}

export function deployCandidates(components: ManifestComponents, rewritten: ReadonlySet<string>, onlyRewritten: boolean): ManifestComponents {
    const session = components.filter(c => c.origin !== "platform" && c.type !== "skill" && c.type !== "connector" && !isFlaggedError(c));
    return onlyRewritten ? session.filter(c => isNamedIn(rewritten, c.name)) : session;
}

function rewrittenNames(changed: readonly string[], notWritten: readonly string[], components: ManifestComponents): Set<string> {
    const notWrittenSet = new Set(notWritten);
    return new Set(expandConnectorsToConsumers(changed, components).filter(name => !isNamedIn(notWrittenSet, name)));
}

export function savedAndReported(name: string, notWritten: readonly string[], components: ManifestComponents): boolean {
    if (!notWritten.includes(name)) return false;
    return !components.some(c => c.name === name && (c.type === "skill" || c.type === "connector"));
}

export function passManifestUpdate(settled: ResolveAndDeployOutcome["settled"], identified: ResolveAndDeployOutcome["identified"], versions: ArtifactVersions | undefined, deployed: ReadonlySet<string>) {
    const ran = settled.filter(entry => deployed.has(entry.name));
    const foundRunning = settled.filter(entry => !deployed.has(entry.name));
    return (prev: BuildManifest): BuildManifest => {
        const next = recordComponentIds(identified)(stampDeployed(foundRunning)(stampDeployed(ran, versions)(prev)));
        return deployed.size > 0 ? dropLoadedForEdit(next) : next;
    };
}

function reportPassWrite(settledCount: number, written: SaveManifestResult, reportFlip: (result: SaveManifestResult) => void) {
    if (settledCount > 0) reportFlip(written);
    else if (!written.ok) console.warn("[builder] could not record platform ids", written);
}

export interface ResolveAndDeployOutcome {
    deployed: string[];
    missing: string[];
    failed: Array<{ name: string; error: string }>;
    settled: Array<{ name: string; id: string; type?: string }>;
    identified: Array<{ name: string; id: string; type?: string }>;
    foreign: string[];
    resolvedIds: Array<{ name: string; id: string; type?: string }>;
}

function isUnsettled(comp: NonNullable<BuildManifest["components"]>[number], versions: ArtifactVersions | undefined, matchedOnPlatform: ReadonlySet<string>): boolean {
    if (comp.type === "workflow" || !comp.filename || !matchedOnPlatform.has(comp.name)) return false;
    return comp.deployed_version === undefined || componentFreshness(comp, versions) === "stale";
}

export async function resolveAndDeploy(
    components: readonly NonNullable<BuildManifest["components"]>[number][],
    tables: PlatformLookups,
    rewritten: ReadonlySet<string>,
    mutators: DeployMutators,
    versions?: ArtifactVersions,
    matchedOnPlatform: ReadonlySet<string> = new Set(),
    saved: ReadonlyMap<string, SaveResult> = new Map()
): Promise<ResolveAndDeployOutcome> {
    const out: ResolveAndDeployOutcome = { deployed: [], missing: [], failed: [], foreign: [], settled: [], identified: [], resolvedIds: [] };
    for (const comp of components) {
        const lookup = resolvePlatformRecord(comp, tables, saved);
        if (!lookup) {
            console.warn("[builder] deploy lookup miss", {
                lookingFor: comp.name,
                id: comp.id,
                normalized: normalizeComponentName(comp.name),
                type: comp.type,
                savedThisSession: saved.has(comp.name),
                platformWorkflows: [...tables.workflowsByName.keys()],
                platformAgents: [...tables.agentsByName.keys()],
            });
            (holdsNoId(comp, saved) && answersToName(comp, tables) ? out.foreign : out.missing).push(comp.name);
            continue;
        }
        const staleWorkflow = comp.type === "workflow" && isComponentDirty(comp, versions);
        const unsettled = isUnsettled(comp, versions, matchedOnPlatform);
        if (!staleWorkflow && !unsettled && !needsDeploy(lookup.deploymentStatus, comp.name, rewritten)) {
            recordOutcome(out, comp, lookup, "settled");
            continue;
        }
        try {
            await deployComponentByType(comp.type, lookup.id, mutators);
            out.deployed.push(comp.name);
            recordOutcome(out, comp, lookup, "settled");
        } catch (err) {
            const msg = getErrorMessage(err, "deploy failed");
            console.warn("[builder] deploy failed", comp.name, err);
            out.failed.push({ name: comp.name, error: msg });
            recordOutcome(out, comp, lookup, "identified");
        }
    }
    return out;
}

function recordOutcome(out: ResolveAndDeployOutcome, comp: ManifestComponents[number], record: PlatformRecord, as: "settled" | "identified"): void {
    const entry = { name: comp.name, id: record.id, type: comp.type };
    out[as].push(entry);
    if (comp.id === record.id) return;
    out.resolvedIds.push(entry);
    if (comp.id) console.warn("[builder] component id replaced", { type: comp.type, from: comp.id, to: record.id });
}

function holdsNoId(comp: ManifestComponents[number], saved: ReadonlyMap<string, SaveResult>): boolean {
    return !comp.id && !saved.has(comp.name);
}

function answersToName(comp: ManifestComponents[number], tables: PlatformLookups): boolean {
    const wanted = normalizeComponentName(comp.name);
    return [...lookupTableForType(comp.type, tables).values()].some(record => normalizeComponentName(record.name ?? "") === wanted);
}

interface DeployKinds {
    agent: (id: string) => Promise<unknown>;
    workflow: (id: string) => Promise<unknown>;
    gateway: (id: string) => Promise<unknown>;
}

function namesOf(component: { name: string } | null): string[] {
    return component ? [component.name] : [];
}

export async function deployOneComponent(result: SaveResult, kinds: DeployKinds): Promise<boolean> {
    switch (result.type) {
        case "agent":
        case "a2a_agent":
            await kinds.agent(result.id);
            return true;
        case "workflow":
            await kinds.workflow(result.id);
            return true;
        case "gateway":
            await kinds.gateway(result.id);
            return true;
        case "connector":
            return true;
        default:
            return false;
    }
}

export const FLIP_FAILED_MESSAGE = "Deploy succeeded but the plan could not be updated. The component is deployed; click Retry to refresh the canvas badge.";
export const FLIP_BLOCKED_MESSAGE = "Deployed, but the build plan can't be read, so the canvas badge can't be updated. Ask the builder to repair the plan.";

export function flipFailureReport(result: SaveManifestResult): { channel: "blocked" | "error"; message: string } | null {
    if (result.ok || result.reason === "session_changed") return null;
    return result.reason === "unreadable" ? { channel: "blocked", message: FLIP_BLOCKED_MESSAGE } : { channel: "error", message: FLIP_FAILED_MESSAGE };
}

export function useManifestDeployment({
    manifest,
    componentsMissingCreds,
    changedOnPlatformRef,
    matchedOnPlatformRef,
    savedResultsRef,
    declinedOnPlatformRef,
    saveManifest,
    savedComponents,
    allSaved,
    autoSavedInThisFlowRef,
    userAgentsRefetch,
    yamlAgentCardsRefetch,
    reportErrorToAgent,
    setError,
    setBlockedError,
    setDeployOutcomeError,
    clearUnlessDeployOutcome,
    autoDeployTriggeredRef,
    manualDeployRef,
    bridged,
}: UseManifestDeploymentParams): UseManifestDeploymentResult {
    const { addNotification, displayError, sessionId } = useChatContext();
    const serverApplyEnabled = useIsBuilderServerApplyEnabled();

    const artifactVersions = useArtifactVersions(manifest);

    const artifactVersionsRef = useRef(artifactVersions);
    useEffect(() => {
        artifactVersionsRef.current = artifactVersions;
    }, [artifactVersions]);

    const queryClient = useQueryClient();
    const fetchArtifactVersions = useCallback(
        (filename: string) =>
            queryClient.fetchQuery({
                queryKey: builderKeys.manifestVersions(sessionId ?? "", filename),
                queryFn: () => listManifestVersions(sessionId ?? "", filename),
                staleTime: 0,
            }),
        [queryClient, sessionId]
    );

    const blockedByMissingCreds = useCallback(
        (notify: boolean): boolean => {
            if (!componentsMissingCreds || componentsMissingCreds.size === 0) return false;
            const msg = buildBlockedByCredsMessage(componentsMissingCreds);
            setError(msg);
            if (notify) displayError({ title: "Credentials required", error: msg, errorSource: "client" });
            return true;
        },
        [componentsMissingCreds, setError, displayError]
    );
    const { deploy: deployWorkflow, isLoading: isDeployingWorkflow } = useWorkflowDeployment();
    const agentDeployment = useAgentDeploymentMutation();
    const gatewayDeployment = useGatewayDeploymentMutation();
    const { data: liveAgentCards } = useAgentCards();

    const [isDeploying, setIsDeploying] = useState(false);
    const [focusTick, setFocusTick] = useState(0);
    useEffect(() => {
        const onFocus = () => setFocusTick(t => t + 1);
        window.addEventListener("focus", onFocus);
        return () => window.removeEventListener("focus", onFocus);
    }, []);
    const [postDeployTick, setPostDeployTick] = useState(0);

    const reportFlipOutcome = useCallback(
        (result: SaveManifestResult) => {
            const report = flipFailureReport(result);
            if (!report) return;
            if (report.channel === "blocked") setBlockedError(report.message);
            else setError(report.message);
            if (!bridged) addNotification(report.message, "warning");
        },
        [setError, setBlockedError, addNotification, bridged]
    );

    const settleAfterDeploy = useCallback(
        async (deployedNames: ReadonlySet<string>) => {
            if (deployedNames.size > 0) {
                const deployedEntries = [...savedComponents.values()].filter(r => deployedNames.has(r.name)).map(r => ({ name: r.name, id: r.id, type: r.type }));
                const versions = withoutVersionsFor(await resolveDeployedVersions(manifest?.components ?? [], deployedNames, artifactVersionsRef.current, fetchArtifactVersions), manifest?.components ?? [], namesIn(declinedOnPlatformRef));
                reportFlipOutcome(await saveManifest(markComponentsDeployed(deployedEntries, versions), { forSession: sessionId }));
            }

            await userAgentsRefetch?.();

            if (yamlAgentCardsRefetch) {
                setTimeout(() => yamlAgentCardsRefetch(), 3000);
            }

            setPostDeployTick(t => t + 1);

            if (deployedNames.size > 0) {
                addNotification(`Deployed ${deployedNames.size} component(s).`, "info");
            }
        },
        [savedComponents, saveManifest, reportFlipOutcome, userAgentsRefetch, yamlAgentCardsRefetch, addNotification, manifest?.components, fetchArtifactVersions, declinedOnPlatformRef, sessionId]
    );

    const recordDeployedBeforeFailure = useCallback(
        async (deployedNames: ReadonlySet<string>) => {
            if (deployedNames.size === 0) return;
            const entries = [...savedComponents.values()].filter(r => deployedNames.has(r.name)).map(r => ({ name: r.name, id: r.id, type: r.type }));
            const versions = withoutVersionsFor(await resolveDeployedVersions(manifest?.components ?? [], deployedNames, artifactVersionsRef.current, fetchArtifactVersions), manifest?.components ?? [], namesIn(declinedOnPlatformRef));
            const result = await saveManifest(markComponentsDeployed(entries, versions), { forSession: sessionId });
            if (!result.ok) console.warn("[builder] could not record the components a failed deploy pass had deployed");
        },
        [savedComponents, saveManifest, sessionId, manifest?.components, fetchArtifactVersions, declinedOnPlatformRef]
    );

    const handleDeploy = useCallback(async () => {
        if (blockedByMissingCreds(false)) return;
        setIsDeploying(true);
        setError(null);

        const deployedNames = new Set<string>();
        const deployKinds: DeployKinds = {
            agent: id => agentDeployment.mutateAsync({ agentId: id, action: "deploy" }),
            workflow: id => deployWorkflow({ workflowId: id, action: "deploy" }),
            gateway: id => gatewayDeployment.mutateAsync({ gatewayId: id, action: "deploy" }),
        };
        let currentDeploy: SaveResult | null = null;

        console.log("[builder] handleDeploy starting", {
            savedCount: savedComponents.size,
            components: [...savedComponents].map(([k, v]) => ({ name: k, type: v.type, id: v.id })),
        });

        try {
            const changedOnPlatform = changedOnPlatformRef?.current;
            const changed = changedOnPlatform && new Set(expandConnectorsToConsumers(changedOnPlatform, manifest?.components ?? []).map(normalizeComponentName));
            const { toDeploy, skippedAsFlagged } = planInFlowDeploy(savedComponents, changed, manifest?.components ?? []);
            for (const result of toDeploy) {
                currentDeploy = result;
                if (await deployOneComponent(result, deployKinds)) {
                    deployedNames.add(result.name);
                }
            }
            currentDeploy = null;

            await settleAfterDeploy(deployedNames);
            notifyFlaggedSkipped(addNotification, skippedAsFlagged);
        } catch (err: unknown) {
            const errorMessage = getErrorMessage(err, "Failed to deploy.");
            setDeployOutcomeError(errorMessage, namesOf(currentDeploy));
            await recordDeployedBeforeFailure(deployedNames);
            const dispatched = reportErrorToAgent(currentDeploy?.name ?? null, `Deploy ${operationNoun(currentDeploy?.type)}`, errorMessage, err);
            if (!dispatched) {
                if (!bridged) displayError({ title: "Deploy failed", error: errorMessage, errorSource: "api" });
            }
        } finally {
            setIsDeploying(false);
        }
    }, [
        blockedByMissingCreds,
        savedComponents,
        manifest,
        changedOnPlatformRef,
        recordDeployedBeforeFailure,
        addNotification,
        displayError,
        userAgentsRefetch,
        yamlAgentCardsRefetch,
        deployWorkflow,
        agentDeployment,
        gatewayDeployment,
        saveManifest,
        reportErrorToAgent,
        setError,
        setDeployOutcomeError,
        bridged,
        settleAfterDeploy,
    ]);

    useEffect(() => {
        const ready = autoSavedInThisFlowRef.current && allSaved && !isDeploying && !isDeployingWorkflow && !autoDeployTriggeredRef.current && !manualDeployRef?.current;
        if (!ready) {
            if (autoSavedInThisFlowRef.current && !autoDeployTriggeredRef.current) {
                console.debug("[builder] auto-deploy waiting", {
                    autoSavedInThisFlow: autoSavedInThisFlowRef.current,
                    allSaved,
                    isDeploying,
                    isDeployingWorkflow,
                    autoDeployTriggered: autoDeployTriggeredRef.current,
                });
            }
            return;
        }
        console.log("[builder] auto-deploy firing");
        autoDeployTriggeredRef.current = true;
        handleDeploy();
    }, [allSaved, isDeploying, isDeployingWorkflow, handleDeploy, autoSavedInThisFlowRef]);

    const [isDeployingManifest, setIsDeployingManifest] = useState(false);
    const handleDeployManifest = useCallback(
        async (changedOnPlatform?: readonly string[], options: DeployManifestOptions = {}) => {
            const rewritten = rewrittenNames(changedOnPlatform ?? [], options.notWritten ?? [], manifest?.components ?? []);
            if (!manifest?.components || isDeployingManifest) return;
            if (blockedByMissingCreds(true)) return;
            setIsDeployingManifest(true);
            setError(null);
            try {
                const sessionComps = deployCandidates(manifest.components, rewritten, !!options.onlyChanged);
                const tables = await fetchPlatformDeploymentLookup();
                const matched = takeProof(matchedOnPlatformRef);

                const { deployed, missing, failed, foreign, settled, identified } = await resolveAndDeploy(
                    sessionComps,
                    tables,
                    rewritten,
                    {
                        deployWorkflow,
                        gatewayDeployment,
                        agentDeployment,
                    },
                    artifactVersionsRef.current,
                    matched,
                    savedResultsRef?.current
                );
                if (settled.length > 0 || identified.length > 0) {
                    const published = await resolveDeployedVersions(sessionComps, new Set(deployed), artifactVersionsRef.current, fetchArtifactVersions);
                    const versions = withoutVersionsFor(published, sessionComps, namesIn(declinedOnPlatformRef));
                    const written = await saveManifest(passManifestUpdate(settled, identified, versions, new Set(deployed)), { forSession: sessionId });
                    reportPassWrite(settled.length, written, reportFlipOutcome);
                }
                if (deployed.length > 0) {
                    addNotification(`Deployed ${deployed.length} component(s).`, "info");
                    await userAgentsRefetch?.();
                    if (yamlAgentCardsRefetch) setTimeout(() => yamlAgentCardsRefetch(), 3000);
                }
                const unexplainedMissing = missing.filter(name => !savedAndReported(name, options.notWritten ?? [], manifest.components ?? []));
                const unexplainedForeign = foreign.filter(name => !savedAndReported(name, options.notWritten ?? [], manifest.components ?? []));
                if (!bridged) notifyUndeployable(addNotification, unexplainedMissing, unexplainedForeign);
                for (const f of failed) {
                    if (!bridged) {
                        reportAndNotifyError(addNotification, `Deploy failed for "${f.name}": ${f.error}`, "api");
                    }
                }
                const outcomeError = deployOutcomeErrorMessage(unexplainedMissing, failed, unexplainedForeign);
                if (outcomeError) setDeployOutcomeError(outcomeError, [...failed.map(f => f.name), ...unexplainedMissing, ...unexplainedForeign]);
            } catch (err) {
                const msg = getErrorMessage(err, "Failed to deploy the plan.");
                setError(msg);
                if (!bridged) displayError({ title: "Deploy failed", error: msg, errorSource: "api" });
            } finally {
                setIsDeployingManifest(false);
            }
        },
        [
            blockedByMissingCreds,
            manifest,
            isDeployingManifest,
            addNotification,
            displayError,
            userAgentsRefetch,
            yamlAgentCardsRefetch,
            deployWorkflow,
            agentDeployment,
            gatewayDeployment,
            saveManifest,
            setError,
            setDeployOutcomeError,
            bridged,
            artifactVersions,
            fetchArtifactVersions,
            matchedOnPlatformRef,
            savedResultsRef,
            declinedOnPlatformRef,
            sessionId,
        ]
    );

    const [platformDeploymentStatus, setPlatformDeploymentStatus] = useState<Map<string, string>>(new Map());
    const [platformStatusById, setPlatformStatusById] = useState<Map<string, string>>(new Map());
    const [platformKindsFetched, setPlatformKindsFetched] = useState<{ agent: boolean; workflow: boolean; gateway: boolean; a2aAgent: boolean }>({ agent: false, workflow: false, gateway: false, a2aAgent: false });
    const manifestNamesKey = (manifest?.components ?? [])
        .filter(c => c.origin !== "platform" && c.type !== "skill" && c.type !== "connector")
        .map(c => c.name)
        .sort()
        .join(",");
    useEffect(() => {
        if (!manifestNamesKey) {
            console.debug("[builder] platform poll: empty manifestNamesKey, clearing");
            setPlatformDeploymentStatus(new Map());
            setPlatformStatusById(new Map());
            setPlatformKindsFetched({ agent: false, workflow: false, gateway: false, a2aAgent: false });
            return;
        }
        let cancelled = false;
        console.debug("[builder] platform poll: starting", { manifestNamesKey });
        (async () => {
            try {
                const { agentsByName, workflowsByName, gatewaysByName, remoteAgentsByName, fetchedKinds } = await fetchPlatformDeploymentLookup();
                if (cancelled) return;
                const next = foldDeploymentStatuses([agentsByName, workflowsByName, gatewaysByName, remoteAgentsByName]);
                console.debug("[builder] platform poll: fetched", {
                    agentsByNameSize: agentsByName.size,
                    workflowsByNameSize: workflowsByName.size,
                    deployedMapSize: next.size,
                    agentKeys: [...agentsByName.keys()],
                    deployedEntries: [...next.entries()].map(([k, v]) => `${k}=${v}`),
                });
                setPlatformDeploymentStatus(next);
                setPlatformStatusById(foldDeploymentStatusesById([agentsByName, workflowsByName, gatewaysByName, remoteAgentsByName]));
                setPlatformKindsFetched(fetchedKinds);
            } catch (err) {
                console.warn("[builder] platform poll: fetch failed", err);
                if (!cancelled) {
                    setPlatformDeploymentStatus(new Map());
                    setPlatformStatusById(new Map());
                    setPlatformKindsFetched({ agent: false, workflow: false, gateway: false, a2aAgent: false });
                }
            }
        })();
        return () => {
            cancelled = true;
        };
    }, [manifestNamesKey, isDeployingManifest, postDeployTick, focusTick]);

    const flippedSignatureRef = useRef<string | null>(null);
    const loggedSnapshotRef = useRef<string | null>(null);
    useEffect(() => {
        const anyKindFetched = Object.values(platformKindsFetched).some(Boolean);
        if (!manifest?.components || !anyKindFetched) {
            console.debug("[builder] self-heal: preconditions not met", {
                hasManifest: !!manifest?.components,
                anyKindFetched,
            });
            return;
        }
        const { upgrade: toUpgrade, downgrade: regressed } = planStatusReconciliation({
            components: manifest.components,
            statusById: platformStatusById,
            statusByName: platformDeploymentStatus,
            fetchedKinds: platformKindsFetched,
            savedNames: new Set(savedComponents.keys()),
            liveCards: liveAgentCards,
            isRunningStale: c => isRunningStaleConfig(c, artifactVersions, serverApplyEnabled),
        });
        const snapshot = {
            manifestComponents: manifest.components.map(c => `${c.name}:${c.origin ?? "?"}:${c.status ?? "?"}`),
            platformStatus: [...platformDeploymentStatus.entries()].map(([k, v]) => `${k}=${v}`),
            fetchedKinds: platformKindsFetched,
            upgrade: toUpgrade.map(c => c.name),
            downgrade: regressed.map(c => c.name),
        };
        const snapshotKey = JSON.stringify(snapshot);
        if (loggedSnapshotRef.current !== snapshotKey) {
            loggedSnapshotRef.current = snapshotKey;
            console.debug("[builder] self-heal evaluating", snapshot);
        }
        if (toUpgrade.length === 0 && regressed.length === 0) {
            flippedSignatureRef.current = null;
            return;
        }
        const signature = `up:${toUpgrade
            .map(c => c.name)
            .sort()
            .join(",")}|down:${regressed
            .map(c => c.name)
            .sort()
            .join(",")}`;
        if (flippedSignatureRef.current === signature) return;
        flippedSignatureRef.current = signature;

        const upgradeNames = new Set(toUpgrade.map(c => c.name));
        const downgradeNames = new Set(regressed.map(s => s.name));
        saveManifest(reconcileComponentStatuses(upgradeNames, downgradeNames)).then(result => {
            const ok = result.ok;
            if (!ok) {
                flippedSignatureRef.current = null;
            } else if (upgradeNames.size > 0) {
                clearUnlessDeployOutcome();
            }
        });
    }, [manifest, platformDeploymentStatus, platformStatusById, platformKindsFetched, savedComponents, saveManifest, liveAgentCards, clearUnlessDeployOutcome, artifactVersions, serverApplyEnabled]);

    const hasUndeployed = !!manifest?.components?.some(c => {
        if (c.origin === "platform") return false;
        if (c.type === "skill" || c.type === "connector") return false;
        const status = platformStatusOfComponent(c, platformStatusById, platformDeploymentStatus);
        return status && status !== "deployed";
    });

    const hasUndeployedSessionComponents = hasSaveableSessionComponent(manifest, platformDeploymentStatus, serverApplyEnabled, artifactVersions, platformStatusById);

    return {
        isDeploying,
        isDeployingManifest,
        hasUndeployed,
        hasUndeployedSessionComponents,
        handleDeploy,
        handleDeployManifest,
        autoDeployTriggeredRef,
    };
}
