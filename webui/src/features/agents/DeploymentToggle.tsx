// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { cn } from "@/utils/cnTailwind";

import { Button } from "@/ui/button";

import type { DeploymentFilter } from "./utils/filters";

const SELECTED_CLASSES = "bg-(--primary-w20)";
const HOVER_CLASSES = "hover:bg-(--secondary-w20)";

interface DeploymentToggleProps {
    selected: DeploymentFilter;
    deployedCount: number | undefined;
    undeployedCount: number | undefined;
    disabled?: boolean;
    onChange: (next: DeploymentFilter) => void;
}

const renderLabel = (label: string, count: number | undefined) => (count === undefined ? label : `${label} (${count})`);

export const DeploymentToggle: React.FC<DeploymentToggleProps> = ({ selected, deployedCount, undeployedCount, disabled, onChange }) => {
    return (
        <div className="flex items-center overflow-hidden rounded-lg border">
            <Button variant="ghost" disabled={disabled} className={cn("rounded-none border-r px-4 py-2", HOVER_CLASSES, selected === "deployed" && cn(SELECTED_CLASSES, "rounded-l-lg"))} onClick={() => onChange("deployed")}>
                <span>{renderLabel("Deployed", deployedCount)}</span>
            </Button>

            <Button variant="ghost" disabled={disabled} className={cn("rounded-none px-4 py-2", HOVER_CLASSES, selected === "undeployed" && cn(SELECTED_CLASSES, "rounded-r-lg"))} onClick={() => onChange("undeployed")}>
                <span>{renderLabel("Undeployed", undeployedCount)}</span>
            </Button>
        </div>
    );
};
