// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/* eslint-disable @typescript-eslint/no-explicit-any -- fixtures intentionally cast partial Connector shapes through any. */
import { describe, expect, it } from "vitest";

import { type Connector, ConnectorType } from "@/types";

import { translateMCPErrorMessage, validateConnector } from "./validateConnector";

function makeOpenApiConnector(overrides: Partial<Connector> = {}): Connector {
    return {
        type: ConnectorType.Api,
        subtype: "openapi",
        values: { allow_list: ["getThing"] },
        resources: { length: 1 } as FileList,
        ...(overrides as any),
    } as Connector;
}

describe("translateMCPErrorMessage", () => {
    it.each(["TaskGroup error from upstream", "401 Unauthorized", "403 forbidden", "Unauthorized: bad token"])("maps auth-related message '%s' to a friendly auth error", raw => {
        expect(translateMCPErrorMessage(raw)).toBe("Failed to authenticate with the MCP server.");
    });

    it.each(["protocol error", "Invalid URL: missing protocol"])("maps URL/protocol message '%s' to an invalid-URL error", raw => {
        expect(translateMCPErrorMessage(raw)).toBe("Invalid server URL. Please ensure the URL starts with https:// and is correctly formatted.");
    });

    it.each(["timeout exceeded", "ECONNREFUSED 127.0.0.1:8080", "ETIMEDOUT"])("maps network reachability message '%s' to an unreachable-server error", raw => {
        expect(translateMCPErrorMessage(raw)).toBe("Unable to reach the MCP server. Please check if the server is running and accessible.");
    });

    it("preserves messages that already start with 'Failed to'", () => {
        expect(translateMCPErrorMessage("Failed to do the thing in a very specific way")).toBe("Failed to do the thing in a very specific way");
    });

    it("preserves messages that already start with 'Unable to'", () => {
        expect(translateMCPErrorMessage("Unable to render thumbnail for some reason")).toBe("Unable to render thumbnail for some reason");
    });

    it("uses the generic fallback for cryptic messages that match no rule", () => {
        expect(translateMCPErrorMessage("something else")).toBe("Failed to connect to MCP server. Please verify the server URL and authentication settings.");
    });

    it.each([
        "The MCP server URL resolves to a loopback or private network address, which is blocked for security (SSRF protection). Use a publicly reachable server URL. If it is a private-network address this deployment is meant to reach, an administrator can allow it.",
        "The MCP server requires authentication (HTTP 401). If it uses OAuth, complete the authorization step; otherwise check the token or API key.",
        "The MCP server refused access (HTTP 403). Verify your credentials and that your account is permitted to reach it.",
        "A network proxy blocked the request (HTTP 407) — usually a corporate firewall or IT policy. Ask your network administrator to allow access to the MCP server URL.",
        "The MCP server returned an unexpected HTTP response. This usually means the URL is wrong or a network/firewall (IT block) is intercepting the request. Verify the server URL and that it is reachable from this environment.",
        "Could not resolve the MCP server host. Please check the server URL.",
        "The MCP server refused the connection. Check the URL and port, and that the server is running.",
        "The connection to the MCP server timed out. It may be unreachable from this environment due to a network or firewall restriction.",
        "The MCP server did not complete the SSE handshake. Check that the server URL is its SSE endpoint, that the connection type matches the server, and that the server advertises an endpoint on its own address.",
        "The MCP server speaks only MCP protocol versions this platform does not support.",
        "The MCP server's tool list did not finish: it kept returning the same page cursor.",
        "TLS/certificate error connecting to the MCP server. Verify it is reachable over HTTPS with a valid certificate.",
        "Could not connect to the MCP server. Verify the server URL and that it is reachable from this environment.",
        "The MCP server answered the discovery request with an error. Its logs will show the reason.",
        "The MCP server's reply exceeded the size limit for tool discovery.",
    ])("shows the platform's discovery message '%s' as is", platformMessage => {
        expect(translateMCPErrorMessage(platformMessage)).toBe(platformMessage);
    });

    it("uses the default 'Unable to connect' message when no message is provided", () => {
        expect(translateMCPErrorMessage(undefined)).toBe("Unable to connect to MCP server.");
    });
});

describe("validateConnector (no-network paths)", () => {
    it("returns immediate success + nextStep 'save' for MCP when not on the validation step", async () => {
        const connector = {
            type: ConnectorType.Mcp,
            values: { server_url: "https://mcp.example.com" },
        } as unknown as Connector;
        const result = await validateConnector(connector, false);
        expect(result).toEqual({ success: true, nextStep: "save" });
    });

    it("returns success for API connectors whose subtype is not openapi", async () => {
        const result = await validateConnector({ type: ConnectorType.Api, subtype: "rest", values: {} } as unknown as Connector, true);
        expect(result).toEqual({ success: true });
    });

    it("rejects an OpenAPI connector that has no spec file", async () => {
        const result = await validateConnector(makeOpenApiConnector({ resources: undefined }), true);
        expect(result).toEqual({ success: false, error: "An OpenAPI specification file is required" });
    });

    it("rejects an OpenAPI connector whose allowList is empty", async () => {
        const result = await validateConnector(makeOpenApiConnector({ values: { allow_list: [] } }), true);
        expect(result).toEqual({ success: false, error: "At least one tool must be selected" });
    });

    it("accepts an OpenAPI connector with a spec file and a non-empty allowList", async () => {
        const result = await validateConnector(makeOpenApiConnector(), true);
        expect(result).toEqual({ success: true });
    });

    it("returns success for connector types that have no special validation", async () => {
        const result = await validateConnector({ type: ConnectorType.Sql, values: {} } as unknown as Connector, true);
        expect(result).toEqual({ success: true });
    });
});
