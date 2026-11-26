// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { XCircle } from "lucide-react";
import { useNavigate } from "react-router-dom";

import { Button } from "@/ui/button";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/ui/dialog";
import type { EvalTargetType } from "@/types/eval";

export type AgentNotDeployedDialogFlow = "create" | "run";

interface AgentNotDeployedDialogProps {
    readonly open: boolean;
    readonly onOpenChange: (open: boolean) => void;
    readonly agentName: string;
    readonly agentId?: string;
    readonly flow: AgentNotDeployedDialogFlow;
    readonly targetType?: EvalTargetType;
}

export function AgentNotDeployedDialog({ open, onOpenChange, agentName, agentId, flow, targetType = "agent" }: AgentNotDeployedDialogProps) {
    const navigate = useNavigate();
    const isWorkflow = targetType === "workflow";
    const kind = isWorkflow ? "workflow" : "agent";

    const handleGoToTarget = () => {
        onOpenChange(false);
        if (isWorkflow) {
            navigate("/workflows");
            return;
        }
        const params = new URLSearchParams();
        if (agentId) params.set("id", agentId);
        params.set("tab", "undeployed");
        navigate(`/agents?${params.toString()}`);
    };

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="max-w-md">
                <DialogHeader>
                    <DialogTitle className="flex items-center gap-2">
                        <XCircle className="size-5 text-(--error-wMain)" />
                        Unable to Run Experiment
                    </DialogTitle>
                </DialogHeader>
                <div className="flex flex-col gap-3 py-2">
                    <p className="text-sm text-(--primary-text-wMain)">
                        {flow === "create" ? (
                            <>
                                Your experiment has been saved, but it cannot be run because the {kind} <strong>{agentName}</strong> is not deployed.
                            </>
                        ) : (
                            <>
                                This experiment cannot be run because the {kind} <strong>{agentName}</strong> is not deployed.
                            </>
                        )}
                    </p>
                    <p className="text-sm text-(--secondary-text-wMain)">Deploy the {kind}, then run the experiment from the Experiment Lab.</p>
                </div>
                <DialogFooter>
                    <Button variant="outline" onClick={() => onOpenChange(false)}>
                        Close
                    </Button>
                    <Button onClick={handleGoToTarget}>{isWorkflow ? "Go to Workflows" : "Go to Agent"}</Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
