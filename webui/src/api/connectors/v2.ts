// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Connector, ConnectorFieldSchema, ConnectorSubtypeSchema, ConnectorType } from "@/types";

import { fetchAllPages, instantToIso, pageQuery, PLATFORM_V2_PREFIX, platformV2, withoutNulls } from "../common";

import {
    absolutizeSpecURL,
    type ConnectorSecretsDiffResponse,
    type ConnectorsListResponse,
    type ConnectorSpecification,
    type ConnectorTypeSchema,
    type CreateConnectorRequest,
    type DetectAuthResponse,
    type DetectedAuthConfig,
    type GetConnectorsParams,
    type SecuritySchemeDetails,
    type UpdateConnectorRequest,
} from "./service";

interface ConnectorV2 {
    id: string;
    type: "connector";
    connectorType: string;
    connectorSubtype: string;
    name: string;
    description: string | null;
    values: Connector["values"];
    agentCount: number;
    hasResource: boolean;
    contentHash: string | null;
    warnings: string[];
    agentRegistrations: unknown[];
    createdBy: string | null;
    updatedBy: string | null;
    createdTime: string | null;
    updatedTime: string | null;
}

interface FieldValidationV2 {
    minLength: number | null;
    maxLength: number | null;
    min: number | null;
    max: number | null;
    pattern: string | null;
    errorMsg: string | null;
}

interface SchemaFieldV2 {
    label: string;
    type: string;
    required: boolean;
    secret: boolean;
    hidden: boolean;
    order: number;
    default: string | number | null;
    placeholder: string | null;
    hint: string | null;
    options: { label: string; value: string; description: string | null }[];
    validation: FieldValidationV2 | null;
    conditionalDisplay: Record<string, string> | null;
}

interface SubtypeSchemaV2 {
    subtype: string;
    name: string;
    description: string;
    usageGuidance: string | null;
    fieldSchema: Record<string, SchemaFieldV2>;
    connectionTemplate: string | null;
    warning: string | null;
    experimental: boolean;
}

interface ConnectorSchemaV2 {
    id: string;
    type: "connectorSchema";
    subtypes: SubtypeSchemaV2[];
}

interface DetectAuthResultV2 {
    schemes: { schemeName: string; authType: string; details: Record<string, string | null> }[];
    suggestedAuth: { authType: string; config: Record<string, unknown> } | null;
}

export function toConnectorUIModel(v2: ConnectorV2): Connector {
    const { id, name, description, values, agentCount, createdBy, updatedBy, createdTime, updatedTime } = v2;
    return {
        ...withoutNulls({ id, name, values, agentCount, createdBy, updatedBy }),
        description: description ?? "",
        type: v2.connectorType as ConnectorType,
        subtype: v2.connectorSubtype as Connector["subtype"],
        createdAt: instantToIso(createdTime),
        updatedAt: instantToIso(updatedTime),
    } as Connector;
}

const readable = (v2: ConnectorV2): Connector => absolutizeSpecURL(toConnectorUIModel(v2));

const savedWithWarnings = (v2: ConnectorV2): Connector => (v2.warnings?.length ? { ...toConnectorUIModel(v2), warnings: v2.warnings } : toConnectorUIModel(v2));

function toFieldSchema(field: SchemaFieldV2): ConnectorFieldSchema {
    const { options, validation, ...rest } = field;
    return {
        ...withoutNulls(rest),
        ...(options.length > 0 && { options: options.map(withoutNulls) }),
        ...(validation && { validation: withoutNulls(validation) }),
    } as ConnectorFieldSchema;
}

function toSubtypeSchema({ fieldSchema, ...rest }: SubtypeSchemaV2): ConnectorSubtypeSchema {
    const fields = Object.entries(fieldSchema).map(([key, field]) => [key, toFieldSchema(field)]);
    return { ...withoutNulls(rest), fieldSchema: Object.fromEntries(fields) } as ConnectorSubtypeSchema;
}

const V1_AUTH_TYPE: Record<string, string> = { mutual_tls: "mutualTLS", openid_connect: "openIdConnect" };
const toV1AuthType = (authType: string) => V1_AUTH_TYPE[authType] ?? authType;

const connectorBody = ({ name, description, type, subtype, values }: { name: string; description: string; type: string; subtype?: string; values?: unknown }) => ({
    name,
    description,
    connectorType: type,
    connectorSubtype: subtype,
    values,
});

