// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Bot } from "lucide-react";

import { formatTimestamp } from "@/utils/format";
import { formatCollaborativeTimestamp } from "@/utils/userFormatting";

import { Tooltip, TooltipContent, TooltipTrigger } from "@/ui/tooltip";

import { UserAvatar } from "./UserAvatar";

interface MessageAttributionProps {
    readonly type: "user" | "agent";
    readonly name: string;
    readonly userIndex?: number;
    readonly timestamp?: number;
    readonly avatarUrl?: string;
}

export function MessageAttribution({ type, name, userIndex = 0, timestamp, avatarUrl }: MessageAttributionProps) {
    return (
        <div className="flex items-center gap-2">
            {type === "agent" ? (
                <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-(--secondary-w10)">
                    <Bot className="h-4 w-4 text-(--brand-wMain)" />
                </div>
            ) : (
                <UserAvatar name={name} userIndex={userIndex} avatarUrl={avatarUrl} />
            )}

            <div className="flex items-baseline gap-2">
                <span className="text-sm font-bold">{name}</span>
                {timestamp !== undefined && (
                    <Tooltip>
                        <TooltipTrigger asChild>
                            <span className="cursor-default text-sm text-(--secondary-text-wMain)">{formatCollaborativeTimestamp(timestamp)}</span>
                        </TooltipTrigger>
                        <TooltipContent>{formatTimestamp(new Date(timestamp).toISOString())}</TooltipContent>
                    </Tooltip>
                )}
            </div>
        </div>
    );
}
