// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type FormEvent, type MutableRefObject, type ReactNode, type RefObject, useCallback, useEffect, useRef, useState } from "react";

import { type CreateAgentRequest, useCreateAgentMutation } from "@/api/agents";
import { getArtifactContentWithValidation } from "@/api/artifacts";
import { previewBuild, useApplyBuildMutation } from "@/api/builder";
import type { BuildApplySummary, BuildOperationResult } from "@/api/builder/applyTypes";
import { type CreateConnectorRequest, useCreateConnectorMutation } from "@/api/connectors";
import { type CreateGatewayRequest, useCreateGatewayMutation } from "@/api/gateways";
import { type CreateRemoteAgentRequest, type RemoteAgentResponse, useCreateRemoteAgentMutation } from "@/api/remote-agents";
import { type CreateSkillFromConfigRequest, type FetchSkillArtifact, type SavedSkill, useSaveSkillFromConfigMutation } from "@/api/skills";
import { type CreateWorkflowRequest, useCreateWorkflowMutation, useUpdateWorkflowMutation } from "@/api/workflows";

import { isSubmissionDropped } from "@/providers/chat/submissionGuard";
import { useChatContext } from "@/providers/contexts";

import { useIsBuilderServerApplyEnabled } from "@/hooks/useIsBuilderServerApplyEnabled";

import { reportOncePerSession } from "@/common/product-analytics/reportOncePerSession";
import type { ProductAnalyticsErrorSource } from "@/common/product-analytics/types";

import { getErrorMessage } from "@/utils/api";

import { type OwnsResource, ownsResource, rememberWrittenResources, writtenRecords } from "../builderOwnership";
import {
    type ConfigType,
    detectConfigType,
    extractA2AAgentMetadata,
    extractAgentMetadata,
    extractConnectorMetadata,
    extractGatewayCreateRequest,
    extractSkillBundleConfig,
    extractWorkflowMetadata,
    fetchBuiltinToolsetLookup,
    fetchPlatformDeploymentLookup,
    fetchPlatformSkillLookup,
    HTTP_SERVICE_UNAVAILABLE,
    infrastructureError,
    isAgentFixableError,
    isSaveOnlyBuild,
    legacyWorkflowName,
    normalizeComponentName,
    operationNoun,
    type PlatformLookupEntry,
    type PlatformSkillLookup,
    resolveToolsetBindings,
    shouldAutoSaveOnly,
    unrecognizedConfigMessage,
    validateAgentMetadata,
} from "../builderUtils";
import { chatChip } from "../chatChip";
import { type ArtifactVersions, isSaveOnlyType, latestVersions, saveOnlyEditState, saveOnlyEntries, saveOnlyFilenames, saveReadVersions, withSavedVersions } from "../componentDirtyState";
import { ConnectorEditDialog } from "../ConnectorEditDialog";
import { OverwriteConfirmDialog } from "../OverwriteConfirmDialog";
import { shouldSkipOverwriteConfirm } from "../overwriteConfirmPreference";
import { isPlatformRefusal, type PlatformCheck } from "../platformCheck";
import { currentPlatformStamps, stampWritten } from "../platformStamps";
import { cancellationOutcome, decideWrite, declineAfterStart, declineKeyOf, entriesSignature, latchReleased, type ReleaseState, retryAction, SAVE_CANCELLED_MESSAGE, SESSION_LEFT_MESSAGE, turnsEndedAfter } from "../saveOnlyPolicy";
import {
    componentNamedIn,
    type ConfigReadOptions,
    type FetchConfigByFilename,
    type PlannedOverwrite,
    plannedOverwrites,
    platformMatchedNames,
    saveWarnings,
    serverApplyKind,
    toBuildComponentInputs,
    updatedInPlaceNames,
    writtenResources,
} from "../serverApply";
import { dependantsOfUnwritten } from "../unwrittenDependants";
import { type ValidationStampReading, validationStampState } from "../validationStamp";

import { type AutoFixEvent, type AutoFixOutcome, type AutoFixState, autoFixTransition, initialAutoFixState } from "./autoFixMachine";
import type { BuildManifest } from "./useBuilderManifest";
import { useCredentialResave } from "./useCredentialResave";
import { usePlatformCheck } from "./usePlatformCheck";
import { usePlatformCheckHandoff } from "./usePlatformCheckHandoff";
import type { SavedComponentsRecorder, SaveProof, VersionsByFile, VersionsRead } from "./useRecordSavedComponents";

export type SaveAction = "created" | "updated" | "unchanged";

export interface SaveResult {
    name: string;
    type: ConfigType;
    id: string;
    action?: SaveAction;
    commit?: () => Promise<void>;
}

export type ReportErrorToAgentFn = (componentName: string | null, operation: string, errorText: string, rawError: unknown) => boolean;

export type DeployFailureState = { kind: "none" } | { kind: "fixing"; message: string } | { kind: "retryable"; message: string } | { kind: "declined"; message: string } | { kind: "blocked"; message: string };

export function toDeployFailureState(error: string | null, autoFixSubmitted: boolean, autoFixExhausted: boolean, blocked = false): DeployFailureState {
    if (!error) return { kind: "none" };
    if (blocked) return { kind: "blocked", message: error };
    if (error === SAVE_CANCELLED_MESSAGE) return { kind: "declined", message: error };
    if (autoFixExhausted) return { kind: "retryable", message: error };
    if (autoFixSubmitted) return { kind: "fixing", message: error };
    return { kind: "retryable", message: error };
}

export function isDeployStatusFlip(prev: string | undefined, next: string | undefined): boolean {
    if (!prev || !next || prev === next) return false;
    const before = prev.split("|");
    const after = next.split("|");
    if (before.length !== after.length) return false;
    const nameOf = (entry: string) => entry.slice(0, entry.lastIndexOf(":"));
    return before.every((entry, i) => entry === after[i] || (after[i].endsWith(":deployed") && nameOf(after[i]) === nameOf(entry)));
}

interface UseSaveOrchestrationParams {
    hasBuildToActivate: boolean;
    configYaml: string;
    configType: ConfigType;
    manifest?: BuildManifest | null;
    fetchConfigByFilename?: FetchConfigByFilename;
    userAgentsRefetch?: () => Promise<void>;
    autoSave?: boolean;
    onAutoSaveComplete?: () => void;
    handleDeployRef: MutableRefObject<(() => Promise<void>) | null>;
    handleDeployManifestRef: MutableRefObject<((changedOnPlatform?: readonly string[], options?: DeployManifestOptions) => Promise<void>) | null>;
    autoDeployTriggeredRef: MutableRefObject<boolean>;
    bridged?: boolean;
    recordSavedComponents?: SavedComponentsRecorder;
    artifactVersions?: ArtifactVersions;
}

export function saveProofOf(outcome: SaveOutcome, declined: readonly string[]): SaveProof {
    return {
        changed: [...(outcome.changedOnPlatform ?? []), ...(outcome.heldByPlatform?.changed ?? [])],
        matched: [...(outcome.matchedOnPlatform ?? []), ...(outcome.heldByPlatform?.matched ?? [])],
        declined,
    };
}

const NO_VERSIONS: ReadonlyMap<string, number> = new Map();
const missingConfigReader: FetchConfigByFilename = async () => null;
const recordNothing: SavedComponentsRecorder = { versionsBeforeSave: async () => ({ at: NO_VERSIONS, failed: [] }), record: async () => NO_VERSIONS };

async function readVersionsBeforeSave(recorder: SavedComponentsRecorder, components: ManifestComponent[]): Promise<VersionsRead> {
    try {
        return await recorder.versionsBeforeSave(components);
    } catch (err) {
        console.warn("[builder] could not read the versions a save is about to send", { error: getErrorMessage(err) });
        return { at: NO_VERSIONS, failed: saveOnlyFilenames(components) };
    }
}

export interface SaveOptions {
    automatic?: boolean;
}

function componentsToSave(components: ManifestComponent[], leftOut: readonly string[]): ManifestComponent[] {
    return components.filter(c => !leftOut.includes(c.name));
}

interface SavePlan {
    readAt: ArtifactVersions;
    components: ManifestComponent[];
    declineKey: string;
    readEntries: ReadonlySet<string>;
    failedLookups: readonly string[];
}

function planSave(manifestComponents: ManifestComponent[], recorded: ManifestComponent[] | undefined, read: VersionsRead, listed: ArtifactVersions | undefined, serverApplyEnabled: boolean): SavePlan {
    const readAt = saveReadVersions(manifestComponents, listed, read.at);
    const leftOut = isSaveOnlyBuild(manifestComponents) ? saveOnlyEditState(recorded, readAt, serverApplyEnabled).leftOut : [];
    const entries = saveOnlyEntries(manifestComponents, readAt);
    return { readAt, components: componentsToSave(manifestComponents, leftOut), declineKey: declineKeyAt(manifestComponents, listed, readAt), readEntries: new Set(entries), failedLookups: read.failed };
}

function declineKeyAt(components: ManifestComponent[] | undefined, listed: ArtifactVersions | undefined, read: ReadonlyMap<string, number>): string {
    return declineKeyOf(saveOnlyEntries(components, latestVersions(listed, read)));
}

function failedLookupsError(failedLookups: readonly string[], components: ManifestComponent[]): Error {
    const named = failedLookups.map(filename => {
        const comp = components.find(c => c.filename === filename);
        return comp ? `"${comp.name}" (${filename})` : filename;
    });
    return infrastructureError(`Could not read the versions of ${named.join(", ")}. Try again.`);
}

function endFailedLookupSave(error: Error, surface: FailureSurface, shown: boolean): SaveAllResult {
    reportSaveFailure(error, null, surface);
    return leftBehind(shown, { kind: "failed", changedOnPlatform: [], notWritten: [] }, error);
}

function pinnedTo(versions: ArtifactVersions, filename: string, options?: ConfigReadOptions): ConfigReadOptions | undefined {
    const version = versions.get(filename);
    return version === undefined ? options : { ...options, version };
}

