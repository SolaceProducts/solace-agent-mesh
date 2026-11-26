// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { ConfigContextValue } from "@/providers/contexts/ConfigContext";
import { ConfigContext } from "@/providers/contexts/ConfigContext";

import { ArtifactsPage } from "@/features/chat/ArtifactsPage";

import { StoryProvider } from "@/testing/providers/StoryProvider";

const mockConfig: ConfigContextValue = {
    webuiServerUrl: "",
    platformServerUrl: "",
    configAuthLoginUrl: "",
    configUseAuthorization: false,
    configWelcomeMessage: "",
    configDisclaimerText: "",
    configRedirectUrl: "",
    configCollectFeedback: false,
    configAppName: "",
    configLogoUrl: "",
    configSmallLogoUrl: "",
    persistenceEnabled: true,
    backgroundTasksDefaultTimeoutMs: 3600000,
    platformConfigured: false,
    identityServiceType: null,
    claimKey: "",
    frontend_use_authorization: false,
    refetch: async () => {},
};

const defaultFlagValues: Record<string, boolean> = {
    artifacts_page: true,
    binary_artifact_preview: true,
};

vi.mock("react-pdf", () => ({
    Document: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
    Page: () => <div data-testid="pdf-page" />,
    pdfjs: { GlobalWorkerOptions: { workerSrc: "" } },
}));

const createdObservers: MockIntersectionObserver[] = [];

class MockIntersectionObserver {
    observe: ReturnType<typeof vi.fn>;
    unobserve = vi.fn();
    disconnect = vi.fn();
    static autoIntersect = true;

    constructor(callback: IntersectionObserverCallback) {
        createdObservers.push(this);
        this.observe = vi.fn().mockImplementation((element: Element) => {
            if (MockIntersectionObserver.autoIntersect) {
                callback([{ isIntersecting: true, target: element }] as IntersectionObserverEntry[], this as unknown as IntersectionObserver);
            }
        });
    }
}
Object.defineProperty(window, "IntersectionObserver", {
    writable: true,
    configurable: true,
    value: MockIntersectionObserver,
});

function makeImageArtifact(overrides: Record<string, unknown> = {}) {
    return {
        filename: "test-image.png",
        size: 1024,
        mimeType: "image/png",
        lastModified: "2026-01-01T00:00:00Z",
        uri: "/api/v1/artifacts/session1/test-image.png/versions/0",
        sessionId: "session1",
        sessionName: "Test Session",
        projectId: null,
        projectName: null,
        source: "upload",
        tags: null,
        ...overrides,
    };
}

function makeTextArtifact(overrides: Record<string, unknown> = {}) {
    return {
        filename: "data.json",
        size: 256,
        mimeType: "application/json",
        lastModified: "2026-01-02T00:00:00Z",
        uri: "/api/v1/artifacts/session2/data.json/versions/0",
        sessionId: "session2",
        sessionName: "Session Two",
        projectId: null,
        projectName: null,
        source: "generated",
        tags: null,
        ...overrides,
    };
}

function makeProjectArtifact(overrides: Record<string, unknown> = {}) {
    return {
        filename: "project-doc.md",
        size: 2048,
        mimeType: "text/markdown",
        lastModified: "2026-01-04T00:00:00Z",
        uri: "/api/v1/artifacts/null/project-doc.md/versions/0",
        sessionId: "project-abc123",
        sessionName: "Project Session",
        projectId: "abc123",
        projectName: "My Project",
        source: "project",
        tags: null,
        ...overrides,
    };
}

function makeInternalArtifact(overrides: Record<string, unknown> = {}) {
    return {
        filename: "web_content_internal.html",
        size: 128,
        mimeType: "text/html",
        lastModified: "2026-01-06T00:00:00Z",
        uri: "/api/v1/artifacts/session5/web_content_internal.html/versions/0",
        sessionId: "session5",
        sessionName: "Session Five",
        projectId: null,
        projectName: null,
        source: "generated",
        tags: ["__working"],
        ...overrides,
    };
}

