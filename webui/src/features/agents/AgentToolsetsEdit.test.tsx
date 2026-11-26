// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";
import type { Agent, Toolset } from "@/types";

import { AgentToolsetsEdit } from "./AgentToolsetsEdit";

const WEB = "builtin_web_request_tools";

const webToolset = (over: Partial<Toolset> = {}): Partial<Toolset> => ({
    id: WEB,
    name: "Web Request Tools",
    description: "Make HTTP requests to external services",
    type: "builtin",
    tools: [{ toolName: "web_request" }, { toolName: "web_search_google" }],
    ...over,
});

let builtinToolsets: Partial<Toolset>[] = [webToolset()];
let builtinToolsetsSuccess = true;

let customToolsets: unknown[] = [];
let customToolsetsSuccess = true;

const toolsetsApi = vi.hoisted(() => ({ create: vi.fn(async () => ({ id: "ts-new" })), uploadBundle: vi.fn(async () => {}), delete: vi.fn(async () => {}) }));

vi.mock("@/api/toolsets", () => ({
    toolsetsApi,
    useBuiltinToolsets: () => ({ data: builtinToolsets, isLoading: false, isSuccess: builtinToolsetsSuccess }),
    useCustomToolsets: () => ({ data: customToolsets, isLoading: false, isSuccess: customToolsetsSuccess, refetch: vi.fn() }),
}));

vi.mock("@/features/toolsets/ToolsetCreateDialog", () => ({
    ToolsetCreateDialog: ({ isOpen, onCreate }: { isOpen: boolean; onCreate: (name: string, description: string) => Promise<void> }) =>
        isOpen ? (
            <button type="button" onClick={() => void onCreate("order_tools", "Looks up customer orders.")}>
                confirm create
            </button>
        ) : null,
}));
vi.mock("@/features/toolsets/ToolsetZipUploadDialog", () => ({
    ToolsetZipUploadDialog: ({ isOpen, onFileSelected }: { isOpen: boolean; onFileSelected?: (file: File) => void }) =>
        isOpen ? (
            <button type="button" onClick={() => onFileSelected?.(new File(["PK"], "order_tools.zip", { type: "application/zip" }))}>
                pick zip
            </button>
        ) : null,
}));

function makeAgent(over: Partial<Agent> = {}): Agent {
    return {
        id: "a1",
        name: "DocFetcher",
        description: "Fetches docs",
        systemPrompt: "p",
        toolsets: [WEB],
        ...over,
    } as Agent;
}

function renderEdit(agent: Agent) {
    const onApply = vi.fn();
    const view = renderWithProviders(<AgentToolsetsEdit agent={agent} onApply={onApply} onDiscard={vi.fn()} />, { initialPath: "/agent-mesh" });
    return { onApply, ...view };
}

function toolCheckbox(getByTestId: (id: string) => HTMLElement, toolName: string): HTMLInputElement {
    return within(getByTestId(`tool-row-${toolName}`)).getByRole("checkbox", { name: toolName }) as HTMLInputElement;
}

afterEach(() => {
    builtinToolsets = [webToolset()];
    builtinToolsetsSuccess = true;
    customToolsets = [];
    customToolsetsSuccess = true;
    vi.restoreAllMocks();
});

