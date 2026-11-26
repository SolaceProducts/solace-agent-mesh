// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useNavigate } from "react-router-dom";

import { Button } from "@/ui";
import { cn } from "@/utils";

interface RecentChatsHeaderProps {
    to: string;
    viewAllDisabled?: boolean;
    className?: string;
}

export const RecentChatsHeader = ({ to, viewAllDisabled = false, className }: Readonly<RecentChatsHeaderProps>) => {
    const navigate = useNavigate();
    return (
        <div className={cn("mb-2 flex items-center justify-between", className)}>
            <span className="text-sm font-bold text-(--darkSurface-textMuted)">Recent Chats</span>
            <Button variant="ghost" size="sm" disabled={viewAllDisabled} onClick={() => navigate(to)} className="px-2 text-sm font-bold text-(--darkSurface-buttonText) enabled:hover:bg-transparent enabled:hover:text-(--darkSurface-buttonTextHover)">
                View All
            </Button>
        </div>
    );
};