function announceSave(notify: (message: string, kind: "success" | "warning") => void, outcome: SaveOutcome, quietIfNothingWritten: boolean | undefined): void {
    if (!(quietIfNothingWritten && savedComponentCount(outcome) === 0)) notify(savedMessage(outcome), "success");
    for (const warning of outcome.warnings) notify(warning, "warning");
}

export interface SavedVersions {
    sessionId: string;
    byFile: VersionsByFile;
}

export function savedVersionsIn(saved: SavedVersions, sessionId: string): ReadonlyMap<string, number> {
    return saved.sessionId === sessionId ? saved.byFile : NO_VERSIONS;
}

export function withSavedAt(saved: SavedVersions, sessionId: string, versions: ReadonlyMap<string, number>): SavedVersions {
    return { sessionId, byFile: new Map([...savedVersionsIn(saved, sessionId), ...versions]) };
}

function confirmIfShown(shownSession: string, saveSession: string, question: PlannedOverwrite[], confirm: (overwrites: PlannedOverwrite[]) => Promise<void>): Promise<void> {
    return shownSession === saveSession ? confirm(question) : Promise.reject(new Error(SESSION_LEFT_MESSAGE));
}

function useTurnsEnded(isResponding: boolean): number {
    const [seen, setSeen] = useState({ ended: 0, responding: isResponding });
    if (seen.responding !== isResponding) setSeen({ ended: turnsEndedAfter(seen.ended, seen.responding, isResponding), responding: isResponding });
    return seen.ended;
}

function useOnSessionChange(sessionId: string, onChange: () => void): void {
    const shown = useRef(sessionId);
    useEffect(() => {
        if (shown.current === sessionId) return;
        shown.current = sessionId;
        onChange();
    });
}

function useReleaseLatch(latch: MutableRefObject<boolean>, versions: string, turnsEnded: number, hasSkill: boolean, saving: boolean, readBySave: RefObject<ReadonlySet<string>>): void {
    const seen = useRef<ReleaseState>({ versions, turnsEnded, hasSkill });
    useEffect(() => {
        if (saving) return;
        const now = { versions, turnsEnded, hasSkill };
        if (latchReleased(seen.current, now, readBySave.current)) latch.current = false;
        seen.current = now;
    }, [latch, versions, turnsEnded, hasSkill, saving, readBySave]);
}

function leftBehind(shown: boolean, result: SaveAllResult, cause?: unknown): SaveAllResult {
    if (shown) return result;
    const stopped = cause as { status?: number; errorId?: string } | null | undefined;
    console.warn("[builder] a save ended after the Builder left its session", { outcome: result.kind, notWritten: result.kind === "failed" ? result.notWritten : [], status: stopped?.status, errorId: stopped?.errorId });
    return { kind: "cancelled" };
}

function whileShown<Args extends unknown[], Result>(shown: () => boolean, act: (...args: Args) => Result, otherwise: Result): (...args: Args) => Result {
    return (...args) => (shown() ? act(...args) : otherwise);
}

async function overwriteGuardFor(sessionId: string, confirm: OverwriteGuard["confirm"]): Promise<OverwriteGuard> {
    const records = writtenRecords(sessionId);
    const asks = !shouldSkipOverwriteConfirm();
    let platformStamps = asks ? await currentPlatformStamps(records) : new Map<string, string | undefined>();
    return {
        owns: ({ id, kind }) => ownsResource(records.get(id), kind, platformStamps.get(id)),
        wrote: id => records.has(id),
        remember: async written => rememberWrittenResources(sessionId, await stampWritten(written)),
        refresh: async () => {
            if (asks) platformStamps = await currentPlatformStamps(records);
        },
        ...(asks ? { confirm } : {}),
    };
}

export type SaveAllResult = { kind: "saved"; changedOnPlatform: string[] | undefined } | { kind: "failed"; changedOnPlatform: string[] | undefined; notWritten: string[] } | { kind: "cancelled" } | { kind: "busy" };

export interface DeployManifestOptions {
    onlyChanged?: boolean;
    notWritten?: readonly string[];
}

export function deployAfterSave(saved: SaveAllResult): { deploy: false } | { deploy: true; changedOnPlatform: string[] | undefined; options: DeployManifestOptions } {
    if (saved.kind === "saved") return { deploy: true, changedOnPlatform: saved.changedOnPlatform, options: {} };
    if (saved.kind !== "failed" || saved.changedOnPlatform?.length === 0) return { deploy: false };
    if (saved.changedOnPlatform === undefined) return { deploy: true, changedOnPlatform: undefined, options: { notWritten: saved.notWritten } };
    return { deploy: true, changedOnPlatform: saved.changedOnPlatform, options: { onlyChanged: true, notWritten: saved.notWritten } };
}

export function savedOrPartial(partiallyFailed: boolean, changedOnPlatform: string[] | undefined, notWritten: string[]): SaveAllResult {
    return partiallyFailed ? { kind: "failed", changedOnPlatform, notWritten } : { kind: "saved", changedOnPlatform };
}

export function joinFailures(saveFailure: string | null, errorMessage: string | null): string | null {
    if (saveFailure && errorMessage && saveFailure !== errorMessage) return `${saveFailure}\n${errorMessage}`;
    return saveFailure ?? errorMessage;
}

async function refreshAgentsAfterSave(refetch: (() => Promise<void>) | undefined): Promise<void> {
    try {
        await refetch?.();
    } catch (err) {
        console.warn("[builder] agent list refresh after save failed", err);
    }
}

const USER_CANCELLED_MESSAGE = "User cancelled";

function isUserCancellation(err: unknown): boolean {
    return err instanceof Error && (err.message === USER_CANCELLED_MESSAGE || err.message === SESSION_LEFT_MESSAGE);
}

export interface UseSaveOrchestrationResult {
    savedComponents: Map<string, SaveResult>;
    isSaving: boolean;
    allSaved: boolean;
    hasManifest: boolean;
    readyToSave: boolean;
    validationStamp: ValidationStampReading;
    platformCheck: PlatformCheck;
    checkAgain: () => void;
    canAskBuilder: boolean;
    askBuilder: () => void;
    turnRunning: boolean;
    deployFailure: DeployFailureState;
    autoSavedInThisFlowRef: MutableRefObject<boolean>;
    reportErrorToAgent: ReportErrorToAgentFn;
    setError: (msg: string | null) => void;
    setBlockedError: (msg: string) => void;
    setDeployOutcomeError: (msg: string, failedNames: readonly string[]) => void;
    clearUnlessDeployOutcome: () => void;
    handleSaveAll: (options?: SaveOptions) => Promise<SaveAllResult>;
    isSavingRef: RefObject<boolean>;
    changedOnPlatformRef: MutableRefObject<string[] | undefined>;
    matchedOnPlatformRef: MutableRefObject<string[]>;
    savedResultsRef: MutableRefObject<ReadonlyMap<string, SaveResult>>;
    declinedOnPlatformRef: MutableRefObject<string[]>;
    handleManualRetry: () => void;
    endApproval: () => void;
    connectorEditDialog: ReactNode;
    overwriteDialog: ReactNode;
}

const AUTO_FIX_WATCHDOG_MS = 30000;
const HELD_AUTO_FIX_LIMIT_MS = 120000;

export function buildOperationError(op: BuildOperationResult, alsoFailed: ReadonlyArray<{ name: string; error?: { message?: string } }> = [], skipped: string[] = [], warnings: string[] = []): Error {
    const parts = [op.error?.message ?? `Failed to save ${operationNoun(op.kind)} "${op.name}".`];

    for (const [field, problems] of Object.entries(op.error?.validationDetails ?? {})) {
        parts.push(`${field}: ${problems.join("; ")}`);
    }
    for (const other of alsoFailed) {
        parts.push(`Also failed — ${other.name}: ${other.error?.message ?? "no detail given"}`);
    }
    if (skipped.length > 0) {
        parts.push(`Not attempted: ${skipped.join(", ")}.`);
    }
    for (const warning of warnings) {
        parts.push(`Warning — ${warning}`);
    }

    return Object.assign(new Error(parts.join("\n")), {
        status: op.error?.httpStatus ?? 500,
        errorId: op.error?.errorId,
    });
}

type ManifestComponent = NonNullable<BuildManifest["components"]>[number];

export interface SaveRun {
    changed: string[];
    declined: string[];
    notWritten: string[];
    ordered: ManifestComponent[];
    results: Map<string, SaveResult>;
    skip: (comp: ManifestComponent) => boolean;
    attribute: (comp: ManifestComponent | null) => void;
    fetchConfigByFilename: NonNullable<UseSaveOrchestrationParams["fetchConfigByFilename"]>;
    saveOne: (yamlText: string, comp: ManifestComponent, connectorIds?: string[], skill?: SkillSaveContext) => Promise<SaveResult | null>;
    onPlanRefused?: () => void;
}

export interface SkillSaveContext {
    guard?: OverwriteGuard;
    deferReplace?: boolean;
}

async function readComponentConfig(run: SaveRun, comp: ManifestComponent): Promise<string> {
    const yamlText = await run.fetchConfigByFilename(comp.filename, { skipAccumulated: true });
    if (!yamlText) {
        throw new Error(`Could not resolve config for "${comp.name}" (${comp.filename}).`);
    }
    return yamlText;
}

type BuildInputs = Awaited<ReturnType<typeof toBuildComponentInputs>>;

type ApplyBuildFn = (vars: { buildName: string; components: BuildInputs; dryRun?: boolean }) => Promise<{
    operations: BuildOperationResult[];
    summary: BuildApplySummary;
}>;

interface OutsideFailure {
    name: string;
    error: unknown;
}

function savedComponentCount(outcome: SaveOutcome): number {
    const written = outcome.summary ? outcome.summary.created + outcome.summary.updated : (outcome.writtenCount ?? 0);
    return written + outcome.savedOutsideTheBuild;
}

function savedMessage(outcome: SaveOutcome): string {
    const saved = `${savedComponentCount(outcome)} component(s) saved successfully.`;
    const updated = outcome.updatedInPlace ?? [];
    return updated.length > 0 ? `${saved} Updated in place: ${updated.join(", ")}.` : saved;
}

