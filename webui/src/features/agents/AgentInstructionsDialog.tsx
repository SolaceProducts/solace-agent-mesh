// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Button } from "@/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/ui/dialog";

import type { Agent } from "@/types";

interface AgentInstructionsDialogProps {
    agent: Agent;
    isOpen: boolean;
    setIsOpen: (isOpen: boolean) => void;
}

export const AgentInstructionsDialog: React.FC<AgentInstructionsDialogProps> = ({ agent, isOpen, setIsOpen }) => {
    return (
        <Dialog open={isOpen} onOpenChange={() => setIsOpen(false)}>
            <DialogContent className="sm:max-w-3xl">
                <DialogHeader>
                    <DialogTitle className="flex max-w-[400px] flex-row gap-1">Instructions</DialogTitle>
                </DialogHeader>
                <div className="min-w-0 space-y-4 pt-4">
                    <div className="max-h-[60vh] overflow-y-auto break-words whitespace-pre-wrap">{agent.systemPrompt}</div>
                    <div className="flex justify-end gap-2">
                        <Button type="button" onClick={() => setIsOpen(false)}>
                            Close
                        </Button>
                    </div>
                </div>
            </DialogContent>
        </Dialog>
    );
};
