// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { platformV2 } from "../common";

import { subjectQuery, usageListQuery, windowQuery } from "./service";
import type {
    UsageDetailFor,
    UsageKind,
    UsageListParams,
    UsageListResponse,
    UsageModelDetail,
    UsageModelRow,
    UsageRow,
    UsageRowFor,
    UsageSavings,
    UsageSelf,
    UsageSubjectDetail,
    UsageSubjectRef,
    UsageSummary,
    UsageUserDetail,
    UsageUserRow,
    UsageWindowParams,
    UsageWorkflowDetail,
    UsageWorkflowNode,
} from "./types";

type UsageResourceType = "agentUsage" | "workflowUsage" | "modelUsage" | "userUsage";

type Typed<T> = T & { type: UsageResourceType };

type UsageRowV2 = Typed<UsageRow>;
type ModelUsageRowV2 = Typed<UsageModelRow>;
type UserUsageRowV2 = Typed<Omit<UsageUserRow, "bookedName">>;

type UsageReportV2 = Typed<UsageSubjectDetail>;
type ModelUsageReportV2 = Typed<UsageModelDetail>;
type UserUsageReportV2 = Typed<UsageUserDetail>;
interface WorkflowUsageNodeV2 extends Omit<UsageWorkflowNode, "nodeId"> {
    key: string;
}
type WorkflowUsageReportV2 = Typed<Omit<UsageWorkflowDetail, "nodes">> & { nodes: WorkflowUsageNodeV2[] };

interface SelfUsageV2 extends Omit<UsageSelf, "id"> {
    userId: string | null;
}

type UsageRowV2For<K extends UsageKind> = K extends "users" ? UserUsageRowV2 : K extends "models" ? ModelUsageRowV2 : UsageRowV2;
type UsageReportV2For<K extends UsageKind> = K extends "workflows" ? WorkflowUsageReportV2 : K extends "users" ? UserUsageReportV2 : K extends "models" ? ModelUsageReportV2 : UsageReportV2;

const USAGE = "/usage";
const SELF_USAGE = "/userUsage";

function withoutType<T extends { type: UsageResourceType }>({ type: _type, ...rest }: T): Omit<T, "type"> {
    return rest;
}

function toUsageRow(kind: UsageKind, row: UsageRowV2 | ModelUsageRowV2 | UserUsageRowV2): UsageRow | UsageModelRow | UsageUserRow {
    const untyped = withoutType(row);
    return kind === "users" ? { ...(untyped as Omit<UserUsageRowV2, "type">), bookedName: null } : (untyped as UsageRow);
}

function toUsageDetail(kind: UsageKind, report: UsageReportV2 | ModelUsageReportV2 | UserUsageReportV2 | WorkflowUsageReportV2): UsageSubjectDetail | UsageUserDetail | UsageWorkflowDetail {
    const untyped = withoutType(report);
    if (kind !== "workflows") return untyped as UsageSubjectDetail | UsageUserDetail;
    const { nodes, ...workflow } = untyped as Omit<WorkflowUsageReportV2, "type">;
    return { ...workflow, nodes: nodes.map(({ key, ...node }) => ({ ...node, nodeId: key })) };
}

function toUsageSelf({ userId, ...rest }: SelfUsageV2): UsageSelf {
    return { ...rest, id: userId };
}

export async function getUsageSummaryV2(windowParams: UsageWindowParams): Promise<UsageSummary> {
    return platformV2.get<UsageSummary>(`${USAGE}/summary?${windowQuery(windowParams).toString()}`);
}

export async function listUsageV2<K extends UsageKind>(kind: K, params: UsageListParams<K>): Promise<UsageListResponse<K>> {
    const page = await platformV2.list<UsageRowV2For<K>>(`${USAGE}/${kind}?${usageListQuery(params).toString()}`);
    return { ...page, data: page.data.map(row => toUsageRow(kind, row) as UsageRowFor<K>) };
}

export async function getUsageDetailV2<K extends UsageKind>(kind: K, ref: UsageSubjectRef, windowParams: UsageWindowParams): Promise<UsageDetailFor<K>> {
    return toUsageDetail(kind, await platformV2.get<UsageReportV2For<K>>(`${USAGE}/${subjectQuery(kind, ref, windowParams)}`)) as UsageDetailFor<K>;
}

export async function getSelfUsageV2(windowParams: UsageWindowParams): Promise<UsageSelf> {
    return toUsageSelf(await platformV2.get<SelfUsageV2>(`${SELF_USAGE}?${windowQuery(windowParams).toString()}`));
}

export async function getUsageSavingsV2(windowParams: UsageWindowParams): Promise<UsageSavings> {
    return platformV2.get<UsageSavings>(`${USAGE}/savings?${windowQuery(windowParams).toString()}`);
}

export async function getSelfUsageSavingsV2(windowParams: UsageWindowParams): Promise<UsageSavings> {
    return platformV2.get<UsageSavings>(`${SELF_USAGE}/savings?${windowQuery(windowParams).toString()}`);
}
