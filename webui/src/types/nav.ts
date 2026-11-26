// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type React from "react";

export interface NavItemConfig {
    id: string;

    label: string;

    icon?: React.ElementType;

    route?: string;

    routeMatch?: string | string[] | RegExp;

    onClick?: () => void;

    badge?: React.ReactNode;

    tooltip?: string;

    disabled?: boolean;

    hidden?: boolean;

    children?: NavItemConfig[];

    defaultExpanded?: boolean;

    position?: "top" | "bottom";
}

export interface HeaderConfig {
    component?: React.ReactNode;
    hideCollapseButton?: boolean;
}

export interface NewChatConfig {
    label?: string;
    icon?: React.ElementType;
    onClick?: () => void;
}
