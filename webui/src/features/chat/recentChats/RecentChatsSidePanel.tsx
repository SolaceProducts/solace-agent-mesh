// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import { User } from "lucide-react";

import { useUIModeContext } from "@/providers/contexts";

import { MAX_RECENT_CHATS } from "@/features/navigation/constants";
import { iconStyles, iconWrapperStyles, navButtonStyles, navTextStyles } from "@/features/navigation/navigationStyles";
import { SettingsDialog } from "@/features/settings/SettingsDialog";

import { RecentChatsHeader } from "./RecentChatsHeader";
import { RecentChatsList } from "./RecentChatsList";

interface RecentChatsSidePanelProps {
    viewAllDisabled?: boolean;
}

export const RecentChatsSidePanel = ({ viewAllDisabled = false }: Readonly<RecentChatsSidePanelProps>) => {
    const { pinnedAgentName } = useUIModeContext();

    const [isSettingsOpen, setIsSettingsOpen] = useState(false);

    const viewAllTo = pinnedAgentName ? `/agent-mode/recent-chats?agent=${encodeURIComponent(pinnedAgentName)}` : "/agent-mode/recent-chats";

    return (
        <div className="flex h-full w-100 flex-col border-r border-(--darkSurface-border) bg-(--darkSurface-bg)">
            <RecentChatsHeader to={viewAllTo} viewAllDisabled={viewAllDisabled} className="h-20 border-b border-(--darkSurface-border) px-6 pt-6 pb-2" />
            <div className="scrollbar-subtle min-h-0 flex-1 overflow-y-auto">
                <RecentChatsList maxItems={MAX_RECENT_CHATS} agentId={pinnedAgentName || undefined} />
            </div>

            <div className="mt-auto border-t border-(--darkSurface-border) py-3">
                <button onClick={() => setIsSettingsOpen(true)} className={navButtonStyles()}>
                    <div className={iconWrapperStyles({ withMargin: true })}>
                        <User className={iconStyles()} />
                    </div>
                    <span className={navTextStyles()}>User Account</span>
                </button>
            </div>

            <SettingsDialog open={isSettingsOpen} onOpenChange={setIsSettingsOpen} />
        </div>
    );
};
