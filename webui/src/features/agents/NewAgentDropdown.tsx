// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { ChevronDown, Plus, Route } from "lucide-react";

import { Button } from "@/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/ui/dropdown-menu";

interface NewAgentDropdownProps {
    platformConfigured: boolean;
    onCreateNew?: () => void;
    onConnectExternal?: () => void;
}

export const NewAgentDropdown: React.FC<NewAgentDropdownProps> = ({ platformConfigured, onCreateNew, onConnectExternal }) => {
    return (
        <DropdownMenu>
            <DropdownMenuTrigger asChild>
                <Button variant="outline" disabled={!platformConfigured} title={platformConfigured ? "Add Agent" : "Platform Service must be configured to create agents"} data-testid="addAgentDropdownTrigger">
                    <Plus />
                    Add Agent
                    <span className="ml-auto flex items-center pl-2">
                        <ChevronDown />
                    </span>
                </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
                {onCreateNew && (
                    <DropdownMenuItem onClick={onCreateNew}>
                        <Plus className="mr-2 size-4" />
                        <div className="flex flex-col">
                            <div className="font-medium">Create New Agent</div>
                            <div className="text-xs text-(--secondary-text-wMain)">Design and build an agent with AI assistance</div>
                        </div>
                    </DropdownMenuItem>
                )}
                {onConnectExternal && (
                    <DropdownMenuItem onClick={onConnectExternal} data-testid="addAgentDropdownConnectExternal">
                        <Route className="mr-2 size-4" />
                        <div className="flex flex-col">
                            <div className="font-medium">Connect External Agent</div>
                            <div className="text-xs text-(--secondary-text-wMain)">Integrate an existing A2A compatible framework</div>
                        </div>
                    </DropdownMenuItem>
                )}
            </DropdownMenuContent>
        </DropdownMenu>
    );
};
