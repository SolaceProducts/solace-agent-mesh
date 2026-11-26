// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useMemo, useRef, useState } from "react";
import { CircleCheckBig, Info, Plus, X } from "lucide-react";

import { useAddToolAuthAgent, useBeginExperimentToolAuth, useDisconnectExperimentToolAuth, useExperimentToolAuth, useRemoveToolAuthAgent, useToolAuthCandidateAgents } from "@/api/evaluations";

import { useUserContext } from "@/providers/contexts";

import { getErrorMessage } from "@/utils/api";
import { formatTimestamp } from "@/utils/format";
import { isWailsRuntime, openExternalLink } from "@/utils/openExternalLink";

import { Button } from "@/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/ui/tooltip";
import type { ExperimentToolAuth, ExperimentToolAuthAgent, ExperimentToolAuthItem } from "@/types/eval";

import { type AgentOption, findAgentOption, useAgentOptions } from "@/features/entrypoints/hooks";

import { ConfirmationDialog, MessageBanner } from "@/common";

import { EVAL_DEFAULT_RUN_AS } from "../constants";
import { agentSectionTitle } from "../utils";

import { AgentAutocomplete } from "./AgentAutocomplete";

const PENDING_POLL_WINDOW_MS = 2 * 60 * 1000;

interface ConnectToolsPanelProps {
    readonly experimentId: string;
    readonly runAs?: string;
    readonly targetIsWorkflow?: boolean;
}

interface CredentialRef {
    readonly agentName: string;
    readonly credentialKey: string;
}

function sameRef(a: CredentialRef | null, b: CredentialRef): boolean {
    return a !== null && a.agentName === b.agentName && a.credentialKey === b.credentialKey;
}

function displayNameOf(item: ExperimentToolAuthItem): string {
    return item.displayName || item.credentialKey;
}

function agentLabel(options: AgentOption[], agentName: string): string {
    return findAgentOption(options, agentName)?.displayName ?? agentName;
}

function ItemRow({
    agentName,
    item,
    authorizing,
    fallbackUri,
    onAuthorize,
    onDisconnect,
}: {
    readonly agentName: string;
    readonly item: ExperimentToolAuthItem;
    readonly authorizing: boolean;
    readonly fallbackUri?: string;
    readonly onAuthorize?: (ref: CredentialRef) => void;
    readonly onDisconnect?: (ref: CredentialRef, label: string) => void;
}) {
    return (
        <li data-testid="connectToolsItem" data-credential-key={item.credentialKey} data-agent-name={agentName} className="flex flex-col gap-1 border-b border-(--secondary-w20)/60 py-3 last:border-b-0">
            <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
                <span className="min-w-0 basis-full text-sm font-medium [overflow-wrap:anywhere] text-(--primary-text-wMain)">{displayNameOf(item)}</span>
                {item.status === "connected" ? (
                    <>
                        <span className="flex items-center gap-1.5 text-sm text-(--success-wMain)">
                            <CircleCheckBig className="size-4 shrink-0" />
                            Connected
                        </span>
                        {onDisconnect && (
                            <Button size="sm" variant="ghost" onClick={() => onDisconnect({ agentName, credentialKey: item.credentialKey }, displayNameOf(item))}>
                                Disconnect
                            </Button>
                        )}
                    </>
                ) : (
                    <>
                        <span className="text-sm text-(--secondary-text-wMain)">Not connected</span>
                        {item.interactive ? (
                            onAuthorize && (
                                <Button size="sm" variant="outline" loading={authorizing} onClick={() => onAuthorize({ agentName, credentialKey: item.credentialKey })}>
                                    Authorize
                                </Button>
                            )
                        ) : (
                            <Tooltip>
                                <TooltipTrigger asChild>
                                    <span className="flex items-center gap-1 text-xs text-(--secondary-text-wMain)">
                                        <Info className="size-3.5" />
                                        Set in configuration
                                    </span>
                                </TooltipTrigger>
                                <TooltipContent>This credential is provisioned in the agent&apos;s configuration. It cannot be authorized from the browser.</TooltipContent>
                            </Tooltip>
                        )}
                    </>
                )}
            </div>
            {item.lastAuthorizedBy && (
                <div className="text-xs text-(--secondary-text-wMain)">{item.lastAuthorizedAt ? `Last authorized by ${item.lastAuthorizedBy} on ${formatTimestamp(item.lastAuthorizedAt)}` : `Last authorized by ${item.lastAuthorizedBy}`}</div>
            )}
            {fallbackUri && (
                <div className="text-xs">
                    The browser blocked the authorization window.{" "}
                    <a href={fallbackUri} target="_blank" rel="noopener noreferrer" className="font-medium text-(--primary-wMain) underline">
                        Open the authorization page
                    </a>
                </div>
            )}
        </li>
    );
}

