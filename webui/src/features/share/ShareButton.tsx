// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Share2 } from "lucide-react";

import { Button } from "@/ui/button";

interface ShareButtonProps {
    sessionId: string;
    sessionTitle: string;
    sessionUpdatedTime?: string;
    className?: string;
    onClick?: () => void;
}

export function ShareButton({ className, onClick }: Readonly<ShareButtonProps>) {
    return (
        <Button variant="ghost" size="sm" onClick={onClick} className={className} title="Share this session">
            <Share2 className="h-4 w-4" />
            <span className="ml-2 hidden sm:inline">Share</span>
        </Button>
    );
}
