// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";

import { TaskContext, type TaskContextValue } from "@/providers/contexts/TaskContext";

const defaultMockTaskContext: TaskContextValue = {
    monitoredTasks: {},
    monitoredTaskOrder: [],
    unavailableTaskIds: new Set<string>(),
    failedTaskIds: new Set<string>(),
    highlightedStepId: null,
    setHighlightedStepId: () => {},
    loadTaskFromBackend: async () => null,
    loadRecentTasks: async () => {},
    registerTaskEarly: () => {},
};

interface MockTaskProviderProps {
    children: React.ReactNode;
    mockValues?: Partial<TaskContextValue>;
}

export const MockTaskProvider: React.FC<MockTaskProviderProps> = ({ children, mockValues = {} }) => {
    const contextValue = {
        ...defaultMockTaskContext,
        ...mockValues,
    };

    return <TaskContext.Provider value={contextValue}>{children}</TaskContext.Provider>;
};
