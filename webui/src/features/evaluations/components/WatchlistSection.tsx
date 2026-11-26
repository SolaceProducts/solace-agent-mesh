// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo, useState } from "react";
import { BarChart3, Pencil } from "lucide-react";

import { useEvalWatchlistChartData } from "@/api/evaluations";

import { Button } from "@/ui/button";

import { HelpPopover, MessageBanner } from "@/common";

import { EditWatchlistDialog } from "./EditWatchlistDialog";
import { WatchlistAgentChart } from "./WatchlistAgentChart";

export function WatchlistSection() {
    const [editOpen, setEditOpen] = useState(false);
    const chartQuery = useEvalWatchlistChartData();

    const charts = useMemo(() => (chartQuery.data ?? []).slice().sort((a, b) => a.position - b.position), [chartQuery.data]);

    const [first, ...rest] = charts;

    const renderBody = () => {
        if (chartQuery.isPending) return null;
        if (chartQuery.isError) {
            return <MessageBanner variant="error" message="Could not load your Watchlist. Try again." />;
        }
        if (charts.length === 0) {
            return (
                <div className="flex items-center justify-center gap-3 rounded-xs bg-(--background-w20) px-4 py-10 text-sm text-(--secondary-text-wMain)">
                    <BarChart3 className="size-6" />
                    <span>Add agents to the Watchlist and run experiments to see experiment performance over time</span>
                </div>
            );
        }
        return (
            <div className="flex flex-col gap-6">
                {first && <WatchlistAgentChart agent={first} fullWidth />}
                {rest.length > 0 && (
                    <div className="grid grid-cols-2 gap-6">
                        {rest.map(agent => (
                            <WatchlistAgentChart key={agent.agentName} agent={agent} />
                        ))}
                    </div>
                )}
            </div>
        );
    };

    return (
        <section className="flex flex-col gap-2">
            <div className="flex items-center justify-between">
                <div className="flex items-center gap-2">
                    <h3 className="text-base font-bold">Watchlist</h3>
                    <HelpPopover
                        content={
                            <div className="flex flex-col gap-4">
                                <div>
                                    <p className="font-semibold">What is the Agent Watchlist?</p>
                                    <p>Select agents to monitor their performance over time. Each graph shows the agent&apos;s 3 most-used experiments and the last 10 reports.</p>
                                </div>
                                <div>
                                    <p className="font-semibold">What if there are multiple models or evaluators in an experiment?</p>
                                    <p>Metrics reflect the model version active at the time each report was run. The evaluator shown is the primary evaluator set when the experiment was configured.</p>
                                </div>
                            </div>
                        }
                    />
                </div>
                <Button variant="ghost" onClick={() => setEditOpen(true)}>
                    <Pencil className="size-4" />
                    Edit Watchlist
                </Button>
            </div>

            {renderBody()}

            <EditWatchlistDialog open={editOpen} onOpenChange={setEditOpen} />
        </section>
    );
}
