// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ErrorInfo, type ReactNode, useState } from "react";
import { ErrorBoundary as ReactErrorBoundary } from "react-error-boundary";

import { navigateToLogin } from "@/utils/loginRedirect";

import ErrorState from "./ErrorState";

const LOGIN_URL = "/api/v1/auth/login";

const CRASH_MESSAGE = "An unexpected error occurred. Please refresh the page and try again. If the problem continues, sign in again or contact an administrator.";
const LOGIN_BLOCKED_MESSAGE = "Sign-in has to open in a separate window because the identity provider cannot be displayed inside this app. Allow pop-ups for this site, then select Return To Login.";

function RootFallback() {
    const [loginBlocked, setLoginBlocked] = useState(false);

    return (
        <ErrorState
            message={loginBlocked ? LOGIN_BLOCKED_MESSAGE : CRASH_MESSAGE}
            actions={[
                { kind: "button", label: "Reload", onClick: () => globalThis.location.reload() },
                {
                    kind: "link",
                    label: "Return To Login",
                    href: LOGIN_URL,
                    onClick: event => {
                        event.preventDefault();
                        setLoginBlocked(!navigateToLogin(LOGIN_URL, { userInitiated: true }));
                    },
                },
            ]}
        />
    );
}

function logError(error: unknown, info: ErrorInfo) {
    console.error("Unhandled UI error:", error, info.componentStack);
}

function ErrorBoundary({ children }: Readonly<{ children: ReactNode }>) {
    return (
        <ReactErrorBoundary FallbackComponent={RootFallback} onError={logError}>
            {children}
        </ReactErrorBoundary>
    );
}

export default ErrorBoundary;
