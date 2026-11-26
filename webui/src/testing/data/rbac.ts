// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type RbacSeed, resetStore, type RoleRecord, seedStore } from "@/api/rbac/store";

import { queryClient } from "@/providers/query/QueryClient";

import { anyInstanceScope, instanceScope, SCOPES, SUPERADMIN_SCOPE } from "@/utils/scopes";

import { COMPONENT_SCOPES, ENTRYPOINT_SCOPES, EVALUATION_SCOPES } from "@/features/userManagement/scopeBundles/bundles";

import type { ClaimMapping, RoleOrigin, User, UserRoleAssignment, UserRoleSource } from "@/types";

import { mockApiUserBudgets } from "./budgets";

const T0 = "2026-01-15T09:00:00.000+00:00";
const T1 = "2026-03-02T14:30:00.000+00:00";

const HR_AGENT_ID = "agent_019eb6a2-b940-781a-8def-91234b461f80";

const consumerScopes = [instanceScope("agent", HR_AGENT_ID, "invoke")];

const viewerScopes = [...EVALUATION_SCOPES.view, SCOPES.analytics.read, SCOPES.analytics.read_content, SCOPES.deployment.read, SCOPES.rbac.read];

const editorScopes = [...COMPONENT_SCOPES, ...EVALUATION_SCOPES.createRun, SCOPES.builder.use, SCOPES.profile_provider.update];

const deployerScopes = [...editorScopes, ...ENTRYPOINT_SCOPES];

const administratorScopes = [...deployerScopes, SCOPES.rbac.read];

const samManagerScopes = [
    ...["agent_builder", "connector", "entrypoint", "skill", "toolset", "model_config", "workflow_builder", "tool"].map(category => anyInstanceScope(category, "*")),
    ...["evaluation", "deployment", "profile_provider", "builder", "webui_settings", "budget"].map(category => anyInstanceScope(category, "*")),
    ...[
        ["agent", "invoke"],
        ["workflow", "invoke"],
        ["agent", "read_feedback"],
        ["workflow", "read_feedback"],
        ["analytics", "read"],
        ["notify", "send"],
        ["notify", "send_others"],
    ].map(([category, verb]) => anyInstanceScope(category, verb)),
    SCOPES.usage.readAll,
];

const readOnlyRole = (origin: Extract<RoleOrigin, "yaml" | "builtin">, id: string, name: string, description: string, scopes: string[]): RoleRecord => ({
    id,
    name,
    description,
    origin,
    scopes,
    createdBy: "system",
    updatedBy: "system",
    createdAt: T0,
    updatedAt: T0,
});

export const SEED_ROLES: RoleRecord[] = [
    readOnlyRole("yaml", "role-consumer", "Consumer", "Use built-in tools.", consumerScopes),
    readOnlyRole("yaml", "role-viewer", "Viewer", "Read-only visibility across builder resources, analytics, evaluations, and RBAC.", viewerScopes),
    readOnlyRole("yaml", "role-editor", "Editor", "Create and modify agents, workflows, connectors, gateways, models, skills, and toolsets.", editorScopes),
    readOnlyRole("yaml", "role-deployer", "Deployer", "Everything an Editor can do, plus deploy agents and gateways.", deployerScopes),
    readOnlyRole("yaml", "role-administrator", "Administrator", "Full administrative access, including RBAC management.", administratorScopes),
    readOnlyRole("yaml", "role-superadmin", "Superadmin", "Unrestricted wildcard access to every permission.", [SUPERADMIN_SCOPE]),
    readOnlyRole("builtin", "00000000-0000-0000-0000-000000000001", "sam_manager", "SAM Manager — administer everything in SAM except RBAC management.", samManagerScopes),
    {
        id: "role-hr-agent-user",
        name: "HR Agent User",
        description: "Invoke the HR assistant agent.",
        origin: "custom",
        scopes: [instanceScope("agent", HR_AGENT_ID, "invoke")],
        createdBy: "alice@example.com",
        updatedBy: "alice@example.com",
        createdAt: T1,
        updatedAt: T1,
    },
    {
        id: "role-evaluations-analyst",
        name: "Evaluations Analyst",
        description: "View and modify evaluations.",
        origin: "custom",
        scopes: [...EVALUATION_SCOPES.createRun, SCOPES.analytics.read],
        createdBy: "bob@example.com",
        updatedBy: "bob@example.com",
        createdAt: T1,
        updatedAt: T1,
    },
];

