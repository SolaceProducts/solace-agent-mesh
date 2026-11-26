// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useState } from "react";

import { evaluationsApi, useEvalExperiments } from "@/api/evaluations";

import { getErrorMessage } from "@/utils/api";

import { Button } from "@/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/ui/dialog";
import { Input } from "@/ui/input";

import { EmptyState, MessageBanner, MultiSelect } from "@/common";

interface RunExperimentsDialogProps {
    readonly open: boolean;
    readonly onOpenChange: (open: boolean) => void;
    readonly onRunComplete?: () => void;
}

export function RunExperimentsDialog({ open, onOpenChange, onRunComplete }: RunExperimentsDialogProps) {
    const { data: experiments = [], isLoading: loading } = useEvalExperiments(undefined, { enabled: open });
    const [selectedNames, setSelectedNames] = useState<string[]>([]);
    const [reportName, setReportName] = useState("");
    const [running, setRunning] = useState(false);
    const [error, setError] = useState<string | null>(null);

    useEffect(() => {
        if (!open) {
            setSelectedNames([]);
            setReportName("");
            setError(null);
        }
    }, [open]);

    const isSingleSelection = selectedNames.length === 1;

    const handleRun = async () => {
        if (selectedNames.length === 0) return;
        setRunning(true);
        setError(null);
        try {
            const selected = experiments.filter(e => selectedNames.includes(e.name));
            const runNameArg = isSingleSelection && reportName.trim() ? { runName: reportName.trim() } : {};
            const outcomes = await Promise.allSettled(selected.map(e => evaluationsApi.triggerRun(e.id, runNameArg)));
            const failures = selected.flatMap((e, i) => {
                const outcome = outcomes[i];
                return outcome.status === "rejected" ? [{ name: e.name, message: getErrorMessage(outcome.reason) }] : [];
            });
            if (failures.length < selected.length) onRunComplete?.();
            if (failures.length === 0) {
                onOpenChange(false);
                return;
            }
            setSelectedNames(failures.map(f => f.name));
            setError(failures.map(f => `${f.name}: ${f.message}`).join(" · "));
        } finally {
            setRunning(false);
        }
    };

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="flex max-w-lg! flex-col gap-0 p-0 outline-none">
                <DialogHeader className="border-b px-6 py-4">
                    <DialogTitle>Run Experiment</DialogTitle>
                </DialogHeader>

                <div className="flex flex-col gap-4 px-6 py-5">
                    {error && <MessageBanner variant="error" message={error} dismissible onDismiss={() => setError(null)} />}

                    <div className="flex flex-col gap-1.5">
                        <label className="text-xs font-medium text-(--secondary-text-wMain)">Experiments</label>
                        {loading ? (
                            <EmptyState variant="loading" title="Loading experiments..." />
                        ) : (
                            <MultiSelect options={experiments.map(e => e.name)} value={selectedNames} onChange={setSelectedNames} placeholder="Select experiments..." disabled={experiments.length === 0} />
                        )}
                    </div>

                    {isSingleSelection && (
                        <div className="flex flex-col gap-1.5">
                            <label htmlFor="run-experiments-report-name" className="text-xs font-medium text-(--secondary-text-wMain)">
                                Report Name <span className="font-normal">(optional)</span>
                            </label>
                            <Input
                                id="run-experiments-report-name"
                                value={reportName}
                                onChange={e => setReportName(e.target.value)}
                                placeholder="Enter a name for this report"
                                onKeyDown={e => {
                                    if (e.key === "Enter") void handleRun();
                                }}
                            />
                        </div>
                    )}
                </div>

                <div className="flex items-center justify-end gap-3 border-t px-6 py-4">
                    <Button variant="outline" onClick={() => onOpenChange(false)} disabled={running}>
                        Cancel
                    </Button>
                    <Button onClick={() => void handleRun()} disabled={running || selectedNames.length === 0}>
                        {running ? "Running..." : "Run"}
                    </Button>
                </div>
            </DialogContent>
        </Dialog>
    );
}
