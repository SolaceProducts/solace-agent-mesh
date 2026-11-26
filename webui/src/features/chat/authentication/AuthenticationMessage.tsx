// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useChatContext } from "@/providers/contexts";

import { Button } from "@/ui/button";

import { MessageBanner } from "@/common";
import type { MessageFE } from "@/types";

const getBannerConfig = (terminalState: "authenticated" | "failed" | "denied", serviceSuffix: string) => {
    switch (terminalState) {
        case "authenticated":
            return { variant: "success" as const, message: `Authenticated${serviceSuffix}` };
        case "failed":
            return { variant: "error" as const, message: `Authentication${serviceSuffix} failed` };
        case "denied":
            return { variant: "info" as const, message: "Authentication request was rejected" };
    }
};

const getTitleForTerminalState = (terminalState: "authenticated" | "failed" | "denied") => {
    switch (terminalState) {
        case "authenticated":
            return "Signed In";
        case "denied":
            return "Request Rejected";
        case "failed":
            return "Action Needed";
    }
};

export const AuthenticationMessage: React.FC<{ message: MessageFE }> = ({ message }) => {
    const { handleCancel, setMessages, isResponding, isCancelling, agentNameDisplayNameMap } = useChatContext();

    if (message.authenticationLink) {
        const authenticationAttempted = message.authenticationLink.authenticationAttempted || false;
        const rejected = message.authenticationLink.rejected || false;

        const handleAuthClick = () => {
            if (authenticationAttempted || rejected) return;

            setMessages((prevMessages: MessageFE[]) =>
                prevMessages.map(msg => (msg.metadata?.messageId === message.metadata?.messageId && msg.authenticationLink ? { ...msg, authenticationLink: { ...msg.authenticationLink, authenticationAttempted: true } } : msg))
            );

            const wailsRuntime = (window as unknown as { runtime?: { BrowserOpenURL?: (url: string) => void } }).runtime;
            if (wailsRuntime?.BrowserOpenURL) {
                wailsRuntime.BrowserOpenURL(message.authenticationLink!.url);
            } else {
                const popup = window.open(message.authenticationLink!.url, "_blank", "width=800,height=700,scrollbars=yes,resizable=yes");
                if (popup) {
                    popup.focus();
                }
            }
        };

        const handleRejectClick = async () => {
            if (rejected) return;

            setMessages((prevMessages: MessageFE[]) =>
                prevMessages.map(msg => (msg.metadata?.messageId === message.metadata?.messageId && msg.authenticationLink ? { ...msg, authenticationLink: { ...msg.authenticationLink, rejected: true } } : msg))
            );

            handleCancel();
        };

        const targetAgent = message.authenticationLink.targetAgent || "Agent";
        const targetAgentLabel = agentNameDisplayNameMap?.[targetAgent] || targetAgent;
        const serviceName = message.authenticationLink.serviceName;
        const serviceSuffix = serviceName ? ` to ${serviceName}` : "";
        const terminalState: "authenticated" | "failed" | "denied" | undefined = rejected ? "denied" : message.authenticationLink.outcome;
        const isAuthenticating = authenticationAttempted && !terminalState;

        if (terminalState) {
            const banner = getBannerConfig(terminalState, serviceSuffix);
            const title = getTitleForTerminalState(terminalState);
            return (
                <div className="w-max rounded-lg border p-4">
                    <div className="font-semibold">{title}</div>
                    <div className="py-4">
                        The <span className="font-semibold">{targetAgentLabel}</span> agent
                        {terminalState === "authenticated" ? " is now signed in" : " requested authentication"}
                        {serviceName ? (
                            <>
                                {" "}
                                to <span className="font-semibold">{serviceName}</span>.
                            </>
                        ) : (
                            <>.</>
                        )}
                    </div>
                    <MessageBanner variant={banner.variant} message={banner.message} />
                </div>
            );
        }

        return (
            <div className="w-max rounded-lg border p-4">
                <div className="font-semibold">Action Needed</div>
                <div className="py-4">
                    The <span className="font-semibold">{targetAgentLabel}</span> agent needs you to sign in
                    {serviceName ? (
                        <>
                            {" "}
                            to <span className="font-semibold">{serviceName}</span>.
                        </>
                    ) : (
                        <> before it can continue.</>
                    )}
                </div>
                <div className="flex flex-row flex-wrap justify-end gap-2">
                    <Button variant="ghost" onClick={handleRejectClick} disabled={!isResponding || isCancelling}>
                        {authenticationAttempted ? "Cancel" : "Deny"}
                    </Button>
                    <Button onClick={handleAuthClick} loading={isAuthenticating} disabled={!isResponding || isCancelling}>
                        {isAuthenticating ? "Authenticating" : message.authenticationLink.text}
                    </Button>
                </div>
                {authenticationAttempted && <div className="mt-4 text-center text-xs text-(--secondary-text-wMain)">Authentication window has been opened. Complete the process in the new window.</div>}
            </div>
        );
    }

    return null;
};
