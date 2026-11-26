// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect } from "react";
import { useLocation } from "react-router-dom";

import { useConfigContext } from "@/providers/contexts";

import { type NavItemConfig } from "@/types/nav";

import { findExactNavMatchLabel } from "@/features/navigation/routeMatch";

const DEFAULT_PAGE_TITLE = "Solace Agent Mesh";

export function usePageTitle(items: NavItemConfig[]): void {
    const location = useLocation();
    const { configAppName } = useConfigContext();

    useEffect(() => {
        const label = findExactNavMatchLabel(items, location.pathname);
        document.title = [configAppName, label].filter(Boolean).join(" | ") || DEFAULT_PAGE_TITLE;
    }, [configAppName, items, location.pathname]);
}
