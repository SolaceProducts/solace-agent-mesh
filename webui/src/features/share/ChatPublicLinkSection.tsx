// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Check, Copy, ExternalLink, Link2, MoreVertical, RefreshCw, Trash2 } from "lucide-react";

import { useUpdateShareSnapshot } from "@/api/share";

import { formatTimestamp } from "@/utils/format";

import { Button } from "@/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/ui/dropdown-menu";
import { Label } from "@/ui/label";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/ui/tooltip";
import type { ShareLink } from "@/types/share";

interface ChatPublicLinkSectionProps {
    shareLink: ShareLink;
    publicLinkCopied: boolean;
    onCopy: () => void | Promise<void>;
    onDelete: () => void | Promise<void>;
    onError?: (error: { title: string; message: string }) => void;
    onSuccess?: (message: string) => void;
}

export function ChatPublicLinkSection({ shareLink, publicLinkCopied, onCopy, onDelete, onError, onSuccess }: Readonly<ChatPublicLinkSectionProps>) {
    const updateSnapshot = useUpdateShareSnapshot();

    const handleRefreshSnapshot = () => {
        updateSnapshot
            .mutateAsync({ shareId: shareLink.id })
            .then(() => onSuccess?.("Snapshot updated"))
            .catch(err => onError?.({ title: "Failed to Update Snapshot", message: err instanceof Error ? err.message : "Unknown error" }));
    };

    return (
        <div data-testid="public-link-section" className="mt-4 rounded bg-(--background-w20) p-4">
            <div className="mb-4 flex items-start justify-between">
                <div>
                    <div className="flex items-center gap-2">
                        <Link2 className="h-4 w-4" />
                        <Label className="text-sm font-bold">Sharing Link</Label>
                    </div>
                    <p className="mt-1 text-sm text-(--secondary-text-wMain)">Anyone in your organization with this link can view the chat in a read-only mode.</p>
                </div>
                <DropdownMenu>
                    <DropdownMenuTrigger asChild>
                        <Button variant="ghost" size="icon" className="h-8 w-8 shrink-0" aria-label="Link actions">
                            <MoreVertical className="h-4 w-4" />
                        </Button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align="end">
                        <DropdownMenuItem onClick={() => window.open(shareLink.shareUrl, "_blank")}>
                            <ExternalLink className="mr-2 h-4 w-4" />
                            Preview Chat
                        </DropdownMenuItem>
                        <DropdownMenuItem onClick={() => onDelete()}>
                            <Trash2 className="mr-2 h-4 w-4" />
                            Remove Public Link
                        </DropdownMenuItem>
                    </DropdownMenuContent>
                </DropdownMenu>
            </div>

            <div className="flex items-center gap-4">
                <div className="relative min-w-0 flex-1">
                    <div className="truncate rounded border bg-(--background-w10) px-3 py-2 pr-10 font-mono text-xs">{shareLink.shareUrl}</div>
                    <Button variant="ghost" size="icon" className="absolute top-1/2 right-1 h-6 w-6 -translate-y-1/2" onClick={() => onCopy()} aria-label="Copy share link">
                        {publicLinkCopied ? <Check className="h-4 w-4 text-(--success-wMain)" /> : <Copy className="h-4 w-4" />}
                    </Button>
                </div>
                <div className="flex shrink-0 flex-col items-start px-4">
                    <span className="text-xs text-(--secondary-text-wMain)">Snapshot</span>
                    <div className="flex items-center gap-1">
                        <span className="text-sm whitespace-nowrap">{formatTimestamp(shareLink.snapshotTime)}</span>
                        <Tooltip>
                            <TooltipTrigger asChild>
                                <Button variant="ghost" size="icon" className="h-5 w-5" aria-label="Refresh public link snapshot" onClick={handleRefreshSnapshot}>
                                    <RefreshCw className="h-3.5 w-3.5" />
                                </Button>
                            </TooltipTrigger>
                            <TooltipContent>
                                <p>Update Snapshot</p>
                            </TooltipContent>
                        </Tooltip>
                    </div>
                </div>
            </div>
        </div>
    );
}
