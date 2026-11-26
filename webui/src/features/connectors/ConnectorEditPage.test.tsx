// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { composeStory } from "@storybook/react";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, describe, expect, test, vi } from "vitest";

import { mockConnectors, mockConnectorsSchemaMap, mockGetConnectorSchema } from "@/testing/data";

import meta from "./ConnectorEditPage.stories";
import * as connectorUtils from "./utils";

vi.mock("./utils", async importOriginal => {
    const actual = await importOriginal<typeof import("./utils")>();
    return { ...actual, saveConnector: vi.fn(actual.saveConnector) };
});

const server = setupServer(
    http.get("*/api/v1/config", () => {
        return HttpResponse.json({});
    }),
    http.get("*/api/v1/config/capabilities", () => {
        return HttpResponse.json({ data: [], meta: { pagination: { pageNumber: 1, count: 0, pageSize: 100, nextPage: null, totalPages: 1 } } });
    }),
    http.get("*/api/v1/speech/config", () => {
        return HttpResponse.json({});
    }),
    http.get("*/api/v1/agentCards", () => {
        return HttpResponse.json({ data: [] });
    }),
    http.get("*/api/v2/platform/connectors", () => {
        return HttpResponse.json({ data: [], meta: { pagination: { pageNumber: 1, count: 0, pageSize: 100, nextPage: null, totalPages: 1 } } });
    })
);

const CreateBedrock = composeStory(
    {
        parameters: {
            chatContext: {
                sessionId: "mock-session-id",
                connectorSchemasMap: mockConnectorsSchemaMap,
                getConnectorSchema: mockGetConnectorSchema,
                useLoaderData: () => ({ type: "knowledge_base", subtype: "bedrock", isNew: true }),
            },
            configContext: {
                persistenceEnabled: true,
            },
        },
    },
    meta
);

const addNotification = vi.fn();

const CreateBedrockWithNotifications = composeStory(
    {
        parameters: {
            chatContext: {
                sessionId: "mock-session-id",
                connectorSchemasMap: mockConnectorsSchemaMap,
                getConnectorSchema: mockGetConnectorSchema,
                useLoaderData: () => ({ type: "knowledge_base", subtype: "bedrock", isNew: true }),
                addNotification,
            },
            configContext: {
                persistenceEnabled: true,
            },
        },
    },
    meta
);

const EditPostgresWithNotifications = composeStory(
    {
        parameters: {
            chatContext: {
                sessionId: "mock-session-id",
                connectorSchemasMap: mockConnectorsSchemaMap,
                getConnectorSchema: mockGetConnectorSchema,
                connectorToEdit: mockConnectors[0],
                addNotification,
            },
            configContext: {
                persistenceEnabled: true,
            },
        },
    },
    meta
);

const createWebSearchStory = (subtype: "tavily" | "exa") =>
    composeStory(
        {
            parameters: {
                chatContext: {
                    sessionId: "mock-session-id",
                    connectorSchemasMap: mockConnectorsSchemaMap,
                    getConnectorSchema: mockGetConnectorSchema,
                    useLoaderData: () => ({ type: "web_search", subtype, isNew: true }),
                    addNotification,
                },
                configContext: {
                    persistenceEnabled: true,
                },
            },
        },
        meta
    );

const webSearchCapability = { key: "web_search_connector", name: "Web Search Connector", description: "", status: "early_access", platform: "linux", hasEnvOverride: false };