function AgentSection({
    group,
    label,
    targetIsWorkflow,
    authorizing,
    fallback,
    onAuthorize,
    onDisconnect,
    onRemove,
    removing,
}: {
    readonly group: ExperimentToolAuthAgent;
    readonly label: string;
    readonly targetIsWorkflow: boolean;
    readonly authorizing: CredentialRef | null;
    readonly fallback: { ref: CredentialRef; uri: string } | null;
    readonly onAuthorize?: (ref: CredentialRef) => void;
    readonly onDisconnect?: (ref: CredentialRef, label: string) => void;
    readonly onRemove?: () => void;
    readonly removing: boolean;
}) {
    return (
        <div data-testid="connectToolsAgentSection" data-agent-name={group.agentName} className="border-b border-(--secondary-w20)/60 last:border-b-0">
            <div className="flex items-baseline gap-2 pt-3">
                <span className="flex-1 text-sm font-medium text-(--primary-text-wMain)">{agentSectionTitle(group, label, targetIsWorkflow)}</span>
                {onRemove && (
                    <Button size="sm" variant="ghost" aria-label={`Remove ${label}`} loading={removing} onClick={onRemove}>
                        <X className="size-4" />
                    </Button>
                )}
            </div>
            {group.reachable ? (
                <ul className="flex flex-col">
                    {group.tools.map(item => {
                        const ref = { agentName: group.agentName, credentialKey: item.credentialKey };
                        return (
                            <ItemRow
                                key={item.credentialKey}
                                agentName={group.agentName}
                                item={item}
                                authorizing={sameRef(authorizing, ref)}
                                fallbackUri={fallback && sameRef(fallback.ref, ref) ? fallback.uri : undefined}
                                onAuthorize={onAuthorize}
                                onDisconnect={onDisconnect}
                            />
                        );
                    })}
                </ul>
            ) : (
                <div className="py-3 text-sm text-(--secondary-text-wMain)">
                    Unable to read the credentials for this agent. The agent may be stopped, undeployed, or running a version without delegated tool authorization. Check that it is deployed and up to date.
                </div>
            )}
        </div>
    );
}

function openAuthUri(authUri: string): boolean {
    if (isWailsRuntime()) {
        openExternalLink(authUri);
        return true;
    }
    const authWindow = window.open(authUri, "_blank");
    if (!authWindow) {
        return false;
    }
    authWindow.opener = null;
    return true;
}

function emptyPickerMessage({ isError, meshTruncated, scanLimit, totalRunningAgents }: { readonly isError: boolean; readonly meshTruncated: boolean; readonly scanLimit: number; readonly totalRunningAgents?: number }): string {
    if (isError) {
        return "Unable to check which agents need authorization. Please try again later.";
    }
    if (meshTruncated) {
        return `Showing the first ${scanLimit} of ${totalRunningAgents} running agents, none of which has an authorizable tool. Agents past that limit were not checked.`;
    }
    return "No agent with an authorizable tool was found";
}

function derivePanelState({
    data,
    groups,
    visibleGroups,
    error,
    canPin,
}: {
    readonly data: ExperimentToolAuth | undefined;
    readonly groups: ExperimentToolAuthAgent[] | undefined;
    readonly visibleGroups: ExperimentToolAuthAgent[];
    readonly error: unknown;
    readonly canPin: boolean;
}) {
    const graphTruncated = (data?.totalAgents ?? 0) > (groups?.length ?? 0);
    const scanLimit = data?.agentScanLimit ?? 0;
    const meshTruncated = scanLimit > 0 && (data?.totalRunningAgents ?? 0) > scanLimit;
    const canAddAgents = (Boolean(data?.allowListIsWildcard) || graphTruncated) && canPin;
    const hasAuthorizable = visibleGroups.some(group => group.tools.some(item => item.interactive));
    const allUnreachable = visibleGroups.length > 0 && visibleGroups.every(group => !group.reachable);
    const unreadable = Boolean(error) && !data;
    const readStatus = (error as (Error & { status?: number }) | null)?.status;
    const silentlyUnreadable = unreadable && (readStatus === 503 || readStatus === 404);

    return { graphTruncated, scanLimit, meshTruncated, canAddAgents, hasAuthorizable, allUnreachable, unreadable, silentlyUnreadable };
}

