// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { zodResolver } from "@hookform/resolvers/zod";
import { type DefaultValues, type FieldValues, useForm, type UseFormReturn } from "react-hook-form";
import type { z } from "zod";

export type ValidatePreset = "default" | "onSubmit" | "immediate";

const MODES = {
    default: { mode: "onTouched", reValidateMode: "onChange" },
    onSubmit: { mode: "onSubmit", reValidateMode: "onChange" },
    immediate: { mode: "onChange", reValidateMode: "onChange" },
} as const;

export interface UseAppFormOptions<S extends z.ZodType<FieldValues, FieldValues>> {
    defaultValues?: DefaultValues<z.input<S>>;
    values?: z.input<S>;
    validate?: ValidatePreset;
}

export function useAppForm<S extends z.ZodType<FieldValues, FieldValues>>(schema: S, options: UseAppFormOptions<S> = {}): UseFormReturn<z.input<S>, unknown, z.output<S>> {
    const { defaultValues, values, validate = "default" } = options;
    return useForm<z.input<S>, unknown, z.output<S>>({
        resolver: zodResolver<FieldValues, unknown, FieldValues, S>(schema),
        defaultValues,
        values,
        ...MODES[validate],
    });
}
