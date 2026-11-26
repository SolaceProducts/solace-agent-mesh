// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { RefreshCcw } from "lucide-react";

import { Menu, type MenuAction } from "@/ui/menu";
import { Popover, PopoverContent, PopoverTrigger } from "@/ui/popover";

interface ConnectorsAvailableMorePopoverProps {
    children: React.ReactNode;
}

export const ConnectorsAvailableMorePopover: React.FC<ConnectorsAvailableMorePopoverProps> = ({ children }) => {
    const menuActions: MenuAction[] = [
        {
            id: "refreshAll",
            label: "Refresh",
            onClick: () => {
                alert("refresh available connectors clicked");
            },
            icon: <RefreshCcw />,
            iconPosition: "left",
        },
    ];

    return (
        <Popover>
            <PopoverTrigger asChild>{children}</PopoverTrigger>
            <PopoverContent align="end" side="bottom" className="w-auto" sideOffset={0}>
                <Menu actions={menuActions} />
            </PopoverContent>
        </Popover>
    );
};
