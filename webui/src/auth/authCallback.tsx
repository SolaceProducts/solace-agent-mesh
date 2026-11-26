// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useRef, useState } from "react";

import { setTokens } from "@/api/client";

import { AUTH_COMPLETE_MESSAGE, LOGIN_POPUP_NAME, navigateToLogin } from "@/utils/loginRedirect";
import { completeTeamsSignIn, couldBeTeamsAuthWindow, failTeamsSignIn } from "@/utils/teamsAuth";
import { consumePostLoginRedirect, markSignInCompleted } from "@/utils/url";

interface SessionTokens {
    accessToken: string;
    samAccessToken?: string | null;
    refreshToken?: string | null;
}

const handOffToOpener = (tokens: SessionTokens): boolean => {
    const opener = globalThis.opener as Window | null;
    if (!opener || opener === globalThis.self) {
        return false;
    }
    try {
        opener.postMessage({ type: AUTH_COMPLETE_MESSAGE, ...tokens }, globalThis.location.origin);
    } catch (error) {
        console.warn("AuthCallback: could not hand the session to the opener; continuing in this window.", error);
        return false;
    }
    if (globalThis.self.name !== LOGIN_POPUP_NAME) {
        return false;
    }
    try {
        globalThis.close();
    } catch (error) {
        console.warn("AuthCallback: the browser refused to close the sign-in popup.", error);
    }
    return globalThis.closed;
};

const LOGIN_URL = "/api/v1/auth/login";

const MAX_AUTO_RETRIES = 5;
const RETRY_COUNT_KEY = "sam_auth_retry_count";
const BASE_RETRY_DELAY_SEC = 2;
const MAX_RETRY_DELAY_SEC = 6;

const COLORS = {
    text: "#273749",
    mutedText: "#647481",
    primary: "#015B82",
    onPrimary: "#FFFFFF",
} as const;

type CallbackStatus = "loading" | "error" | "retrying" | "handed-off";

const containerStyle: React.CSSProperties = {
    minHeight: "100vh",
    display: "flex",
    flexDirection: "column",
    alignItems: "center",
    justifyContent: "center",
    gap: "1rem",
    padding: "2rem",
    fontFamily: "system-ui, -apple-system, Segoe UI, Roboto, sans-serif",
    color: COLORS.text,
    textAlign: "center",
};

const titleStyle: React.CSSProperties = {
    fontSize: "1.25rem",
    fontWeight: 600,
    margin: 0,
};

const messageStyle: React.CSSProperties = {
    maxWidth: "28rem",
    margin: 0,
    color: COLORS.mutedText,
};

const returnButtonStyle: React.CSSProperties = {
    marginTop: "0.5rem",
    padding: "0.5rem 1rem",
    borderRadius: "0.375rem",
    background: COLORS.primary,
    color: COLORS.onPrimary,
    textDecoration: "none",
    fontWeight: 500,
};

const referenceStyle: React.CSSProperties = {
    margin: 0,
    fontSize: "0.75rem",
    color: COLORS.mutedText,
};

function readRetryCount(): number {
    try {
        return Number(sessionStorage.getItem(RETRY_COUNT_KEY)) || 0;
    } catch {
        return 0;
    }
}

function writeRetryCount(n: number): boolean {
    try {
        if (n <= 0) sessionStorage.removeItem(RETRY_COUNT_KEY);
        else sessionStorage.setItem(RETRY_COUNT_KEY, String(n));
        return true;
    } catch {
        return false;
    }
}

