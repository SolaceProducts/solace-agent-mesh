// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useState } from "react";
import { AlertTriangle } from "lucide-react";

import { Input } from "@/ui/input";

import { ConfirmationDialog } from "@/common";

interface RunExperimentDialogProps {
    readonly open: boolean;
    readonly experimentName: string;
    readonly onOpenChange: (open: boolean) => void;
    readonly onConfirm: (runName: string) => void | Promise<void>;
    readonly isLoading?: boolean;
    readonly datasetChanged?: boolean;
}

export function RunExperimentDialog({ open, experimentName, onOpenChange, onConfirm, isLoading, datasetChanged }: RunExperimentDialogProps) {
    const [runName, setRunName] = useState("");

    useEffect(() => {
        if (!open) setRunName("");
    }, [open]);

    const handleConfirm = () => onConfirm(runName.trim());

    return (
        <ConfirmationDialog
            title="Run Experiment"
            actionLabels={{ confirm: "Run Experiment" }}
            content={
                <div className="flex flex-col gap-4">
                    <p className="text-sm text-(--secondary-text-wMain)">
                        This will run <strong>{experimentName}</strong>. Provide a brief name for this run to help identify it later. If no name is provided, the run will use the experiment name.
                    </p>
                    {datasetChanged && (
                        <p data-testid="datasetChangedWarning" className="inline-flex items-start gap-1.5 text-sm text-(--secondary-text-wMain)">
                            <AlertTriangle className="mt-0.5 size-4 shrink-0 text-(--warning-w100)" />
                            The dataset has changed since the last run, so new results may not be comparable to earlier ones.
                        </p>
                    )}
                    <div className="flex flex-col gap-1.5">
                        <label htmlFor="run-name-input" className="text-xs font-medium text-(--secondary-text-wMain)">
                            Run Name <span className="font-normal">(optional)</span>
                        </label>
                        <Input
                            id="run-name-input"
                            autoFocus
                            value={runName}
                            onChange={e => setRunName(e.target.value)}
                            onKeyDown={e => {
                                if (e.key === "Enter") void handleConfirm();
                            }}
                        />
                    </div>
                </div>
            }
            open={open}
            onOpenChange={onOpenChange}
            onCancel={() => onOpenChange(false)}
            onConfirm={handleConfirm}
            isLoading={isLoading}
        />
    );
}
