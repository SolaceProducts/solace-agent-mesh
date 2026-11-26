// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";

import { AddCommentMenu } from "./AddCommentMenu";
import type { SelectionContextMenuProps } from "./types";

export const SelectionContextMenu: React.FC<SelectionContextMenuProps> = ({ isOpen, position, selectedText, sourceTaskId, onClose }) => {
    const handleAddComment = () => {
        globalThis.dispatchEvent(
            new CustomEvent("follow-up-question", {
                detail: {
                    text: selectedText,
                    prompt: "",
                    autoSubmit: false,
                    sourceMessageId: sourceTaskId,
                },
            })
        );
        onClose();
    };

    return <AddCommentMenu isOpen={isOpen} position={position} onAddComment={handleAddComment} onClose={onClose} />;
};