function AuthCallback() {
    const [status, setStatus] = useState<CallbackStatus>("loading");
    const [message, setMessage] = useState<string>("");
    const [errorId, setErrorId] = useState<string>("");
    const [secondsLeft, setSecondsLeft] = useState<number>(0);
    const [retryDelaySec, setRetryDelaySec] = useState<number>(BASE_RETRY_DELAY_SEC);
    const redirectTimer = useRef<ReturnType<typeof globalThis.setTimeout> | null>(null);

    useEffect(() => {
        const params = new URLSearchParams(globalThis.location.hash.substring(1));

        const oauthError = params.get("error");
        const accessToken = params.get("access_token");
        const samAccessToken = params.get("sam_access_token");
        const refreshToken = params.get("refresh_token");
        const retryable = params.get("retryable") === "1";
        const id = params.get("errorId") || "";

        if (oauthError || !accessToken) {
            const detail = params.get("error_description") || oauthError || "The identity provider did not return an access token.";
            console.error("AuthCallback: login failed —", detail, id ? `(errorId=${id})` : "");
            setMessage(detail);
            setErrorId(id);

            const attempts = readRetryCount();
            const canAutoRetry = retryable && attempts < MAX_AUTO_RETRIES && writeRetryCount(attempts + 1);
            if (canAutoRetry) {
                const delay = Math.min(BASE_RETRY_DELAY_SEC + attempts, MAX_RETRY_DELAY_SEC);
                setRetryDelaySec(delay);
                setSecondsLeft(delay);
                setStatus("retrying");
                return;
            }
            writeRetryCount(0);
            if (couldBeTeamsAuthWindow()) void failTeamsSignIn(oauthError ? "idp_error" : "no_access_token");
            setStatus("error");
            return;
        }

        writeRetryCount(0);

        if (handOffToOpener({ accessToken, samAccessToken, refreshToken })) {
            return;
        }

        if (!couldBeTeamsAuthWindow()) {
            completeInThisWindow();
            return;
        }

        void completeTeamsSignIn({ accessToken, samAccessToken, refreshToken }).then(handedBack => {
            if (handedBack) {
                setStatus("handed-off");
                return;
            }
            completeInThisWindow();
        });

        function completeInThisWindow() {
            try {
                setTokens(accessToken as string, samAccessToken ?? "", refreshToken ?? "");
            } catch (error) {
                console.error("AuthCallback: could not store session tokens (browser storage unavailable).", error);
                setMessage("Your browser blocked storing the session. Turn off private browsing or allow site storage, then try again.");
                setStatus("error");
                return;
            }

            markSignInCompleted();
            globalThis.location.href = consumePostLoginRedirect();
        }
    }, []);

    useEffect(() => {
        if (status !== "retrying") return;
        setSecondsLeft(retryDelaySec);
        const tick = globalThis.setInterval(() => {
            setSecondsLeft(s => (s > 1 ? s - 1 : 0));
        }, 1000);
        redirectTimer.current = globalThis.setTimeout(() => {
            navigateToLogin(LOGIN_URL);
        }, retryDelaySec * 1000);
        return () => {
            globalThis.clearInterval(tick);
            if (redirectTimer.current) globalThis.clearTimeout(redirectTimer.current);
        };
    }, [status, retryDelaySec]);

    if (status === "retrying") {
        return (
            <div style={containerStyle}>
                <h1 style={titleStyle}>Finishing sign-in…</h1>
                <output style={messageStyle} aria-live="polite">
                    The service is still starting up. Retrying automatically in {secondsLeft}s.
                </output>
                <a
                    href={LOGIN_URL}
                    style={returnButtonStyle}
                    onClick={event => {
                        event.preventDefault();
                        navigateToLogin(LOGIN_URL, { userInitiated: true });
                    }}
                >
                    Try now
                </a>
                {errorId && <p style={referenceStyle}>Reference: {errorId}</p>}
            </div>
        );
    }

    if (status === "handed-off") {
        return <div style={containerStyle}>You can close this window.</div>;
    }

    if (status === "error") {
        return (
            <div style={containerStyle} role="alert">
                <h1 style={titleStyle}>Sign-in failed</h1>
                <p style={messageStyle}>{message}</p>
                <a
                    href={LOGIN_URL}
                    style={returnButtonStyle}
                    onClick={event => {
                        event.preventDefault();
                        writeRetryCount(0);
                        navigateToLogin(LOGIN_URL, { userInitiated: true });
                    }}
                >
                    Try again
                </a>
                {errorId && <p style={referenceStyle}>Reference: {errorId}</p>}
            </div>
        );
    }

    return <div style={containerStyle}>Signing you in…</div>;
}

export default AuthCallback;
