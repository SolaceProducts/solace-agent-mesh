// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { AppliedBudget, AppliedTopUp } from "@/api/budgets/types";

import type {
    ClaimMapping,
    ClaimMappingOrigin,
    CreateClaimMappingRequest,
    CreateRoleRequest,
    DefaultRolesOrigin,
    DefaultRolesResponse,
    RBACRole,
    RoleOrigin,
    SystemUser,
    UpdateClaimMappingRequest,
    UpdateRoleRequest,
    User,
    UserRoleSource,
} from "@/types";

import { fetchAllPages, instantToIso, pageQuery, type PaginatedResponse, platformV2 } from "../common";

import type { ComponentAccessPageParams, ComponentAccessTarget, ComponentAccessUser, GetUserParams, ListClaimMappingsParams, ListUsersParams } from "./service";

interface UserAccessV2 {
    userId: string;
    displayName: string | null;
    userName: string | null;
    email: string | null;
    isSystemUser: boolean;
}

function toComponentAccessUser({ userId, displayName, userName, email, isSystemUser }: UserAccessV2): ComponentAccessUser {
    return { id: userId, displayName: displayName ?? "", userName: userName ?? "", email: email ?? "", isSystemUser };
}

function componentAccessPathV2(target: ComponentAccessTarget): string {
    return "agentId" in target ? `/agents/${encodeURIComponent(target.agentId)}/access` : `/workflows/${encodeURIComponent(target.workflowId)}/access`;
}

export async function listComponentAccessUsersV2(target: ComponentAccessTarget, params: ComponentAccessPageParams): Promise<PaginatedResponse<ComponentAccessUser>> {
    const page = await platformV2.list<UserAccessV2>(`${componentAccessPathV2(target)}?${listQuery(params)}`);
    return toPage(page, toComponentAccessUser);
}

interface RoleV2 {
    id: string;
    name: string;
    description: string | null;
    scopes: string[];
    inherits: string[];
    origin: RoleOrigin;
    createdBy: string | null;
    updatedBy: string | null;
    createdTime: string | null;
    updatedTime: string | null;
}

interface RoleIdsV2 {
    roleIds: string[];
}

interface DefaultRolesV2 {
    roleIds: string[];
    origin: DefaultRolesOrigin;
}

interface UserV2 {
    id: string;
    displayName: string | null;
    userName: string | null;
    email: string | null;
    issuer: string | null;
    sub: string | null;
    oidcProvider: string | null;
    provisionedVia: string;
    active: boolean;
    groups: string[];
    roles: { roleId: string; source: UserRoleSource; manageable: boolean }[];
    lastLoginTime: string | null;
    createdBy: string | null;
    updatedBy: string | null;
    createdTime: string | null;
    updatedTime: string | null;
    budget: AppliedBudget | null;
    budgetTopUp: AppliedTopUp | null;
    effectiveBudgetAmount: string | null;
}

interface SystemUserV2 {
    id: string;
    name: string;
    sub: string;
    displayName: string | null;
    roleIds: string[];
}

interface ClaimMappingV2 {
    id: string;
    name: string | null;
    oidcProvider: string | null;
    claimKey: string | null;
    claimValue: string;
    roleIds: string[];
    origin: ClaimMappingOrigin;
    createdBy: string | null;
    updatedBy: string | null;
    createdTime: string | null;
    updatedTime: string | null;
}

function toRole(r: RoleV2): RBACRole {
    return {
        id: r.id,
        name: r.name,
        description: r.description ?? "",
        origin: r.origin,
        scopes: r.scopes,
        inherits: r.inherits,
        createdBy: r.createdBy ?? "",
        updatedBy: r.updatedBy ?? "",
        createdAt: instantToIso(r.createdTime),
        updatedAt: instantToIso(r.updatedTime),
    };
}

function toUser(u: UserV2, expandBudget: boolean): User {
    const user: User = {
        id: u.id,
        displayName: u.displayName ?? undefined,
        userName: u.userName ?? undefined,
        email: u.email ?? undefined,
        issuer: u.issuer ?? undefined,
        sub: u.sub ?? undefined,
        oidcProvider: u.oidcProvider ?? undefined,
        provisionedVia: u.provisionedVia,
        active: u.active,
        groups: u.groups,
        roles: u.roles.map(({ roleId, source }) => ({ roleId, source })),
        lastLoginAt: u.lastLoginTime === null ? undefined : instantToIso(u.lastLoginTime),
        createdBy: u.createdBy ?? undefined,
        updatedBy: u.updatedBy ?? undefined,
        createdAt: instantToIso(u.createdTime),
        updatedAt: instantToIso(u.updatedTime),
    };
    if (expandBudget) {
        user.budget = u.budget;
        user.budgetTopUp = u.budgetTopUp;
        user.effectiveBudgetAmount = u.effectiveBudgetAmount;
    }
    return user;
}

function toSystemUser(u: SystemUserV2): SystemUser {
    return { id: u.id, sub: u.sub, name: u.name, displayName: u.displayName ?? undefined, roleIds: u.roleIds };
}

function toClaimMapping(m: ClaimMappingV2): ClaimMapping {
    return {
        id: m.id,
        name: m.name ?? "",
        oidcProvider: m.oidcProvider ?? "",
        claimKey: m.claimKey ?? "",
        claimValue: m.claimValue,
        roleIds: m.roleIds,
        origin: m.origin,
        createdBy: m.createdBy ?? "",
        updatedBy: m.updatedBy ?? "",
        createdAt: instantToIso(m.createdTime),
        updatedAt: instantToIso(m.updatedTime),
    };
}

