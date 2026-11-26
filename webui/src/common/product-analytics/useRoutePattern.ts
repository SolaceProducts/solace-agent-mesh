// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMatches } from "react-router-dom";

import { toRoutePattern } from "./routePattern";

export function useRoutePattern(): string {
    const matches = useMatches();
    const leaf = matches.at(-1);
    return leaf ? toRoutePattern(leaf.pathname, leaf.params) : "/";
}