const direct = (roleId: string): UserRoleAssignment => ({ roleId, source: "direct" });
const inherited = (roleId: string, source: Exclude<UserRoleSource, "direct">): UserRoleAssignment => ({ roleId, source });

export const SEED_USERS: User[] = [
    { id: "1a000000-0000-4000-8000-000000000001", displayName: "Alice Anderson", email: "alice@example.com", sub: "oidc|alice", roles: [inherited("role-administrator", "yaml")] },
    { id: "1a000000-0000-4000-8000-000000000002", displayName: "Bob Brown", email: "bob@example.com", sub: "oidc|bob", roles: [inherited("role-consumer", "yaml")] },
    { id: "1a000000-0000-4000-8000-000000000003", displayName: "Carol Clarke", email: "carol@example.com", sub: "oidc|carol", roles: [direct("role-hr-agent-user")] },
    { id: "1a000000-0000-4000-8000-000000000004", displayName: "Dave Davies", email: "dave@example.com", sub: "oidc|dave", roles: [direct("role-hr-agent-user"), inherited("role-evaluations-analyst", "claim")] },
    { id: "1a000000-0000-4000-8000-000000000005", displayName: "Erin Evans", email: "erin@example.com", sub: "oidc|erin", roles: [direct("role-evaluations-analyst")] },
    { id: "1a000000-0000-4000-8000-000000000006", displayName: "Frank Foster", email: "frank@example.com", sub: "oidc|frank", roles: [inherited("role-superadmin", "yaml")] },
    { id: "1a000000-0000-4000-8000-000000000007", displayName: "Grace Green", email: "grace@example.com", sub: "oidc|grace", roles: [direct("role-hr-agent-user"), direct("role-evaluations-analyst")] },
    { id: "1a000000-0000-4000-8000-000000000008", email: "heidi.olsson@example.com", sub: "oidc|heidi", roles: [inherited("role-deployer", "yaml")] },
    { id: "1a000000-0000-4000-8000-000000000009", email: "ivan.petrov@example.com", sub: "oidc|ivan", roles: [inherited("role-consumer", "yaml")] },
    { id: "1a000000-0000-4000-8000-00000000000a", displayName: "Judy Jones", email: "judy@example.com", sub: "oidc|judy", roles: [inherited("role-viewer", "yaml"), direct("role-hr-agent-user")] },
    { id: "1a000000-0000-4000-8000-00000000000b", displayName: "Mallory Mills", email: "mallory@example.com", sub: "oidc|mallory", roles: [inherited("role-evaluations-analyst", "default")] },
    { id: "1a000000-0000-4000-8000-00000000000c", email: "niaj.khan@example.com", sub: "oidc|niaj", roles: [] },
];