export async function getConnectorsV2(params: GetConnectorsParams = {}): Promise<ConnectorsListResponse> {
    const name = params.name?.trim();
    const query = pageQuery({ pageNumber: params.pageNumber ?? 1, pageSize: params.pageSize ?? 100 }) + (name ? `&name=${encodeURIComponent(name)}` : "");
    const resp = await platformV2.list<ConnectorV2>(`/connectors?${query}`);
    return { ...resp, data: resp.data.map(readable) };
}

export async function getConnectorV2(id: string): Promise<Connector> {
    return readable(await platformV2.get<ConnectorV2>(`/connectors/${encodeURIComponent(id)}`));
}

export async function getAllConnectorsV2(): Promise<Connector[]> {
    const all = await fetchAllPages(params => platformV2.list<ConnectorV2>(`/connectors?${pageQuery(params)}`));
    return all.map(readable).sort((a, b) => a.name.localeCompare(b.name));
}

export async function createConnectorV2(payload: CreateConnectorRequest): Promise<{ id?: string } | null> {
    return toConnectorUIModel(await platformV2.post<ConnectorV2>("/connectors", connectorBody(payload)));
}

export async function createConnectorFromFormV2(connector: Connector): Promise<Connector> {
    return savedWithWarnings(await platformV2.post<ConnectorV2>("/connectors", connectorBody(connector)));
}

export async function updateConnectorV2(id: string, { name, description, values }: UpdateConnectorRequest): Promise<Connector> {
    const body = { ...(name && { name }), ...(description && { description }), ...(values && { values }) };
    return savedWithWarnings(await platformV2.patch<ConnectorV2>(`/connectors/${encodeURIComponent(id)}`, body));
}

export async function deleteConnectorV2(id: string): Promise<void> {
    await platformV2.delete(`/connectors/${encodeURIComponent(id)}`);
}

export async function uploadConnectorResourceV2(id: string, form: FormData): Promise<Connector> {
    return savedWithWarnings(await platformV2.put<ConnectorV2>(`/connectors/${encodeURIComponent(id)}/resource`, form));
}

export async function diffConnectorSecretsV2(id: string, values: Record<string, unknown>): Promise<ConnectorSecretsDiffResponse> {
    return platformV2.put<ConnectorSecretsDiffResponse>(`/connectors/${encodeURIComponent(id)}/secretsDiff`, { values });
}

export async function testConnectorConnectionV2(request: { connectorId?: string; values?: Record<string, unknown> }): Promise<{ success: boolean; message?: string }> {
    const { success, message } = await platformV2.put<{ success: boolean; message: string | null }>("/connectors/testConnection", request);
    return withoutNulls({ success, message });
}

export async function getConnectorSchemasV2(): Promise<{ data: ConnectorTypeSchema[] }> {
    const all = await fetchAllPages(params => platformV2.list<ConnectorSchemaV2>(`/connectorSchemas?${pageQuery(params)}`));
    return { data: all.map(({ id, subtypes }) => ({ type: id as ConnectorType, subtypes: subtypes.map(toSubtypeSchema) })) };
}

async function specificationOf(form: FormData): Promise<{ specificationContent: string } | { specificationUrl: string }> {
    const file = form.get("file");
    if (file instanceof Blob) return { specificationContent: await file.text() };
    const url = form.get("specificationUrl");
    return { specificationUrl: typeof url === "string" ? url : "" };
}

export async function detectConnectorAuthV2(form: FormData): Promise<DetectAuthResponse> {
    const result = await platformV2.put<DetectAuthResultV2>("/connectors/openapi/detectAuth", await specificationOf(form));
    const suggested: DetectedAuthConfig | null = result.suggestedAuth && { authType: toV1AuthType(result.suggestedAuth.authType), config: result.suggestedAuth.config };
    return {
        schemes: result.schemes.map(({ schemeName, authType, details }) => ({ schemeName, authType: toV1AuthType(authType), details: withoutNulls(details) as SecuritySchemeDetails["details"] })),
        suggestedAuth: suggested,
    };
}

export async function getConnectorSpecificationV2(id: string): Promise<ConnectorSpecification> {
    return platformV2.get<ConnectorSpecification>(`/connectors/${encodeURIComponent(id)}/specification`);
}

export function connectorResourcePathV2(id: string): string {
    return `${PLATFORM_V2_PREFIX}/connectors/${id}/resource`;
}
