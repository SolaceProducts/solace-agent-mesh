// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import * as React from "react";

import { cn } from "@/utils";

import { Textarea } from "../textarea";

type ChatInputProps = React.TextareaHTMLAttributes<HTMLTextAreaElement>;

const ChatInput = React.forwardRef<HTMLTextAreaElement, ChatInputProps>(({ className, ...props }, ref) => (
    <Textarea
        autoComplete="off"
        ref={ref}
        name="message"
        data-testid="chat-input"
        className={cn(`flex w-full items-center rounded-md bg-(--background-w10) px-4 py-3 placeholder:text-(--secondary-wMain) disabled:cursor-not-allowed disabled:opacity-50`, className)}
        {...props}
    />
));
ChatInput.displayName = "ChatInput";

export { ChatInput };
