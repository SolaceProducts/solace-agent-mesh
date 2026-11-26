// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { TextSelectionContext } from "@/features/chat/selection/TextSelectionContext";
import { type SelectionContextValue } from "@/features/chat/selection/types";

interface MockTextSelectionProviderProps {
    children: React.ReactNode;
    mockValues: Partial<SelectionContextValue>;
}

const defaultTextSelectionValues: SelectionContextValue = {
    selectedText: null,
    selectionRange: null,
    menuPosition: null,
    sourceMessageId: null,
    sourceTaskId: null,
    isMenuOpen: false,
    setSelection: () => {},
    clearSelection: () => {},
    handleFollowUpQuestion: () => {},
};

export const MockTextSelectionProvider: React.FC<MockTextSelectionProviderProps> = ({ children, mockValues = {} }) => {
    const contextValue = {
        ...defaultTextSelectionValues,
        ...mockValues,
    };

    return <TextSelectionContext.Provider value={contextValue}>{children}</TextSelectionContext.Provider>;
};
