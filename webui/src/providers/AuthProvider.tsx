// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { type ReactNode, useCallback, useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";

import { api, cancelProactiveRefresh, scheduleProactiveRefresh, SESSION_IDLE_TIMEOUT_EVENT, setTokens } from "@/api/client";

import { useConfigContext, useCsrfContext } from "@/providers/contexts";
import { AuthContext } from "@/providers/contexts/AuthContext";

import { EmptyState } from "@/common/status/EmptyState";

import { AUTH_COMPLETE_MESSAGE, FRAMED_LOGIN_BLOCKED_EVENT, type FramedLoginBlockedDetail, type FramedLoginBlockedReason, navigateToLogin } from "@/utils/loginRedirect";
import { knownToBeInTeams, signInThroughTeams, startTeamsHandshakeIfPossible, type TeamsSessionTokens } from "@/utils/teamsAuth";
import { stashPostLoginRedirect } from "@/utils/url";

interface AuthProviderProps {
    children: ReactNode;
}

const FORCE_ACCOUNT_SELECTION_KEY = "sam_force_account_selection";

const SESSION_STORE_BLOCKED_SUBTITLE = "Your browser blocked storing the session for this embedded app. Allow site storage for this site (or turn off private browsing), then select Sign in.";
const POPUP_REFUSED_SUBTITLE = "Sign-in has to open in a separate window because the identity provider cannot be displayed inside this app. Allow pop-ups for this site, then select Sign in.";
const SESSION_ENDED_SUBTITLE = "Your session has ended. Select Sign in to continue.";
const INACTIVITY_SUBTITLE = "You were signed out after a period of inactivity. Sign in again to continue.";
const TEAMS_SIGN_IN_INCOMPLETE_SUBTITLE = "Sign-in did not finish. Select Sign in to try again.";

const removeRefreshTokenSoReloadCannotRefresh = (): void => {
    localStorage.removeItem("refresh_token");
};

const ABANDONED_TEAMS_SIGN_IN_MS = 5 * 60_000;

type FramedLoginBlockedState = FramedLoginBlockedReason | "unspecified" | null;

const signInBlockedSubtitle = (sessionStoreBlocked: boolean, framedLoginBlocked: FramedLoginBlockedState, teamsSignInIncomplete: boolean, idleTimedOut: boolean): string | undefined => {
    if (sessionStoreBlocked) return SESSION_STORE_BLOCKED_SUBTITLE;
    if (framedLoginBlocked === "popup-refused" && !knownToBeInTeams()) return POPUP_REFUSED_SUBTITLE;
    if (teamsSignInIncomplete) return TEAMS_SIGN_IN_INCOMPLETE_SUBTITLE;
    if (framedLoginBlocked) return SESSION_ENDED_SUBTITLE;
    if (idleTimedOut) return INACTIVITY_SUBTITLE;
    return undefined;
};

const accountChooserPending = (): boolean => {
    try {
        return sessionStorage.getItem(FORCE_ACCOUNT_SELECTION_KEY) === "true";
    } catch {
        return false;
    }
};

const clearAccountChooser = (): void => {
    try {
        sessionStorage.removeItem(FORCE_ACCOUNT_SELECTION_KEY);
    } catch {}
};

const withPromptSelectAccount = (loginUrl: string): string => {
    try {
        const url = new URL(loginUrl, globalThis.location.href);
        url.searchParams.set("prompt", "select_account");
        return url.toString();
    } catch {
        return loginUrl;
    }
};