function makeBinaryArtifact(overrides: Record<string, unknown> = {}) {
    return {
        filename: "archive.zip",
        size: 8192,
        mimeType: "application/zip",
        lastModified: "2026-01-07T00:00:00Z",
        uri: "/api/v1/artifacts/session6/archive.zip/versions/0",
        sessionId: "session6",
        sessionName: "Session Six",
        projectId: null,
        projectName: null,
        source: "upload",
        tags: null,
        ...overrides,
    };
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
let mockArtifactsList: Record<string, any>[] = [makeImageArtifact()];
const MOCK_TEXT_CONTENT = '{"key": "value", "nested": {"a": 1}}\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9extra';
let fetchShouldFail = false;

const mockFetch = vi.fn().mockImplementation((url: string) => {
    const urlStr = String(url);

    if (urlStr.includes("/api/v1/artifacts/all")) {
        const count = mockArtifactsList.length;
        const responseData = {
            data: mockArtifactsList,
            meta: { pagination: { pageNumber: 1, count, pageSize: count, nextPage: null, totalPages: 1 } },
        };
        return Promise.resolve({
            ok: true,
            status: 200,
            json: () => Promise.resolve(responseData),
            text: () => Promise.resolve(JSON.stringify(responseData)),
            blob: () => Promise.resolve(new Blob([])),
            headers: new Headers({ "content-type": "application/json" }),
        });
    }

    if (fetchShouldFail) {
        return Promise.reject(new Error("Network error"));
    }

    if (
        urlStr.includes("data.json") ||
        urlStr.includes("report.csv") ||
        urlStr.includes("project-doc.md") ||
        urlStr.includes("observer-test.json") ||
        urlStr.includes("config.yaml") ||
        urlStr.includes("data.xml") ||
        urlStr.includes("app.js") ||
        urlStr.includes("index.ts") ||
        urlStr.includes("internal.html")
    ) {
        return Promise.resolve({
            ok: true,
            status: 200,
            json: () => Promise.resolve({}),
            text: () => Promise.resolve(MOCK_TEXT_CONTENT),
            blob: () => Promise.resolve(new Blob([MOCK_TEXT_CONTENT], { type: "text/plain" })),
            headers: new Headers({ "content-type": "text/plain" }),
        });
    }

    return Promise.resolve({
        ok: true,
        status: 200,
        json: () => Promise.resolve({}),
        text: () => Promise.resolve(""),
        blob: () => Promise.resolve(new Blob(["image-data"], { type: "image/png" })),
        headers: new Headers({ "content-type": "image/png" }),
    });
});

const renderWithProviders = (configOverrides: Partial<ConfigContextValue> = {}, flagOverrides: Record<string, boolean> = {}) => {
    const config = { ...mockConfig, ...configOverrides };
    const flags = { ...defaultFlagValues, ...flagOverrides };
    return render(
        <MemoryRouter>
            <ConfigContext.Provider value={config}>
                <StoryProvider flagValues={flags}>
                    <ArtifactsPage />
                </StoryProvider>
            </ConfigContext.Provider>
        </MemoryRouter>
    );
};

async function waitForArtifactsLoaded(container: HTMLElement) {
    await act(async () => {
        await new Promise(resolve => setTimeout(resolve, 200));
    });
    await waitFor(
        () => {
            const html = container.innerHTML;
            if (html.includes("animate-spin")) throw new Error("Still loading");
        },
        { timeout: 3000 }
    );
}

describe("ArtifactsPage", () => {
    beforeEach(() => {
        vi.clearAllMocks();
        createdObservers.length = 0;
        MockIntersectionObserver.autoIntersect = true;
        fetchShouldFail = false;
        mockArtifactsList = [makeImageArtifact()];
        vi.stubGlobal("fetch", mockFetch);
        Object.defineProperty(window, "URL", {
            writable: true,
            value: {
                ...URL,
                createObjectURL: vi.fn().mockReturnValue("blob:mock-url"),
                revokeObjectURL: vi.fn(),
            },
        });
    });

    afterEach(() => {
        vi.restoreAllMocks();
        vi.unstubAllGlobals();
    });

    it("renders artifact grid with image artifact", async () => {
        const { container } = renderWithProviders();
        await waitForArtifactsLoaded(container);
        expect(container.innerHTML).toContain("test-image.png");
    });

    it("loads image preview via authenticated API (blob URL code path)", async () => {
        renderWithProviders();
        await act(async () => {
            await new Promise(resolve => setTimeout(resolve, 500));
        });
        const fetchCalls = mockFetch.mock.calls.map(c => String(c[0]));
        expect(fetchCalls.some(url => url.includes("test-image.png"))).toBe(true);
    });

    it("revokes blob URL on unmount (cleanup code path)", async () => {
        const { unmount } = renderWithProviders();
        await act(async () => {
            await new Promise(resolve => setTimeout(resolve, 500));
        });
        await act(async () => {
            unmount();
        });
        expect(window.URL.revokeObjectURL).toHaveBeenCalledWith("blob:mock-url");
    });

    it("fetches and displays text preview for JSON artifacts", async () => {
        mockArtifactsList = [makeTextArtifact()];
        const { container } = renderWithProviders();
        await act(async () => {
            await new Promise(resolve => setTimeout(resolve, 500));
        });
        const fetchCalls = mockFetch.mock.calls.map(c => String(c[0]));
        expect(fetchCalls.some(url => url.includes("data.json"))).toBe(true);
        expect(fetchCalls.some(url => url.includes("data.json") && url.includes("maxBytes=2048"))).toBe(true);
        expect(container.innerHTML).toContain("data.json");
    });

    it("caches text preview and serves from cache on re-render", async () => {
        mockArtifactsList = [makeTextArtifact()];
        const { unmount, container } = renderWithProviders();
        await act(async () => {
            await new Promise(resolve => setTimeout(resolve, 500));
        });
        expect(container.innerHTML).toContain("data.json");

        const firstRenderFetchCount = mockFetch.mock.calls.filter(c => String(c[0]).includes("data.json")).length;

        await act(async () => {
            unmount();
        });

        const { container: container2 } = renderWithProviders();
        await act(async () => {
            await new Promise(resolve => setTimeout(resolve, 500));
        });
        expect(container2.innerHTML).toContain("data.json");

        const secondRenderFetchCount = mockFetch.mock.calls.filter(c => String(c[0]).includes("data.json")).length;
        expect(secondRenderFetchCount).toBe(firstRenderFetchCount);
    });

    it("uses project_id query param for project artifacts", async () => {
        mockArtifactsList = [makeProjectArtifact()];
        const { container } = renderWithProviders();
        await act(async () => {
            await new Promise(resolve => setTimeout(resolve, 500));
        });
        const fetchCalls = mockFetch.mock.calls.map(c => String(c[0]));
        const projectFetch = fetchCalls.find(url => url.includes("projectId="));
        expect(projectFetch).toBeDefined();
        expect(projectFetch).toContain("projectId=abc123");
        expect(container.innerHTML).toContain("project-doc.md");
    });

    it("lists a project with no loaded artifacts in the Project filter", async () => {
        Element.prototype.hasPointerCapture = vi.fn(() => false);
        Element.prototype.setPointerCapture = vi.fn();
        Element.prototype.releasePointerCapture = vi.fn();

        mockArtifactsList = [makeImageArtifact()];
        const projects = [{ id: "empty-proj-1", name: "Artifactless Project", userId: "u1", createdAt: "2026-01-01T00:00:00Z" }];

        const { container, getAllByRole, findByText } = render(
            <MemoryRouter>
                <ConfigContext.Provider value={mockConfig}>
                    <StoryProvider flagValues={defaultFlagValues} projectContextValues={{ projects }}>
                        <ArtifactsPage />
                    </StoryProvider>
                </ConfigContext.Provider>
            </MemoryRouter>
        );
        await waitForArtifactsLoaded(container);

        const projectCombobox = getAllByRole("combobox")[0];
        await act(async () => {
            fireEvent.pointerDown(projectCombobox, { button: 0, pointerType: "mouse" });
            fireEvent.pointerUp(projectCombobox, { button: 0, pointerType: "mouse" });
            fireEvent.click(projectCombobox);
        });

        const option = await findByText("Artifactless Project");
        expect(option).toBeInTheDocument();

        await act(async () => {
            fireEvent.click(option);
        });
        await waitFor(() => {
            const calls = mockFetch.mock.calls.map(c => String(c[0]));
            expect(calls.some(url => url.includes("/api/v1/artifacts/all") && url.includes("projectId=empty-proj-1"))).toBe(true);
        });
    });

    it("sends the __no_project__ sentinel, which must match the server's unassignedProjectFilter, when '(No Project)' is selected", async () => {
        Element.prototype.hasPointerCapture = vi.fn(() => false);
        Element.prototype.setPointerCapture = vi.fn();
        Element.prototype.releasePointerCapture = vi.fn();

        mockArtifactsList = [makeImageArtifact()];
        const projects = [{ id: "proj-1", name: "Some Project", userId: "u1", createdAt: "2026-01-01T00:00:00Z" }];

        const { container, getAllByRole, findByText } = render(
            <MemoryRouter>
                <ConfigContext.Provider value={mockConfig}>
                    <StoryProvider flagValues={defaultFlagValues} projectContextValues={{ projects }}>
                        <ArtifactsPage />
                    </StoryProvider>
                </ConfigContext.Provider>
            </MemoryRouter>
        );
        await waitForArtifactsLoaded(container);

        const projectCombobox = getAllByRole("combobox")[0];
        await act(async () => {
            fireEvent.pointerDown(projectCombobox, { button: 0, pointerType: "mouse" });
            fireEvent.pointerUp(projectCombobox, { button: 0, pointerType: "mouse" });
            fireEvent.click(projectCombobox);
        });

        const noProjectOption = await findByText("(No Project)");
        await act(async () => {
            fireEvent.click(noProjectOption);
        });
        await waitFor(() => {
            const calls = mockFetch.mock.calls.map(c => String(c[0]));
            expect(calls.some(url => url.includes("/api/v1/artifacts/all") && url.includes("projectId=__no_project__"))).toBe(true);
        });
    });

    it("shows the 'no matches for your filter' empty state when a selected project has no artifacts", async () => {
        Element.prototype.hasPointerCapture = vi.fn(() => false);
        Element.prototype.setPointerCapture = vi.fn();
        Element.prototype.releasePointerCapture = vi.fn();

        const emptyOnProjectFetch = vi.fn().mockImplementation((url: string) => {
            const u = String(url);
            if (u.includes("/api/v1/artifacts/all")) {
                const list = u.includes("projectId=") ? [] : [makeImageArtifact()];
                const count = list.length;
                const body = { data: list, meta: { pagination: { pageNumber: 1, count, pageSize: 50, nextPage: null, totalPages: 1 } } };
                return Promise.resolve({
                    ok: true,
                    status: 200,
                    json: () => Promise.resolve(body),
                    text: () => Promise.resolve(JSON.stringify(body)),
                    blob: () => Promise.resolve(new Blob([])),
                    headers: new Headers({ "content-type": "application/json" }),
                });
            }
            return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve({}), text: () => Promise.resolve(""), blob: () => Promise.resolve(new Blob([])), headers: new Headers() });
        });
        vi.stubGlobal("fetch", emptyOnProjectFetch);

        const projects = [{ id: "proj-1", name: "Some Project", userId: "u1", createdAt: "2026-01-01T00:00:00Z" }];
        const { container, getAllByRole, findByText } = render(
            <MemoryRouter>
                <ConfigContext.Provider value={mockConfig}>
                    <StoryProvider flagValues={defaultFlagValues} projectContextValues={{ projects }}>
                        <ArtifactsPage />
                    </StoryProvider>
                </ConfigContext.Provider>
            </MemoryRouter>
        );
        await waitForArtifactsLoaded(container);

        const projectCombobox = getAllByRole("combobox")[0];
        await act(async () => {
            fireEvent.pointerDown(projectCombobox, { button: 0, pointerType: "mouse" });
            fireEvent.pointerUp(projectCombobox, { button: 0, pointerType: "mouse" });
            fireEvent.click(projectCombobox);
        });
        const option = await findByText("Some Project");
        await act(async () => {
            fireEvent.click(option);
        });

        await waitFor(() => {
            expect(container.textContent).toContain("No artifacts found matching your");
        });
        expect(container.textContent).not.toContain("No artifacts available");
    });

    it("creates IntersectionObserver for text artifacts that need preview fetch", async () => {
        mockArtifactsList = [
            makeTextArtifact({
                sessionId: "session-observer-test",
                filename: "observer-test.json",
                uri: "/api/v1/artifacts/session-observer-test/observer-test.json/versions/0",
            }),
        ];
        const { container } = renderWithProviders();
        await waitForArtifactsLoaded(container);
        expect(createdObservers.length).toBeGreaterThan(0);
    });

    it("renders binary artifacts without needing IntersectionObserver for preview", async () => {
        mockArtifactsList = [makeBinaryArtifact()];
        const observerCountBefore = createdObservers.length;
        const { container } = renderWithProviders();
        await act(async () => {
            await new Promise(resolve => setTimeout(resolve, 300));
        });
        expect(container.innerHTML).toContain("archive.zip");
        expect(createdObservers.length).toBe(observerCountBefore);
    });

    it("defers preview fetch until card is visible when autoIntersect is off", async () => {
        MockIntersectionObserver.autoIntersect = false;
        mockArtifactsList = [makeTextArtifact()];
        const { container } = renderWithProviders();
        await act(async () => {
            await new Promise(resolve => setTimeout(resolve, 500));
        });
        const textFetches = mockFetch.mock.calls.filter(c => String(c[0]).includes("data.json"));
        expect(textFetches.length).toBe(0);
        expect(container.innerHTML).toContain("data.json");
    });

    it("handles fetch errors gracefully without crashing", async () => {
        fetchShouldFail = true;
        mockArtifactsList = [makeTextArtifact()];
        const consoleSpy = vi.spyOn(console, "error").mockImplementation(() => {});
        const { container } = renderWithProviders();
        await act(async () => {
            await new Promise(resolve => setTimeout(resolve, 500));
        });
        expect(container.innerHTML).toContain("data.json");
        consoleSpy.mockRestore();
    });

    it("renders multiple artifact types together (image, text, project, binary)", async () => {
        mockArtifactsList = [makeImageArtifact(), makeTextArtifact(), makeProjectArtifact(), makeBinaryArtifact()];
        const { container } = renderWithProviders();
        await act(async () => {
            await new Promise(resolve => setTimeout(resolve, 500));
        });
        expect(container.innerHTML).toContain("test-image.png");
        expect(container.innerHTML).toContain("data.json");
        expect(container.innerHTML).toContain("project-doc.md");
        expect(container.innerHTML).toContain("archive.zip");
    });

    it("hides internal artifacts by default", async () => {
        mockArtifactsList = [makeImageArtifact(), makeInternalArtifact()];
        const { container } = renderWithProviders();
        await waitForArtifactsLoaded(container);
        expect(container.innerHTML).toContain("test-image.png");
        expect(container.innerHTML).not.toContain("web_content_internal.html");
    });

    it("shows project filter when artifacts have project names", async () => {
        mockArtifactsList = [makeImageArtifact(), makeProjectArtifact()];
        const { container } = renderWithProviders();
        await act(async () => {
            await new Promise(resolve => setTimeout(resolve, 500));
        });
        expect(container.innerHTML).toContain("My Project");
    });

    it("handles Enter key on artifact card", async () => {
        mockArtifactsList = [makeImageArtifact()];
        const { container } = renderWithProviders();
        await waitForArtifactsLoaded(container);
        const cards = container.querySelectorAll('[role="button"]');
        expect(cards.length).toBeGreaterThan(0);

        expect(container.innerHTML).not.toContain("Download");

        await act(async () => {
            fireEvent.keyDown(cards[0], { key: "Enter" });
        });

        await waitFor(() => {
            expect(container.querySelector('[class*="border-l"]')).not.toBeNull();
        });
    });

    it("handles Space key on artifact card", async () => {
        mockArtifactsList = [makeImageArtifact()];
        const { container } = renderWithProviders();
        await waitForArtifactsLoaded(container);
        const cards = container.querySelectorAll('[role="button"]');
        expect(cards.length).toBeGreaterThan(0);

        await act(async () => {
            fireEvent.keyDown(cards[0], { key: " " });
        });

        await waitFor(() => {
            expect(container.querySelector('[class*="border-l"]')).not.toBeNull();
        });
    });

    it("closes the preview and returns to the artifact list when the previewed artifact is deleted", async () => {
        mockArtifactsList = [makeImageArtifact()];
        vi.stubGlobal("fetch", (url: string, init?: RequestInit) => {
            if (init?.method === "DELETE") {
                mockArtifactsList = [];
                return Promise.resolve({ ok: true, status: 204, text: () => Promise.resolve(""), json: () => Promise.resolve({}), headers: new Headers() });
            }
            return mockFetch(url);
        });
        const { container } = renderWithProviders();
        await waitForArtifactsLoaded(container);

        await act(async () => {
            fireEvent.keyDown(container.querySelectorAll('[role="button"]')[0], { key: "Enter" });
        });
        await waitFor(() => expect(screen.getByText("Go to Chat")).toBeInTheDocument());

        const user = userEvent.setup();
        await user.click(container.querySelector('button[aria-haspopup="menu"]')!);
        await user.click(await screen.findByRole("menuitem", { name: "Delete" }));
        await user.click(await screen.findByRole("button", { name: "Delete" }));

        await waitFor(() => expect(screen.getByText("No artifacts available")).toBeInTheDocument());
        expect(screen.queryByText("Go to Chat")).not.toBeInTheDocument();
    });

    it("shows empty state when no artifacts exist", async () => {
        mockArtifactsList = [];
        const { container } = renderWithProviders();
        await act(async () => {
            await new Promise(resolve => setTimeout(resolve, 500));
        });
        expect(container.innerHTML).toContain("No artifacts available");
    });

    it("displays artifact count", async () => {
        mockArtifactsList = [makeImageArtifact(), makeTextArtifact()];
        const { container } = renderWithProviders();
        await act(async () => {
            await new Promise(resolve => setTimeout(resolve, 500));
        });
        expect(container.innerHTML).toContain("2 artifact");
    });

    it.each([
        { filename: "config.yaml", mimeType: "application/x-yaml" },
        { filename: "data.xml", mimeType: "application/xml" },
        { filename: "app.js", mimeType: "application/javascript" },
        { filename: "index.ts", mimeType: "application/typescript" },
    ])("renders text preview for $filename", async ({ filename, mimeType }) => {
        const sessionId = `session-${filename}`;
        mockArtifactsList = [
            makeTextArtifact({
                filename,
                mimeType,
                sessionId,
                uri: `/api/v1/artifacts/${sessionId}/${filename}/versions/0`,
            }),
        ];
        const { container } = renderWithProviders();
        await waitFor(() => expect(container.innerHTML).toContain(filename), { timeout: 2000 });
        await waitFor(() => expect(container.innerHTML).toContain('"key"'), { timeout: 2000 });
    });

    it("shows the empty state when there are no artifacts at all", async () => {
        mockArtifactsList = [];
        const { container } = renderWithProviders();
        await waitForArtifactsLoaded(container);
        expect(container.innerHTML).toContain("No artifacts available");
    });

    it("filters artifacts by the search input", async () => {
        mockArtifactsList = [makeImageArtifact(), makeBinaryArtifact()];
        const { container, getByPlaceholderText } = renderWithProviders();
        await waitForArtifactsLoaded(container);
        expect(container.innerHTML).toContain("test-image.png");
        expect(container.innerHTML).toContain("archive.zip");

        fireEvent.change(getByPlaceholderText("Search artifacts..."), { target: { value: "image" } });

        await waitFor(() => {
            expect(container.innerHTML).toContain("test-image.png");
            expect(container.innerHTML).not.toContain("archive.zip");
        });
    });

    it("reverses artifact order when the sort direction is toggled", async () => {
        const older = makeImageArtifact({ filename: "older.png", lastModified: "2026-01-01T00:00:00Z", uri: "/api/v1/artifacts/s1/older.png/versions/0" });
        const newer = makeImageArtifact({ filename: "newer.png", lastModified: "2026-06-01T00:00:00Z", uri: "/api/v1/artifacts/s1/newer.png/versions/0" });
        mockArtifactsList = [older, newer];

        const { container, getByTitle } = renderWithProviders();
        await waitForArtifactsLoaded(container);

        const initialHtml = container.innerHTML;
        expect(initialHtml.indexOf("newer.png")).toBeLessThan(initialHtml.indexOf("older.png"));

        fireEvent.click(getByTitle("Descending"));

        await waitFor(() => {
            const afterHtml = container.innerHTML;
            expect(afterHtml.indexOf("older.png")).toBeLessThan(afterHtml.indexOf("newer.png"));
        });
    });
});
