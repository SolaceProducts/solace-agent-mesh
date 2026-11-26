// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { composeStory } from "@storybook/react";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, test, vi } from "vitest";

import { type ModelProbeErrorClass, REDACTED_SENTINEL } from "@/api/models";

import { productAnalyticsClient } from "@/common/product-analytics/client";

import { anthropicModelConfig } from "@/testing/data/models";
import { savedModelV2, toModelV2, toSupportedModelV2 } from "@/testing/data/modelsV2";
import { StoryProvider } from "@/testing/providers/StoryProvider";

import { ModelEditPage } from "./ModelEditPage";
import meta, { CreateNewModel } from "./ModelEditPage.stories";

const server = setupServer();

beforeAll(() => server.listen({ onUnhandledFrame: "bypass" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

const MODEL_PATH = "*/api/v2/platform/models/:id";
const SUPPORTED_MODELS_PATH = "*/api/v2/platform/providers/:provider/models";
const TEST_CONNECTION_PATH = "*/api/v2/platform/models/testConnection";

const supportedModelsPage = (models: Array<{ id: string; label: string }>) =>
    HttpResponse.json({
        data: models.map(toSupportedModelV2("anthropic")),
        meta: { pagination: { pageNumber: 1, pageSize: 100, count: models.length, totalPages: 1, nextPage: null } },
    });

type Probe = { success: true } | { success: false; message: string; errorClass: ModelProbeErrorClass };

const PROBE_PASSED: Probe = { success: true };
const probeFailed = (message: string, errorClass: ModelProbeErrorClass = "auth_credentials_invalid"): Probe => ({ success: false, message, errorClass });
const probeResult = (probe: Probe) => HttpResponse.json({ data: probe.success ? { success: true, message: null, errorClass: null } : probe });

function setupEditHandlers(testConnectionResponse: Probe) {
    server.use(
        http.get(MODEL_PATH, ({ params }) => {
            if (params.id === anthropicModelConfig.id) {
                return HttpResponse.json({ data: toModelV2(anthropicModelConfig) });
            }
            return HttpResponse.json({ message: "Not found", errorId: "test-not-found" }, { status: 404 });
        }),
        http.put(SUPPORTED_MODELS_PATH, () => {
            return supportedModelsPage([
                { id: "claude-3-5-sonnet", label: "Claude 3.5 Sonnet" },
                { id: "claude-3-opus", label: "Claude 3 Opus" },
            ]);
        }),
        http.put(TEST_CONNECTION_PATH, () => probeResult(testConnectionResponse)),
        http.patch(MODEL_PATH, async ({ request }) => {
            const body = await request.json();
            return HttpResponse.json({ data: savedModelV2(anthropicModelConfig.id, body, anthropicModelConfig) });
        })
    );
}

function setupCreateHandlers(testConnectionResponse: Probe) {
    server.use(
        http.put(SUPPORTED_MODELS_PATH, () => {
            return supportedModelsPage([
                { id: "claude-3-5-sonnet", label: "Claude 3.5 Sonnet" },
                { id: "claude-3-opus", label: "Claude 3 Opus" },
            ]);
        }),
        http.put(TEST_CONNECTION_PATH, () => probeResult(testConnectionResponse)),
        http.post("*/api/v2/platform/models", async ({ request }) => HttpResponse.json({ data: savedModelV2("new-model-id", await request.json()) }, { status: 201 }))
    );
}

function renderEditPage() {
    return render(
        <StoryProvider>
            <MemoryRouter initialEntries={[`/models/${anthropicModelConfig.id}/edit`]}>
                <Routes>
                    <Route path="/models/:id/edit" element={<ModelEditPage />} />
                    <Route path="/models" element={<div>Models List</div>} />
                </Routes>
            </MemoryRouter>
        </StoryProvider>
    );
}

function renderCreatePage() {
    return render(
        <StoryProvider>
            <MemoryRouter initialEntries={["/models/new/edit"]}>
                <Routes>
                    <Route path="/models/new/edit" element={<ModelEditPage />} />
                    <Route path="/models" element={<div>Models List</div>} />
                </Routes>
            </MemoryRouter>
        </StoryProvider>
    );
}

describe("ModelEditPage - edit mode: save with connection test", () => {
    beforeEach(() => {
        setupEditHandlers(PROBE_PASSED);
    });

    test("saves directly when connection test passes", async () => {
        renderEditPage();
        const user = userEvent.setup();

        await screen.findAllByText("Edit anthropic-model");

        await user.click(screen.getByRole("button", { name: /Save/i }));

        await waitFor(() => {
            expect(screen.queryByRole("dialog")).toBeNull();
        });
    });

    test("shows dialog when connection test fails", async () => {
        setupEditHandlers(probeFailed("Authentication failed: Invalid API key provided"));
        renderEditPage();
        const user = userEvent.setup();

        await screen.findAllByText("Edit anthropic-model");

        await user.click(screen.getByRole("button", { name: /Save/i }));

        const dialog = await screen.findByRole("dialog");
        expect(dialog).toBeInTheDocument();
        expect(screen.getByText("Connection Test Failed")).toBeInTheDocument();
        expect(screen.getByText(/Authentication failed/i)).toBeInTheDocument();

        expect(screen.getByRole("button", { name: /Go Back/i })).toBeInTheDocument();
        expect(screen.getByRole("button", { name: /Save Anyway/i })).toBeInTheDocument();
    });

    test("closes dialog and preserves form when user clicks Go Back", async () => {
        setupEditHandlers(probeFailed("Authentication failed"));
        renderEditPage();
        const user = userEvent.setup();

        await screen.findAllByText("Edit anthropic-model");

        await user.click(screen.getByRole("button", { name: /Save/i }));

        await screen.findByRole("dialog");

        await user.click(screen.getByRole("button", { name: /Go Back/i }));

        await waitFor(() => {
            expect(screen.queryByRole("dialog")).toBeNull();
        });
        expect(screen.getByDisplayValue("anthropic-model")).toBeInTheDocument();
    });

    test("saves when user clicks Save Anyway after test failure", async () => {
        setupEditHandlers(probeFailed("Authentication failed"));
        renderEditPage();
        const user = userEvent.setup();

        await screen.findAllByText("Edit anthropic-model");

        await user.click(screen.getByRole("button", { name: /Save/i }));

        await screen.findByRole("dialog");
        await user.click(screen.getByRole("button", { name: /Save Anyway/i }));

        await screen.findByText("Models List");
    });

    test("shows dialog when connection test request throws", async () => {
        server.use(http.put(TEST_CONNECTION_PATH, () => HttpResponse.error()));
        renderEditPage();
        const user = userEvent.setup();

        await screen.findAllByText("Edit anthropic-model");

        await user.click(screen.getByRole("button", { name: /Save/i }));

        const dialog = await screen.findByRole("dialog");
        expect(dialog).toBeInTheDocument();
        expect(screen.getByText("Connection Test Failed")).toBeInTheDocument();
    });
});

async function fillCreateForm(user: ReturnType<typeof userEvent.setup>) {
    const aliasInput = document.querySelector('input[name="alias"]') as HTMLInputElement;
    await user.type(aliasInput, "my-new-model");

    const descInput = document.querySelector('textarea[name="description"]') as HTMLTextAreaElement;
    await user.type(descInput, "A test model for integration");

    const providerComboboxes = screen.getAllByRole("combobox");
    await user.click(providerComboboxes[0]);
    await user.click(await screen.findByText("Anthropic"));

    await waitFor(() => {
        expect(document.querySelector('input[name="apiKey"]')).toBeTruthy();
    });
    const apiKeyInput = document.querySelector('input[name="apiKey"]') as HTMLInputElement;
    await user.type(apiKeyInput, "sk-ant-test-key");

    const modelCombo = await screen.findByPlaceholderText(/Type or select a model/i);
    await user.click(modelCombo);
    await user.click(await screen.findByText("claude-3-5-sonnet"));
}

describe("ModelEditPage - create mode: save with connection test", () => {
    beforeEach(() => {
        setupCreateHandlers(PROBE_PASSED);
    });

    test("saves directly when connection test passes in create mode", async () => {
        renderCreatePage();
        const user = userEvent.setup();

        await screen.findAllByText("Add Model");
        await fillCreateForm(user);

        await user.click(screen.getByRole("button", { name: /Add/i }));

        await waitFor(() => {
            expect(screen.queryByRole("dialog")).toBeNull();
        });

        await screen.findByText("Models List");
    });

    test("shows dialog when connection test fails in create mode", async () => {
        setupCreateHandlers(probeFailed("Invalid credentials"));
        renderCreatePage();
        const user = userEvent.setup();

        await screen.findAllByText("Add Model");
        await fillCreateForm(user);

        await user.click(screen.getByRole("button", { name: /Add/i }));

        const dialog = await screen.findByRole("dialog");
        expect(dialog).toBeInTheDocument();
        expect(screen.getByText("Connection Test Failed")).toBeInTheDocument();
        expect(screen.getByText(/Invalid credentials/i)).toBeInTheDocument();
    });
});

interface CapturedRequests {
    patchBody: Record<string, unknown> | null;
    testBody: Record<string, unknown> | null;
}

function setupRedactedEditHandlers(requests: CapturedRequests, getResponse?: Partial<typeof anthropicModelConfig>) {
    const fixture = {
        ...anthropicModelConfig,
        authConfig: { type: "apikey", api_key: REDACTED_SENTINEL },
        ...getResponse,
    };

    server.use(
        http.get(MODEL_PATH, ({ params }) => {
            if (params.id === fixture.id) return HttpResponse.json({ data: toModelV2(fixture) });
            return HttpResponse.json({ message: "Not found", errorId: "test-not-found" }, { status: 404 });
        }),
        http.put(SUPPORTED_MODELS_PATH, () => {
            return supportedModelsPage([
                { id: "claude-3-5-sonnet", label: "Claude 3.5 Sonnet" },
                { id: "claude-3-opus", label: "Claude 3 Opus" },
            ]);
        }),
        http.put(TEST_CONNECTION_PATH, async ({ request }) => {
            requests.testBody = (await request.json()) as Record<string, unknown>;
            return probeResult(PROBE_PASSED);
        }),
        http.patch(MODEL_PATH, async ({ request }) => {
            requests.patchBody = (await request.json()) as Record<string, unknown>;
            return HttpResponse.json({ data: savedModelV2(fixture.id, requests.patchBody, fixture) });
        })
    );
}

describe("ModelEditPage - REDACTED API key round-trip", () => {
    test("save with untouched API key omits api_key from PATCH body", async () => {
        const requests: CapturedRequests = { patchBody: null, testBody: null };
        setupRedactedEditHandlers(requests);

        renderEditPage();
        const user = userEvent.setup();
        await screen.findAllByText("Edit anthropic-model");

        await user.click(screen.getByRole("button", { name: /Save/i }));

        await waitFor(() => expect(requests.patchBody).not.toBeNull());

        const authConfig = requests.patchBody!.authConfig as Record<string, unknown> | undefined;
        expect(authConfig).toBeDefined();
        expect(authConfig).not.toHaveProperty("api_key");
        expect(authConfig?.api_key).not.toBe(REDACTED_SENTINEL);

        expect(requests.patchBody!.modelParams).toEqual({ temperature: 0.1, max_tokens: 4096, system_prompt_caching: true, cache_strategy: "5m" });
    });

    test("API key input does not display the REDACTED sentinel to the user", async () => {
        const requests: CapturedRequests = { patchBody: null, testBody: null };
        setupRedactedEditHandlers(requests);

        renderEditPage();

        await screen.findAllByText("Edit anthropic-model");

        await waitFor(() => {
            expect(document.querySelector('input[name="apiKey"]')).toBeTruthy();
        });
        const apiKeyInput = document.querySelector('input[name="apiKey"]') as HTMLInputElement;

        expect(apiKeyInput.value).toBe("");
        expect(apiKeyInput.placeholder).toBe("•".repeat(16));
    });

    test("user-entered new API key still flows into the PATCH body", async () => {
        const requests: CapturedRequests = { patchBody: null, testBody: null };
        setupRedactedEditHandlers(requests);

        renderEditPage();
        const user = userEvent.setup();
        await screen.findAllByText("Edit anthropic-model");

        await waitFor(() => {
            expect(document.querySelector('input[name="apiKey"]')).toBeTruthy();
        });
        const apiKeyInput = document.querySelector('input[name="apiKey"]') as HTMLInputElement;
        await user.clear(apiKeyInput);
        await user.type(apiKeyInput, "sk-rotated-by-user");

        await user.click(screen.getByRole("button", { name: /Save/i }));

        await waitFor(() => expect(requests.patchBody).not.toBeNull());

        const authConfig = requests.patchBody!.authConfig as Record<string, unknown>;
        expect(authConfig.api_key).toBe("sk-rotated-by-user");
    });

    test("test connection alone sends modelId and omits api_key when untouched", async () => {
        const requests: CapturedRequests = { patchBody: null, testBody: null };
        setupRedactedEditHandlers(requests);

        renderEditPage();
        const user = userEvent.setup();
        await screen.findAllByText("Edit anthropic-model");

        const testButton = await screen.findByTestId("test-connection-button");
        await user.click(testButton);

        await waitFor(() => expect(requests.testBody).not.toBeNull());

        expect(requests.testBody!.modelId).toBe(anthropicModelConfig.id);

        const authConfig = requests.testBody!.authConfig as Record<string, unknown>;
        expect(authConfig).toBeDefined();
        expect(authConfig).not.toHaveProperty("api_key");
        expect(authConfig.api_key).not.toBe(REDACTED_SENTINEL);
    });

    test("oauth2 client_secret REDACTED sentinel is not echoed back on save", async () => {
        const requests: CapturedRequests = { patchBody: null, testBody: null };
        setupRedactedEditHandlers(requests, {
            authType: "oauth2",
            authConfig: {
                type: "oauth2",
                client_id: "client-abc",
                client_secret: REDACTED_SENTINEL,
                token_url: "https://auth.example.com/token",
            },
        });

        renderEditPage();
        const user = userEvent.setup();
        await screen.findAllByText("Edit anthropic-model");

        await user.click(screen.getByRole("button", { name: /Save/i }));

        await waitFor(() => expect(requests.patchBody).not.toBeNull());

        const authConfig = requests.patchBody!.authConfig as Record<string, unknown> | undefined;
        expect(authConfig).toBeDefined();
        expect(authConfig).not.toHaveProperty("client_secret");
        expect(authConfig?.client_secret).not.toBe(REDACTED_SENTINEL);
    });

    interface SecretRedactionCase {
        name: string;
        fixture: Partial<typeof anthropicModelConfig>;
        forbiddenAuthKeys: string[];
    }

    const secretRedactionCases: SecretRedactionCase[] = [
        {
            name: "aws_iam secret_access_key + session_token",
            fixture: {
                provider: "bedrock",
                authType: "aws_iam",
                authConfig: {
                    type: "aws_iam",
                    aws_access_key_id: "AKIAEXAMPLE",
                    aws_secret_access_key: REDACTED_SENTINEL,
                    aws_session_token: REDACTED_SENTINEL,
                    aws_region_name: "us-east-1",
                },
            },
            forbiddenAuthKeys: ["aws_secret_access_key", "aws_session_token"],
        },
        {
            name: "gcp_service_account vertex_credentials",
            fixture: {
                provider: "vertex_ai",
                authType: "gcp_service_account",
                authConfig: {
                    type: "gcp_service_account",
                    vertex_credentials: REDACTED_SENTINEL,
                    vertex_project: "my-gcp-project",
                    vertex_location: "us-central1",
                },
            },
            forbiddenAuthKeys: ["vertex_credentials"],
        },
    ];

    test.each(secretRedactionCases)("$name REDACTED sentinel is not echoed back on save", async ({ fixture, forbiddenAuthKeys }) => {
        const requests: CapturedRequests = { patchBody: null, testBody: null };
        setupRedactedEditHandlers(requests, fixture);

        renderEditPage();
        const user = userEvent.setup();
        await screen.findAllByText("Edit anthropic-model");

        await user.click(screen.getByRole("button", { name: /Save/i }));

        await waitFor(() => expect(requests.patchBody).not.toBeNull());

        const authConfig = requests.patchBody!.authConfig as Record<string, unknown> | undefined;
        expect(authConfig).toBeDefined();
        for (const key of forbiddenAuthKeys) {
            expect(authConfig, `${key} must be omitted when the input is unchanged`).not.toHaveProperty(key);
            expect((authConfig ?? {})[key], `${key} must not carry the redaction sentinel`).not.toBe(REDACTED_SENTINEL);
        }
    });

    test("a stored whitespace-only model name blocks save with an inline error", async () => {
        const requests: CapturedRequests = { patchBody: null, testBody: null };
        setupRedactedEditHandlers(requests, { modelName: "   " });

        renderEditPage();
        const user = userEvent.setup();
        await screen.findAllByText("Edit anthropic-model");

        await user.click(screen.getByRole("button", { name: /Save/i }));

        expect(await screen.findByText(/Model Name is required/i)).toBeInTheDocument();
        expect(requests.patchBody).toBeNull();
    });

    test.each([
        { label: "an empty", stored: "", errorType: "required" },
        { label: "a whitespace-only", stored: "   ", errorType: "validate" },
    ])("$label stored model name is reported as $errorType", async ({ stored, errorType }) => {
        const track = vi.spyOn(productAnalyticsClient, "track").mockImplementation(() => {});
        const requests: CapturedRequests = { patchBody: null, testBody: null };
        setupRedactedEditHandlers(requests, { modelName: stored });

        renderEditPage();
        const user = userEvent.setup();
        await screen.findAllByText("Edit anthropic-model");

        await user.click(screen.getByRole("button", { name: /Save/i }));

        await waitFor(() => expect(track).toHaveBeenCalledWith("form:validation_error", expect.objectContaining({ field: "modelName", errorType })));
        track.mockRestore();
    });
});

function authTypeSelect(): HTMLSelectElement | undefined {
    return Array.from(document.querySelectorAll<HTMLSelectElement>('select[aria-hidden="true"]')).find(sel =>
        Array.from(sel.options).some(o => o.value === "apikey" || o.value === "oauth2" || o.value === "aws_iam" || o.value === "gcp_service_account")
    );
}

function setAuthType(value: string) {
    const sel = authTypeSelect();
    if (!sel) throw new Error(`auth-type select not found (wanted value=${value})`);
    fireEvent.change(sel, { target: { value } });
}

async function selectProviderAndAuth(user: ReturnType<typeof userEvent.setup>, providerLabel: string, authValue?: string) {
    await user.click(screen.getAllByRole("combobox")[0]);
    await user.click(await screen.findByText(providerLabel));
    await screen.findByPlaceholderText(/Type or select a (model|deployment)/i);
    if (authValue) {
        setAuthType(authValue);
    }
}

describe("ModelEditPage - provider form field matrix", () => {
    const CreateForm = composeStory(CreateNewModel, meta);

    interface ProviderCase {
        label: string;
        expectedFields: string[];
        forbiddenFields: string[];
        authOptions: string[];
        readonlyAuthLabel?: string;
    }

    const cases: ProviderCase[] = [
        {
            label: "OpenAI",
            expectedFields: ["apiKey"],
            forbiddenFields: ["apiBase", "tokenUrl", "clientId", "clientSecret", "awsAccessKeyId", "awsSecretAccessKey", "awsRegionName", "vertexCredentials", "vertexProject", "vertexLocation", "apiVersion"],
            authOptions: [],
            readonlyAuthLabel: "API Key",
        },
        {
            label: "Anthropic",
            expectedFields: ["apiKey"],
            forbiddenFields: ["apiBase", "tokenUrl", "clientId", "clientSecret", "awsAccessKeyId", "vertexCredentials", "apiVersion"],
            authOptions: [],
            readonlyAuthLabel: "API Key",
        },
        {
            label: "Microsoft Foundry",
            expectedFields: ["apiBase", "apiKey", "apiVersion"],
            forbiddenFields: ["tokenUrl", "clientId", "clientSecret", "awsAccessKeyId", "vertexCredentials"],
            authOptions: ["apikey", "oauth2"],
        },
        {
            label: "Google AI Studio",
            expectedFields: ["apiKey"],
            forbiddenFields: ["apiBase", "tokenUrl", "awsAccessKeyId", "vertexCredentials", "apiVersion"],
            authOptions: [],
            readonlyAuthLabel: "API Key",
        },
        {
            label: "Google Vertex AI",
            expectedFields: ["vertexCredentials", "vertexProject", "vertexLocation"],
            forbiddenFields: ["apiBase", "apiKey", "tokenUrl", "clientId", "awsAccessKeyId", "apiVersion"],
            authOptions: [],
            readonlyAuthLabel: "GCP Service Account",
        },
        {
            label: "Amazon Bedrock",
            expectedFields: ["awsAccessKeyId", "awsSecretAccessKey", "awsSessionToken", "awsRegionName"],
            forbiddenFields: ["apiBase", "apiKey", "tokenUrl", "clientId", "vertexCredentials", "apiVersion"],
            authOptions: [],
            readonlyAuthLabel: "AWS IAM",
        },
        {
            label: "Ollama",
            expectedFields: ["apiBase", "apiKey"],
            forbiddenFields: ["tokenUrl", "clientId", "awsAccessKeyId", "vertexCredentials", "apiVersion"],
            authOptions: ["apikey", "none"],
        },
        {
            label: "Custom",
            expectedFields: ["apiBase", "apiKey"],
            forbiddenFields: ["tokenUrl", "clientId", "awsAccessKeyId", "vertexCredentials", "apiVersion"],
            authOptions: ["apikey", "oauth2", "none"],
        },
    ];

    test.each(cases)("$label: renders only fields the provider config declares", async ({ label, expectedFields, forbiddenFields, authOptions, readonlyAuthLabel }) => {
        render(<CreateForm />);
        const user = userEvent.setup();

        await selectProviderAndAuth(user, label);

        if (authOptions.length > 0) {
            const sel = authTypeSelect();
            expect(sel, `${label}: expected an auth-type dropdown`).toBeTruthy();
            const values = Array.from(sel!.options)
                .map(o => o.value)
                .filter(Boolean);
            expect(new Set(values)).toEqual(new Set(authOptions));
            expect(document.querySelector('[name="apiKey"]'), `${label}: credentials must be hidden before an auth type is chosen`).toBeFalsy();
            setAuthType(authOptions[0]);
        } else {
            expect(authTypeSelect(), `${label}: single-option provider must not render an auth dropdown`).toBeFalsy();
            expect((await screen.findAllByText(readonlyAuthLabel!)).length, `${label}: read-only auth type "${readonlyAuthLabel}" must render`).toBeGreaterThan(0);
        }

        for (const name of expectedFields) {
            expect(document.querySelector(`[name="${name}"]`), `${label}: expected field "${name}" to render`).toBeTruthy();
        }
        for (const name of forbiddenFields) {
            expect(document.querySelector(`[name="${name}"]`), `${label}: field "${name}" must not render`).toBeFalsy();
        }
    });
});

describe("ModelEditPage - auth type switching", () => {
    const CreateForm = composeStory(CreateNewModel, meta);

    test("Azure OpenAI: switching to OAuth2 reveals OAuth fields and hides API Key", async () => {
        render(<CreateForm />);
        const user = userEvent.setup();

        await selectProviderAndAuth(user, "Microsoft Foundry", "apikey");
        expect(document.querySelector('[name="apiKey"]')).toBeTruthy();
        expect(document.querySelector('[name="tokenUrl"]')).toBeFalsy();

        setAuthType("oauth2");

        expect(document.querySelector('[name="apiKey"]')).toBeFalsy();
        for (const name of ["tokenUrl", "clientId", "clientSecret", "oauthScope", "oauthAudience", "oauthCaCert", "oauthTokenRefreshBufferSeconds", "oauthMaxRetries"]) {
            expect(document.querySelector(`[name="${name}"]`), `expected OAuth field "${name}" to render`).toBeTruthy();
        }
    });

    test("Azure OpenAI: Azure Workload Identity, where enabled, asks for client and tenant IDs, no secret", async () => {
        const EnabledCreateForm = composeStory({ ...CreateNewModel, parameters: { ...CreateNewModel.parameters, configContext: { azureWorkloadIdentityEnabled: true } } }, meta);
        render(<EnabledCreateForm />);
        const user = userEvent.setup();

        await selectProviderAndAuth(user, "Microsoft Foundry", "apikey");
        const offered = Array.from(authTypeSelect()!.options).map(o => o.value);
        expect(offered).toContain("azure_workload_identity");
        setAuthType("azure_workload_identity");

        for (const name of ["apiKey", "tokenUrl", "clientSecret"]) {
            expect(document.querySelector(`[name="${name}"]`), `credential field "${name}" should not render`).toBeFalsy();
        }
        for (const name of ["apiBase", "apiVersion", "clientId", "tenantId"]) {
            expect(document.querySelector(`[name="${name}"]`), `field "${name}" should render`).toBeTruthy();
        }
    });

    test("Azure OpenAI: Azure Workload Identity rejects a client or tenant ID that is not a GUID", async () => {
        const EnabledCreateForm = composeStory({ ...CreateNewModel, parameters: { ...CreateNewModel.parameters, configContext: { azureWorkloadIdentityEnabled: true } } }, meta);
        render(<EnabledCreateForm />);
        const user = userEvent.setup();

        await selectProviderAndAuth(user, "Microsoft Foundry", "apikey");
        setAuthType("azure_workload_identity");

        await user.type(document.querySelector<HTMLInputElement>('[name="clientId"]')!, "not-a-guid");
        await user.type(document.querySelector<HTMLInputElement>('[name="tenantId"]')!, "not-a-guid");
        await user.click(screen.getByRole("button", { name: /^Add/i }));

        expect(await screen.findAllByText("Invalid format. Enter a value using the format: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx")).toHaveLength(2);
    });

    test("Azure OpenAI: Azure Workload Identity shows the values the Azure admin trusts", async () => {
        server.use(
            http.get("*/api/v2/platform/models/azureWorkloadIdentity", () =>
                HttpResponse.json({ data: { type: "azureWorkloadIdentity", issuer: "https://oidc.example.com/id/abc", namespace: "sam", serviceAccount: "solace-agent-mesh", audiences: ["api://AzureADTokenExchange"] } })
            )
        );
        const EnabledCreateForm = composeStory({ ...CreateNewModel, parameters: { ...CreateNewModel.parameters, configContext: { azureWorkloadIdentityEnabled: true } } }, meta);
        render(<EnabledCreateForm />);
        const user = userEvent.setup();

        await selectProviderAndAuth(user, "Microsoft Foundry", "apikey");
        expect(screen.queryByTestId("azureWorkloadIdentityInfo")).not.toBeInTheDocument();
        setAuthType("azure_workload_identity");

        const info = await screen.findByTestId("azureWorkloadIdentityInfo");
        for (const value of ["https://oidc.example.com/id/abc", "sam", "solace-agent-mesh", "api://AzureADTokenExchange"]) {
            expect(await within(info).findByText(value)).toBeInTheDocument();
        }

        await user.click(within(info).getByRole("button", { name: "Copy All Values" }));
        expect(await navigator.clipboard.readText()).toBe(
            "Cluster issuer URL: https://oidc.example.com/id/abc\nNamespace: sam\nService account: solace-agent-mesh\nSubject identifier: system:serviceaccount:sam:solace-agent-mesh\nAudience: api://AzureADTokenExchange"
        );
    });

    test("Azure OpenAI: 'Deployment Name' label survives auth-type switch (ComboBox remount)", async () => {
        render(<CreateForm />);
        const user = userEvent.setup();

        await selectProviderAndAuth(user, "Microsoft Foundry");
        await screen.findByText("Deployment Name");
        expect(screen.queryByText("Model Name")).not.toBeInTheDocument();
        expect(screen.getByPlaceholderText(/Type or select a deployment/i)).toBeInTheDocument();

        setAuthType("oauth2");

        await screen.findByText("Deployment Name");
        expect(screen.queryByText("Model Name")).not.toBeInTheDocument();
        expect(screen.getByPlaceholderText(/Type or select a deployment/i)).toBeInTheDocument();
    });

    test("Ollama: switching to None hides the API Key field but keeps API Base", async () => {
        render(<CreateForm />);
        const user = userEvent.setup();

        await selectProviderAndAuth(user, "Ollama", "apikey");
        expect(document.querySelector('[name="apiKey"]')).toBeTruthy();

        setAuthType("none");

        expect(document.querySelector('[name="apiKey"]')).toBeFalsy();
        expect(document.querySelector('[name="apiBase"]')).toBeTruthy();
    });

    test("Custom: OAuth2 → None cycle clears out prior auth fields", async () => {
        render(<CreateForm />);
        const user = userEvent.setup();

        await selectProviderAndAuth(user, "Custom", "oauth2");
        expect(document.querySelector('[name="tokenUrl"]')).toBeTruthy();
        expect(document.querySelector('[name="apiKey"]')).toBeFalsy();

        setAuthType("none");

        for (const name of ["apiKey", "tokenUrl", "clientId", "clientSecret"]) {
            expect(document.querySelector(`[name="${name}"]`), `${name} must not render for authType=none`).toBeFalsy();
        }
        expect(document.querySelector('[name="apiBase"]')).toBeTruthy();
    });

    test("Ollama: switching to None preserves the model name selection", async () => {
        render(<CreateForm />);
        const user = userEvent.setup();

        await user.click(screen.getAllByRole("combobox")[0]);
        await user.click(await screen.findByText("Ollama"));

        setAuthType("apikey");

        await user.type(document.querySelector('[name="apiBase"]') as HTMLInputElement, "http://localhost:11434");
        await user.type(document.querySelector('[name="apiKey"]') as HTMLInputElement, "any-value");

        const modelInput = (await screen.findByPlaceholderText(/Type or select a model/i)) as HTMLInputElement;
        await waitFor(() => expect(modelInput).not.toBeDisabled());
        fireEvent.change(modelInput, { target: { value: "ollama/llama2" } });
        await waitFor(() => expect(modelInput.value).toBe("ollama/llama2"));

        setAuthType("none");

        expect(document.querySelector('[name="apiKey"]')).toBeFalsy();
        const modelInputAfter = screen.getByPlaceholderText(/Type or select a model/i) as HTMLInputElement;
        expect(modelInputAfter.value).toBe("ollama/llama2");
    });
});

describe("ModelEditPage - temperature migration on edit-load", () => {
    test("surfaces a stored temperature entry as a row in Custom Parameters (no dedicated field)", async () => {
        const fixture = {
            ...anthropicModelConfig,
            modelParams: { temperature: 0.1, max_tokens: 4096 },
        };
        server.use(
            http.get(MODEL_PATH, () => HttpResponse.json({ data: toModelV2(fixture) })),
            http.put(SUPPORTED_MODELS_PATH, () => supportedModelsPage([]))
        );

        renderEditPage();
        const user = userEvent.setup();
        await screen.findAllByText("Edit anthropic-model");

        await user.click(await screen.findByText("Advanced Settings"));

        expect(document.querySelector('input[name="temperature"]')).toBeNull();

        const maxTokens = document.querySelector('input[name="max_tokens"]') as HTMLInputElement;
        expect(maxTokens).toBeTruthy();
        expect(maxTokens.value).toBe("4096");

        const keyValues = Array.from(document.querySelectorAll<HTMLInputElement>('input[aria-label^="Key"]')).map(el => el.value);
        expect(keyValues).toContain("temperature");
        const values = Array.from(document.querySelectorAll<HTMLInputElement>('input[aria-label^="Value"]')).map(el => el.value);
        expect(values).toContain("0.1");
    });
});

describe("ModelEditPage - custom headers edit-load", () => {
    test("hydrates extra_headers into the headers editor and excludes it from Custom Parameters", async () => {
        const fixture = {
            ...anthropicModelConfig,
            modelParams: { extra_headers: { "X-Correlation-ID": "abc-123" }, top_p: 0.9 },
        };
        server.use(
            http.get(MODEL_PATH, () => HttpResponse.json({ data: toModelV2(fixture) })),
            http.put(SUPPORTED_MODELS_PATH, () => supportedModelsPage([]))
        );

        renderEditPage();
        const user = userEvent.setup();
        await screen.findAllByText("Edit anthropic-model");

        await user.click(await screen.findByText("Advanced Settings"));

        const keyValues = () => Array.from(document.querySelectorAll<HTMLInputElement>('input[aria-label^="Key"]')).map(el => el.value);
        await waitFor(() => expect(keyValues()).toContain("X-Correlation-ID"));
        expect(keyValues()).toContain("top_p");
        expect(keyValues()).not.toContain("extra_headers");
        const values = Array.from(document.querySelectorAll<HTMLInputElement>('input[aria-label^="Value"]')).map(el => el.value);
        expect(values).toContain("abc-123");
    });
});

describe("ModelEditPage - supported-models refetch on connection change", () => {
    interface RefetchCase {
        name: string;
        fixture: Partial<typeof anthropicModelConfig>;
        editFieldName: string;
        newValue: string;
        expectedAuthKey: string;
    }

    const cases: RefetchCase[] = [
        {
            name: "Bedrock: edit awsAccessKeyId",
            fixture: {
                provider: "bedrock",
                authType: "aws_iam",
                authConfig: { type: "aws_iam", aws_access_key_id: "AKIAOLD", aws_region_name: "us-east-1" },
            },
            editFieldName: "awsAccessKeyId",
            newValue: "AKIAROTATED",
            expectedAuthKey: "aws_access_key_id",
        },
        {
            name: "Vertex AI: edit vertexProject",
            fixture: {
                provider: "vertex_ai",
                authType: "gcp_service_account",
                authConfig: { type: "gcp_service_account", vertex_project: "old-project", vertex_location: "us-central1" },
            },
            editFieldName: "vertexProject",
            newValue: "new-project",
            expectedAuthKey: "vertex_project",
        },
        {
            name: "Azure OpenAI: edit apiVersion",
            fixture: {
                provider: "azure_openai",
                authType: "apikey",
                apiBase: "https://myresource.openai.azure.com",
                authConfig: { type: "apikey", api_version: "2023-05-15" },
            },
            editFieldName: "apiVersion",
            newValue: "2024-02-15-preview",
            expectedAuthKey: "api_version",
        },
    ];

    test.each(cases)("$name triggers a refetch that carries the new value", async ({ fixture, editFieldName, newValue, expectedAuthKey }) => {
        const supportedModelsBodies: Array<Record<string, unknown>> = [];
        const fullFixture = { ...anthropicModelConfig, ...fixture };

        server.use(
            http.get(MODEL_PATH, ({ params }) => {
                if (params.id === anthropicModelConfig.id) return HttpResponse.json({ data: toModelV2(fullFixture) });
                return HttpResponse.json({ message: "Not found", errorId: "not-found" }, { status: 404 });
            }),
            http.put(SUPPORTED_MODELS_PATH, async ({ request }) => {
                supportedModelsBodies.push((await request.json()) as Record<string, unknown>);
                return supportedModelsPage([{ id: "model-a", label: "Model A" }]);
            })
        );

        renderEditPage();
        const user = userEvent.setup();

        await waitFor(() => expect(supportedModelsBodies.length).toBeGreaterThanOrEqual(1));
        const initialCount = supportedModelsBodies.length;

        const fieldSelector = `[name="${editFieldName}"]`;
        await waitFor(() => expect(document.querySelector(fieldSelector), `expected ${editFieldName} to render for ${fullFixture.provider}`).toBeTruthy());
        const input = document.querySelector(fieldSelector) as HTMLInputElement | HTMLTextAreaElement | null;
        await user.clear(input!);
        await user.type(input!, newValue);

        const comboboxes = screen.getAllByRole("combobox");
        await user.click(comboboxes[comboboxes.length - 1]);

        await waitFor(() => expect(supportedModelsBodies.length).toBeGreaterThan(initialCount));
        const latest = supportedModelsBodies[supportedModelsBodies.length - 1];
        const authConfig = latest.authConfig as Record<string, unknown>;
        expect(authConfig[expectedAuthKey]).toBe(newValue);
    });
});

describe("ModelEditPage - pricing on the wire", () => {
    const priced = { pricing: { input: "3", output: "15", cacheRead: null, cacheWrite: null } };

    async function saveAndCapture(edit: (user: ReturnType<typeof userEvent.setup>) => Promise<void>) {
        const requests: CapturedRequests = { patchBody: null, testBody: null };
        setupRedactedEditHandlers(requests, priced);
        renderEditPage();
        const user = userEvent.setup();
        await screen.findAllByText("Edit anthropic-model");
        await edit(user);
        await user.click(screen.getByRole("button", { name: /Save/i }));
        await waitFor(() => expect(requests.patchBody).not.toBeNull());
        return requests.patchBody!;
    }

    test("emptying every rate clears the price with an object of nulls", async () => {
        const body = await saveAndCapture(async user => {
            await user.clear(await screen.findByLabelText(/Input Price/));
            await user.clear(screen.getByLabelText(/Output Price/));
        });

        expect(body.pricing).toEqual({ input: null, output: null, cacheRead: null, cacheWrite: null });
    });
});

describe("ModelEditPage: Azure Workload Identity model where the deployment no longer offers it", () => {
    test("keeps the stored type selectable and lets the admin switch to an API key", async () => {
        setupEditHandlers(PROBE_PASSED);
        const azureModel = {
            ...anthropicModelConfig,
            alias: "azure-wi",
            provider: "azure_openai",
            modelName: "gpt-4.1-mini",
            apiBase: "https://sam.openai.azure.com/",
            authType: "azure_workload_identity",
            authConfig: { type: "azure_workload_identity", client_id: "11111111-2222-3333-4444-555555555555", tenant_id: "66666666-7777-8888-9999-000000000000" },
            modelParams: {},
        };
        server.use(http.get(MODEL_PATH, () => HttpResponse.json({ data: toModelV2(azureModel) })));

        render(
            <StoryProvider configContextValues={{ azureWorkloadIdentityEnabled: false }}>
                <MemoryRouter initialEntries={[`/models/${azureModel.id}/edit`]}>
                    <Routes>
                        <Route path="/models/:id/edit" element={<ModelEditPage />} />
                    </Routes>
                </MemoryRouter>
            </StoryProvider>
        );
        await screen.findAllByText("Edit azure-wi");

        await waitFor(() => expect(authTypeSelect()?.value).toBe("azure_workload_identity"));
        expect(Array.from(authTypeSelect()!.options).map(o => o.value)).toContain("apikey");
        expect(screen.queryByTestId("azureWorkloadIdentityInfo")).not.toBeInTheDocument();

        setAuthType("apikey");
        await waitFor(() => expect(document.querySelector('[name="apiKey"]')).toBeTruthy());
    });
});

describe("ModelEditPage: a model that calls with a desktop sign-in", () => {
    test("shows the sign-in type and no key field", async () => {
        setupEditHandlers(PROBE_PASSED);
        const delegatedModel = {
            ...anthropicModelConfig,
            alias: "general",
            provider: "openai",
            modelName: "free-model",
            authType: "oauth2_delegated",
            authConfig: { type: "oauth2_delegated", grant_id: "0192f7e2-8a4b-7c3d-9e5f-1a2b3c4d5e6f" },
            modelParams: {},
        };
        server.use(http.get(MODEL_PATH, () => HttpResponse.json({ data: toModelV2(delegatedModel) })));

        render(
            <StoryProvider>
                <MemoryRouter initialEntries={[`/models/${delegatedModel.id}/edit`]}>
                    <Routes>
                        <Route path="/models/:id/edit" element={<ModelEditPage />} />
                    </Routes>
                </MemoryRouter>
            </StoryProvider>
        );
        await screen.findAllByText("Edit general");

        expect(await screen.findByText("Sign-in")).toBeInTheDocument();
        expect(screen.queryByText("API Key")).not.toBeInTheDocument();
        expect(document.querySelector('[name="apiKey"]')).toBeFalsy();
    });
});