function reportPartialFailures(failures: OutsideFailure[], ctx: PartialFailureContext): void {
    const [first, ...rest] = failures;
    const detail = [getErrorMessage(first.error, `Failed to save "${first.name}".`), ...rest.map(f => `Also failed — ${f.name}: ${getErrorMessage(f.error, "no detail given")}`)].join("\n");
    ctx.setError(detail);
    ctx.keepSaveFailure(detail);
    const dispatched = ctx.reportErrorToAgent(first.name, `Save ${operationNoun(ctx.componentType)}`, detail, first.error);
    if (!dispatched && !ctx.bridged) {
        ctx.displayError({ title: "Some components were not saved", error: detail, errorSource: "api" });
    }
}

type FailureSurface = Omit<PartialFailureContext, "componentType">;

function reportSaveFailure(err: unknown, comp: ManifestComponent | null, surface: FailureSurface): void {
    const errorMessage = getErrorMessage(err, "Failed to save all components.");
    surface.setError(errorMessage);
    surface.keepSaveFailure(errorMessage);
    const dispatched = surface.reportErrorToAgent(comp?.name ?? null, `Save ${operationNoun(comp?.type)}`, errorMessage, err);
    if (!dispatched && !surface.bridged) surface.displayError({ title: "Save failed", error: errorMessage, errorSource: "api" });
}

interface PartialFailureContext {
    componentType?: string;
    bridged?: boolean;
    setError: (msg: string | null) => void;
    keepSaveFailure: (msg: string) => void;
    reportErrorToAgent: ReportErrorToAgentFn;
    displayError: (args: { title: string; error: string; errorSource?: ProductAnalyticsErrorSource }) => void;
}

function attributeFirst(run: SaveRun, components: ManifestComponent[], failures: OutsideFailure[]): OutsideFailure[] | undefined {
    if (failures.length === 0) return undefined;
    run.attribute(components.find(c => c.name === failures[0].name) ?? null);
    return failures;
}

async function saveComponentsOutsideTheBuild(run: SaveRun, guard?: OverwriteGuard): Promise<OutsideSaves> {
    const outside: OutsideSaves = { saved: 0, updated: [], pending: [], held: { changed: [], matched: [] }, failures: [] };
    for (const comp of run.ordered) {
        if (serverApplyKind(comp) !== null) continue;
        if (run.skip(comp)) continue;

        run.attribute(comp);
        try {
            const result = await run.saveOne(await readComponentConfig(run, comp), comp, undefined, { guard, deferReplace: true });
            if (result) await recordOutsideSave(run, outside, comp, result, guard);
        } catch (err) {
            run.notWritten.push(comp.name);
            outside.failures.push({ name: comp.name, error: err });
        }
    }
    return outside;
}

interface OutsideSaves {
    saved: number;
    updated: string[];
    pending: PendingSkillReplacement[];
    held: HeldByPlatform;
    failures: OutsideFailure[];
}

interface HeldByPlatform {
    changed: string[];
    matched: string[];
}

interface PendingSkillReplacement {
    comp: ManifestComponent;
    id: string;
    commit: () => Promise<void>;
}

async function recordOutsideSave(run: SaveRun, outside: OutsideSaves, comp: ManifestComponent, result: SaveResult, guard?: OverwriteGuard): Promise<void> {
    if (result.commit) {
        outside.pending.push({ comp, id: result.id, commit: result.commit });
        return;
    }
    run.results.set(comp.name, result);
    if (result.action !== "unchanged") outside.saved++;
    noteHeld(outside.held, comp.name, result);
    await rememberWrite(result, guard);
}

function noteHeld(held: HeldByPlatform, name: string, result: SaveResult): void {
    if (!isSaveOnlyType(result.type) || !result.action) return;
    (result.action === "unchanged" ? held.matched : held.changed).push(name);
}

async function rememberWrite(result: SaveResult, guard?: OverwriteGuard): Promise<void> {
    if (result.action === "created" || result.action === "updated") await guard?.remember([{ id: result.id, kind: result.type }]);
}

function pendingSkillOverwrites(pending: PendingSkillReplacement[], owns: OwnsResource): PlannedOverwrite[] {
    return pending.filter(p => decideWrite(owns({ id: p.id, kind: "skill" })) === "confirm").map(p => ({ name: p.comp.name, kind: "skill", changedFields: ["bundle"] }));
}

async function commitPendingSkills(run: SaveRun, outside: OutsideSaves, guard?: OverwriteGuard): Promise<void> {
    for (const { comp, id, commit } of outside.pending.splice(0)) {
        try {
            await commit();
            run.results.set(comp.name, { name: comp.name, type: "skill", id, action: "updated" });
            outside.held.changed.push(comp.name);
            await guard?.remember([{ id, kind: "skill" }]);
            outside.saved++;
            outside.updated.push(comp.name);
        } catch (err) {
            run.notWritten.push(comp.name);
            outside.failures.push({ name: comp.name, error: err });
        }
    }
}

async function dependantsOfSkills(run: SaveRun, skills: readonly string[]): Promise<string[]> {
    if (skills.length === 0) return [];
    return dependantsOfUnwritten(skills, run.ordered, async c => {
        try {
            const config = await run.fetchConfigByFilename(c.filename, { skipAccumulated: true });
            if (!config) console.warn("[builder] a config came back empty when looking for what uses an unwritten skill", { component: c.name });
            return config;
        } catch (err) {
            console.warn("[builder] could not re-read a config to find what uses an unwritten skill", { component: c.name, err });
            return null;
        }
    });
}

async function withholdDependantsOfUnwrittenSkills(run: SaveRun): Promise<void> {
    const skills = run.ordered.filter(c => c.type === "skill" && run.notWritten.includes(c.name)).map(c => c.name);
    for (const name of await dependantsOfSkills(run, skills)) if (!run.notWritten.includes(name)) run.notWritten.push(name);
}

function namingTheComponent(err: unknown, name: string): unknown {
    if (!(err instanceof Error) || isUserCancellation(err) || err.message.includes(`"${name}"`)) return err;
    return rewrapped(err, `Failed to save "${name}": ${err.message}`);
}

function rewrapped(err: unknown, message: string): Error {
    const cause = err as { status?: number; code?: string; errorId?: string; response?: unknown } | null;
    return Object.assign(new Error(message, { cause: err }), { status: cause?.status, code: cause?.code, errorId: cause?.errorId, response: cause?.response });
}

async function saveRecordingFailure(run: SaveRun, comp: ManifestComponent, yamlText: string, connectorIds: string[] | undefined, guard?: OverwriteGuard): Promise<SaveResult | null | undefined> {
    try {
        return await run.saveOne(yamlText, comp, connectorIds, { guard });
    } catch (err: unknown) {
        run.notWritten.push(comp.name);
        throw namingTheComponent(err, comp.name);
    }
}

function withSkillOutcomes(err: unknown, outside: OutsideSaves): unknown {
    if (isUserCancellation(err)) return err;
    const notes = outside.failures.map(f => `Also failed — ${f.name}: ${getErrorMessage(f.error, "no detail given")}`);
    if (notes.length === 0 && outside.updated.length === 0) return err;
    const message = (err as { message?: string } | null)?.message ?? String(err);
    return withUpdatedSkills(rewrapped(err, [message, ...notes].join("\n")), outside);
}

async function settleSkillsOnly(run: SaveRun, outside: OutsideSaves, guard?: OverwriteGuard): Promise<void> {
    if (guard?.confirm) await confirmUntilStable({ ...guard, confirm: guard.confirm }, async () => pendingSkillOverwrites(outside.pending, guard.owns));
    await commitPendingSkills(run, outside, guard);
}

async function confirmUntilStable(guard: OverwriteGuard & { confirm: NonNullable<OverwriteGuard["confirm"]> }, overwritesNow: (afterAnAnswer: boolean) => Promise<PlannedOverwrite[]>): Promise<void> {
    const asked = new Set<string>();
    for (let afterAnAnswer = false; ; afterAnAnswer = true) {
        await guard.refresh?.();
        const fresh = (await overwritesNow(afterAnAnswer)).filter(o => !asked.has(`${o.kind}:${o.name}`));
        if (fresh.length === 0) return;
        await guard.confirm(fresh);
        for (const o of fresh) asked.add(`${o.kind}:${o.name}`);
    }
}

interface PlanVerdict {
    operations: BuildOperationResult[];
    rejected: BuildOperationResult[];
    warnings: string[];
}

function notePlanRefusal(run: SaveRun, failures: BuildOperationResult[]): void {
    if (failures.some(op => isPlatformRefusal(op.error?.httpStatus))) run.onPlanRefused?.();
}

async function planVerdict(applyBuild: ApplyBuildFn, buildName: string, inputs: BuildInputs): Promise<PlanVerdict> {
    const plan = await applyBuild({ buildName, components: inputs, dryRun: true });
    return { operations: plan.operations, rejected: plan.operations.filter(op => op.status === "failed"), warnings: saveWarnings(plan.operations) };
}

async function planAndConfirm(guard: OverwriteGuard & { confirm: NonNullable<OverwriteGuard["confirm"]> }, buildName: string, inputs: BuildInputs, applyBuild: ApplyBuildFn, outside: OutsideSaves): Promise<PlanVerdict> {
    let verdict = await planVerdict(applyBuild, buildName, inputs);
    await confirmUntilStable(guard, async afterAnAnswer => {
        if (afterAnAnswer) verdict = await planVerdict(applyBuild, buildName, inputs);
        return verdict.rejected.length > 0 ? [] : [...pendingSkillOverwrites(outside.pending, guard.owns), ...plannedOverwrites(verdict.operations, guard.owns)];
    });
    return verdict;
}

function recordApplyOperations(run: SaveRun, applied: { operations: BuildOperationResult[] }): void {
    for (const op of applied.operations) {
        if (op.status === "succeeded" && op.resourceId) {
            run.results.set(op.name, { name: op.name, type: op.kind, id: op.resourceId });
        }
        if (op.status === "succeeded" && (op.action === "update" || op.action === "create")) {
            run.changed.push(op.name);
        }
        if (op.status === "succeeded" && op.changesDeclined) {
            run.declined.push(op.name);
        }
        if (op.status === "failed" || op.status === "skipped") {
            run.notWritten.push(op.name);
        }
    }
}