export const SEED_CLAIM_MAPPINGS: ClaimMapping[] = [
    {
        id: "claim-engineering",
        name: "Engineering",
        oidcProvider: "azure",
        claimKey: "groups",
        claimValue: "1dca4db7-9ff1-4fd5-bc28-93755f43c828",
        roleIds: ["role-editor"],
        origin: "custom",
        createdBy: "alice@example.com",
        updatedBy: "alice@example.com",
        createdAt: T0,
        updatedAt: T0,
    },
    {
        id: "claim-support",
        name: "Support",
        oidcProvider: "azure",
        claimKey: "groups",
        claimValue: "7b3e2c10-4a6d-4f9b-8c21-0f5a7d2e9b14",
        roleIds: ["role-consumer", "role-viewer"],
        origin: "custom",
        createdBy: "alice@example.com",
        updatedBy: "bob@example.com",
        createdAt: T0,
        updatedAt: T1,
    },
    {
        id: "claim-hr",
        name: "HR Team",
        oidcProvider: "azure",
        claimKey: "groups",
        claimValue: "c9f0a1b2-3d4e-5f60-7a89-b0c1d2e3f405",
        roleIds: ["role-hr-agent-user"],
        origin: "custom",
        createdBy: "bob@example.com",
        updatedBy: "bob@example.com",
        createdAt: T1,
        updatedAt: T1,
    },
    {
        id: "claim-platform-admins",
        name: "Platform Admins",
        oidcProvider: "azure",
        claimKey: "groups",
        claimValue: "f4e3d2c1-b0a9-4877-9665-5443322110ff",
        roleIds: ["role-administrator"],
        origin: "custom",
        createdBy: "alice@example.com",
        updatedBy: "alice@example.com",
        createdAt: T1,
        updatedAt: T1,
    },
];

export const SEED_DEFAULT_ROLE_IDS = ["role-evaluations-analyst"];

export const FULL_RBAC_SEED: RbacSeed = { roles: SEED_ROLES, users: SEED_USERS, claimMappings: SEED_CLAIM_MAPPINGS, defaultRoleIds: SEED_DEFAULT_ROLE_IDS };

export function makeManyUsers(count = 25): User[] {
    const directRoleCycle = ["role-hr-agent-user", "role-evaluations-analyst"];
    return Array.from({ length: count }, (_, i) => {
        const num = String(i + 1).padStart(3, "0");
        return {
            id: `2c000000-0000-4000-8000-000000000${num}`,
            displayName: `User ${num}`,
            email: `user${num}@example.com`,
            sub: `oidc|user${num}`,
            roles: [direct(directRoleCycle[i % directRoleCycle.length])],
        };
    });
}

export const MANY_USERS_SEED: RbacSeed = { roles: SEED_ROLES, users: makeManyUsers() };

export const BUDGET_RBAC_SEED = {
    budgets: Object.fromEntries(mockApiUserBudgets.map(budget => [budget.id, { name: budget.name, amount: budget.amount }])),
    budgetByUser: { [SEED_USERS[0].id]: "budget-engineering" },
    defaultBudgetId: "budget-standard",
} satisfies RbacSeed;

export function makeManyRoles(count = 25): RoleRecord[] {
    const generated = Array.from({ length: count }, (_, i) => {
        const num = String(i + 1).padStart(3, "0");
        return {
            id: `role-generated-${num}`,
            name: `Role ${num}`,
            description: `Generated role ${num}.`,
            origin: "custom" as const,
            scopes: [SCOPES.analytics.read],
            createdBy: "alice@example.com",
            updatedBy: "alice@example.com",
            createdAt: T1,
            updatedAt: T1,
        };
    });
    return [...SEED_ROLES, ...generated];
}

export function makeManyClaimMappings(count = 25): ClaimMapping[] {
    const roleCycle = ["role-viewer", "role-editor", "role-consumer"];
    return Array.from({ length: count }, (_, i) => {
        const num = String(i + 1).padStart(3, "0");
        const isYaml = i === count - 1;
        return {
            id: `claim-generated-${num}`,
            name: isYaml ? "" : `Mapping ${num}`,
            oidcProvider: "azure",
            claimKey: "groups",
            claimValue: `group-${num}`,
            roleIds: [roleCycle[i % roleCycle.length]],
            origin: isYaml ? ("yaml" as const) : ("custom" as const),
            createdBy: "alice@example.com",
            updatedBy: "alice@example.com",
            createdAt: T0,
            updatedAt: T0,
        };
    });
}

export const MANY_CLAIM_MAPPINGS_SEED: RbacSeed = { roles: SEED_ROLES, claimMappings: makeManyClaimMappings() };

export function seedRbacForStory(seed: RbacSeed = {}): () => void {
    resetStore();
    seedStore(seed);
    queryClient.clear();
    return () => {
        resetStore();
        queryClient.clear();
    };
}