describe("AgentToolsetsEdit — built-in toolset exclusions", () => {
    test("a narrowed built-in toolset is visibly marked", () => {
        const { getByTestId } = renderEdit(makeAgent({ toolsetExcludedTools: { [WEB]: ["web_search_google"] } }));

        expect(getByTestId(`builtin-narrowed-${WEB}`)).toHaveTextContent("1 of 2 tools");
    });

    test("a fully-granted toolset has no marker and every tool checked", () => {
        const { queryByTestId, getByTestId } = renderEdit(makeAgent());

        expect(queryByTestId(`builtin-narrowed-${WEB}`)).toBeNull();
        fireEvent.click(getByTestId(`expand-toolset-${WEB}`));
        expect(toolCheckbox(getByTestId, "web_request").checked).toBe(true);
        expect(toolCheckbox(getByTestId, "web_search_google").checked).toBe(true);
    });

    test("the per-tool list shows which tools the exclusion removed", () => {
        const { getByTestId } = renderEdit(makeAgent({ toolsetExcludedTools: { [WEB]: ["web_search_google"] } }));

        fireEvent.click(getByTestId(`expand-toolset-${WEB}`));

        expect(toolCheckbox(getByTestId, "web_request").checked).toBe(true);
        expect(toolCheckbox(getByTestId, "web_search_google").checked).toBe(false);
    });

    test("an existing exclusion survives a submit that does not touch it", async () => {
        const { onApply, getByTestId } = renderEdit(makeAgent({ toolsetExcludedTools: { [WEB]: ["web_search_google"] } }));

        fireEvent.click(getByTestId("agentApplyButton"));

        await waitFor(() => expect(onApply).toHaveBeenCalledWith(expect.objectContaining({ toolsets: [WEB], toolsetExcludedTools: { [WEB]: ["web_search_google"] } })));
    });

    test("unchecking a tool narrows the toolset and reaches onApply", async () => {
        const { onApply, getByTestId } = renderEdit(makeAgent());

        fireEvent.click(getByTestId(`expand-toolset-${WEB}`));
        fireEvent.click(toolCheckbox(getByTestId, "web_search_google"));
        fireEvent.click(getByTestId("agentApplyButton"));

        await waitFor(() => expect(onApply).toHaveBeenCalledWith(expect.objectContaining({ toolsetExcludedTools: { [WEB]: ["web_search_google"] } })));
    });

    test("re-checking every tool clears the exclusion rather than sending an empty list", async () => {
        const { onApply, getByTestId } = renderEdit(makeAgent({ toolsetExcludedTools: { [WEB]: ["web_search_google"] } }));

        fireEvent.click(getByTestId(`expand-toolset-${WEB}`));
        fireEvent.click(toolCheckbox(getByTestId, "web_search_google"));
        fireEvent.click(getByTestId("agentApplyButton"));

        await waitFor(() => expect(onApply).toHaveBeenCalledWith(expect.objectContaining({ toolsetExcludedTools: {} })));
    });
});

describe("AgentToolsetsEdit — unselected built-in toolsets", () => {
    test("an unselected toolset can be expanded and shows no tools checked", () => {
        const { getByTestId } = renderEdit(makeAgent({ toolsets: [] }));

        fireEvent.click(getByTestId(`expand-toolset-${WEB}`));

        expect(toolCheckbox(getByTestId, "web_request").checked).toBe(false);
        expect(toolCheckbox(getByTestId, "web_search_google").checked).toBe(false);
    });

    test("checking one tool selects the toolset with only that tool", async () => {
        const { onApply, getByTestId } = renderEdit(makeAgent({ toolsets: [] }));

        fireEvent.click(getByTestId(`expand-toolset-${WEB}`));
        fireEvent.click(toolCheckbox(getByTestId, "web_request"));
        fireEvent.click(getByTestId("agentApplyButton"));

        await waitFor(() => expect(onApply).toHaveBeenCalledWith(expect.objectContaining({ toolsets: [WEB], toolsetExcludedTools: { [WEB]: ["web_search_google"] } })));
    });

    test("enable all selects the toolset with every tool granted", async () => {
        const { onApply, getByTestId, getByLabelText } = renderEdit(makeAgent({ toolsets: [] }));

        fireEvent.click(getByTestId(`expand-toolset-${WEB}`));
        fireEvent.click(getByLabelText("Enable all"));
        fireEvent.click(getByTestId("agentApplyButton"));

        await waitFor(() => expect(onApply).toHaveBeenCalledWith(expect.objectContaining({ toolsets: [WEB], toolsetExcludedTools: {} })));
    });

    test("checking the only tool of a single-tool toolset grants it without an empty exclusion", async () => {
        builtinToolsets = [webToolset({ tools: [{ toolName: "web_request" }] })];
        const { onApply, getByTestId } = renderEdit(makeAgent({ toolsets: [] }));

        fireEvent.click(getByTestId(`expand-toolset-${WEB}`));
        fireEvent.click(toolCheckbox(getByTestId, "web_request"));
        fireEvent.click(getByTestId("agentApplyButton"));

        await waitFor(() => expect(onApply).toHaveBeenCalledWith(expect.objectContaining({ toolsets: [WEB], toolsetExcludedTools: {} })));
    });

    test("re-binding a toolset with a leftover exclusion grants every tool", async () => {
        const { onApply, getByTestId, getByRole } = renderEdit(makeAgent({ toolsets: [], toolsetExcludedTools: { [WEB]: ["web_search_google"] } }));

        fireEvent.click(getByRole("checkbox", { name: "Web Request Tools" }));
        fireEvent.click(getByTestId("agentApplyButton"));

        await waitFor(() => expect(onApply).toHaveBeenCalledWith(expect.objectContaining({ toolsets: [WEB], toolsetExcludedTools: {} })));
    });

    test("an exclusion for an unbound toolset is not submitted", async () => {
        const { onApply, getByTestId } = renderEdit(makeAgent({ toolsets: [], toolsetExcludedTools: { [WEB]: ["web_search_google"] } }));

        fireEvent.click(getByTestId("agentApplyButton"));

        await waitFor(() => expect(onApply).toHaveBeenCalledWith(expect.objectContaining({ toolsets: [], toolsetExcludedTools: {} })));
    });
});