export interface OverwriteGuard {
    owns: OwnsResource;
    wrote: (id: string) => boolean;
    remember: (written: Array<{ id: string; kind: string }>) => Promise<void>;
    refresh?: () => Promise<void>;
    confirm?: (overwrites: PlannedOverwrite[]) => Promise<void>;
}

async function failedBuildError(run: SaveRun, components: ManifestComponent[], failures: BuildOperationResult[], skipped: string[], warnings: string[], outside: OutsideSaves): Promise<Error> {
    run.attribute(components.find(c => c.name === failures[0].name) ?? null);
    const alsoFailed = [...failures.slice(1), ...outside.failures.map(f => ({ name: f.name, error: { message: f.error instanceof Error ? f.error.message : String(f.error) } }))];
    const error = buildOperationError(failures[0], alsoFailed, skipped, warnings);
    return withUpdatedSkills(await withSkillInfrastructureStatus(error, failures[0].name, run, outside), outside);
}

async function withSkillInfrastructureStatus(error: Error, rejected: string, run: SaveRun, outside: OutsideSaves): Promise<Error> {
    if (!isAgentFixableError(error)) return error;
    for (const failure of outside.failures) {
        if (isAgentFixableError(failure.error)) continue;
        const users = await dependantsOfSkills(run, [failure.name]);
        if (!users.some(name => normalizeComponentName(name) === normalizeComponentName(rejected))) continue;
        const status = (failure.error as { status?: number } | null)?.status;
        return Object.assign(error, { status: status ?? HTTP_SERVICE_UNAVAILABLE });
    }
    return error;
}

function withUpdatedSkills(error: Error, outside: OutsideSaves): Error {
    if (outside.updated.length > 0) error.message += `\nAlready updated before the failure: ${outside.updated.join(", ")}.`;
    return error;
}

export async function runServerApply(run: SaveRun, manifest: BuildManifest, applyBuild: ApplyBuildFn, guard?: OverwriteGuard): Promise<SaveOutcome> {
    try {
        const outcome = await applyWithSkillsFirst(run, manifest, applyBuild, guard);
        await withholdDependantsOfUnwrittenSkills(run);
        return outcome;
    } catch (err) {
        if (!isUserCancellation(err)) await withholdDependantsOfUnwrittenSkills(run);
        throw err;
    }
}

async function applyWithSkillsFirst(run: SaveRun, manifest: BuildManifest, applyBuild: ApplyBuildFn, guard?: OverwriteGuard): Promise<SaveOutcome> {
    const outside = await saveComponentsOutsideTheBuild(run, guard);
    const outsideFailures = outside.failures;

    const components = manifest.components ?? [];
    const inputs = await toBuildComponentInputs(components, run.fetchConfigByFilename, run.attribute);
    run.attribute(null);
    if (inputs.length === 0) {
        await settleSkillsOnly(run, outside, guard);
        return { savedOutsideTheBuild: outside.saved, warnings: [], changedOnPlatform: run.changed, updatedInPlace: outside.updated, heldByPlatform: outside.held, partialFailures: attributeFirst(run, components, outsideFailures) };
    }

    const attributeAndRethrow = (err: unknown): never => {
        run.attribute(componentNamedIn(getErrorMessage(err, ""), components));
        throw withSkillOutcomes(err, outside);
    };

    if (guard?.confirm) {
        let verdict: PlanVerdict;
        try {
            verdict = await planAndConfirm({ ...guard, confirm: guard.confirm }, manifest.name, inputs, applyBuild, outside);
        } catch (err) {
            return attributeAndRethrow(err);
        }
        if (verdict.rejected.length > 0) {
            notePlanRefusal(run, verdict.rejected);
            throw await failedBuildError(run, components, verdict.rejected, [], verdict.warnings, outside);
        }
    }

    await commitPendingSkills(run, outside, guard);
    let applied: Awaited<ReturnType<ApplyBuildFn>>;
    try {
        applied = await applyBuild({ buildName: manifest.name, components: inputs });
    } catch (err) {
        return attributeAndRethrow(err);
    }
    recordApplyOperations(run, applied);
    await guard?.remember(writtenResources(applied.operations));

    const warnings = saveWarnings(applied.operations);

    const failures = applied.operations.filter(op => op.status === "failed");
    if (failures.length > 0) {
        const skipped = applied.operations.filter(op => op.status === "skipped").map(op => op.name);
        notePlanRefusal(run, failures);
        throw await failedBuildError(run, components, failures, skipped, warnings, outside);
    }
    return {
        summary: applied.summary,
        savedOutsideTheBuild: outside.saved,
        changedOnPlatform: run.changed,
        updatedInPlace: [...outside.updated, ...updatedInPlaceNames(applied.operations)],
        matchedOnPlatform: platformMatchedNames(applied.operations),
        heldByPlatform: outside.held,
        warnings,
        partialFailures: attributeFirst(run, components, outsideFailures),
    };
}

export interface SaveOutcome {
    summary?: BuildApplySummary;
    writtenCount?: number;
    savedOutsideTheBuild: number;
    warnings: string[];
    updatedInPlace?: string[];
    partialFailures?: OutsideFailure[];
    heldByPlatform?: HeldByPlatform;
    changedOnPlatform?: string[];
    matchedOnPlatform?: string[];
}

export function requireRecognizedConfig(yamlText: string, comp: Pick<ManifestComponent, "name" | "type">): ConfigType {
    const type = detectConfigType(yamlText);
    if (type === "unknown") throw new Error(unrecognizedConfigMessage(comp.name, comp.type));
    return type;
}

export async function runPerKindSaves(run: SaveRun, components: ManifestComponent[], platformConnectorsByName: Map<string, PlatformLookupEntry>, guard?: OverwriteGuard): Promise<SaveOutcome> {
    const updatedInPlace: string[] = [];
    const failures: OutsideFailure[] = [];
    try {
        return await saveEachKind(run, components, platformConnectorsByName, updatedInPlace, failures, guard);
    } catch (err) {
        throw withSkillOutcomes(err, { saved: 0, updated: updatedInPlace, pending: [], held: { changed: [], matched: [] }, failures });
    }
}

async function recordPerKindSave(run: SaveRun, comp: ManifestComponent, result: SaveResult, tally: { updatedInPlace: string[]; held: HeldByPlatform }, guard?: OverwriteGuard): Promise<boolean> {
    run.results.set(comp.name, result);
    if (result.action === "updated") tally.updatedInPlace.push(comp.name);
    noteHeld(tally.held, comp.name, result);
    await rememberWrite(result, guard);
    return result.action !== "unchanged";
}

function createWouldBeRefused(comp: ManifestComponent, platformConnectorsByName: Map<string, PlatformLookupEntry>, guard?: OverwriteGuard): boolean {
    if (comp.type !== "connector") return false;
    const held = platformConnectorsByName.get(comp.name) ?? platformConnectorsByName.get(normalizeComponentName(comp.name));
    return held !== undefined && guard?.wrote(held.id) === true;
}

async function saveEachKind(run: SaveRun, components: ManifestComponent[], platformConnectorsByName: Map<string, PlatformLookupEntry>, updatedInPlace: string[], failures: OutsideFailure[], guard?: OverwriteGuard): Promise<SaveOutcome> {
    let writtenCount = 0;
    const held: HeldByPlatform = { changed: [], matched: [] };
    for (const comp of run.ordered) {
        if (run.skip(comp) || createWouldBeRefused(comp, platformConnectorsByName, guard)) continue;

        run.attribute(comp);
        let yamlText: string;
        try {
            yamlText = await readComponentConfig(run, comp);
        } catch (err: unknown) {
            failures.push({ name: comp.name, error: err });
            run.notWritten.push(comp.name);
            continue;
        }
        const connectorIds = detectConfigType(yamlText) === "agent" ? resolveConnectorIds(comp, components, run.results, platformConnectorsByName) : undefined;

        const result = await saveRecordingFailure(run, comp, yamlText, connectorIds, guard);
        if (result && (await recordPerKindSave(run, comp, result, { updatedInPlace, held }, guard))) writtenCount++;
    }
    run.attribute(null);
    return { writtenCount, savedOutsideTheBuild: 0, warnings: [], updatedInPlace, heldByPlatform: held, partialFailures: attributeFirst(run, components, failures) };
}

export function orderComponentsForSave(components: ManifestComponent[]): ManifestComponent[] {
    const skills = components.filter(c => c.type === "skill");
    const connectors = components.filter(c => c.type === "connector");
    const a2aAgents = components.filter(c => c.type === "a2a_agent");
    const agents = components.filter(c => c.type !== "workflow" && c.type !== "connector" && c.type !== "skill" && c.type !== "gateway" && c.type !== "a2a_agent");
    const workflows = components.filter(c => c.type === "workflow");
    const gateways = components.filter(c => c.type === "gateway");
    return [...skills, ...connectors, ...a2aAgents, ...agents, ...workflows, ...gateways];
}

export function shouldSkipSave(comp: ManifestComponent, alreadySaved: (name: string) => boolean, alreadyOnPlatform: (name: string) => boolean): boolean {
    if (comp.origin === "platform") return true;
    if (comp.type === "toolset") return true;
    if (comp.type === "workflow") return false;
    if (comp.type === "skill") return false;
    if (alreadySaved(comp.name)) return true;
    const isAgentOrGateway = comp.type === "gateway" || (comp.type !== "connector" && comp.type !== "skill" && comp.type !== "a2a_agent");
    return isAgentOrGateway && alreadyOnPlatform(comp.name);
}

