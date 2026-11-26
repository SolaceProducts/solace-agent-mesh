// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Copy } from "lucide-react";

import { cn } from "@/utils/cnTailwind";

import { Button } from "@/ui/button";

import { useClipboardCopy } from "./hooks";

interface TeamsWebhookUrlSectionProps {
    webhookUrl: string;
    onCopy?: () => void;
    className?: string;
}

export function TeamsWebhookUrlSection({ webhookUrl, onCopy, className = "" }: TeamsWebhookUrlSectionProps) {
    const { handleCopy: copyToClipboard, copied } = useClipboardCopy(onCopy);

    return (
        <div className={cn("flex items-center gap-2 rounded border border-(--secondary-w20) px-3 py-2.5", className)}>
            <span className="flex-1 overflow-x-auto text-sm text-(--primary-text-wMain)">{webhookUrl}</span>
            <Button variant="ghost" size="sm" onClick={() => copyToClipboard(webhookUrl)} title={copied ? "Copied!" : "Copy URL"} className="h-6 w-6 shrink-0 p-0">
                <Copy className="size-4 text-(--secondary-text-w50)" />
            </Button>
        </div>
    );
}