describe("AgentToolsetsEdit — granting no tools unbinds the toolset", () => {
    test("unchecking the last granted tool unbinds the toolset", async () => {
        const { onApply, getByTestId, getByRole } = renderEdit(makeAgent({ toolsetExcludedTools: { [WEB]: ["web_search_google"] } }));

        fireEvent.click(getByTestId(`expand-toolset-${WEB}`));
        fireEvent.click(toolCheckbox(getByTestId, "web_request"));

        expect((getByRole("checkbox", { name: "Web Request Tools" }) as HTMLInputElement).checked).toBe(false);
        fireEvent.click(getByTestId("agentApplyButton"));
        await waitFor(() => expect(onApply).toHaveBeenCalledWith(expect.objectContaining({ toolsets: [], toolsetExcludedTools: {} })));
    });

    test("clearing enable all unbinds the toolset", async () => {
        const { onApply, getByTestId, getByLabelText } = renderEdit(makeAgent());

        fireEvent.click(getByTestId(`expand-toolset-${WEB}`));
        fireEvent.click(getByLabelText("Enable all"));
        fireEvent.click(getByTestId("agentApplyButton"));

        await waitFor(() => expect(onApply).toHaveBeenCalledWith(expect.objectContaining({ toolsets: [], toolsetExcludedTools: {} })));
    });

    test("re-checking a tool after unbinding re-binds with only that tool", async () => {
        const { onApply, getByTestId } = renderEdit(makeAgent({ toolsetExcludedTools: { [WEB]: ["web_search_google"] } }));

        fireEvent.click(getByTestId(`expand-toolset-${WEB}`));
        fireEvent.click(toolCheckbox(getByTestId, "web_request"));
        fireEvent.click(toolCheckbox(getByTestId, "web_search_google"));
        fireEvent.click(getByTestId("agentApplyButton"));

        await waitFor(() => expect(onApply).toHaveBeenCalledWith(expect.objectContaining({ toolsets: [WEB], toolsetExcludedTools: { [WEB]: ["web_request"] } })));
    });
});

