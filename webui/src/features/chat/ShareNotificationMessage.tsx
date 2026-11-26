// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { ArrowRight, Eye, Pencil } from "lucide-react";

import { formatCollaborativeTimestamp } from "@/utils/userFormatting";

import { Tooltip, TooltipContent, TooltipTrigger } from "@/ui/tooltip";

const SHARE_MESSAGES = {
    createdLink: () => `You created a view-only sharing link`,
} as const;

export type ShareNotificationMessageProps =
    | {
          variant: "shared-with-users";
          sharedBy?: string;
          sharedWith: string[];
          accessLevel: "viewer" | "editor";
          timestamp: number;
      }
    | {
          variant: "created-link";
          sharedBy: string;
          timestamp: number;
      }
    | {
          variant: "role-changed";
          sharedBy?: string;
          sharedWith: string[];
          fromAccessLevel: "viewer" | "editor";
          toAccessLevel: "viewer" | "editor";
          timestamp: number;
      };

function formatRecipients(recipients: string[]): string {
    if (recipients.length === 1) {
        return recipients[0];
    }
    if (recipients.length === 2) {
        return `${recipients[0]} and ${recipients[1]}`;
    }
    return `${recipients.slice(0, -1).join(", ")}, and ${recipients.at(-1)}`;
}

export function ShareNotificationMessage(props: ShareNotificationMessageProps) {
    const { timestamp } = props;

    if (props.variant === "shared-with-users") {
        const { sharedBy = "You", sharedWith, accessLevel } = props;
        const AccessIcon = accessLevel === "viewer" ? Eye : Pencil;
        const accessLevelText = accessLevel === "viewer" ? "Viewer" : "Editor";

        const maxVisible = 3;
        const visibleRecipients = sharedWith.slice(0, maxVisible);
        const hiddenRecipients = sharedWith.slice(maxVisible);
        const hasHidden = hiddenRecipients.length > 0;

        const visibleRecipientsText = formatRecipients(visibleRecipients);

        return (
            <div className="flex flex-col items-center gap-1 py-4">
                <p className="text-sm text-(--secondary-text-wMain)">{formatCollaborativeTimestamp(timestamp)}</p>
                <p className="flex items-center gap-1.5 text-sm text-(--primary-text-wMain)">
                    <span>{sharedBy} gave</span>
                    <span className="inline-flex items-center gap-1">
                        <AccessIcon className="h-3.5 w-3.5" />
                        <span className="font-bold">{accessLevelText}</span>
                    </span>
                    <span>
                        access to {visibleRecipientsText}
                        {hasHidden && (
                            <>
                                {" and "}
                                <Tooltip>
                                    <TooltipTrigger asChild>
                                        <span
                                            className="cursor-pointer text-(--secondary-text-wMain) hover:bg-(--secondary-w20)"
                                            style={{
                                                borderBottom: "1px dashed var(--secondary-wMain)",
                                                paddingBottom: "2px",
                                            }}
                                        >
                                            {hiddenRecipients.length} other {hiddenRecipients.length === 1 ? "user" : "users"}
                                        </span>
                                    </TooltipTrigger>
                                    <TooltipContent>
                                        <div className="flex flex-col gap-1">
                                            {hiddenRecipients.map(name => (
                                                <span key={name}>{name}</span>
                                            ))}
                                        </div>
                                    </TooltipContent>
                                </Tooltip>
                            </>
                        )}
                    </span>
                </p>
            </div>
        );
    } else if (props.variant === "role-changed") {
        const { sharedBy = "You", sharedWith, fromAccessLevel, toAccessLevel } = props;
        const FromIcon = fromAccessLevel === "viewer" ? Eye : Pencil;
        const ToIcon = toAccessLevel === "viewer" ? Eye : Pencil;
        const fromText = fromAccessLevel === "viewer" ? "Viewer" : "Editor";
        const toText = toAccessLevel === "viewer" ? "Viewer" : "Editor";

        const recipientsText = formatRecipients(sharedWith);

        return (
            <div className="flex flex-col items-center gap-1 py-4">
                <p className="text-sm text-(--secondary-text-wMain)">{formatCollaborativeTimestamp(timestamp)}</p>
                <p className="flex flex-wrap items-center justify-center gap-1.5 text-sm text-(--primary-text-wMain)">
                    <span>
                        {sharedBy} changed {recipientsText}&apos;s access from
                    </span>
                    <span className="inline-flex items-center gap-1">
                        <FromIcon className="h-3.5 w-3.5" />
                        <span className="font-bold">{fromText}</span>
                    </span>
                    <ArrowRight className="h-3.5 w-3.5" />
                    <span className="inline-flex items-center gap-1">
                        <ToIcon className="h-3.5 w-3.5" />
                        <span className="font-bold">{toText}</span>
                    </span>
                </p>
            </div>
        );
    } else {
        const messageText = SHARE_MESSAGES.createdLink();

        return (
            <div className="flex flex-col items-center gap-1 py-4">
                <p className="text-sm text-(--secondary-text-wMain)">{formatCollaborativeTimestamp(timestamp)}</p>
                <p className="flex items-center gap-1.5 text-sm text-(--primary-text-wMain)">
                    <span>{messageText}</span>
                </p>
            </div>
        );
    }
}

export const ShareNotification = ShareNotificationMessage;
