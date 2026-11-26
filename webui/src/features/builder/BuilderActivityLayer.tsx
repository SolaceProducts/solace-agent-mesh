// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ReactNode } from "react";

import { ActivityPanelHeader } from "./ActivityPanelHeader";

interface BuilderActivityLayerProps {
    onBack: () => void;
    children: ReactNode;
}

export function BuilderActivityLayer({ onBack, children }: Readonly<BuilderActivityLayerProps>) {
    return (
        <div className="absolute inset-0 z-20 flex flex-col bg-(--background-w10)">
            <ActivityPanelHeader title="Activity" onBack={onBack} backTooltip="Back to chat" />
            <div className="min-h-0 flex-1 overflow-hidden">{children}</div>
        </div>
    );
}