describe("AgentToolsetsEdit — exclusions naming tools outside the group", () => {
    test("the count reflects the tools actually granted, never a negative", () => {
        const { getByTestId } = renderEdit(makeAgent({ toolsetExcludedTools: { [WEB]: ["web_search_google", "removed_in_a_later_release", "typo"] } }));

        expect(getByTestId(`builtin-narrowed-${WEB}`)).toHaveTextContent("1 of 2 tools");
    });

    test("a stale exclusion alone does not mark an otherwise-full grant as narrowed", () => {
        const { queryByTestId } = renderEdit(makeAgent({ toolsetExcludedTools: { [WEB]: ["gone_in_v2"] } }));

        expect(queryByTestId(`builtin-narrowed-${WEB}`)).toBeNull();
    });

    test("toggling a listed tool keeps a stale exclusion", async () => {
        const { onApply, getByTestId } = renderEdit(makeAgent({ toolsetExcludedTools: { [WEB]: ["web_search_google", "gone_in_v2"] } }));

        fireEvent.click(getByTestId(`expand-toolset-${WEB}`));
        fireEvent.click(toolCheckbox(getByTestId, "web_search_google"));
        fireEvent.click(getByTestId("agentApplyButton"));

        await waitFor(() => expect(onApply).toHaveBeenCalledWith(expect.objectContaining({ toolsets: [WEB], toolsetExcludedTools: { [WEB]: ["gone_in_v2"] } })));
    });

    test("enable all on a narrowed toolset keeps a stale exclusion", async () => {
        const { onApply, getByTestId, getByLabelText } = renderEdit(makeAgent({ toolsetExcludedTools: { [WEB]: ["web_search_google", "gone_in_v2"] } }));

        fireEvent.click(getByTestId(`expand-toolset-${WEB}`));
        fireEvent.click(getByLabelText("Enable all"));
        fireEvent.click(getByTestId("agentApplyButton"));

        await waitFor(() => expect(onApply).toHaveBeenCalledWith(expect.objectContaining({ toolsets: [WEB], toolsetExcludedTools: { [WEB]: ["gone_in_v2"] } })));
    });

    test("a stale exclusion does not count as a granted tool, so unchecking the last listed tool still unbinds", async () => {
        const { onApply, getByTestId } = renderEdit(makeAgent({ toolsetExcludedTools: { [WEB]: ["web_search_google", "gone_in_v2"] } }));

        fireEvent.click(getByTestId(`expand-toolset-${WEB}`));
        fireEvent.click(toolCheckbox(getByTestId, "web_request"));
        fireEvent.click(getByTestId("agentApplyButton"));

        await waitFor(() => expect(onApply).toHaveBeenCalledWith(expect.objectContaining({ toolsets: [], toolsetExcludedTools: {} })));
    });
});

describe("AgentToolsetsEdit — toolsets that advertise no tools", () => {
    test("a toolset with no tools is bindable but not expandable", () => {
        builtinToolsets = [webToolset({ tools: [] })];
        const { queryByTestId, getByText } = renderEdit(makeAgent());

        expect(getByText("Web Request Tools")).toBeInTheDocument();
        expect(queryByTestId(`expand-toolset-${WEB}`)).toBeNull();
        expect(queryByTestId(`builtin-narrowed-${WEB}`)).toBeNull();
    });
});

describe("AgentToolsetsEdit — a toolset ref the catalog carries no row for", () => {
    test("still renders, checked, with a humanized fallback label", () => {
        const { getByRole } = renderEdit(makeAgent({ toolsets: ["activity_reporting"] }));

        const checkbox = getByRole("checkbox", { name: "Activity Reporting" }) as HTMLInputElement;
        expect(checkbox.checked).toBe(true);
    });

    test("cannot be unchecked — there is no catalog row to re-add it from", () => {
        const { getByRole } = renderEdit(makeAgent({ toolsets: ["activity_reporting"] }));

        const checkbox = getByRole("checkbox", { name: "Activity Reporting" }) as HTMLInputElement;
        expect(checkbox.disabled).toBe(true);
    });

    test("survives a submit untouched", async () => {
        const { onApply, getByTestId } = renderEdit(makeAgent({ toolsets: ["activity_reporting"] }));

        fireEvent.click(getByTestId("agentApplyButton"));

        await waitFor(() => expect(onApply).toHaveBeenCalledWith(expect.objectContaining({ toolsets: ["activity_reporting"] })));
    });

    test("a resolvable toolset alongside it still renders normally", () => {
        const { getByRole } = renderEdit(makeAgent({ toolsets: [WEB, "activity_reporting"] }));

        expect((getByRole("checkbox", { name: "Web Request Tools" }) as HTMLInputElement).disabled).toBe(false);
        expect((getByRole("checkbox", { name: "Activity Reporting" }) as HTMLInputElement).disabled).toBe(true);
    });
});

const VOLUME = "builtin_volume_tools";

const volumeToolset = (): Partial<Toolset> => ({
    id: VOLUME,
    name: "Volume Tools",
    description: "Read, write, search, and manage files in a mounted SAM volume",
    type: "builtin",
    tools: [{ toolName: "volume_read" }, { toolName: "volume_write" }],
});

