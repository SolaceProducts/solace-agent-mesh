// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import type { ConnectorTypeSchema } from "@/api/connectors/service";
import { connectorsApi } from "@/api/connectors/versioned";

import { mockConnectorsSchemaMap } from "@/testing/data";
import { ConnectorType } from "@/types";

import exaLogo from "../assets/exa.svg";
import tavilyLogo from "../assets/tavily.svg";

import { SCHEMA_TYPE_NAME_MAP, useConnectorSchemas } from "./useConnectorSchemas";

const webSearchTypeSchema: ConnectorTypeSchema = {
    type: ConnectorType.WebSearch,
    subtypes: [mockConnectorsSchemaMap.get("web_search-tavily")!, mockConnectorsSchemaMap.get("web_search-exa")!],
};

describe("useConnectorSchemas for the web_search type", () => {
    beforeEach(() => {
        vi.spyOn(connectorsApi, "getSchemas").mockResolvedValue({ data: [webSearchTypeSchema] });
    });

    test("labels the type Web Search, distinct from the Elasticsearch/OpenSearch Search type", () => {
        expect(SCHEMA_TYPE_NAME_MAP.webSearch).toBe("Web Search");
        expect(SCHEMA_TYPE_NAME_MAP.search).toBe("Search");
    });

    test.each([
        { subtype: "tavily", name: "Tavily | Web Search", logo: tavilyLogo },
        { subtype: "exa", name: "Exa | Web Search", logo: exaLogo },
    ])("names the $subtype subtype '$name' and gives it the provider's logo", async ({ subtype, name, logo }) => {
        const { result } = renderHook(() => useConnectorSchemas({ enabled: true }));

        await waitFor(() => expect(result.current.connectorSchemas).toHaveLength(2));
        const schema = result.current.getConnectorSchema("web_search", subtype);

        expect(schema?.name).toBe(name);
        expect(schema?.type).toBe("web_search");
        expect(schema?.icon).toBe(logo);
    });

    test.each(["tavily", "exa"])("keeps the %s field schema the form renders, with approvals hidden", async subtype => {
        const { result } = renderHook(() => useConnectorSchemas({ enabled: true }));

        await waitFor(() => expect(result.current.connectorSchemas).toHaveLength(2));
        const fields = result.current.getConnectorSchema("web_search", subtype)?.fieldSchema ?? {};
        const visibleLabels = Object.values(fields)
            .filter(field => !field.hidden)
            .map(field => field.label);

        expect(visibleLabels).toEqual(["API Key", "API Base URL", "Results per Search", "Safe Search"]);
        expect(fields.api_key).toMatchObject({ type: "password", secret: true, required: true });
        expect(fields.hil).toMatchObject({ type: "hil", hidden: true });
    });
});
