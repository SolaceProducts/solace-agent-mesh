// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";

export function usePageNumber(resetKey: string): [number, (page: number) => void] {
    const [page, setPage] = useState(1);
    const [prevResetKey, setPrevResetKey] = useState(resetKey);

    if (resetKey !== prevResetKey) {
        setPrevResetKey(resetKey);
        setPage(1);
    }

    return [page, setPage];
}
