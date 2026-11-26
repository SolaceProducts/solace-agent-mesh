// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

const BASE = "/user-management";

export const USER_MANAGEMENT_ROUTES = {
    base: BASE,
    users: `${BASE}/users`,
    roles: `${BASE}/roles`,
    role: (id: string) => `${BASE}/roles/${id}`,
    claimMapping: `${BASE}/claim-mapping`,
} as const;
