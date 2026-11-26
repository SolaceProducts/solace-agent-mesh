// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { PanelLeftIcon } from "lucide-react";

import { Button } from "@/ui";

export const RecentChatsToggleButton = ({ onToggle }: Readonly<{ onToggle: () => void }>) => (
    <Button data-testid="showRecentChats" variant="ghost" onClick={onToggle} className="h-10 w-10 p-0" tooltip="Toggle Chat Sessions">
        <PanelLeftIcon className="size-5" />
    </Button>
);