export function ConnectToolsPanel({ experimentId, runAs, targetIsWorkflow = false }: ConnectToolsPanelProps) {
    const [pending, setPending] = useState<CredentialRef | null>(null);
    const [fallback, setFallback] = useState<{ ref: CredentialRef; uri: string } | null>(null);
    const [actionError, setActionError] = useState<string | null>(null);
    const [confirmDisconnect, setConfirmDisconnect] = useState<{ ref: CredentialRef; label: string } | null>(null);
    const [picking, setPicking] = useState(false);
    const pendingTimeoutRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

    const principal = runAs?.trim() ? runAs : EVAL_DEFAULT_RUN_AS;
    const agentOptions = useAgentOptions();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const canAuthorize = userHasCapability(CAPABILITIES.invokeEvaluations);
    const canPin = userHasCapability(CAPABILITIES.createEvaluations);
    const canUnpin = userHasCapability(CAPABILITIES.deleteEvaluations);

    const { data, isLoading, error, refetch } = useExperimentToolAuth(experimentId, { pollWhilePending: pending !== null, principal });
    const shownPrincipal = data?.runAs ?? principal;
    const begin = useBeginExperimentToolAuth();
    const disconnect = useDisconnectExperimentToolAuth();
    const addAgent = useAddToolAuthAgent();
    const removeAgent = useRemoveToolAuthAgent();

    const groups = data?.agents;

    const clearPendingWindow = () => {
        if (pendingTimeoutRef.current) clearTimeout(pendingTimeoutRef.current);
        pendingTimeoutRef.current = undefined;
    };

    useEffect(() => clearPendingWindow, []);

    useEffect(() => {
        if (!pending) return;
        const group = groups?.find(g => g.agentName === pending.agentName);
        if (group?.tools.some(item => item.credentialKey === pending.credentialKey && item.status === "connected")) {
            setPending(null);
            setFallback(current => (current && sameRef(current.ref, pending) ? null : current));
            clearPendingWindow();
        }
    }, [groups, pending]);

    const candidates = useToolAuthCandidateAgents(experimentId, { enabled: picking });

    const visibleGroups = useMemo(() => groups?.filter(group => !group.reachable || group.tools.some(item => item.interactive)) ?? [], [groups]);

    const addableAgents = useMemo(() => {
        const brokerName = (agentName: string) => findAgentOption(agentOptions, agentName)?.name ?? agentName;
        const candidateNames = new Set((candidates.data ?? []).map(brokerName));
        const listed = new Set((groups ?? []).map(group => brokerName(group.agentName)));
        return agentOptions.filter(option => candidateNames.has(option.name) && !listed.has(option.name));
    }, [agentOptions, candidates.data, groups]);

    const runAction = async (action: () => Promise<unknown>) => {
        setActionError(null);
        try {
            await action();
        } catch (err) {
            setActionError(getErrorMessage(err));
        }
    };

    const handleAuthorize = (ref: CredentialRef) =>
        runAction(async () => {
            setFallback(null);
            const { authUri } = await begin.mutateAsync({ experimentId, agentName: ref.agentName, credentialKey: ref.credentialKey });
            setPending(ref);
            clearPendingWindow();
            pendingTimeoutRef.current = setTimeout(() => setPending(null), PENDING_POLL_WINDOW_MS);
            if (!openAuthUri(authUri)) {
                setFallback({ ref, uri: authUri });
            }
        });

    const handleAddAgent = (agentName: string) => {
        setPicking(false);
        return runAction(() => addAgent.mutateAsync({ experimentId, agentName }));
    };

    const handleRemoveAgent = (agentName: string) => runAction(() => removeAgent.mutateAsync({ experimentId, agentName }));

    const handleDisconnect = async (ref: CredentialRef) => {
        await runAction(() => disconnect.mutateAsync({ experimentId, agentName: ref.agentName, credentialKey: ref.credentialKey }));
        setConfirmDisconnect(null);
    };

    const { graphTruncated, scanLimit, meshTruncated, canAddAgents, hasAuthorizable, allUnreachable, unreadable, silentlyUnreadable } = derivePanelState({
        data,
        groups,
        visibleGroups,
        error,
        canPin,
    });

    if (isLoading || silentlyUnreadable) {
        return null;
    }
    if (unreadable) {
        return (
            <section data-testid="connectToolsPanel">
                <h2 className="mb-3 text-sm font-bold">Connect Tools</h2>
                <div className="py-3">
                    <MessageBanner
                        variant="error"
                        message={
                            <span className="flex flex-wrap items-center gap-2">
                                Unable to read which tools need authorization. {getErrorMessage(error)}
                                <Button size="sm" variant="outline" onClick={() => refetch()}>
                                    Retry
                                </Button>
                            </span>
                        }
                    />
                </div>
            </section>
        );
    }
    if (!groups?.length || (!hasAuthorizable && !canAddAgents && !allUnreachable && !graphTruncated)) {
        return null;
    }

    const authorizingRef = begin.isPending ? (begin.variables ?? null) : null;
    const allTools = visibleGroups.flatMap(group => group.tools).filter(item => item.interactive);
    const connectedCount = allTools.filter(item => item.status === "connected").length;

    return (
        <section data-testid="connectToolsPanel">
            <div className="mb-3 flex items-baseline gap-2">
                <h2 className="text-sm font-bold">Connect Tools</h2>
                {allTools.length > 0 && (
                    <span className="text-sm text-(--secondary-text-wMain)">
                        ({connectedCount} of {allTools.length} connected)
                    </span>
                )}
            </div>
            <div>
                <div className="border-b border-(--secondary-w20)/60 py-3">
                    <div className="text-sm" data-testid="connectToolsPrincipal">
                        Runs as: <span className="font-medium">{shownPrincipal}</span>
                    </div>
                    <p className="mt-1 text-sm text-(--secondary-text-wMain)">
                        Authorizing signs in on behalf of the service account shown in Runs as, not the current user. Sign in with a shared account at the remote system, so any teammate can re-authorize and restore the same access.
                    </p>
                    {graphTruncated && (
                        <p className="mt-1 text-sm text-(--secondary-text-wMain)">
                            Showing {groups?.length ?? 0} of {data?.totalAgents} agents this experiment can delegate to.
                        </p>
                    )}
                </div>
                {actionError && (
                    <div className="pt-3">
                        <MessageBanner variant="error" message={actionError} dismissible onDismiss={() => setActionError(null)} />
                    </div>
                )}
                {error && (
                    <div className="pt-3">
                        <MessageBanner
                            variant="warning"
                            message={
                                <span className="flex flex-wrap items-center gap-2">
                                    Could not refresh these statuses just now, so they may be out of date.
                                    <Button size="sm" variant="outline" onClick={() => refetch()}>
                                        Retry
                                    </Button>
                                </span>
                            }
                        />
                    </div>
                )}
                {visibleGroups.map(group => (
                    <AgentSection
                        key={group.agentName}
                        group={group}
                        label={agentLabel(agentOptions, group.agentName)}
                        targetIsWorkflow={targetIsWorkflow}
                        authorizing={authorizingRef}
                        fallback={fallback}
                        onAuthorize={canAuthorize ? handleAuthorize : undefined}
                        onDisconnect={canAuthorize ? (ref, label) => setConfirmDisconnect({ ref, label }) : undefined}
                        onRemove={group.addedBy && canUnpin ? () => handleRemoveAgent(group.agentName) : undefined}
                        removing={removeAgent.isPending && removeAgent.variables?.agentName === group.agentName}
                    />
                ))}
                {canAddAgents && (
                    <div className="flex flex-col gap-2 py-3" data-testid="connectToolsAddAgent">
                        <p className="text-sm text-(--secondary-text-wMain)">
                            {data?.allowListIsWildcard
                                ? "Some agents here can call any other agent, so their delegated tools cannot all be listed automatically. Add an agent to see the credentials it needs."
                                : "This experiment delegates to more agents than one read lists. Add an agent to see the credentials it needs."}
                        </p>
                        {picking ? (
                            <div className="flex w-lg max-w-full flex-col gap-1">
                                <AgentAutocomplete options={addableAgents} value="" disabled={candidates.isLoading || addAgent.isPending} onChange={handleAddAgent} />
                                {!candidates.isLoading && addableAgents.length === 0 && (
                                    <span className="text-xs text-(--secondary-text-wMain)">{emptyPickerMessage({ isError: candidates.isError, meshTruncated, scanLimit, totalRunningAgents: data?.totalRunningAgents })}</span>
                                )}
                            </div>
                        ) : (
                            <Button size="sm" variant="outline" className="self-start" onClick={() => setPicking(true)}>
                                <Plus className="size-4" />
                                Add Agent
                            </Button>
                        )}
                    </div>
                )}
            </div>
            {confirmDisconnect && (
                <ConfirmationDialog
                    open
                    title={`Disconnect ${confirmDisconnect.label}`}
                    content={
                        <span className="text-sm">
                            Disconnecting removes the credential that <span className="font-medium">{agentLabel(agentOptions, confirmDisconnect.ref.agentName)}</span> holds for <span className="font-medium">{shownPrincipal}</span>. Runs of this
                            experiment fail on tool authorization until the credential is authorized again, with this account or another one.
                        </span>
                    }
                    actionLabels={{ confirm: "Disconnect" }}
                    isLoading={disconnect.isPending}
                    onOpenChange={open => {
                        if (!open) setConfirmDisconnect(null);
                    }}
                    onConfirm={() => handleDisconnect(confirmDisconnect.ref)}
                />
            )}
        </section>
    );
}
