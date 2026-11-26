// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useAuthContext } from "@/providers/contexts";

export function useCacheUserId(): string {
    const { userInfo } = useAuthContext();
    const username = userInfo?.username;
    if (typeof username === "string" && username.length > 0) return username;
    const id = userInfo?.id;
    if (typeof id === "string" && id.length > 0) return id;
    return "anonymous";
}
