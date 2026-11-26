// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Tooltip, TooltipContent, TooltipTrigger } from "@/ui/tooltip";
import type { CollaborativeUser } from "@/types/collaboration";

import { UserAvatar } from "./UserAvatar";

interface UserPresenceAvatarsProps {
    readonly users: CollaborativeUser[];
    readonly currentUserId?: string;
}

export function UserPresenceAvatars({ users, currentUserId }: UserPresenceAvatarsProps) {
    if (!users || users.length === 0) {
        return null;
    }

    const displayUsers = currentUserId ? users.filter(u => u.id !== currentUserId) : users;

    if (displayUsers.length === 0) {
        return null;
    }

    const activeUsers = displayUsers.filter(u => u.isOnline);

    const maxVisible = 5;
    const visibleUsers = activeUsers.slice(0, maxVisible);
    const overflowUsers = activeUsers.slice(maxVisible);
    const hasOverflow = overflowUsers.length > 0;

    return (
        <div className="flex items-center">
            {visibleUsers.map((user, index) => {
                const emailHash = (user.email || user.name || "")
                    .toLowerCase()
                    .split("")
                    .reduce((acc: number, char: string) => acc + char.charCodeAt(0), 0);
                return (
                    <div key={user.id} style={{ marginLeft: index > 0 ? "-8px" : "0" }}>
                        <UserAvatar name={user.name} userIndex={emailHash} avatarUrl={user.avatar} className="cursor-pointer" showTooltip={true} />
                    </div>
                );
            })}

            {hasOverflow && (
                <Tooltip>
                    <TooltipTrigger asChild>
                        <div className="flex h-8 w-8 cursor-pointer items-center justify-center rounded-full bg-(--secondary-w20) text-xs font-semibold text-(--secondary-text-wMain) ring-2 ring-(--background-w10)" style={{ marginLeft: "-8px" }}>
                            +{overflowUsers.length}
                        </div>
                    </TooltipTrigger>
                    <TooltipContent className="flex flex-col gap-2 p-3">
                        {overflowUsers.map(user => {
                            const overflowHash = (user.email || user.name || "")
                                .toLowerCase()
                                .split("")
                                .reduce((acc: number, char: string) => acc + char.charCodeAt(0), 0);
                            return (
                                <div key={user.id} className="flex items-center gap-2">
                                    <UserAvatar name={user.name} userIndex={overflowHash} avatarUrl={user.avatar} />
                                    <span className="text-sm">{user.name}</span>
                                </div>
                            );
                        })}
                    </TooltipContent>
                </Tooltip>
            )}
        </div>
    );
}
