// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useLayoutEffect } from "react";

import { productAnalyticsClient } from "./client";
import { publishRoutePattern } from "./routePattern";
import { useRoutePattern } from "./useRoutePattern";

export function RouteChangeTracker() {
    const path = useRoutePattern();

    useLayoutEffect(() => {
        publishRoutePattern(path);
    }, [path]);

    useEffect(() => {
        productAnalyticsClient.track("page_view", { path });
    }, [path]);

    return null;
}