export const AuthProvider: React.FC<AuthProviderProps> = ({ children }) => {
    const { configUseAuthorization, configAuthLoginUrl } = useConfigContext();
    const { fetchCsrfToken, clearCsrfToken } = useCsrfContext();
    const queryClient = useQueryClient();
    const [isAuthenticated, setIsAuthenticated] = useState(false);
    const [isLoading, setIsLoading] = useState(true);
    const [userInfo, setUserInfo] = useState<Record<string, unknown> | null>(null);
    const [framedLoginBlocked, setFramedLoginBlocked] = useState<FramedLoginBlockedState>(null);
    const [sessionStoreBlocked, setSessionStoreBlocked] = useState(false);
    const [teamsSignInIncomplete, setTeamsSignInIncomplete] = useState(false);
    const [idleTimedOut, setIdleTimedOut] = useState(false);
    const loginPopup = useRef<Window | null>(null);
    const teamsSignInInFlight = useRef(false);
    const teamsSignInAttempt = useRef(0);
    const [signingInThroughTeams, setSigningInThroughTeams] = useState(false);

    const adoptSession = useCallback((accessToken: string, samAccessToken: string | null, refreshToken: string | null) => {
        try {
            setTokens(accessToken, samAccessToken ?? "", refreshToken ?? "");
        } catch (error) {
            console.error("AuthProvider: could not store the session handed back by the sign-in window.", error);
            setSessionStoreBlocked(true);
            setIsAuthenticated(false);
            return;
        }
        globalThis.location.reload();
    }, []);

    useEffect(() => {
        const handleAuthComplete = (event: MessageEvent) => {
            if (event.origin !== globalThis.location.origin) return;
            if (!loginPopup.current) return;
            if (event.source && event.source !== loginPopup.current) return;
            if ((event.data as { type?: string } | null)?.type !== AUTH_COMPLETE_MESSAGE) return;

            const { accessToken, samAccessToken, refreshToken } = event.data as {
                accessToken?: unknown;
                samAccessToken?: unknown;
                refreshToken?: unknown;
            };
            if (typeof accessToken !== "string" || !accessToken) return;

            adoptSession(accessToken, typeof samAccessToken === "string" ? samAccessToken : null, typeof refreshToken === "string" ? refreshToken : null);
        };

        const handleFramedLoginBlocked = (event: Event) => {
            setFramedLoginBlocked((event as CustomEvent<FramedLoginBlockedDetail>).detail?.reason ?? "unspecified");
            setIsAuthenticated(false);
        };

        const handleIdleTimeout = () => {
            setIdleTimedOut(true);
            setIsAuthenticated(false);
        };

        globalThis.addEventListener("message", handleAuthComplete);
        globalThis.addEventListener(FRAMED_LOGIN_BLOCKED_EVENT, handleFramedLoginBlocked);
        globalThis.addEventListener(SESSION_IDLE_TIMEOUT_EVENT, handleIdleTimeout);
        return () => {
            globalThis.removeEventListener("message", handleAuthComplete);
            globalThis.removeEventListener(FRAMED_LOGIN_BLOCKED_EVENT, handleFramedLoginBlocked);
            globalThis.removeEventListener(SESSION_IDLE_TIMEOUT_EVENT, handleIdleTimeout);
        };
    }, [adoptSession]);

    useEffect(() => {
        startTeamsHandshakeIfPossible();
    }, []);

    useEffect(() => {
        sessionStorage.removeItem("logout_in_progress");
        let isMounted = true;

        const checkAuthStatus = async () => {
            if (!configUseAuthorization) {
                if (isMounted) {
                    setIsAuthenticated(true);
                    setIsLoading(false);
                }
                return;
            }

            try {
                const userData = await api.webui.get<Record<string, unknown>>("/api/v1/user");
                console.log("User is authenticated:", userData);

                if (isMounted) {
                    setUserInfo(userData);
                    setIsAuthenticated(true);

                    scheduleProactiveRefresh();
                }

                console.log("Fetching CSRF token for authenticated requests...");
                await fetchCsrfToken();
            } catch (authError) {
                console.error("Error checking authentication:", authError);
                if (isMounted) {
                    setIsAuthenticated(false);
                }
            } finally {
                if (isMounted) {
                    setIsLoading(false);
                }
            }
        };

        checkAuthStatus();

        const handleStorageChange = (event: StorageEvent) => {
            if (event.key === "access_token" || event.key === "sam_access_token") {
                checkAuthStatus();
            }
        };

        globalThis.addEventListener("storage", handleStorageChange);

        return () => {
            isMounted = false;
            globalThis.removeEventListener("storage", handleStorageChange);
            cancelProactiveRefresh();
        };
    }, [configUseAuthorization, configAuthLoginUrl, fetchCsrfToken]);

    const login = async () => {
        if (teamsSignInInFlight.current) return;
        setSessionStoreBlocked(false);
        setFramedLoginBlocked(null);
        setTeamsSignInIncomplete(false);
        stashPostLoginRedirect();
        const chooserPending = accountChooserPending();
        const loginUrl = chooserPending ? withPromptSelectAccount(configAuthLoginUrl) : configAuthLoginUrl;
        if (knownToBeInTeams()) {
            teamsSignInInFlight.current = true;
            setSigningInThroughTeams(true);
            const attempt = ++teamsSignInAttempt.current;
            const isCurrentAttempt = () => teamsSignInAttempt.current === attempt;
            const releaseSignIn = () => {
                if (!isCurrentAttempt()) return;
                teamsSignInInFlight.current = false;
                setSigningInThroughTeams(false);
            };
            const abandoned = globalThis.setTimeout(releaseSignIn, ABANDONED_TEAMS_SIGN_IN_MS);
            let teamsSession: TeamsSessionTokens | null;
            try {
                teamsSession = await signInThroughTeams(loginUrl);
            } finally {
                globalThis.clearTimeout(abandoned);
                releaseSignIn();
            }
            if (!teamsSession) {
                if (isCurrentAttempt()) setTeamsSignInIncomplete(true);
                return;
            }
            if (chooserPending) clearAccountChooser();
            adoptSession(teamsSession.accessToken, teamsSession.samAccessToken ?? null, teamsSession.refreshToken ?? null);
            return;
        }

        const started = navigateToLogin(loginUrl, {
            userInitiated: true,
            onPopupOpened: popup => {
                loginPopup.current = popup;
            },
        });
        if (started && chooserPending) {
            clearAccountChooser();
        }
    };

    const logout = async () => {
        try {
            if (configUseAuthorization) {
                removeRefreshTokenSoReloadCannotRefresh();
                sessionStorage.setItem("logout_in_progress", "true");
                cancelProactiveRefresh();

                await api.webui.post("/api/v1/auth/logout");
            }
        } catch (error) {
            console.warn("Error during logout:", error);
        } finally {
            localStorage.removeItem("access_token");
            localStorage.removeItem("sam_access_token");

            queryClient.removeQueries();

            setIsAuthenticated(false);
            setUserInfo(null);
            clearCsrfToken();

            sessionStorage.removeItem("logout_in_progress");

            try {
                sessionStorage.setItem(FORCE_ACCOUNT_SELECTION_KEY, "true");
            } catch (error) {
                console.warn("Could not record the account-chooser hint for the next sign-in.", error);
            }

            globalThis.location.href = "/";
        }
    };

    if (isLoading) {
        return <EmptyState variant="loading" title="Checking Authentication..." className="h-screen w-screen" />;
    }

    if (configUseAuthorization && !isAuthenticated) {
        const subtitle = signInBlockedSubtitle(sessionStoreBlocked, framedLoginBlocked, teamsSignInIncomplete, idleTimedOut);
        const idleSignInLabel = subtitle ? "Sign in" : "Login";
        const signInLabel = signingInThroughTeams ? "Signing in…" : idleSignInLabel;
        return (
            <EmptyState
                variant="noImage"
                title="Welcome to Solace Agent Mesh"
                subtitle={subtitle}
                className="h-screen w-screen"
                buttons={[
                    {
                        text: signInLabel,
                        onClick: () => login(),
                        variant: "default",
                        disabled: signingInThroughTeams,
                    },
                ]}
            />
        );
    }

    return (
        <AuthContext.Provider
            value={{
                isAuthenticated,
                useAuthorization: configUseAuthorization,
                login,
                logout,
                userInfo,
            }}
        >
            {children}
        </AuthContext.Provider>
    );
};
