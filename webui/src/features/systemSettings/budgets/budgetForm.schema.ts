// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { z } from "zod";

import { AMOUNT_PATTERN, toCents } from "@/utils";

import type { BudgetRow, BudgetScope } from "./types";

export const MAX_AMOUNT_CENTS = 92_233_720_368n;

const INTEGER_PATTERN = /^\d+$/;

export const budgetFormSchema = z
    .object({
        name: z
            .string()
            .trim()
            .refine(name => [...name].length >= 3, "Use at least 3 characters")
            .refine(name => [...name].length <= 255, "Use 255 characters or fewer")
            .refine(name => !/\p{Cc}/u.test(name), "Remove tabs, line breaks and other control characters"),
        scope: z.enum(["user", "mesh"]),
        unlimited: z.boolean(),
        amount: z.string().trim(),
        warnPercentage: z.string().trim(),
    })
    .superRefine((values, ctx) => {
        if (values.unlimited) return;

        if (values.amount === "") {
            ctx.addIssue({ code: "custom", path: ["amount"], message: "Enter a limit, or select Unlimited" });
        } else if (!AMOUNT_PATTERN.test(values.amount)) {
            ctx.addIssue({ code: "custom", path: ["amount"], message: "Enter an amount such as 500 or 500.50" });
        } else if (toCents(values.amount) > MAX_AMOUNT_CENTS) {
            ctx.addIssue({ code: "custom", path: ["amount"], message: "Enter a smaller amount" });
        }

        if (values.warnPercentage !== "") {
            const pct = Number(values.warnPercentage);
            if (!INTEGER_PATTERN.test(values.warnPercentage) || pct < 1 || pct > 100) {
                ctx.addIssue({ code: "custom", path: ["warnPercentage"], message: "Enter a whole number from 1 to 100" });
            }
        }
    });

export type BudgetFormInput = z.infer<typeof budgetFormSchema>;

export interface BudgetFormValues {
    name: string;
    scope: BudgetScope;
    amount: string | null;
    warnPercentage: number | null;
}

export const emptyBudgetForm: BudgetFormInput = { name: "", scope: "user", unlimited: false, amount: "", warnPercentage: "" };

export function budgetToFormInput(budget: BudgetRow): BudgetFormInput {
    return {
        name: budget.name,
        scope: budget.scope,
        unlimited: budget.amount === null,
        amount: budget.amount ?? "",
        warnPercentage: budget.warnPercentage === null ? "" : String(budget.warnPercentage),
    };
}

export function formInputToValues(input: BudgetFormInput): BudgetFormValues {
    if (input.unlimited) return { name: input.name, scope: input.scope, amount: null, warnPercentage: null };
    return { name: input.name, scope: input.scope, amount: input.amount, warnPercentage: input.warnPercentage === "" ? null : Number(input.warnPercentage) };
}
