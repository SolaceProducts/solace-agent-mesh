// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { ChevronLeft } from "lucide-react";

import { Button } from "@/ui/button";

interface ActivityPanelHeaderProps {
    title: string;
    onBack: () => void;
    backTooltip?: string;
}

export function ActivityPanelHeader({ title, onBack, backTooltip = "Back" }: Readonly<ActivityPanelHeaderProps>) {
    return (
        <div className="flex shrink-0 items-center gap-1 border-b bg-(--background-w10) px-2 py-2">
            <Button variant="ghost" size="sm" className="h-7 gap-1 px-2" onClick={onBack} tooltip={backTooltip}>
                <ChevronLeft className="h-4 w-4" />
                Back
            </Button>
            <span className="text-sm font-medium">{title}</span>
        </div>
    );
}
