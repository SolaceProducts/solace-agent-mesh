// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Component, type ErrorInfo, type ReactNode } from "react";

interface MessageErrorBoundaryProps {
    children: ReactNode;
}

interface MessageErrorBoundaryState {
    hasError: boolean;
}

export class MessageErrorBoundary extends Component<MessageErrorBoundaryProps, MessageErrorBoundaryState> {
    state: MessageErrorBoundaryState = { hasError: false };

    static getDerivedStateFromError(): MessageErrorBoundaryState {
        return { hasError: true };
    }

    componentDidCatch(error: Error, info: ErrorInfo) {
        console.error("[MessageErrorBoundary] failed to render a chat message", error, info.componentStack);
    }

    render() {
        if (this.state.hasError) {
            return (
                <output className="mx-auto my-2 block max-w-prose rounded-md border bg-(--background-w10) px-3 py-2 text-sm text-(--secondary-text-wMain)" data-testid="message-render-error">
                    This message couldn't be displayed.
                </output>
            );
        }
        return this.props.children;
    }
}