export function resolveConnectorIds(comp: ManifestComponent, components: ManifestComponent[], saved: Map<string, SaveResult>, connectorsByName: Map<string, PlatformLookupEntry>): string[] | undefined {
    const connectorNames = new Set<string>(comp.connectors ?? []);
    for (const dep of comp.depends_on ?? []) {
        if (components.some(c => c.name === dep && c.type === "connector")) {
            connectorNames.add(dep);
        }
    }
    const ids = new Set<string>();
    for (const connName of connectorNames) {
        const savedConn = saved.get(connName);
        if (savedConn) {
            ids.add(savedConn.id);
            continue;
        }
        const platformConn = connectorsByName.get(connName) ?? connectorsByName.get(normalizeComponentName(connName));
        if (platformConn) ids.add(platformConn.id);
    }
    return ids.size > 0 ? [...ids] : undefined;
}

async function fetchSaveContext(): Promise<{ onPlatform: Set<string>; connectorsByName: Map<string, PlatformLookupEntry>; workflowsByName: Map<string, PlatformLookupEntry>; workflowsListed: boolean }> {
    const onPlatform = new Set<string>();
    try {
        const { agentsByName, workflowsByName, gatewaysByName, connectorsByName, fetchedKinds } = await fetchPlatformDeploymentLookup();
        for (const name of agentsByName.keys()) onPlatform.add(name);
        for (const name of workflowsByName.keys()) onPlatform.add(name);
        for (const name of gatewaysByName.keys()) onPlatform.add(name);
        return { onPlatform, connectorsByName, workflowsByName, workflowsListed: fetchedKinds.workflow };
    } catch {
        return { onPlatform, connectorsByName: new Map(), workflowsByName: new Map(), workflowsListed: false };
    }
}

function workflowListUnavailableError(name: string): Error {
    return infrastructureError(`Could not read the existing workflows from the platform, so workflow "${name}" was not saved. Try again.`);
}

export function requireWorkflowListForCreate(type: string, platformId: string | undefined, workflowsListed: boolean, name: string): void {
    if (type === "workflow" && !platformId && !workflowsListed) throw workflowListUnavailableError(name);
}

type CreateMutation<TPayload, TResponse = { id?: string } | null | undefined> = { mutateAsync: (payload: TPayload) => Promise<TResponse> };

export interface SkillBindings {
    skillIds?: string[];
    skillRefs?: string[];
}

export function resolveSkillBindings(names: string[] | undefined, saved: Map<string, SaveResult>, lookup: PlatformSkillLookup): SkillBindings {
    if (!names?.length) return {};
    const ids = new Set<string>();
    const refs = new Set<string>();
    const unresolved: string[] = [];
    for (const skillName of names) {
        const savedSkill = saved.get(skillName);
        if (savedSkill?.type === "skill") {
            ids.add(savedSkill.id);
            continue;
        }
        const platformID = lookup.customIdsByName.get(skillName);
        if (platformID) {
            ids.add(platformID);
            continue;
        }
        if (lookup.builtinNames.has(skillName)) {
            refs.add(skillName);
            continue;
        }
        unresolved.push(skillName);
    }
    if (unresolved.length > 0) {
        const quotedNames = unresolved.map(n => `"${n}"`).join(", ");
        throw new Error(`Cannot bind skill(s) ${quotedNames} to this agent: not created in this build, not found on the platform, and not a built-in skill. Create the skill first or fix the agent's skills list.`);
    }
    return {
        ...(ids.size > 0 ? { skillIds: [...ids] } : {}),
        ...(refs.size > 0 ? { skillRefs: [...refs] } : {}),
    };
}

export async function saveAgentComponent(yamlText: string, name: string, connectorIds: string[] | undefined, createAgent: CreateMutation<CreateAgentRequest>, savedComponents: Map<string, SaveResult>): Promise<SaveResult> {
    const extracted = extractAgentMetadata(yamlText);
    if (!extracted) throw new Error(`Failed to parse agent configuration for "${name}".`);
    const { loadableSkillNames, builtinGroupNames, builtinToolNames, ...agentData } = extracted;
    const validationErrors = validateAgentMetadata(agentData);
    if (validationErrors.length > 0) {
        console.warn("[builder] agent save blocked by client validation", { name, errors: validationErrors, extracted: agentData });
        throw new Error(`Validation failed for "${name}": ${validationErrors.join(" ")}`);
    }
    const skillBindings = loadableSkillNames?.length ? resolveSkillBindings(loadableSkillNames, savedComponents, await fetchPlatformSkillLookup()) : {};
    const toolsetBindings: { toolsets?: string[]; toolsetExcludedTools?: Record<string, string[]> } =
        builtinGroupNames?.length || builtinToolNames?.length ? resolveToolsetBindings(builtinGroupNames ?? [], builtinToolNames ?? [], await fetchBuiltinToolsetLookup()) : {};
    const mergedToolsets = [...new Set([...(agentData.toolsets ?? []), ...(toolsetBindings.toolsets ?? [])])];
    const agentPayload = {
        ...agentData,
        ...(connectorIds && connectorIds.length > 0 ? { connectors: connectorIds } : {}),
        ...skillBindings,
        ...(toolsetBindings.toolsetExcludedTools ? { toolsetExcludedTools: toolsetBindings.toolsetExcludedTools } : {}),
        ...(mergedToolsets.length ? { toolsets: mergedToolsets } : {}),
    };
    const response = await createAgent.mutateAsync(agentPayload);
    const id = response?.id;
    if (!id) throw new Error(`No ID returned when saving agent "${name}".`);
    return { name, type: "agent", id };
}

async function saveConnectorComponent(yamlText: string, name: string, createConnector: CreateMutation<CreateConnectorRequest>): Promise<SaveResult> {
    const connectorData = extractConnectorMetadata(yamlText);
    if (!connectorData) throw new Error(`Failed to parse connector configuration for "${name}".`);
    const payload = {
        name: connectorData.name,
        description: connectorData.description || "",
        type: connectorData.type,
        subtype: connectorData.subtype,
        values: connectorData.values,
    };
    const response = await createConnector.mutateAsync(payload);
    const id = response?.id;
    if (!id) throw new Error(`No ID returned when saving connector "${name}".`);
    return { name, type: "connector", id, action: "created" };
}

async function saveA2AAgentComponent(jsonText: string, name: string, createRemoteAgent: CreateMutation<CreateRemoteAgentRequest, RemoteAgentResponse>): Promise<SaveResult> {
    const a2aData = extractA2AAgentMetadata(jsonText);
    if (!a2aData) throw new Error(`Failed to parse remote A2A agent configuration for "${name}".`);
    const response = await createRemoteAgent.mutateAsync(a2aData);
    const id = response?.id;
    if (!id) throw new Error(`No ID returned when saving remote A2A agent "${name}".`);
    return { name, type: "a2a_agent", id };
}

export function platformWorkflowId(comp: ManifestComponent, workflowsByName: ReadonlyMap<string, PlatformLookupEntry>, names: WorkflowSaveNames = {}): string | undefined {
    if (comp.type !== "workflow") return undefined;
    const { platformName, legacyName } = names;
    const ownRow = platformName ? (rowNamed(workflowsByName, platformName) ?? rowNamedOnPlatform(workflowsByName, platformName)) : undefined;
    if (ownRow) return ownRow.id;
    const hit = workflowsByName.get(comp.name);
    const matchedExactly = !!hit && (hit.name === comp.name || hit.displayName === comp.name);
    if (matchedExactly) return hit.id;
    if (manifestIdBelongsTo(comp, workflowsByName, names)) return comp.id;
    const legacyRow = legacyName ? rowNamed(workflowsByName, legacyName) : undefined;
    return legacyRow && isLegacyRowOf(legacyRow, names) ? legacyRow.id : undefined;
}

export interface WorkflowSaveNames {
    platformName?: string;
    legacyName?: string;
}

export function workflowSaveNames(yamlText: string): WorkflowSaveNames {
    return { platformName: extractWorkflowMetadata(yamlText)?.name, legacyName: legacyWorkflowName(yamlText) };
}

function rowNamed(workflowsByName: ReadonlyMap<string, PlatformLookupEntry>, name: string): PlatformLookupEntry | undefined {
    for (const entry of workflowsByName.values()) {
        if (entry.name === name) return entry;
    }
    return undefined;
}

function rowNamedOnPlatform(workflowsByName: ReadonlyMap<string, PlatformLookupEntry>, name: string): PlatformLookupEntry | undefined {
    const platformKey = (value: string) => value.toLowerCase().replaceAll(" ", "");
    const wanted = platformKey(name);
    for (const entry of workflowsByName.values()) {
        if (entry.name !== undefined && platformKey(entry.name) === wanted) return entry;
    }
    return undefined;
}

function isLegacyRowOf(entry: PlatformLookupEntry, { platformName, legacyName }: WorkflowSaveNames): boolean {
    return !!legacyName && !!platformName && entry.name === legacyName && entry.agentName === platformName;
}

function manifestIdBelongsTo(comp: ManifestComponent, workflowsByName: ReadonlyMap<string, PlatformLookupEntry>, names: WorkflowSaveNames): boolean {
    if (!comp.id) return false;
    for (const entry of workflowsByName.values()) {
        if (entry.id !== comp.id) continue;
        return entry.name === comp.name || entry.displayName === comp.name || isLegacyRowOf(entry, names);
    }
    return true;
}

export async function saveWorkflowComponent(
    yamlText: string,
    name: string,
    createWorkflow: CreateMutation<CreateWorkflowRequest>,
    existingId?: string,
    updateWorkflow?: CreateMutation<{ id: string; payload: CreateWorkflowRequest }, unknown>
): Promise<SaveResult> {
    const workflowData = extractWorkflowMetadata(yamlText);
    if (!workflowData) throw new Error(`Failed to parse workflow configuration for "${name}".`);
    if (existingId && updateWorkflow) {
        await updateWorkflow.mutateAsync({ id: existingId, payload: workflowData });
        return { name, type: "workflow", id: existingId };
    }
    const response = await createWorkflow.mutateAsync(workflowData);
    const id = response?.id;
    if (!id) throw new Error(`No ID returned when saving workflow "${name}".`);
    return { name, type: "workflow", id };
}

async function saveGatewayComponent(yamlText: string, name: string, createGateway: CreateMutation<CreateGatewayRequest>): Promise<SaveResult> {
    const gw = extractGatewayCreateRequest(yamlText, name);
    if (!gw) throw new Error(`Failed to parse entrypoint configuration for "${name}".`);
    const response = await createGateway.mutateAsync(gw);
    const id = response?.id;
    if (!id) throw new Error(`No ID returned when saving entrypoint "${name}".`);
    return { name, type: "gateway", id };
}

