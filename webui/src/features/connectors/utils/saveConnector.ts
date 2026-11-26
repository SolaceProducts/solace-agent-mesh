// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ConnectorsApi, connectorsApi } from "@/api/connectors/versioned";

import type { Connector } from "@/types";

import { parseKeyValuePairs } from "./authConfigBuilder";
import { isSpecificationLoadedFromUrl } from "./fetchConnector";

export interface SavedConnector {
    id: string;
    warnings: string[];
}

export class ConnectorResourceUploadError extends Error {
    readonly warnings: string[];

    constructor(cause: unknown, warnings: string[]) {
        super(cause instanceof Error ? cause.message : "Failed to upload the connector resource", { cause });
        this.name = "ConnectorResourceUploadError";
        this.warnings = warnings;
    }
}

const UNCHECKED_GATES_WARNING_SUFFIXES = [
    "the connector has no uploaded specification and none could be read from its specification URL, so a gate on an operation the specification lacks stops the agents bound to this connector from starting, or is not applied when an agent selects operations with an allow_list",
    "the uploaded specification could not be parsed, so a gate on an operation the specification lacks stops the agents bound to this connector from starting, or is not applied when an agent selects operations with an allow_list",
    "the uploaded specification could not be read, so a gate on an operation the specification lacks stops the agents bound to this connector from starting, or is not applied when an agent selects operations with an allow_list",
    "name no operation in the specification at the connector's specification URL, so agents bound to this connector that do not narrow operations with an allow_list fail to start until the key is removed or corrected",
];

const isUncheckedGatesWarning = (warning: string) => UNCHECKED_GATES_WARNING_SUFFIXES.some(suffix => warning.endsWith(suffix));

export async function saveConnector(connector: Connector, isNew: boolean, connectorId: string | undefined): Promise<SavedConnector> {
    const { connector: prepared, resource: attachedResource } = prepareConnectorForSave(connector);
    const resource = storedResourceToUpload(attachedResource);

    let savedId: string;
    const warnings: string[] = [];
    if (isNew) {
        const created = await connectorsApi.createFromForm(prepared);
        savedId = created?.id;
        if (!savedId) {
            throw new Error("Failed to create connector: No ID returned");
        }
        warnings.push(...(created.warnings ?? []));
    } else {
        if (!connectorId) {
            throw new Error("Connector id is required for updating existing connectors");
        }
        const updatePayload = {
            name: prepared.name,
            description: prepared.description,
            values: prepared.values,
        };
        const updated = await connectorsApi.update(connectorId, updatePayload);
        warnings.push(...(updated?.warnings ?? []));
        savedId = connectorId;
    }

    if (savedId && resource) {
        const uploadWarnings = await saveConnectorResourceOrRollBack(connectorsApi, savedId, resource, isNew, warnings);
        return { id: savedId, warnings: [...warnings.filter(warning => !isUncheckedGatesWarning(warning)), ...uploadWarnings] };
    }

    return { id: savedId, warnings };
}

function storedResourceToUpload(attachedResource: File | null): File | null {
    return attachedResource && !isSpecificationLoadedFromUrl(attachedResource) ? attachedResource : null;
}

async function saveConnectorResourceOrRollBack(api: ConnectorsApi, savedId: string, resource: File, isNew: boolean, saveWarnings: string[]): Promise<string[]> {
    try {
        return await saveConnectorResource(api, savedId, resource);
    } catch (error) {
        if (isNew) {
            try {
                await api.delete(savedId);
                console.error(`Connector '${savedId}' creation rolled back due to file upload failure`);
            } catch (deleteError) {
                console.error(`Failed to rollback connector '${savedId}' after file upload failure:`, deleteError);
            }
        }
        throw isNew ? error : new ConnectorResourceUploadError(error, saveWarnings);
    }
}

function prepareConnectorForSave(connector: Connector): {
    connector: Connector;
    resource: File | null;
} {
    const prepared = { ...connector };
    let resource: File | null = null;

    if (prepared.resources) {
        resource = prepared.resources[0];
        delete prepared.resources;
    }

    delete (prepared as any)._isValidationStep;
    delete (prepared as any).mcpTools;

    if (prepared.values.manifest && typeof prepared.values.manifest === "string") {
        try {
            prepared.values.manifest = JSON.parse(prepared.values.manifest);
        } catch (e) {
            console.warn("Failed to parse manifest JSON, sending as-is", e);
        }
    }

    for (const fieldName of Object.keys(prepared.values)) {
        const fieldValue = prepared.values[fieldName];
        if (Array.isArray(fieldValue) && fieldValue.length > 0 && typeof fieldValue[0] === "object" && fieldValue[0] !== null && "key" in fieldValue[0]) {
            prepared.values[fieldName] = parseKeyValuePairs(fieldValue);
        } else if (Array.isArray(fieldValue) && fieldValue.length === 0) {
            prepared.values[fieldName] = null;
        }
    }

    return { connector: prepared, resource };
}

async function saveConnectorResource(api: ConnectorsApi, connectorId: string, resource: File): Promise<string[]> {
    const formData = new FormData();
    formData.append("file", resource);
    const uploaded = await api.uploadResource(connectorId, formData);
    return uploaded?.warnings ?? [];
}