describe("AgentToolsetsEdit — toolset config bindings survive Apply", () => {
    const bindings = [
        {
            toolsetName: VOLUME,
            configValues: { volume_read: { encoding: "utf-8" } },
            volumeBindings: { workspace: "shared_ws" },
        },
    ];

    const renderVolumeAgent = (over: Partial<Agent> = {}) => {
        builtinToolsets = [volumeToolset()];
        return renderEdit(makeAgent({ toolsets: [VOLUME], toolsetConfigs: bindings, ...over }));
    };

    test("volume bindings are round-tripped, not dropped", async () => {
        const { onApply, getByTestId } = renderVolumeAgent();

        fireEvent.click(getByTestId("agentApplyButton"));

        await waitFor(() =>
            expect(onApply).toHaveBeenCalledWith(
                expect.objectContaining({
                    toolsetConfigs: [{ toolsetName: VOLUME, configValues: { volume_read: { encoding: "utf-8" } }, volumeBindings: { workspace: "shared_ws" } }],
                })
            )
        );
    });

    test("narrowing the toolset keeps the volume binding intact", async () => {
        const { onApply, getByTestId } = renderVolumeAgent();

        fireEvent.click(getByTestId(`expand-toolset-${VOLUME}`));
        fireEvent.click(toolCheckbox(getByTestId, "volume_write"));
        fireEvent.click(getByTestId("agentApplyButton"));

        await waitFor(() =>
            expect(onApply).toHaveBeenCalledWith(
                expect.objectContaining({
                    toolsetExcludedTools: { [VOLUME]: ["volume_write"] },
                    toolsetConfigs: [expect.objectContaining({ volumeBindings: { workspace: "shared_ws" } })],
                })
            )
        );
    });

    test("unbinding a configured toolset clears its config rather than preserving it", async () => {
        const { onApply, getByTestId, getByRole } = renderVolumeAgent();

        fireEvent.click(getByRole("checkbox", { name: "Volume Tools" }));
        fireEvent.click(getByTestId("agentApplyButton"));

        await waitFor(() => expect(onApply).toHaveBeenCalledWith(expect.objectContaining({ toolsets: [], toolsetConfigs: [] })));
    });

    test("an editor that never had a binding still omits the field", async () => {
        builtinToolsets = [volumeToolset()];
        const { onApply, getByTestId } = renderEdit(makeAgent({ toolsets: [VOLUME] }));

        fireEvent.click(getByTestId("agentApplyButton"));

        await waitFor(() => expect(onApply).toHaveBeenCalledWith(expect.objectContaining({ toolsetConfigs: undefined })));
    });
});

const PKG = "acme-tools";

