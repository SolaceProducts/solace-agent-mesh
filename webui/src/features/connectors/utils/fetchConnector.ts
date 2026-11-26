// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { connectorsApi } from "@/api/connectors/versioned";

import { getResource, invalidateResource } from "@/features/connectors/utils/resourcesCache";

import { type Connector, ConnectorType } from "@/types";

const HTTP_OK = 200;
const HTTP_NOT_FOUND = 404;
const PLATFORM_CONNECTOR_RESOURCE_PATH = /\/api\/v[12]\/platform\/connectors\/[^/]+\/resource$/;

type SpecificationLoad = { connector: Connector; error?: string };

const specFilesLoadedFromUrl = new WeakSet<File>();

export function isSpecificationLoadedFromUrl(file: File | undefined): boolean {
    return file !== undefined && specFilesLoadedFromUrl.has(file);
}

export async function fetchConnector(connector: Connector): Promise<{
    error?: string;
    connector?: Connector;
}> {
    switch (connector.type) {
        case ConnectorType.Api:
            return await fetchAPIConnector(connector);

        default:
            return { connector };
    }
}

const EXTERNAL_URL_PREFIX = /^https?:\/\//;

function externalSpecificationUrl(connector: Connector): URL | undefined {
    const value = connector.values?.specification_url;
    if (typeof value !== "string" || !EXTERNAL_URL_PREFIX.test(value)) return undefined;
    try {
        const url = new URL(value);
        return PLATFORM_CONNECTOR_RESOURCE_PATH.test(url.pathname) ? undefined : url;
    } catch {
        return undefined;
    }
}

const DISPLAY_PATH_PREFIX = /^\/api\/v[12]\/platform\/connectors\//;

function hasUnusableSpecificationUrl(connector: Connector): boolean {
    const value = connector.values?.specification_url;
    return typeof value === "string" && value.trim() !== "" && !EXTERNAL_URL_PREFIX.test(value) && !DISPLAY_PATH_PREFIX.test(value);
}

function describeSpecificationUrl(url: URL): string {
    return `${url.origin}${url.pathname}`;
}

function withSpecificationFile(connector: Connector, file: File): Connector {
    const dataTransfer = new DataTransfer();
    dataTransfer.items.add(file);
    return { ...connector, resources: dataTransfer.files };
}

function describeFailure(status: number | undefined, statusText: string | undefined): string {
    return statusText || `HTTP ${status}`;
}

async function loadStoredSpecification(connector: Connector): Promise<SpecificationLoad & { notFound?: boolean }> {
    const resourcePath = connectorsApi.resourcePath(connector.id);
    try {
        invalidateResource(resourcePath);
        const specResp = await getResource(resourcePath);
        if (specResp.status === HTTP_OK && specResp.text) {
            const fileName = `${connector.name || "connector"}-spec.${specResp.contentType.includes("yaml") ? "yaml" : "json"}`;
            return { connector: withSpecificationFile(connector, new File([specResp.text], fileName, { type: specResp.contentType })) };
        }
        return {
            connector,
            notFound: specResp.status === HTTP_NOT_FOUND,
            error: `Unable to fetch OpenAPI specification: ${specResp.status === HTTP_OK ? "the stored specification is empty" : describeFailure(specResp.status, specResp.statusText)}`,
        };
    } catch (error) {
        return { connector, error: `Error fetching OpenAPI specification: ${error}` };
    }
}

async function loadSpecificationFromUrl(connector: Connector, url: URL): Promise<SpecificationLoad> {
    try {
        const { content, format } = await connectorsApi.getSpecification(connector.id);
        const file = new File([content], `${connector.name || "connector"}-spec.${format}`, { type: `application/${format}` });
        specFilesLoadedFromUrl.add(file);
        return { connector: withSpecificationFile(connector, file) };
    } catch (error) {
        const reason = error instanceof Error ? error.message : String(error);
        return { connector, error: `Unable to read the OpenAPI specification from ${describeSpecificationUrl(url)}: ${reason}` };
    }
}

export async function fetchAPIConnector(connector: Connector): Promise<SpecificationLoad> {
    if (!connector.id || connector.id === "new") {
        return { connector };
    }

    const { notFound, ...stored } = await loadStoredSpecification(connector);
    const url = externalSpecificationUrl(connector);
    if (notFound && url) {
        return loadSpecificationFromUrl(connector, url);
    }
    if (notFound && hasUnusableSpecificationUrl(connector)) {
        return { connector, error: "The connector's specification URL is not an http:// or https:// URL. Correct it, or upload the specification file." };
    }
    return stored;
}
