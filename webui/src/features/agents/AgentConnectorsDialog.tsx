// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useMemo, useState } from "react";
import { Check, Plug, Plus } from "lucide-react";

import { Button } from "@/ui/button";
import { Dialog, DialogClose, DialogContent, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/ui/dialog";
import { SearchInput } from "@/ui/search-input";

import type { Connector } from "@/types";

import { AgentConnectorCard } from "./AgentAbilityCard";
import { useConnectorRuntimeToolNames } from "./hooks";

interface AgentConnectorsDialogProps {
    connectors: Connector[];
    selectedConnectors: string[];
    onToggleConnector: (connectorId: string) => void;
    triggerButton?: React.ReactNode;
}

export const AgentConnectorsDialog: React.FC<AgentConnectorsDialogProps> = ({ connectors, selectedConnectors, onToggleConnector, triggerButton }) => {
    const [searchTerm, setSearchTerm] = useState("");
    const runtimeToolNamesByConnector = useConnectorRuntimeToolNames();

    const filteredConnectors = useMemo(() => {
        if (!searchTerm.trim()) return connectors;

        const searchLower = searchTerm.toLowerCase();
        return connectors.filter(connector => connector.name.toLowerCase().includes(searchLower) || connector.description.toLowerCase().includes(searchLower));
    }, [connectors, searchTerm]);

    return (
        <Dialog>
            <DialogTrigger asChild>
                {triggerButton ?? (
                    <Button variant="ghost" title="Add Connector" testid="addConnector">
                        <Plus />
                        Add Connector
                    </Button>
                )}
            </DialogTrigger>

            <DialogContent className="flex h-[80vh] flex-col sm:max-w-3xl">
                <DialogHeader>
                    <DialogTitle>Add Connectors</DialogTitle>
                </DialogHeader>

                <div className="pt-6 pb-4">
                    <SearchInput className="w-xs" value={searchTerm} onChange={setSearchTerm} />
                </div>

                <div className="min-h-0 flex-1 overflow-y-auto">
                    {filteredConnectors.length > 0 &&
                        filteredConnectors.map(connector => {
                            const isSelected = selectedConnectors.includes(connector.id || "");
                            return (
                                <div key={connector.id} data-testid={`agent-connector-option-${connector.id}`} className="mb-2 flex items-start gap-2 rounded-sm border p-4">
                                    <div
                                        role="checkbox"
                                        aria-checked={isSelected}
                                        tabIndex={0}
                                        onClick={() => onToggleConnector(connector.id || "")}
                                        onKeyDown={e => {
                                            if (e.key === "Enter" || e.key === " ") {
                                                e.preventDefault();
                                                onToggleConnector(connector.id || "");
                                            }
                                        }}
                                        className="flex h-6 w-6 flex-shrink-0 cursor-pointer items-center justify-center rounded-sm border hover:border-(--primary-wMain) focus:outline-none"
                                    >
                                        {isSelected && <Check className="h-5 w-5 text-(--primary-wMain)" strokeWidth={2} />}
                                    </div>

                                    <div className="min-w-0 flex-1">
                                        <AgentConnectorCard connector={connector} className="border-none p-0" showTools={true} iconSize="sm" runtimeToolNames={connector.id ? runtimeToolNamesByConnector.get(connector.id) : undefined} />
                                    </div>
                                </div>
                            );
                        })}
                    {filteredConnectors.length === 0 && (
                        <div className="flex h-full flex-col items-center justify-center gap-4 py-8 text-sm text-(--secondary-text-wMain)">
                            <Plug className="mx-auto h-12 w-12" />
                            {searchTerm ? (
                                <>
                                    <div className="text-center">
                                        <div className="font-semibold">No connectors found</div>
                                        <div className="text-(--secondary-text-wMain)">Try adjusting your search terms</div>
                                    </div>
                                    <Button variant="default" onClick={() => setSearchTerm("")}>
                                        Clear Filter
                                    </Button>
                                </>
                            ) : (
                                <div>No connectors available</div>
                            )}
                        </div>
                    )}
                </div>
                <DialogFooter>
                    <DialogClose asChild>
                        <Button variant="outline" testid="closeAvailableConnectorsDialog" title="Close">
                            Close
                        </Button>
                    </DialogClose>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
};
