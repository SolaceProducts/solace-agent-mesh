// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

const mockConfigData = {
    frontendServerUrl: "http://webui",
    frontendPlatformServerUrl: "http://platform",
    frontendAuthLoginUrl: "/login",
    frontendUseAuthorization: false,
    frontendWelcomeMessage: "Welcome",
    frontendRedirectUrl: "/",
    frontendCollectFeedback: false,
    frontendAppName: "Bot",
    frontendLogoUrl: "/logo.png",
    persistenceEnabled: false,
    identityServiceType: null,
};

describe("ConfigProvider", () => {
    let ConfigProvider: React.ComponentType<{ children: React.ReactNode }>;
    let useConfigContext: typeof import("@/providers/contexts").useConfigContext;
    let mockFetchCsrfToken: ReturnType<typeof vi.fn>;
    let mockWebuiGet: ReturnType<typeof vi.fn>;
    let mockApiConfigure: ReturnType<typeof vi.fn>;
    let queryClient: QueryClient;

    beforeEach(async () => {
        vi.resetModules();

        queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });

        mockFetchCsrfToken = vi.fn().mockResolvedValue(null);
        mockWebuiGet = vi.fn();
        mockApiConfigure = vi.fn();

        vi.doMock("@/providers/contexts/CsrfContext", () => ({
            useCsrfContext: () => ({
                fetchCsrfToken: mockFetchCsrfToken,
                clearCsrfToken: vi.fn(),
            }),
        }));

        vi.doMock("@/api/client", () => ({
            api: {
                webui: { get: mockWebuiGet },
                configure: mockApiConfigure,
            },
            configureSessionIdleTimeout: vi.fn(),
        }));

        vi.doMock("@/components", () => ({
            EmptyState: ({ title }: { title: string }) => React.createElement("div", null, title),
        }));

        const providerMod = await import("@/providers/ConfigProvider");
        ConfigProvider = providerMod.ConfigProvider;
        const contextsMod = await import("@/providers/contexts");
        useConfigContext = contextsMod.useConfigContext;
    });

    function renderWithQuery(children: React.ReactNode) {
        return render(<QueryClientProvider client={queryClient}>{<ConfigProvider>{children}</ConfigProvider>}</QueryClientProvider>);
    }

    function makeOkResponse(data: unknown) {
        return new Response(JSON.stringify(data), {
            status: 200,
            headers: { "Content-Type": "application/json" },
        });
    }

    function makeErrorResponse(status: number, body = "Error") {
        return new Response(body, { status });
    }

    describe("successful initialization", () => {
        test("renders children after config loads successfully", async () => {
            mockWebuiGet.mockResolvedValue(makeOkResponse(mockConfigData));

            renderWithQuery(<div data-testid="child">Hello</div>);

            await waitFor(() => {
                expect(screen.getByTestId("child")).toBeInTheDocument();
            });
        });

        test("configures api client with server URLs from config response", async () => {
            mockWebuiGet.mockResolvedValue(makeOkResponse(mockConfigData));

            renderWithQuery(<div data-testid="child">Hello</div>);

            await waitFor(() => {
                expect(screen.getByTestId("child")).toBeInTheDocument();
            });

            expect(mockApiConfigure).toHaveBeenCalledWith("http://webui", "http://platform");
        });

        test("configures the api client before children mount", async () => {
            mockWebuiGet.mockResolvedValue(makeOkResponse(mockConfigData));

            let configureCallsAtChildMount = -1;
            const ChildProbe = () => {
                React.useEffect(() => {
                    configureCallsAtChildMount = mockApiConfigure.mock.calls.length;
                }, []);
                return <div data-testid="child">Hello</div>;
            };

            renderWithQuery(<ChildProbe />);

            await waitFor(() => {
                expect(screen.getByTestId("child")).toBeInTheDocument();
            });

            expect(configureCallsAtChildMount).toBeGreaterThan(0);
        });
    });

    describe("config fetch error handling", () => {
        test("shows error state when config fetch fails with non-403 status", async () => {
            mockWebuiGet.mockResolvedValue(makeErrorResponse(500));

            renderWithQuery(<div data-testid="child">Hello</div>);

            await waitFor(() => {
                expect(screen.getByText("Configuration Error")).toBeInTheDocument();
            });
        });
    });

    describe("CSRF retry on 403", () => {
        test("retries config with CSRF token after initial 403", async () => {
            mockFetchCsrfToken.mockResolvedValue("csrf-token-xyz");
            let configCallCount = 0;

            mockWebuiGet.mockImplementation(async () => {
                configCallCount++;
                if (configCallCount === 1) return makeErrorResponse(403, "Forbidden");
                return makeOkResponse(mockConfigData);
            });

            renderWithQuery(<div data-testid="child">Hello</div>);

            await waitFor(() => {
                expect(screen.getByTestId("child")).toBeInTheDocument();
            });

            expect(mockFetchCsrfToken).toHaveBeenCalled();
            expect(configCallCount).toBe(2);
        });

        test("shows error state when CSRF retry config fetch also fails", async () => {
            mockFetchCsrfToken.mockResolvedValue("csrf-token-xyz");

            mockWebuiGet.mockResolvedValue(makeErrorResponse(403, "Forbidden"));

            renderWithQuery(<div data-testid="child">Hello</div>);

            await waitFor(() => {
                expect(screen.getByText("Configuration Error")).toBeInTheDocument();
            });
        });

        test("shows error state when CSRF token cannot be obtained after 403", async () => {
            mockFetchCsrfToken.mockResolvedValue(null);

            mockWebuiGet.mockResolvedValue(makeErrorResponse(403, "Forbidden"));

            renderWithQuery(<div data-testid="child">Hello</div>);

            await waitFor(() => {
                expect(screen.getByText("Configuration Error")).toBeInTheDocument();
            });
        });
    });

    describe("platformConfigured derivation", () => {
        async function readPlatformConfigured(data: unknown): Promise<boolean | undefined> {
            mockWebuiGet.mockResolvedValue(makeOkResponse(data));
            let captured: boolean | undefined;
            const Probe = () => {
                const ctx = useConfigContext();
                captured = ctx.platformConfigured;
                return <span data-testid="probe">ready</span>;
            };
            renderWithQuery(<Probe />);
            await waitFor(() => expect(screen.getByTestId("probe")).toBeInTheDocument());
            return captured;
        }

        test("explicit flag=true wins over empty URL", async () => {
            const result = await readPlatformConfigured({
                ...mockConfigData,
                frontendPlatformServerUrl: "",
                frontendPlatformServiceEnabled: true,
            });
            expect(result).toBe(true);
        });

        test("explicit flag=false wins over a non-empty URL", async () => {
            const result = await readPlatformConfigured({
                ...mockConfigData,
                frontendPlatformServerUrl: "http://platform",
                frontendPlatformServiceEnabled: false,
            });
            expect(result).toBe(false);
        });

        test("flag absent falls back to URL-truthy check (legacy backend)", async () => {
            const result = await readPlatformConfigured({
                ...mockConfigData,
                frontendPlatformServerUrl: "http://platform",
            });
            expect(result).toBe(true);
        });

        test("flag absent + empty URL falls back to false (legacy backend)", async () => {
            const result = await readPlatformConfigured({
                ...mockConfigData,
                frontendPlatformServerUrl: "",
            });
            expect(result).toBe(false);
        });
    });

    describe("refetch", () => {
        test("clears a sticky error and renders children once the retry succeeds", async () => {
            mockWebuiGet.mockResolvedValueOnce(makeErrorResponse(500));

            renderWithQuery(<div data-testid="child">Hello</div>);
            await waitFor(() => expect(screen.getByText("Configuration Error")).toBeInTheDocument());

            mockWebuiGet.mockResolvedValue(makeOkResponse(mockConfigData));
            await queryClient.refetchQueries({ queryKey: ["config"] });

            await waitFor(() => expect(screen.getByTestId("child")).toBeInTheDocument());
        });

        test("re-fetches and updates context without unmounting on refetch()", async () => {
            mockWebuiGet.mockResolvedValue(makeOkResponse(mockConfigData));
            let captured: { configAppName: string; refetch: () => Promise<void> } | undefined;
            const Probe = () => {
                const ctx = useConfigContext();
                captured = { configAppName: ctx.configAppName, refetch: ctx.refetch };
                return <span data-testid="probe">{ctx.configAppName}</span>;
            };

            renderWithQuery(<Probe />);
            await waitFor(() => expect(screen.getByTestId("probe")).toHaveTextContent("Bot"));

            mockWebuiGet.mockResolvedValue(makeOkResponse({ ...mockConfigData, frontendAppName: "Renamed" }));
            await captured!.refetch();

            await waitFor(() => expect(screen.getByTestId("probe")).toHaveTextContent("Renamed"));
        });
    });

    describe("shared query with AudioSettingsProvider", () => {
        test("fetches /api/v1/config exactly once for both providers combined, not once each", async () => {
            mockWebuiGet.mockImplementation(async (path: string) => {
                if (path === "/api/v1/speech/config") {
                    return { sttExternal: false, ttsExternal: false };
                }
                return makeOkResponse(mockConfigData);
            });

            const { AudioSettingsProvider } = await import("@/providers/AudioSettingsProvider");
            const { useAudioSettings } = await import("@/providers/contexts");

            function Probe() {
                const configCtx = useConfigContext();
                const audioCtx = useAudioSettings();
                return (
                    <span data-testid="probe">
                        {configCtx.configAppName}/{audioCtx.isInitialized ? "ready" : "loading"}
                    </span>
                );
            }

            render(
                <QueryClientProvider client={queryClient}>
                    <ConfigProvider>
                        <AudioSettingsProvider>
                            <Probe />
                        </AudioSettingsProvider>
                    </ConfigProvider>
                </QueryClientProvider>
            );

            await waitFor(() => expect(screen.getByTestId("probe")).toHaveTextContent("ready"));

            const configCalls = mockWebuiGet.mock.calls.filter(call => call[0] !== "/api/v1/speech/config");
            expect(configCalls).toHaveLength(1);
        });
    });
    describe("peopleSearchAvailable", () => {
        async function readPeopleSearchAvailable(data: unknown): Promise<boolean | undefined> {
            mockWebuiGet.mockResolvedValue(makeOkResponse(data));
            let captured: boolean | undefined;
            const Probe = () => {
                captured = useConfigContext().peopleSearchAvailable;
                return <span data-testid="probe">ready</span>;
            };
            renderWithQuery(<Probe />);
            await waitFor(() => expect(screen.getByTestId("probe")).toBeInTheDocument());
            return captured;
        }

        test("is false when the backend omits the field", async () => {
            expect(await readPeopleSearchAvailable(mockConfigData)).toBe(false);
        });

        test("passes through true from the backend", async () => {
            expect(await readPeopleSearchAvailable({ ...mockConfigData, peopleSearchAvailable: true })).toBe(true);
        });
    });
});