const toPage = <W, T>(page: PaginatedResponse<W>, to: (w: W) => T): PaginatedResponse<T> => ({ ...page, data: page.data.map(to) });

function listQuery(params: { pageNumber: number; pageSize: number; search?: string; roleId?: string; budgetId?: string; sort?: string; expandBudget?: boolean }): string {
    const query = new URLSearchParams(pageQuery(params));
    const search = params.search?.trim();
    if (search) query.set("search", search);
    if (params.roleId) query.set("roleId", params.roleId);
    if (params.budgetId) query.set("budgetId", params.budgetId);
    if (params.sort) query.set("sort", params.sort);
    if (params.expandBudget) query.set("expand", "budget");
    return query.toString();
}

const ROLES = "/rbac/roles";
const role = (id: string) => `${ROLES}/${encodeURIComponent(id)}`;

export async function listRolesV2(): Promise<RBACRole[]> {
    const all = await fetchAllPages(params => platformV2.list<RoleV2>(`${ROLES}?${pageQuery(params)}`));
    return all.map(toRole);
}

export async function getRoleV2(id: string): Promise<RBACRole> {
    return toRole(await platformV2.get<RoleV2>(role(id)));
}

export async function createRoleV2(req: CreateRoleRequest): Promise<RBACRole> {
    return toRole(await platformV2.post<RoleV2>(ROLES, { name: req.name, description: req.description ?? "", scopes: req.scopes ?? [], inherits: [] }));
}

export async function updateRoleV2(id: string, req: UpdateRoleRequest): Promise<RBACRole> {
    return toRole(await platformV2.put<RoleV2>(role(id), { name: req.name, description: req.description, scopes: req.scopes, inherits: req.inherits ?? [] }));
}

export async function deleteRoleV2(id: string): Promise<void> {
    await platformV2.delete(role(id));
}

const DEFAULT_ROLES = "/rbac/defaultRoles";

export async function getDefaultRolesV2(): Promise<DefaultRolesResponse> {
    const { roleIds, origin } = await platformV2.get<DefaultRolesV2>(DEFAULT_ROLES);
    return { roleIds, origin };
}

export async function setDefaultRolesV2(roleIds: string[]): Promise<DefaultRolesResponse> {
    const resp = await platformV2.put<DefaultRolesV2>(DEFAULT_ROLES, { roleIds });
    return { roleIds: resp.roleIds, origin: resp.origin };
}

const user = (id: string) => `/users/${encodeURIComponent(id)}`;

export async function listUsersV2(params: ListUsersParams): Promise<PaginatedResponse<User>> {
    const page = await platformV2.list<UserV2>(`/users?${listQuery(params)}`);
    return toPage(page, u => toUser(u, Boolean(params.expandBudget)));
}

export async function getUserV2(id: string, params: GetUserParams = {}): Promise<User> {
    const query = params.expandBudget ? "?expand=budget" : "";
    return toUser(await platformV2.get<UserV2>(`${user(id)}${query}`), Boolean(params.expandBudget));
}

export async function getUserRoleGrantsV2(userId: string): Promise<string[]> {
    return (await platformV2.get<RoleIdsV2>(`${user(userId)}/roleGrants`)).roleIds;
}

export async function setUserRoleGrantsV2(userId: string, roleIds: string[]): Promise<string[]> {
    return (await platformV2.put<RoleIdsV2>(`${user(userId)}/roleGrants`, { roleIds })).roleIds;
}

export async function listSystemUsersV2(): Promise<PaginatedResponse<SystemUser>> {
    return toPage(await platformV2.list<SystemUserV2>("/rbac/serviceAccounts?pageSize=100"), toSystemUser);
}

const CLAIM_MAPPINGS = "/rbac/claimMappings";
const claimMapping = (id: string) => `${CLAIM_MAPPINGS}/${encodeURIComponent(id)}`;

export async function listClaimMappingsV2(params: ListClaimMappingsParams): Promise<PaginatedResponse<ClaimMapping>> {
    return toPage(await platformV2.list<ClaimMappingV2>(`${CLAIM_MAPPINGS}?${listQuery(params)}`), toClaimMapping);
}

export async function createClaimMappingV2(req: CreateClaimMappingRequest): Promise<ClaimMapping> {
    return toClaimMapping(
        await platformV2.post<ClaimMappingV2>(CLAIM_MAPPINGS, {
            name: req.name,
            ...(req.oidcProvider === undefined ? {} : { oidcProvider: req.oidcProvider }),
            claimKey: req.claimKey,
            claimValue: req.claimValue,
            roleIds: req.roleIds,
        })
    );
}

export async function updateClaimMappingV2(id: string, req: UpdateClaimMappingRequest): Promise<ClaimMapping> {
    return toClaimMapping(
        await platformV2.put<ClaimMappingV2>(claimMapping(id), {
            name: req.name,
            oidcProvider: req.oidcProvider,
            claimKey: req.claimKey,
            claimValue: req.claimValue,
            roleIds: req.roleIds,
        })
    );
}

export async function deleteClaimMappingV2(id: string): Promise<void> {
    await platformV2.delete(claimMapping(id));
}
