// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import * as React from "react";
import { useState } from "react";

import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/ui/dialog";
import { Separator } from "@/ui/separator";
import { type AgentPerformanceMetrics, type PerformanceReport } from "@/types/activities";

import { getAgentColumns } from "./AgentColumns";
import { AgentDataTable } from "./AgentDataTable";
import { GanttChart } from "./GanttChart";

interface PerformanceModalProps {
    isOpen: boolean;
    onOpenChange: (isOpen: boolean) => void;
    report: PerformanceReport | null;
    isTaskComplete: boolean;
}

export const PerformanceModal: React.FC<PerformanceModalProps> = ({ isOpen, onOpenChange, report, isTaskComplete }) => {
    const [selectedAgent, setSelectedAgent] = useState<AgentPerformanceMetrics | null>(null);

    React.useEffect(() => {
        if (isOpen) {
            setSelectedAgent(null);
        }
    }, [isOpen, report]);

    const handleAgentSelect = (agent: AgentPerformanceMetrics) => {
        setSelectedAgent(agent);
    };

    const agentColumns = React.useMemo(() => getAgentColumns(), []);

    const renderContent = () => {
        if (!isTaskComplete) {
            return (
                <div className="flex flex-grow items-center justify-center">
                    <p className="text-center text-(--secondary-text-wMain)">Performance data will populate once the task is complete.</p>
                </div>
            );
        }

        if (!report) {
            return (
                <div className="flex flex-grow items-center justify-center">
                    <p className="text-center text-(--secondary-text-wMain)">No performance data available for this task.</p>
                </div>
            );
        }

        return (
            <div className="flex-grow overflow-y-auto pr-6">
                <div>
                    <h3 className="my-2 text-base font-semibold">Overall Summary</h3>
                    <div className="flex items-center justify-between">
                        <p>Total Task Duration: {(report.overall.totalTaskDurationMs / 1000).toFixed(2)}s</p>
                    </div>
                </div>

                <Separator className="my-4" />

                <div className="mb-4">
                    <h3 className="mb-2 text-base font-semibold">Agent Breakdown</h3>
                    <AgentDataTable columns={agentColumns} data={Object.values(report.agents)} onRowClick={handleAgentSelect} selectedRowId={selectedAgent?.instanceId} />
                </div>

                <Separator className="my-4" />

                <div>
                    <h3 className="mb-2 text-base font-semibold">Detailed Agent View</h3>
                    {selectedAgent ? (
                        <div>
                            <h4 className="mb-2 text-sm font-medium">Agent Timeline</h4>
                            <GanttChart agent={selectedAgent} />
                        </div>
                    ) : (
                        <div className="py-8 text-center text-(--secondary-text-wMain)">
                            <p>Select an agent from the table above to see details.</p>
                        </div>
                    )}
                </div>
            </div>
        );
    };

    return (
        <Dialog open={isOpen} onOpenChange={onOpenChange}>
            <DialogContent className="flex h-[90vh] flex-col sm:max-w-5xl" showCloseButton>
                <DialogHeader>
                    <DialogTitle className="pb-4">Performance Insights</DialogTitle>
                </DialogHeader>
                {renderContent()}
            </DialogContent>
        </Dialog>
    );
};
