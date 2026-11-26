// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export type BudgetLevel = "none" | "warn" | "limit";

export interface BudgetStatus {
    ownLevel: BudgetLevel;
    meshLimitReached: boolean;
    periodEnd: string;
    capKey: string | null;
}
