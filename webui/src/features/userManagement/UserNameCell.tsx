// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { UserRound } from "lucide-react";
import type { ReactNode } from "react";

import { Avatar, AvatarFallback } from "@/ui/avatar";

interface UserNameCellProps {
    displayName?: string;
    secondaryLine?: string;
    badge?: ReactNode;
}

export function UserNameCell({ displayName, secondaryLine, badge }: Readonly<UserNameCellProps>) {
    return (
        <div className="flex items-center gap-3">
            <Avatar>
                <AvatarFallback>
                    <UserRound className="size-4 text-(--secondary-text-wMain)" />
                </AvatarFallback>
            </Avatar>
            <div className="flex min-w-0 flex-col">
                <div className="flex h-6 min-w-0 items-center gap-2">
                    <span className="truncate font-medium">{displayName || "—"}</span>
                    {badge}
                </div>
                {secondaryLine && <span className="truncate text-sm text-(--secondary-text-wMain)">{secondaryLine}</span>}
            </div>
        </div>
    );
}
