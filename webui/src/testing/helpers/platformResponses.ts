// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Workflow } from "@/types/workflow";

import { toEntrypointV2 } from "@/testing/data/entrypointsV2";
import { agentToV2, connectorToV2, remoteAgentToV2, skillToV2, workflowToV2 } from "@/testing/data/platformWireV2";
import type { Agent, Connector, Gateway, RemoteAgent, Skill } from "@/types";

const asksForFullResponse = (args: unknown[]) => (args.at(-1) as { fullResponse?: boolean } | undefined)?.fullResponse === true;

export function respondingAsV2(mock: (...args: unknown[]) => unknown) {
    return async (...args: unknown[]): Promise<unknown> => {
        const body = await mock(...args);
        if (!asksForFullResponse(args) || body instanceof Response) return body;
        return Response.json(body ?? null);
    };
}

export function v2DeploymentOf(body: unknown, status: "success" | "failed" = "success") {
    return {
        data: { id: "deployment-1", action: "deploy", ...(body as object), status, failure: status === "failed" ? { reason: "runtime_error", message: "deploy failed", errorId: null } : null, createdBy: "", createdTime: null, updatedTime: null },
    };
}

export const v2Page = <T>(data: T[]) => ({ data, meta: { pagination: { pageNumber: 1, count: data.length, pageSize: 100, totalPages: 1, nextPage: null } } });

export const v2Error = (status: number, message: string) => Response.json({ message, errorId: "test-error" }, { status });

type PlatformRecord = { id?: string; name: string } & Record<string, unknown>;

const WIRE_BY_PATH: Array<[string, (record: PlatformRecord) => unknown]> = [
    ["/api/v2/platform/agents", r => agentToV2(r as unknown as Agent)],
    ["/api/v2/platform/workflows", r => workflowToV2(r as unknown as Workflow)],
    ["/api/v2/platform/remoteAgents", r => remoteAgentToV2(r as unknown as RemoteAgent)],
    ["/api/v2/platform/entrypoints", r => toEntrypointV2(r as unknown as Gateway)],
    ["/api/v2/platform/connectors", r => connectorToV2(r as unknown as Connector)],
    ["/api/v2/platform/skills", r => skillToV2(r as unknown as Skill)],
];

export function toV2Record(path: string, record: PlatformRecord): unknown {
    const wire = WIRE_BY_PATH.find(([prefix]) => path.startsWith(prefix))?.[1];
    if (!wire) throw new Error(`no /api/v2 wire form for ${path}`);
    return wire(record);
}

export const v2PageOf = (path: string, records: PlatformRecord[]) => v2Page(records.map(r => toV2Record(path, r)));

export const v2ResourceOf = (path: string, record: PlatformRecord) => ({ data: toV2Record(path, record) });

function inV2WireForm(path: string, body: unknown, answer: unknown): unknown {
    if (answer instanceof Response || !path.startsWith("/api/v2/platform/")) return answer;
    if (path.includes("Deployments")) return v2DeploymentOf(body);
    const record = (answer as { data?: unknown } | undefined)?.data ?? answer;
    if (path.includes("pageNumber=")) return v2PageOf(path, Array.isArray(record) ? record : []);
    return v2ResourceOf(path, { name: (body as { name?: string } | undefined)?.name ?? "", ...(record as object) });
}

type Spy = (...args: unknown[]) => unknown;

export function v2PlatformClient(spies: { get?: Spy; post?: Spy; put?: Spy; patch?: Spy; delete?: Spy }) {
    const read = (spy: Spy | undefined) => respondingAsV2(async (...args: unknown[]) => inV2WireForm(String(args[0]), undefined, await spy?.(args[0])));
    const write = (spy: Spy | undefined) => respondingAsV2(async (...args: unknown[]) => inV2WireForm(String(args[0]), args[1], await spy?.(args[0], args[1])));
    return { get: read(spies.get), delete: read(spies.delete), post: write(spies.post), put: write(spies.put), patch: write(spies.patch) };
}
