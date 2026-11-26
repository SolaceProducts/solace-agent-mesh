// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useMemo, useRef, useState } from "react";
import { GripVertical } from "lucide-react";

import { useAgentCards } from "@/api/agent-cards";
import { useAgents } from "@/api/agents/hooks";
import { useEvalWatchlist, useSetEvalWatchlist } from "@/api/evaluations";
import { useRemoteAgents } from "@/api/remote-agents";

import { getErrorMessage } from "@/utils/api";
import { cn } from "@/utils/cnTailwind";

import { Button } from "@/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/ui/dialog";
import { SearchableCombobox } from "@/ui/searchable-combobox";
import type { WatchlistEntry } from "@/types/eval";

import { type AgentOption, useAgentOptions } from "@/features/entrypoints/hooks";

import { EmptyState, MessageBanner } from "@/common";
import { useIsWorkflowEvalsEnabled } from "@/hooks";

import { useWorkflowOptions } from "../hooks/useWorkflowOptions";

const MAX_AGENTS = 5;
const EMPTY_ROWS: string[] = Array(MAX_AGENTS).fill("");

interface EditWatchlistDialogProps {
    readonly open: boolean;
    readonly onOpenChange: (open: boolean) => void;
}

const getOptionId = (o: AgentOption) => o.name;
const getOptionSearchText = (o: AgentOption) => o.displayName;
const renderOptionSelected = (o: AgentOption) => <span>{o.displayName}</span>;
const renderOptionItem = (o: AgentOption) => <span className="font-medium">{o.displayName}</span>;

