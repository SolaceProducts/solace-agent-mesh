// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import { Edit } from "lucide-react";

import { useChatContext, useConfigContext } from "@/providers/contexts";

import { Button } from "@/ui/button";

import { ConfirmationDialog } from "@/common";

interface NewChatButtonProps {
    text?: string;
    onClick?: () => void;
}

const NewChatButton: React.FC<NewChatButtonProps> = ({ text, onClick }) => {
    return (
        <Button data-testid="startNewChat" variant="ghost" onClick={onClick} tooltip="Start New Chat Session">
            <Edit className="size-5" />
            {text}
        </Button>
    );
};

interface ChatSessionDialogProps {
    buttonText?: string;
}
export const ChatSessionDialog: React.FC<ChatSessionDialogProps> = ({ buttonText }) => {
    const { handleNewSession } = useChatContext();
    const { persistenceEnabled } = useConfigContext();
    const [isOpen, setIsOpen] = useState(false);

    return persistenceEnabled ? (
        <NewChatButton text={buttonText} onClick={() => handleNewSession()} />
    ) : (
        <ConfirmationDialog
            open={isOpen}
            onOpenChange={setIsOpen}
            title="Start New Chat Session"
            description="Starting a new chat session will clear the current chat history and files. Are you sure you want to proceed?"
            actionLabels={{ confirm: "Start New Chat" }}
            onConfirm={handleNewSession}
            trigger={<NewChatButton text={buttonText} />}
        />
    );
};
