// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ClaimMapping, RBACRole, User } from "@/types";

export type RoleRecord = RBACRole;

interface RbacDb {
    roles: RoleRecord[];
    users: User[];
    claimMappings: ClaimMapping[];
    defaultRoleIds: string[];
    budgetByUser: Record<string, string>;
    defaultBudgetId: string | null;
    budgets: Record<string, { name: string; amount: string | null }>;
    topUpByUser: Record<string, { amount: string; expiresTime: string }>;
}

export const db: RbacDb = {
    roles: [],
    users: [],
    claimMappings: [],
    defaultRoleIds: [],
    budgetByUser: {},
    defaultBudgetId: null,
    budgets: {},
    topUpByUser: {},
};

export interface RbacSeed {
    roles?: readonly RoleRecord[];
    users?: readonly User[];
    claimMappings?: readonly ClaimMapping[];
    defaultRoleIds?: readonly string[];
    budgetByUser?: Readonly<Record<string, string>>;
    defaultBudgetId?: string;
    budgets?: Readonly<Record<string, { name: string; amount: string | null }>>;
    topUpByUser?: Readonly<Record<string, { amount: string; expiresTime: string }>>;
}

export function resetStore(): void {
    db.roles = [];
    db.users = [];
    db.claimMappings = [];
    db.defaultRoleIds = [];
    db.budgetByUser = {};
    db.defaultBudgetId = null;
    db.budgets = {};
    db.topUpByUser = {};
}

export function seedStore(seed: RbacSeed): void {
    if (seed.roles) db.roles = seed.roles.map(r => ({ ...r, scopes: [...r.scopes] }));
    if (seed.users) db.users = seed.users.map(u => ({ ...u, roles: u.roles.map(role => ({ ...role })) }));
    if (seed.claimMappings) db.claimMappings = seed.claimMappings.map(m => ({ ...m, roleIds: [...m.roleIds] }));
    if (seed.defaultRoleIds) db.defaultRoleIds = [...seed.defaultRoleIds];
    if (seed.budgetByUser) db.budgetByUser = { ...seed.budgetByUser };
    if (seed.defaultBudgetId) db.defaultBudgetId = seed.defaultBudgetId;
    if (seed.budgets) db.budgets = { ...seed.budgets };
    if (seed.topUpByUser) db.topUpByUser = { ...seed.topUpByUser };
}

resetStore();

export function findRole(id: string): RoleRecord | undefined {
    return db.roles.find(r => r.id === id);
}

export function findUser(id: string): User | undefined {
    return db.users.find(u => u.id === id);
}