export function EditWatchlistDialog({ open, onOpenChange }: EditWatchlistDialogProps) {
    const watchlistQuery = useEvalWatchlist();
    const agentsQuery = useAgents();
    const meshAgentsQuery = useAgentCards();
    const setWatchlistMutation = useSetEvalWatchlist();

    const [rows, setRows] = useState<string[]>(EMPTY_ROWS);
    const [error, setError] = useState<string | null>(null);
    const [saving, setSaving] = useState(false);
    const [orphansDropped, setOrphansDropped] = useState(0);

    const agentOptions = useAgentOptions();
    const workflowEvalsEnabled = useIsWorkflowEvalsEnabled();
    const workflows = useWorkflowOptions(workflowEvalsEnabled);
    const remoteAgentsQuery = useRemoteAgents();
    const sourcesFailed = [agentsQuery, meshAgentsQuery, remoteAgentsQuery].some(q => !!q.error || q.fetchStatus === "paused") || workflows.isError;
    const allOptions = useMemo<AgentOption[]>(() => {
        const listedWorkflows = workflowEvalsEnabled ? workflows.options.map(w => ({ name: w.name, displayName: `${w.displayName} (workflow)` })) : [];
        const listed = [...agentOptions, ...listedWorkflows];
        const listedNames = new Set(listed.map(o => o.name));
        const unresolved = (watchlistQuery.data ?? [])
            .filter(e => !listedNames.has(e.agentName) && (sourcesFailed || (e.targetType === "workflow" && !(workflowEvalsEnabled && workflows.readable))))
            .map(e => ({ name: e.agentName, displayName: e.targetType === "workflow" ? `${e.agentName} (workflow)` : e.agentName }));
        return [...listed, ...unresolved];
    }, [agentOptions, workflows.options, workflows.readable, workflowEvalsEnabled, sourcesFailed, watchlistQuery.data]);

    const watchlistUnloaded = watchlistQuery.data === undefined && !watchlistQuery.isLoading;
    const isLoading = watchlistQuery.isLoading || agentsQuery.isLoading || meshAgentsQuery.isLoading || remoteAgentsQuery.isLoading || workflows.isLoading;
    const initializedRef = useRef(false);
    const agentsError = sourcesFailed;

    useEffect(() => {
        if (!open) {
            initializedRef.current = false;
            setOrphansDropped(0);
            return;
        }
        if (isLoading || initializedRef.current || watchlistUnloaded) return;
        initializedRef.current = true;
        const knownNames = new Set(allOptions.map(o => o.name));
        const persisted = (watchlistQuery.data ?? [])
            .slice()
            .sort((a: WatchlistEntry, b: WatchlistEntry) => a.position - b.position)
            .map((e: WatchlistEntry) => e.agentName);
        const live = persisted.filter(name => knownNames.has(name));
        const padded = [...live, ...EMPTY_ROWS].slice(0, MAX_AGENTS);
        setRows(padded);
        setError(null);
        setOrphansDropped(persisted.length - live.length);
    }, [open, isLoading, watchlistQuery.data, watchlistUnloaded, allOptions]);

    const refetchWatchlist = watchlistQuery.refetch;
    const retryWatchlist = () => {
        refetchWatchlist().catch(() => undefined);
    };
    useEffect(() => {
        if (open && watchlistUnloaded) retryWatchlist();
    }, [open]); // eslint-disable-line react-hooks/exhaustive-deps

    const usedNames = useMemo(() => new Set(rows.filter(Boolean)), [rows]);

    const unusedOptions = useMemo(() => allOptions.filter(o => !usedNames.has(o.name)), [allOptions, usedNames]);

    const optionsForRow = (value: string): AgentOption[] => {
        if (!value) return unusedOptions;
        const selected = allOptions.find(o => o.name === value);
        return selected ? [selected, ...unusedOptions] : unusedOptions;
    };

    const handleRowChange = (index: number, name: string) => {
        setRows(prev => {
            const next = [...prev];
            next[index] = name;
            return next;
        });
    };

    const handleSave = async () => {
        const trimmed = rows.map(r => r.trim()).filter(Boolean);
        const deduped: string[] = [];
        const seen = new Set<string>();
        for (const name of trimmed) {
            if (seen.has(name)) continue;
            seen.add(name);
            deduped.push(name);
        }
        if (deduped.length > MAX_AGENTS) {
            setError(`Select at most ${MAX_AGENTS} agents.`);
            return;
        }
        setSaving(true);
        setError(null);
        try {
            await setWatchlistMutation.mutateAsync(deduped);
            onOpenChange(false);
        } catch (err) {
            setError(getErrorMessage(err));
        } finally {
            setSaving(false);
        }
    };

    const handleDiscard = () => {
        onOpenChange(false);
    };

    const [dragIndex, setDragIndex] = useState<number | null>(null);

    const onDragStart = (idx: number) => setDragIndex(idx);
    const onDragOver = (e: React.DragEvent) => e.preventDefault();
    const onDrop = (idx: number) => {
        if (dragIndex === null || dragIndex === idx) {
            setDragIndex(null);
            return;
        }
        setRows(prev => {
            const next = [...prev];
            const [moved] = next.splice(dragIndex, 1);
            next.splice(idx, 0, moved);
            return next;
        });
        setDragIndex(null);
    };

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="flex max-w-2xl! flex-col gap-0 p-0">
                <DialogHeader className="border-b px-6 py-4">
                    <DialogTitle>Edit Watchlist</DialogTitle>
                </DialogHeader>

                <div className="flex-1 overflow-auto px-6 py-5">
                    {error && (
                        <div className="mb-4">
                            <MessageBanner variant="error" message={error} dismissible onDismiss={() => setError(null)} />
                        </div>
                    )}
                    {agentsError && (
                        <div className="mb-4">
                            <MessageBanner variant="error" message="Could not load agents. Try again." />
                        </div>
                    )}
                    {watchlistUnloaded && (
                        <div className="mb-4">
                            <MessageBanner
                                variant="error"
                                message={
                                    <span className="flex items-center gap-2">
                                        Could not load your watchlist.
                                        <Button variant="link" dense onClick={retryWatchlist}>
                                            Retry
                                        </Button>
                                    </span>
                                }
                            />
                        </div>
                    )}
                    {orphansDropped > 0 && (
                        <div className="mb-4">
                            <MessageBanner
                                variant="warning"
                                message={`${orphansDropped} previously watched ${orphansDropped === 1 ? "agent is" : "agents are"} no longer available and ${orphansDropped === 1 ? "was" : "were"} dropped from your Watchlist.`}
                                dismissible
                                onDismiss={() => setOrphansDropped(0)}
                            />
                        </div>
                    )}

                    <p className="mb-4 text-sm">Select up to {MAX_AGENTS} agents to display on your Watchlist.</p>

                    {isLoading ? (
                        <EmptyState variant="loading" title="Loading agents..." />
                    ) : (
                        <div className="flex flex-col gap-6">
                            {rows.map((value, idx) => (
                                <div key={`row-${idx}`} className="flex items-center gap-3" onDragOver={onDragOver} onDrop={() => onDrop(idx)}>
                                    <button type="button" draggable onDragStart={() => onDragStart(idx)} className={cn("cursor-grab text-(--secondary-text-wMain) active:cursor-grabbing", dragIndex === idx && "opacity-50")} aria-label="Reorder row">
                                        <GripVertical className="size-5" />
                                    </button>
                                    <div className="flex-1">
                                        <SearchableCombobox
                                            items={optionsForRow(value)}
                                            value={value}
                                            onChange={name => handleRowChange(idx, name)}
                                            getId={getOptionId}
                                            getSearchText={getOptionSearchText}
                                            renderSelected={renderOptionSelected}
                                            renderItem={renderOptionItem}
                                            placeholder="Select an agent..."
                                            searchPlaceholder="Search agents..."
                                            noMatchText="No agents match your search."
                                            clearable
                                        />
                                    </div>
                                </div>
                            ))}
                        </div>
                    )}
                </div>

                <div className="flex items-center justify-end gap-3 border-t px-6 py-4">
                    <Button variant="ghost" onClick={handleDiscard} disabled={saving}>
                        Discard Changes
                    </Button>
                    <Button onClick={() => void handleSave()} disabled={saving || isLoading || watchlistUnloaded}>
                        {saving ? "Saving..." : "Save"}
                    </Button>
                </div>
            </DialogContent>
        </Dialog>
    );
}