describe("AgentToolsetsEdit — editing one tool's config", () => {
    const renderPackageAgent = (configValues: Record<string, Record<string, unknown>>, over: Partial<Agent> = {}) => {
        builtinToolsets = [];
        customToolsets = [
            {
                id: "pkg-1",
                name: PKG,
                description: "Vendor tools",
                type: "custom",
                discoveryStatus: "ready",
                files: [
                    { toolName: "fetch", toolDescription: "Fetch a record", configSchema: [{ key: "region", type: "string" }] },
                    { toolName: "push", toolDescription: "Push a record", configSchema: [{ key: "endpoint", type: "string" }] },
                ],
            },
        ];
        return renderEdit(makeAgent({ toolsets: [PKG], toolsetConfigs: [{ toolsetName: PKG, configValues }], ...over }));
    };

    test("checking one tool of an unselected custom toolset binds it with only that tool", async () => {
        const { onApply, getByTestId } = renderPackageAgent({}, { toolsets: [] });

        fireEvent.click(getByTestId(`expand-toolset-${PKG}`));
        expect(toolCheckbox(getByTestId, "fetch").checked).toBe(false);
        fireEvent.click(toolCheckbox(getByTestId, "fetch"));
        fireEvent.click(getByTestId("agentApplyButton"));

        await waitFor(() => expect(onApply).toHaveBeenCalledWith(expect.objectContaining({ toolsets: [PKG], toolsetExcludedTools: { [PKG]: ["push"] } })));
    });

    test("enable all on an unselected custom toolset binds it with every tool granted", async () => {
        const { onApply, getByTestId, getByLabelText } = renderPackageAgent({}, { toolsets: [] });

        fireEvent.click(getByTestId(`expand-toolset-${PKG}`));
        fireEvent.click(getByLabelText("Enable all"));
        fireEvent.click(getByTestId("agentApplyButton"));

        await waitFor(() => expect(onApply).toHaveBeenCalledWith(expect.objectContaining({ toolsets: [PKG], toolsetExcludedTools: {} })));
    });

    test("unchecking the last granted tool of a custom toolset unbinds it", async () => {
        const { onApply, getByTestId, getByRole } = renderPackageAgent({}, { toolsetExcludedTools: { [PKG]: ["push"] } });

        fireEvent.click(getByTestId(`expand-toolset-${PKG}`));
        fireEvent.click(toolCheckbox(getByTestId, "fetch"));

        expect((getByRole("checkbox", { name: PKG }) as HTMLInputElement).checked).toBe(false);
        fireEvent.click(getByTestId("agentApplyButton"));
        await waitFor(() => expect(onApply).toHaveBeenCalledWith(expect.objectContaining({ toolsets: [], toolsetExcludedTools: {} })));
    });

    test("clearing enable all on a custom toolset unbinds it", async () => {
        const { onApply, getByTestId, getByLabelText } = renderPackageAgent({});

        fireEvent.click(getByTestId(`expand-toolset-${PKG}`));
        fireEvent.click(getByLabelText("Enable all"));
        fireEvent.click(getByTestId("agentApplyButton"));

        await waitFor(() => expect(onApply).toHaveBeenCalledWith(expect.objectContaining({ toolsets: [], toolsetExcludedTools: {} })));
    });

    test("a tool's config is not offered until its custom toolset is bound", () => {
        const { getByTestId, queryAllByTitle } = renderPackageAgent({}, { toolsets: [] });

        fireEvent.click(getByTestId(`expand-toolset-${PKG}`));
        expect(queryAllByTitle("Configure")).toHaveLength(0);

        fireEvent.click(toolCheckbox(getByTestId, "fetch"));
        expect(queryAllByTitle("Configure")).toHaveLength(1);
    });

    test("saving one tool's config leaves its sibling's config alone", async () => {
        const { onApply, getByTestId, getByRole, getAllByTitle } = renderPackageAgent({ fetch: { region: "us-east-1" } });

        fireEvent.click(getByTestId(`expand-toolset-${PKG}`));
        fireEvent.click(getAllByTitle("Configure")[1]);
        fireEvent.change(getByTestId("schemaField-endpoint"), { target: { value: "https://acme.test" } });
        fireEvent.click(getByRole("button", { name: "Save Configuration" }));
        fireEvent.click(getByTestId("agentApplyButton"));

        await waitFor(() =>
            expect(onApply).toHaveBeenCalledWith(
                expect.objectContaining({
                    toolsetConfigs: [{ toolsetName: PKG, configValues: { fetch: { region: "us-east-1" }, push: { endpoint: "https://acme.test" } } }],
                })
            )
        );
    });
});

describe("AgentToolsetsEdit — catalog fetch has not succeeded", () => {
    test("shows a couldn't-load message instead of the picker, and never renders the agent's real toolset as unresolved", () => {
        builtinToolsetsSuccess = false;
        const { getByText, queryByText, queryByRole } = renderEdit(makeAgent());

        expect(getByText(/Couldn't load the toolset catalog/)).toBeInTheDocument();
        expect(queryByText("Other Toolsets")).toBeNull();
        expect(queryByRole("checkbox", { name: "Web Request Tools" })).toBeNull();
    });

    test("also gates on the custom-toolsets fetch", () => {
        customToolsetsSuccess = false;
        const { getByText } = renderEdit(makeAgent());

        expect(getByText(/Couldn't load the toolset catalog/)).toBeInTheDocument();
    });
});

describe("AgentToolsetsEdit — creating a toolset", () => {
    beforeEach(() => vi.clearAllMocks());

    test("the new toolset is created and its bundle uploaded", async () => {
        const { getByRole, findByRole } = renderWithProviders(<AgentToolsetsEdit agent={makeAgent()} onApply={vi.fn()} onDiscard={vi.fn()} />, {
            initialPath: "/agents",
            userContextValues: { userHasCapability: () => true },
        });

        fireEvent.click(getByRole("button", { name: /Create Toolset/ }));
        fireEvent.click(await findByRole("button", { name: "pick zip" }));
        fireEvent.click(await findByRole("button", { name: "confirm create" }));

        await waitFor(() => expect(toolsetsApi.uploadBundle).toHaveBeenCalledWith("ts-new", expect.any(File)));
        expect(toolsetsApi.create).toHaveBeenCalledWith({ name: "order_tools", description: "Looks up customer orders." });
    });
});