function skillReplacementConfirmer(guard: OverwriteGuard): NonNullable<CreateSkillFromConfigRequest["confirmReplace"]> {
    return async existing => {
        await guard.refresh?.();
        const { confirm } = guard;
        if (!confirm || decideWrite(guard.owns({ id: existing.id, kind: "skill" })) === "write") return;
        await confirm([{ name: existing.name, kind: "skill", changedFields: ["bundle"] }]);
    };
}

export async function saveSkillComponent(yamlText: string, name: string, saveSkill: CreateMutation<CreateSkillFromConfigRequest, SavedSkill>, fetchArtifact: FetchSkillArtifact, context: SkillSaveContext = {}): Promise<SaveResult> {
    const config = extractSkillBundleConfig(yamlText);
    if (!config) {
        throw new Error(`Skill "${name}" is missing required name/description in its YAML.`);
    }
    const { guard, deferReplace } = context;
    const response = await saveSkill.mutateAsync({ config, fetchArtifact, deferReplace, ...(guard && !deferReplace ? { confirmReplace: skillReplacementConfirmer(guard) } : {}) });
    const id = response?.id;
    if (!id) throw new Error(`No ID returned when saving skill "${name}".`);
    return response.action === "replace_deferred" ? { name, type: "skill", id, commit: response.commit } : { name, type: "skill", id, action: response.action };
}

