// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { createContext, type ReactNode, useContext, useMemo, useRef } from "react";

export type FeedbackTab = "history";

interface FilterMemory {
    read: <T>(tab: FeedbackTab) => T | undefined;
    remember: (tab: FeedbackTab, filters: unknown) => void;
}

const FeedbackFilterMemoryContext = createContext<FilterMemory | null>(null);

export function FeedbackFilterMemoryProvider({ children }: Readonly<{ children: ReactNode }>) {
    const store = useRef<Partial<Record<FeedbackTab, unknown>>>({});

    const value = useMemo<FilterMemory>(
        () => ({
            read: <T,>(tab: FeedbackTab) => store.current[tab] as T | undefined,
            remember: (tab: FeedbackTab, filters: unknown) => {
                store.current[tab] = filters;
            },
        }),
        []
    );

    return <FeedbackFilterMemoryContext.Provider value={value}>{children}</FeedbackFilterMemoryContext.Provider>;
}

export function useFeedbackFilterMemory(): FilterMemory {
    const memory = useContext(FeedbackFilterMemoryContext);
    if (!memory) throw new Error("useFeedbackFilterMemory must be used inside FeedbackFilterMemoryProvider");
    return memory;
}