describe("ConnectorEditPage", () => {
    beforeAll(() => server.listen());
    afterEach(() => server.resetHandlers());
    afterAll(() => server.close());

    describe("CreateBedrock story", () => {
        test("renders all required fields", async () => {
            const { container } = render(<CreateBedrock />);

            expect(await screen.findByText("Amazon Bedrock")).toBeInTheDocument();
            expect(screen.getAllByText("Create Knowledge Base Connector").length).toBeGreaterThan(0);

            expect(screen.getByText("Connector Name")).toBeInTheDocument();
            expect(screen.getByText("Description")).toBeInTheDocument();
            expect(screen.getByText("Knowledge Base ID")).toBeInTheDocument();
            expect(screen.getByText("AWS Region")).toBeInTheDocument();
            expect(screen.getByText("Authentication Scheme")).toBeInTheDocument();

            expect(container.querySelector('input[name="name"]')).toBeInTheDocument();
            expect(container.querySelector('textarea[name="description"]')).toBeInTheDocument();
            expect(container.querySelector('input[name="values.kb_id"]')).toBeInTheDocument();
            expect(container.querySelector('input[name="values.region"]')).toBeInTheDocument();
            expect(container.querySelector('button[id="values.auth_type"]')).toBeInTheDocument();
            expect(container.querySelector('input[name="values.aws_access_key_id"]')).toBeInTheDocument();
            expect(container.querySelector('input[name="values.aws_secret_access_key"]')).toBeInTheDocument();

            const authSelect = container.querySelector('button[id="values.auth_type"]') as HTMLButtonElement;
            expect(authSelect).toBeInTheDocument();
            expect(authSelect.textContent).toContain("AWS Access Key");

            const hiddenSelect = container.querySelector('select[aria-hidden="true"]') as HTMLSelectElement;
            expect(hiddenSelect).toBeInTheDocument();
            expect(hiddenSelect.options.length).toBe(2);
            expect(hiddenSelect.options[0].value).toBe("iam");
            expect(hiddenSelect.options[0].text).toBe("AWS IAM Role Chaining");
            expect(hiddenSelect.options[1].value).toBe("access_key");
            expect(hiddenSelect.options[1].text).toBe("AWS Access Key");
            expect(hiddenSelect.value).toBe("access_key");

            expect(screen.getByText(/All agents using this connector will have access to the same Knowledge Base/)).toBeInTheDocument();

            expect(screen.getByTestId("saveConnectorButton")).toBeInTheDocument();
            expect(screen.getByTestId("connectorCancel")).toBeInTheDocument();
        });

        test("submits form with filled data and calls API", async () => {
            const user = userEvent.setup();

            let capturedRequestBody: unknown = null;
            server.use(
                http.post("*/api/v2/platform/connectors", async ({ request }) => {
                    capturedRequestBody = await request.json();
                    return HttpResponse.json({ data: { id: "new-connector-id", ...(capturedRequestBody as JSON), createdTime: null, updatedTime: null } }, { status: 201 });
                })
            );

            const { container } = render(<CreateBedrock />);

            expect(await screen.findByText("Amazon Bedrock")).toBeInTheDocument();
            const nameInput = container.querySelector('input[name="name"]') as HTMLInputElement;
            const descriptionInput = container.querySelector('textarea[name="description"]') as HTMLTextAreaElement;
            const kbIdInput = container.querySelector('input[name="values.kb_id"]') as HTMLInputElement;
            const regionInput = container.querySelector('input[name="values.region"]') as HTMLInputElement;
            const hiddenAuthSelect = container.querySelector('select[aria-hidden="true"]') as HTMLSelectElement;
            const accessKeyInput = container.querySelector('input[name="values.aws_access_key_id"]') as HTMLInputElement;
            const secretKeyInput = container.querySelector('input[name="values.aws_secret_access_key"]') as HTMLInputElement;

            await user.clear(nameInput);
            await user.type(nameInput, "My Bedrock Connector");
            await user.clear(descriptionInput);
            await user.type(descriptionInput, "Test connector for Bedrock KB");
            await user.clear(kbIdInput);
            await user.type(kbIdInput, "ABC123XYZ");
            await user.clear(regionInput);
            await user.type(regionInput, "us-east-1");

            await user.clear(accessKeyInput);
            await user.type(accessKeyInput, "AKIAIOSFODNN7EXAMPLE");
            await user.clear(secretKeyInput);
            await user.type(secretKeyInput, "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY");

            expect(nameInput.value).toBe("My Bedrock Connector");
            expect(descriptionInput.value).toBe("Test connector for Bedrock KB");
            expect(kbIdInput.value).toBe("ABC123XYZ");
            expect(regionInput.value).toBe("us-east-1");
            expect(hiddenAuthSelect.value).toBe("access_key");
            expect(accessKeyInput.value).toBe("AKIAIOSFODNN7EXAMPLE");
            expect(secretKeyInput.value).toBe("wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY");

            const submitButton = screen.getByTestId("saveConnectorButton");
            await user.click(submitButton);

            await waitFor(() => {
                expect(capturedRequestBody).not.toBeNull();
            });

            expect(capturedRequestBody).toMatchObject({
                name: "My Bedrock Connector",
                description: "Test connector for Bedrock KB",
                connectorType: "knowledge_base",
                connectorSubtype: "bedrock",
                values: {
                    kb_id: "ABC123XYZ",
                    region: "us-east-1",
                    auth_type: "access_key",
                    aws_access_key_id: "AKIAIOSFODNN7EXAMPLE",
                    aws_secret_access_key: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
                },
            });
        });

        test("shows the platform's warnings from the save as warning notifications", async () => {
            const user = userEvent.setup();
            const warning = 'approval gate on "query_bedrock_kb" was removed: the connector no longer provides that tool';
            server.use(http.post("*/api/v2/platform/connectors", () => HttpResponse.json({ data: { id: "new-connector-id", warnings: [warning], createdTime: null, updatedTime: null } }, { status: 201 })));

            const { container } = render(<CreateBedrockWithNotifications />);

            expect(await screen.findByText("Amazon Bedrock")).toBeInTheDocument();
            await user.clear(container.querySelector('input[name="name"]') as HTMLInputElement);
            await user.paste("My Bedrock Connector");
            await user.clear(container.querySelector('textarea[name="description"]') as HTMLTextAreaElement);
            await user.paste("Test connector for Bedrock KB");
            await user.clear(container.querySelector('input[name="values.kb_id"]') as HTMLInputElement);
            await user.paste("ABC123XYZ");
            await user.clear(container.querySelector('input[name="values.region"]') as HTMLInputElement);
            await user.paste("us-east-1");
            await user.clear(container.querySelector('input[name="values.aws_access_key_id"]') as HTMLInputElement);
            await user.paste("AKIAIOSFODNN7EXAMPLE");
            await user.clear(container.querySelector('input[name="values.aws_secret_access_key"]') as HTMLInputElement);
            await user.paste("wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY");
            await user.click(screen.getByTestId("saveConnectorButton"));

            await waitFor(() => {
                expect(addNotification).toHaveBeenCalledWith(warning, "warning");
            });
        });

        test("toggles password visibility", async () => {
            const user = userEvent.setup();
            const { container } = render(<CreateBedrock />);

            expect(await screen.findByText("Amazon Bedrock")).toBeInTheDocument();
            const secretKeyInput = container.querySelector('input[name="values.aws_secret_access_key"]') as HTMLInputElement;
            const toggleButton = screen.getByTestId("Show password");

            expect(secretKeyInput.type).toBe("password");

            await user.click(toggleButton);

            await waitFor(() => {
                expect(secretKeyInput.type).toBe("text");
            });

            await user.click(toggleButton);

            await waitFor(() => {
                expect(secretKeyInput.type).toBe("password");
            });
        });

        test("validates required fields and field constraints", async () => {
            const user = userEvent.setup();
            const { container } = render(<CreateBedrock />);

            expect(await screen.findByText("Amazon Bedrock")).toBeInTheDocument();
            const nameInput = container.querySelector('input[name="name"]') as HTMLInputElement;
            const kbIdInput = container.querySelector('input[name="values.kb_id"]') as HTMLInputElement;
            const regionInput = container.querySelector('input[name="values.region"]') as HTMLInputElement;
            const accessKeyInput = container.querySelector('input[name="values.aws_access_key_id"]') as HTMLInputElement;
            const secretKeyInput = container.querySelector('input[name="values.aws_secret_access_key"]') as HTMLInputElement;
            const submitButton = screen.getByTestId("saveConnectorButton");

            await user.click(submitButton);

            await waitFor(() => {
                expect(screen.getByText("Name must be at least 3 characters")).toBeInTheDocument();
                expect(screen.getByText("Knowledge Base ID is required")).toBeInTheDocument();
            });

            await user.clear(kbIdInput);
            await user.paste("A".repeat(101));
            await user.click(submitButton);
            await waitFor(() => {
                expect(screen.getByText("Maximum length is 100 characters")).toBeInTheDocument();
            });

            await user.clear(kbIdInput);
            await user.paste("VALIDKB");
            await user.clear(accessKeyInput);
            await user.paste("SHORT");
            await user.click(submitButton);
            await waitFor(() => {
                expect(screen.getByText("Minimum length is 16 characters")).toBeInTheDocument();
            });

            await user.clear(secretKeyInput);
            await user.paste("TooShort");
            await user.click(submitButton);
            await waitFor(() => {
                expect(screen.getByText("Minimum length is 30 characters")).toBeInTheDocument();
            });

            await user.clear(nameInput);
            await user.paste("Valid Connector Name");
            await user.clear(kbIdInput);
            await user.paste("VALIDKBID123");
            await user.clear(regionInput);
            await user.paste("us-west-2");
            await user.clear(accessKeyInput);
            await user.paste("AKIAIOSFODNN7EXAMPLE");
            await user.clear(secretKeyInput);
            await user.paste("wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY");

            expect(screen.queryByText("Name must be at least 3 characters")).not.toBeInTheDocument();
            expect(screen.queryByText("Knowledge Base ID is required")).not.toBeInTheDocument();
            expect(screen.queryByText("Minimum length is 16 characters")).not.toBeInTheDocument();
            expect(screen.queryByText("Minimum length is 30 characters")).not.toBeInTheDocument();
        });
    });

    describe("EditPOSTGRESQL story", () => {
        test("shows the warnings of the save next to the error when the spec upload fails after the connector was saved", async () => {
            const user = userEvent.setup();
            const warning = 'approval gate on "sql_query" was removed: the connector no longer provides that tool';
            vi.mocked(connectorUtils.saveConnector).mockRejectedValueOnce(new connectorUtils.ConnectorResourceUploadError(new Error("The spec could not be stored"), [warning]));

            render(<EditPostgresWithNotifications />);
            await user.click(await screen.findByTestId("saveConnectorButton"));

            await waitFor(() => {
                expect(addNotification).toHaveBeenCalledWith(warning, "warning");
            });
            expect(await screen.findByText("Connector Update Failed")).toBeInTheDocument();
            expect(screen.getByText("The spec could not be stored")).toBeInTheDocument();
        });
    });

    describe("Web Search create", () => {
        test.each([
            { subtype: "tavily" as const, provider: "Tavily", apiBase: "https://api.tavily.com" },
            { subtype: "exa" as const, provider: "Exa", apiBase: "https://api.exa.ai" },
        ])("$provider renders the API key, API base, results and safe search fields from its schema", async ({ subtype, provider, apiBase }) => {
            const CreateWebSearch = createWebSearchStory(subtype);
            const { container } = render(<CreateWebSearch />);

            expect(await screen.findByText(provider)).toBeInTheDocument();
            expect(screen.getAllByText("Create Web Search Connector").length).toBeGreaterThan(0);
            expect(screen.getByText("API Key")).toBeInTheDocument();
            expect(screen.getByText("API Base URL")).toBeInTheDocument();
            expect(screen.getByText("Results per Search")).toBeInTheDocument();
            expect(screen.getByText("Safe Search")).toBeInTheDocument();
            expect(screen.queryByText("Tool Approvals")).not.toBeInTheDocument();

            expect((container.querySelector('input[name="values.api_key"]') as HTMLInputElement).type).toBe("password");
            expect((container.querySelector('input[name="values.api_base"]') as HTMLInputElement).value).toBe(apiBase);
            expect(container.querySelector('input[name="values.max_results"]')).toBeInTheDocument();
            expect((container.querySelector('button[id="values.safe_search"]') as HTMLButtonElement).textContent).toContain("No");
            expect(screen.getByText(new RegExp(`${provider} bills each search to this API key`))).toBeInTheDocument();
        });

        test("shows the Early Access badge from the web_search_connector capability", async () => {
            server.use(http.get("*/api/v1/config/capabilities", () => HttpResponse.json({ data: [webSearchCapability], meta: { pagination: { pageNumber: 1, count: 1, pageSize: 100, nextPage: null, totalPages: 1 } } })));
            const CreateTavily = createWebSearchStory("tavily");
            render(<CreateTavily />);

            expect(await screen.findByText("EARLY ACCESS")).toBeInTheDocument();
        });

        test("shows the warnings of a save the provider could not verify as warning notifications", async () => {
            const user = userEvent.setup();
            const warning = "Tavily rate-limited the key check, so the API key was saved without being verified.";
            let capturedRequestBody: unknown = null;
            server.use(
                http.post("*/api/v2/platform/connectors", async ({ request }) => {
                    capturedRequestBody = await request.json();
                    return HttpResponse.json({ data: { id: "new-connector-id", warnings: [warning], createdTime: null, updatedTime: null } }, { status: 201 });
                })
            );
            const CreateTavily = createWebSearchStory("tavily");
            const { container } = render(<CreateTavily />);

            expect(await screen.findByText("Tavily")).toBeInTheDocument();
            await user.clear(container.querySelector('input[name="name"]') as HTMLInputElement);
            await user.paste("Team Web Search");
            await user.clear(container.querySelector('textarea[name="description"]') as HTMLTextAreaElement);
            await user.paste("Cited web search for the research agents");
            await user.click(container.querySelector('input[name="values.api_key"]') as HTMLInputElement);
            await user.paste("tvly-example-key");
            await user.click(screen.getByTestId("saveConnectorButton"));

            await waitFor(() => {
                expect(addNotification).toHaveBeenCalledWith(warning, "warning");
            });
            expect(capturedRequestBody).toMatchObject({
                connectorType: "web_search",
                connectorSubtype: "tavily",
                values: { api_key: "tvly-example-key", api_base: "https://api.tavily.com", safe_search: "no" },
            });
        });
    });
});