export function useSaveOrchestration({
    hasBuildToActivate,
    configYaml,
    configType,
    manifest,
    fetchConfigByFilename,
    userAgentsRefetch,
    autoSave,
    onAutoSaveComplete,
    handleDeployRef,
    handleDeployManifestRef,
    autoDeployTriggeredRef,
    bridged,
    recordSavedComponents = recordNothing,
    artifactVersions,
}: UseSaveOrchestrationParams): UseSaveOrchestrationResult {
    const { addNotification, displayError, uploadArtifactFile, sessionId, handleSubmit, isResponding, isCancelling, selectedAgentName } = useChatContext();

    const createAgent = useCreateAgentMutation();
    const createConnector = useCreateConnectorMutation();
    const createWorkflow = useCreateWorkflowMutation();
    const updateWorkflow = useUpdateWorkflowMutation();
    const createGateway = useCreateGatewayMutation();
    const saveSkill = useSaveSkillFromConfigMutation();
    const createRemoteAgent = useCreateRemoteAgentMutation();
    const applyBuild = useApplyBuildMutation();
    const serverApplyEnabled = useIsBuilderServerApplyEnabled();
    const credentialResave = useCredentialResave(manifest);

    const [isSaving, setIsSaving] = useState(false);
    const isSavingRef = useRef(false);
    const saveIncompleteRef = useRef(false);
    const changedOnPlatformRef = useRef<string[] | undefined>(undefined);
    const matchedOnPlatformRef = useRef<string[]>([]);
    const savedResultsRef = useRef<ReadonlyMap<string, SaveResult>>(new Map());
    const declinedOnPlatformRef = useRef<string[]>([]);
    const [savedComponents, setSavedComponents] = useState<Map<string, SaveResult>>(new Map());
    const [savedVersions, setSavedVersions] = useState<SavedVersions>({ sessionId, byFile: NO_VERSIONS });
    const savedVersionsHere = savedVersionsIn(savedVersions, sessionId);
    const [errorMessage, setErrorMessage] = useState<string | null>(null);
    const [errorBlocked, setErrorBlocked] = useState(false);
    const [saveFailure, setSaveFailureState] = useState<string | null>(null);
    const saveFailureRef = useRef<string | null>(null);
    const setSaveFailure = useCallback((message: string | null) => {
        saveFailureRef.current = message;
        setSaveFailureState(message);
    }, []);
    const failedDeployNamesRef = useRef<readonly string[] | null>(null);
    const deployedAtFailureRef = useRef<ReadonlySet<string>>(new Set());
    const manifestComponentsRef = useRef(manifest?.components);
    manifestComponentsRef.current = manifest?.components;
    const errorMessageRef = useRef<string | null>(null);
    const setError = useCallback((msg: string | null) => {
        setErrorBlocked(false);
        failedDeployNamesRef.current = null;
        errorMessageRef.current = msg;
        setErrorMessage(msg);
    }, []);
    const setDeployOutcomeError = useCallback(
        (msg: string, failedNames: readonly string[]) => {
            setError(msg);
            failedDeployNamesRef.current = failedNames;
            deployedAtFailureRef.current = new Set(manifestComponentsRef.current?.filter(c => c.status === "deployed").map(c => c.name));
        },
        [setError]
    );
    const deployOutcomeStands = useCallback(() => {
        const statusByName = new Map(manifestComponentsRef.current?.map(c => [c.name, c.status]));
        return !!failedDeployNamesRef.current?.some(name => statusByName.has(name) && (statusByName.get(name) !== "deployed" || deployedAtFailureRef.current.has(name)));
    }, []);
    const clearUnlessDeployOutcome = useCallback(() => {
        if (!deployOutcomeStands()) setError(null);
    }, [deployOutcomeStands, setError]);
    const setBlockedError = useCallback((msg: string) => {
        setErrorBlocked(true);
        failedDeployNamesRef.current = null;
        errorMessageRef.current = msg;
        setErrorMessage(msg);
    }, []);

    const [connectorEditDialog, setConnectorEditDialog] = useState<{
        connectorJson: string;
    } | null>(null);
    const saveResumeRef = useRef<((filledJson: string) => void) | null>(null);
    const cancelRef = useRef<((reason?: unknown) => void) | null>(null);
    const [overwritePrompt, setOverwritePrompt] = useState<PlannedOverwrite[] | null>(null);
    const overwriteDecisionRef = useRef<{ resolve: () => void; reject: (reason: Error) => void } | null>(null);

    const hasManifest = !!manifest?.components?.length && !!fetchConfigByFilename;
    const allSaved = hasManifest ? manifest!.components!.every(c => c.origin === "platform" || savedComponents.has(c.name)) : savedComponents.size > 0;

    const sessionComponents = manifest?.components?.filter(c => c.origin !== "platform") ?? [];
    const allSessionValidated = sessionComponents.length > 0 && sessionComponents.every(c => c.status === "validated");
    const validationStamp = validationStampState(manifest, artifactVersions ?? NO_VERSIONS);
    const readyToSave = hasManifest && allSessionValidated && validationStamp.state === "current";
    const {
        check: platformCheck,
        key: platformCheckKey,
        checkAgain,
    } = usePlatformCheck({
        enabled: serverApplyEnabled && hasManifest && validationStamp.state === "current" && hasBuildToActivate,
        idle: !isResponding && !isCancelling,
        buildName: manifest?.name ?? "",
        sessionId: sessionId ?? "",
        components: manifest?.components ?? [],
        fetchConfigByFilename: fetchConfigByFilename ?? missingConfigReader,
        applyBuild: (vars, signal) => previewBuild({ ...vars, builderSessionId: sessionId }, { signal }),
    });

    const saveOnlyBuild = isSaveOnlyBuild(manifest?.components);
    const recordedComponents = withSavedVersions(manifest?.components, savedVersionsHere);
    const saveOnlyEdits = saveOnlyEditState(recordedComponents, artifactVersions, serverApplyEnabled);
    const recordedComponentsRef = useRef(recordedComponents);
    recordedComponentsRef.current = recordedComponents;
    const lastReadRef = useRef<VersionsByFile>(NO_VERSIONS);
    const listVersionsRef = useRef(artifactVersions);
    listVersionsRef.current = artifactVersions;
    const versionsSignature = entriesSignature(saveOnlyEntries(manifest?.components, artifactVersions));
    const declineKey = declineKeyAt(manifest?.components, artifactVersions, lastReadRef.current);
    const declinedAtRef = useRef<string | null>(null);
    const shownSessionRef = useRef(sessionId);
    shownSessionRef.current = sessionId;
    const mountedRef = useRef(true);
    useEffect(() => {
        mountedRef.current = true;
        return () => {
            mountedRef.current = false;
        };
    }, []);
    const readBySaveRef = useRef<ReadonlySet<string>>(new Set());

    const autoSaveTriggeredRef = useRef(false);
    const saveOnlyTriggeredRef = useRef(false);
    const autoSavedInThisFlowRef = useRef(false);

    const agentRespondedThisMountRef = useRef(false);
    useEffect(() => {
        if (isResponding) agentRespondedThisMountRef.current = true;
    }, [isResponding]);

    const autoFixRef = useRef<AutoFixState>(initialAutoFixState);
    const [autoFix, setAutoFix] = useState<AutoFixState>(initialAutoFixState);
    const isRespondingRef = useRef(isResponding);
    isRespondingRef.current = isResponding;
    const isCancellingRef = useRef(isCancelling);
    isCancellingRef.current = isCancelling;
    const handleSubmitRef = useRef(handleSubmit);
    handleSubmitRef.current = handleSubmit;
    const sessionIdRef = useRef(sessionId);
    sessionIdRef.current = sessionId;

    const dispatchAutoFix = useCallback((event: AutoFixEvent): AutoFixOutcome => {
        const outcome = autoFixTransition(autoFixRef.current, event);
        if (outcome.state !== autoFixRef.current) {
            autoFixRef.current = outcome.state;
            setAutoFix(outcome.state);
        }
        if (outcome.dropped) console.debug("[builder] held auto-fix dropped", { reason: outcome.dropped });
        if (outcome.send) {
            const fakeEvent = { preventDefault: () => {} } as FormEvent;
            handleSubmitRef.current(fakeEvent, null, outcome.send.message, null, outcome.send.displayChip, null, null, null, { auto_fix: true, resume_build: true });
        }
        return outcome;
    }, []);
    const autoFixSubmitted = autoFix.submitted;
    const autoFixExhausted = autoFix.exhausted;

    const postToBuilder = useCallback((message: string, displayChip: string) => {
        const fakeEvent = { preventDefault: () => {} } as FormEvent;
        handleSubmitRef.current(fakeEvent, null, message, null, displayChip, null, null, null, { resume_build: true });
    }, []);
    const { canAskBuilder, askBuilder } = usePlatformCheckHandoff({
        check: platformCheck,
        key: platformCheckKey,
        idle: !isResponding && !isCancelling,
        postable: !isSubmissionDropped({ hasContent: true, isResponding, isCancelling, selectedAgentName }),
        sessionId: sessionId ?? "",
        agentResponded: agentRespondedThisMountRef.current,
        reportHeld: autoFix.submitted || autoFix.held !== null || isSaving || saveFailure !== null || (!!autoSave && !allSaved),
        autoFixExhausted,
        dispatchAutoFix,
        postToBuilder,
    });

    useEffect(() => {
        if (isResponding || !autoFixRef.current.held) return;
        const standingFailures = [saveFailureRef.current, errorMessageRef.current].filter((text): text is string => text !== null);
        dispatchAutoFix({ type: "turnEnded", turn: { isCancelling, sessionId, standingFailures } });
    }, [isResponding, isCancelling, sessionId, autoFix.held, dispatchAutoFix]);

    useEffect(() => {
        if (isCancelling && autoFixRef.current.held) dispatchAutoFix({ type: "cancelled" });
    }, [isCancelling, autoFix.held, dispatchAutoFix]);

    const reportErrorToAgent = useCallback<ReportErrorToAgentFn>(
        (componentName, operation, errorText, rawError) => {
            if (!isAgentFixableError(rawError)) return false;
            const key = `${operation}|${componentName ?? ""}|${errorText}`;

            const target = componentName ? `for **${componentName}**` : "";
            const chipSubject = componentName ? ` (${componentName})` : "";
            const message =
                `[automated] ${operation} ${target} failed with:\n\n` +
                "```\n" +
                errorText +
                "\n```\n\n" +
                "Please diagnose the root cause, fix the affected configuration(s), and resume the build. " +
                "If this is a validation error, update the config YAML/JSON to satisfy the schema.";

            const displayChip = chatChip(`⟳ Auto-fix: ${operation}${chipSubject}`);

            return dispatchAutoFix({ type: "report", fix: { message, displayChip, key, sessionId: sessionIdRef.current, errorText }, responding: isRespondingRef.current || isCancellingRef.current }).accepted === true;
        },
        [dispatchAutoFix]
    );

    const manifestSignature = manifest?.components?.map(c => `${c.name}:${c.status}`).join("|");
    const previousSignatureRef = useRef(manifestSignature);
    const validatedVersionsSignature = JSON.stringify(manifest?.full_validation_versions);
    useEffect(() => {
        const settledByDeploy = isDeployStatusFlip(previousSignatureRef.current, manifestSignature);
        previousSignatureRef.current = manifestSignature;
        if (settledByDeploy && deployOutcomeStands()) return;
        setError(null);
        if (!settledByDeploy) setSaveFailure(null);
        if (!settledByDeploy || saveFailureRef.current === null) dispatchAutoFix({ type: "contentChanged" });
        autoSavedInThisFlowRef.current = false;
        autoDeployTriggeredRef.current = false;
        saveOnlyTriggeredRef.current = false;
    }, [manifestSignature, manifest?.full_validation_passed, validatedVersionsSignature, autoDeployTriggeredRef, deployOutcomeStands, setSaveFailure, dispatchAutoFix]);

    useEffect(() => {
        if (!autoFixSubmitted) return;
        const timer = setTimeout(() => dispatchAutoFix({ type: "watchdog" }), autoFix.held ? HELD_AUTO_FIX_LIMIT_MS : AUTO_FIX_WATCHDOG_MS);
        return () => clearTimeout(timer);
    }, [autoFixSubmitted, autoFix.held, dispatchAutoFix]);

    const promptForConnectorEdit = useCallback((json: string): Promise<string> => {
        return new Promise((resolve, reject) => {
            saveResumeRef.current = resolve;
            cancelRef.current = reject;
            setConnectorEditDialog({ connectorJson: json });
        });
    }, []);

    const handleConnectorEditSave = useCallback((updatedJson: string) => {
        if (saveResumeRef.current) {
            saveResumeRef.current(updatedJson);
            saveResumeRef.current = null;
            cancelRef.current = null;
        }
        setConnectorEditDialog(null);
    }, []);

    const confirmOverwrites = useCallback((overwrites: PlannedOverwrite[]): Promise<void> => {
        if (shouldSkipOverwriteConfirm()) return Promise.resolve();
        return new Promise<void>((resolve, reject) => {
            overwriteDecisionRef.current = { resolve, reject };
            setOverwritePrompt(overwrites);
        });
    }, []);

    const handleOverwriteConfirm = useCallback(() => {
        overwriteDecisionRef.current?.resolve();
        overwriteDecisionRef.current = null;
        setOverwritePrompt(null);
    }, []);

    const handleOverwriteCancel = useCallback(() => {
        overwriteDecisionRef.current?.reject(new Error(USER_CANCELLED_MESSAGE));
        overwriteDecisionRef.current = null;
        setOverwritePrompt(null);
    }, []);

    const withdrawOverwritePrompt = useCallback(() => {
        overwriteDecisionRef.current?.reject(new Error(SESSION_LEFT_MESSAGE));
        overwriteDecisionRef.current = null;
        setOverwritePrompt(null);
    }, []);

    const handleConnectorEditClose = useCallback(() => {
        if (cancelRef.current) {
            cancelRef.current(new Error(USER_CANCELLED_MESSAGE));
            cancelRef.current = null;
            saveResumeRef.current = null;
        }
        setConnectorEditDialog(null);
    }, []);

    const fetchSkillArtifact = useCallback<FetchSkillArtifact>(
        async artifactName => {
            if (!sessionId) {
                throw new Error(`No active session to resolve artifact "${artifactName}" from.`);
            }
            return getArtifactContentWithValidation({ sessionId, filename: artifactName });
        },
        [sessionId]
    );

    const saveOneComponent = useCallback(
        async (yamlText: string, type: ConfigType, name: string, connectorIds?: string[], savedSoFar?: Map<string, SaveResult>, existingPlatformId?: string, skillContext?: SkillSaveContext): Promise<SaveResult | null> => {
            switch (type) {
                case "agent":
                    return saveAgentComponent(yamlText, name, connectorIds, createAgent, savedSoFar ?? new Map());
                case "connector":
                    return saveConnectorComponent(yamlText, name, createConnector);
                case "workflow":
                    return saveWorkflowComponent(yamlText, name, createWorkflow, existingPlatformId, updateWorkflow);
                case "gateway":
                    return saveGatewayComponent(yamlText, name, createGateway);
                case "skill":
                    return saveSkillComponent(yamlText, name, saveSkill, fetchSkillArtifact, skillContext);
                case "a2a_agent":
                    return saveA2AAgentComponent(yamlText, name, createRemoteAgent);
                default:
                    return null;
            }
        },
        [promptForConnectorEdit, uploadArtifactFile, sessionId, createAgent, createConnector, createWorkflow, updateWorkflow, createGateway, saveSkill, createRemoteAgent, fetchSkillArtifact]
    );

    const handleSaveAll = useCallback(
        async (options: SaveOptions = {}): Promise<SaveAllResult> => {
            if (!manifest?.components || !fetchConfigByFilename) return { kind: "saved", changedOnPlatform: undefined };
            if (isSavingRef.current) return { kind: "busy" };
            isSavingRef.current = true;
            setIsSaving(true);
            setError(null);
            setSaveFailure(null);
            dispatchAutoFix({ type: "saveStarted" });
            saveIncompleteRef.current = false;
            declinedAtRef.current = declineAfterStart(declinedAtRef.current, options.automatic);
            const shown = () => mountedRef.current && shownSessionRef.current === sessionId;
            const surface: FailureSurface = {
                bridged,
                setError: whileShown(shown, setError, undefined),
                keepSaveFailure: whileShown(shown, setSaveFailure, undefined),
                reportErrorToAgent: whileShown(shown, reportErrorToAgent, false),
                displayError: whileShown(shown, displayError, undefined),
            };
            const keepResults = whileShown(shown, (kept: Map<string, SaveResult>) => setSavedComponents(kept), undefined);
            const markFlowSaved = whileShown(
                shown,
                () => {
                    autoSavedInThisFlowRef.current = true;
                },
                undefined
            );

            const versionsAtStart = credentialResave.currentVersions();
            const results = new Map(savedComponents);
            const accumulatedChanges: string[] = [];
            const accumulatedDeclines: string[] = [];
            const accumulatedNotWritten: string[] = [];
            let changedOnPlatform: string[] | undefined;
            let partiallyFailed = false;

            const recordedBefore = recordedComponentsRef.current;
            const listedBefore = listVersionsRef.current;
            const plan = planSave(manifest.components, recordedBefore, await readVersionsBeforeSave(recordSavedComponents, manifest.components), listedBefore, serverApplyEnabled);
            if (plan.failedLookups.length > 0) {
                isSavingRef.current = false;
                setIsSaving(false);
                saveIncompleteRef.current = shown();
                return endFailedLookupSave(failedLookupsError(plan.failedLookups, manifest.components), surface, shown());
            }
            lastReadRef.current = plan.readAt;
            readBySaveRef.current = plan.readEntries;
            const { components } = plan;
            const ordered = orderComponentsForSave(components);

            const { onPlatform, connectorsByName: platformConnectorsByName, workflowsByName, workflowsListed } = await fetchSaveContext();
            const alreadyOnPlatform = (name: string) => onPlatform.has(name) || onPlatform.has(normalizeComponentName(name));
            const alreadySaved = (name: string) => results.has(name);

            const attribution: { comp: ManifestComponent | null } = { comp: null };
            const run: SaveRun = {
                changed: accumulatedChanges,
                declined: accumulatedDeclines,
                notWritten: accumulatedNotWritten,
                ordered,
                results,
                skip: comp => shouldSkipSave(comp, alreadySaved, alreadyOnPlatform),
                attribute: comp => {
                    attribution.comp = comp;
                },
                onPlanRefused: checkAgain,
                fetchConfigByFilename: (filename, readOptions) => fetchConfigByFilename(filename, pinnedTo(plan.readAt, filename, readOptions)),
                saveOne: async (yamlText, comp, connectorIds, skill) => {
                    const platformId = platformWorkflowId(comp, workflowsByName, comp.type === "workflow" ? workflowSaveNames(yamlText) : {});
                    requireWorkflowListForCreate(comp.type, platformId, workflowsListed, comp.name);
                    return saveOneComponent(yamlText, requireRecognizedConfig(yamlText, comp), comp.name, connectorIds, results, platformId, skill);
                },
            };

            changedOnPlatformRef.current = serverApplyEnabled ? accumulatedChanges : undefined;
            matchedOnPlatformRef.current = [];
            savedResultsRef.current = results;
            declinedOnPlatformRef.current = accumulatedDeclines;

            try {
                const overwriteGuard = await overwriteGuardFor(sessionId, question => confirmIfShown(shownSessionRef.current, sessionId, question, confirmOverwrites));
                const outcome = serverApplyEnabled
                    ? await runServerApply(run, { ...manifest, components }, vars => applyBuild.mutateAsync({ ...vars, builderSessionId: sessionId }), overwriteGuard)
                    : await runPerKindSaves(run, components, platformConnectorsByName, overwriteGuard);

                markFlowSaved();
                keepResults(results);
                await refreshAgentsAfterSave(userAgentsRefetch);
                reportOncePerSession("builder:completed");
                announceSave(whileShown(shown, addNotification, undefined), outcome, options.automatic);
                changedOnPlatform = outcome.changedOnPlatform;
                matchedOnPlatformRef.current = outcome.matchedOnPlatform ?? [];

                if (outcome.partialFailures?.length) {
                    reportPartialFailures(outcome.partialFailures, { componentType: attribution.comp?.type, ...surface });
                    partiallyFailed = true;
                    saveIncompleteRef.current = shown();
                }
                const proof = saveProofOf(outcome, accumulatedDeclines);
                credentialResave.recordSaved(manifest.components, proof, latestVersions(versionsAtStart, plan.readAt));
                const versionsSavedAt = await recordSavedComponents.record(proof, plan.readAt, components);
                setSavedVersions(prev => withSavedAt(prev, sessionId, versionsSavedAt));
            } catch (err: unknown) {
                keepResults(results);
                if (isUserCancellation(err)) {
                    isSavingRef.current = false;
                    setIsSaving(false);
                    const outcome = cancellationOutcome(err, declinedAtRef.current, plan.declineKey);
                    const declined = outcome.kind === "declined";
                    setError(outcome.banner);
                    saveIncompleteRef.current = declined;
                    declinedAtRef.current = outcome.declinedAt;
                    if (declined) dispatchAutoFix({ type: "contentChanged" });
                    return { kind: "cancelled" };
                }
                saveIncompleteRef.current = shown();
                reportSaveFailure(err, attribution.comp, surface);
                return leftBehind(shown(), { kind: "failed", changedOnPlatform: changedOnPlatform ?? changedOnPlatformRef.current, notWritten: accumulatedNotWritten }, err);
            } finally {
                isSavingRef.current = false;
                setIsSaving(false);
            }
            return leftBehind(shown(), savedOrPartial(partiallyFailed, changedOnPlatform ?? changedOnPlatformRef.current, accumulatedNotWritten));
        },
        [
            manifest,
            fetchConfigByFilename,
            savedComponents,
            saveOneComponent,
            addNotification,
            displayError,
            userAgentsRefetch,
            reportErrorToAgent,
            bridged,
            serverApplyEnabled,
            applyBuild,
            confirmOverwrites,
            sessionId,
            setError,
            recordSavedComponents,
            credentialResave,
            dispatchAutoFix,
            checkAgain,
        ]
    );

    const handleSaveSingle = useCallback(async () => {
        setIsSaving(true);
        setError(null);

        try {
            const result = await saveOneComponent(configYaml, configType, "component");
            if (result) {
                setSavedComponents(new Map([["component", result]]));
                await userAgentsRefetch?.();
                addNotification(`${configType === "agent" ? "Agent" : "Workflow"} saved successfully.`, "success");
            }
        } catch (err: unknown) {
            if (isUserCancellation(err)) {
                setIsSaving(false);
                return;
            }
            const errorMessage = getErrorMessage(err, "Failed to save configuration.");
            setError(errorMessage);
            if (!bridged) displayError({ title: "Save failed", error: errorMessage });
        } finally {
            setIsSaving(false);
        }
    }, [configYaml, configType, saveOneComponent, addNotification, displayError, userAgentsRefetch, bridged]);

    void handleSaveSingle;

    useEffect(() => {
        if (autoSave && readyToSave && !allSaved && !isSaving && !autoSaveTriggeredRef.current) {
            console.log("[builder] auto-save firing");
            autoSaveTriggeredRef.current = true;
            saveOnlyTriggeredRef.current = true;
            handleSaveAll().then(saved => {
                if (saved.kind !== "busy") onAutoSaveComplete?.();
            });
        } else if (autoSave && !autoSaveTriggeredRef.current) {
            console.debug("[builder] auto-save waiting", {
                autoSave,
                readyToSave,
                allSaved,
                isSaving,
                hasManifest: !!manifest?.components?.length,
                fullValidationPassed: manifest?.full_validation_passed,
                sessionStatuses: manifest?.components?.filter(c => c.origin !== "platform").map(c => `${c.name}:${c.status ?? "<undef>"}`),
            });
        }
        if (!autoSave) {
            autoSaveTriggeredRef.current = false;
        }
    }, [autoSave, readyToSave, allSaved, isSaving, handleSaveAll, onAutoSaveComplete, manifest]);

    const { hasUnsavedEdits, allMatchLastSave, hasSkill } = saveOnlyEdits;
    const turnsEnded = useTurnsEnded(isResponding);
    useReleaseLatch(saveOnlyTriggeredRef, versionsSignature, turnsEnded, hasSkill, isSaving, readBySaveRef);
    useOnSessionChange(sessionId, () => {
        agentRespondedThisMountRef.current = isResponding;
        declinedAtRef.current = null;
        saveOnlyTriggeredRef.current = false;
        if (errorMessageRef.current === SAVE_CANCELLED_MESSAGE) setError(null);
        dispatchAutoFix({ type: "retry" });
        withdrawOverwritePrompt();
    });
    useEffect(() => {
        if (
            !shouldAutoSaveOnly({
                saveOnlyBuild,
                readyToSave,
                agentResponded: agentRespondedThisMountRef.current,
                allSaved,
                isSaving,
                isResponding,
                hasUnsavedEdits,
                allMatchLastSave,
                hasSkill,
                declined: declinedAtRef.current === declineKey,
                saveOnlyTriggered: saveOnlyTriggeredRef.current,
                autoSaveTriggered: autoSaveTriggeredRef.current,
            })
        )
            return;
        saveOnlyTriggeredRef.current = true;
        autoDeployTriggeredRef.current = true;
        handleSaveAll({ automatic: true }).then(saved => {
            if (saved.kind === "saved" || saved.kind === "failed") onAutoSaveComplete?.();
        });
    }, [saveOnlyBuild, readyToSave, allSaved, isSaving, isResponding, hasUnsavedEdits, allMatchLastSave, hasSkill, versionsSignature, declineKey, turnsEnded, handleSaveAll, onAutoSaveComplete, autoDeployTriggeredRef]);

    const approvalRef = useRef({ autoSave, onAutoSaveComplete });
    approvalRef.current = { autoSave, onAutoSaveComplete };
    const endApproval = useCallback(() => {
        if (approvalRef.current.autoSave) approvalRef.current.onAutoSaveComplete?.();
    }, []);

    const handleManualRetry = useCallback(() => {
        if (isSavingRef.current) return;
        endApproval();
        dispatchAutoFix({ type: "retry" });
        autoSaveTriggeredRef.current = false;
        autoDeployTriggeredRef.current = false;
        setError(null);
        if (retryAction(allSaved, saveIncompleteRef.current, serverApplyEnabled && credentialResave.awaitingSave(manifest?.components)) === "save") {
            autoDeployTriggeredRef.current = true;
            handleSaveAll().then(saved => {
                const next = deployAfterSave(saved);
                if (next.deploy) handleDeployManifestRef.current?.(next.changedOnPlatform, next.options);
            });
        } else {
            autoSavedInThisFlowRef.current = true;
            const fn = handleDeployRef.current;
            if (fn) fn();
        }
    }, [allSaved, manifest, serverApplyEnabled, credentialResave, handleSaveAll, endApproval, dispatchAutoFix]);

    const connectorDialogElement = <ConnectorEditDialog isOpen={!!connectorEditDialog} onClose={handleConnectorEditClose} connectorJson={connectorEditDialog?.connectorJson ?? "{}"} onSave={handleConnectorEditSave} />;

    const overwriteDialogElement = <OverwriteConfirmDialog overwrites={overwritePrompt} onConfirm={handleOverwriteConfirm} onCancel={handleOverwriteCancel} />;

    return {
        savedComponents,
        isSaving,
        allSaved,
        hasManifest,
        readyToSave,
        validationStamp,
        platformCheck,
        checkAgain,
        canAskBuilder,
        askBuilder,
        turnRunning: isResponding || isCancelling,
        deployFailure: toDeployFailureState(joinFailures(saveFailure, errorMessage), autoFixSubmitted, autoFixExhausted, errorBlocked),
        autoSavedInThisFlowRef,
        reportErrorToAgent,
        setError,
        setBlockedError,
        setDeployOutcomeError,
        clearUnlessDeployOutcome,
        handleSaveAll,
        isSavingRef,
        changedOnPlatformRef,
        matchedOnPlatformRef,
        savedResultsRef,
        declinedOnPlatformRef,
        handleManualRetry,
        endApproval,
        connectorEditDialog: connectorDialogElement,
        overwriteDialog: overwriteDialogElement,
    };
}
