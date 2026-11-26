// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { Network } from "lucide-react";

import { Button } from "@/ui/button";

interface ViewWorkflowButtonProps {
    onClick: () => void;
    className?: string;
}

export const ViewWorkflowButton: React.FC<ViewWorkflowButtonProps> = ({ onClick, className }) => {
    return (
        <Button data-testid="viewActivity" variant="ghost" size="sm" onClick={onClick} tooltip="View activity details" className={className}>
            <Network className="h-4 w-4" />
        </Button>
    );
};
