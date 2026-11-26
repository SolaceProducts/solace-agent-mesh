// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { Star } from "lucide-react";

import { Button } from "@/ui";

interface PinButtonProps {
    isPinned: boolean;
    onClick: (e: React.MouseEvent) => void;
    disabled?: boolean;
}

export const PinButton: React.FC<PinButtonProps> = ({ isPinned, onClick, disabled }) => {
    return (
        <Button variant="ghost" size="icon" disabled={disabled} onClick={onClick} className={isPinned ? "text-(--primary-wMain)" : "text-(--secondary-text-wMain)"} tooltip={isPinned ? "Remove from favorites" : "Add to favorites"}>
            <Star size={16} fill={isPinned ? "currentColor" : "none"} />
        </Button>
    );
};
