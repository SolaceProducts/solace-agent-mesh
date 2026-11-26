// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type FieldValues, get, type Path, type UseFormReturn } from "react-hook-form";

import { ApiV2Error, type ValidationDetails } from "@/api/common/v2";

import { getErrorMessage } from "@/utils/api";

export interface ServerErrorBanner {
    message: string;
    errorId?: string;
}

const FALLBACK_MESSAGE = "Something went wrong.";

const formatDetails = (details: ValidationDetails) =>
    Object.entries(details)
        .map(([field, errs]) => `${field}: ${(errs ?? []).join(", ")}`)
        .join("; ");

function baseMessage(error: ApiV2Error): string {
    const suffix = error.validationDetails ? formatDetails(error.validationDetails) : "";
    const appended = `: ${suffix}`;
    return suffix && error.message.endsWith(appended) ? error.message.slice(0, -appended.length) : error.message;
}

const isMountedField = <T extends FieldValues>(form: UseFormReturn<T, unknown, T>, key: string) => (get(form.control._fields, key) as { _f?: { mount?: boolean } } | undefined)?._f?.mount === true;

export function applyServerErrors<T extends FieldValues>(
    // eslint-disable-next-line @typescript-eslint/no-explicit-any -- the transformed output type is irrelevant here
    form: UseFormReturn<T, unknown, any>,
    error: unknown
): ServerErrorBanner | null {
    if (!(error instanceof ApiV2Error)) return { message: getErrorMessage(error, FALLBACK_MESSAGE) };

    const details = error.validationDetails ?? {};
    const unmapped = Object.fromEntries(Object.entries(details).filter(([key]) => !isMountedField(form, key)));
    Object.entries(details)
        .filter(([key]) => !(key in unmapped))
        .forEach(([key, messages], index) => form.setError(key as Path<T>, { type: "server", message: (messages ?? []).join(" ") }, { shouldFocus: index === 0 }));

    const hasDetails = Object.keys(details).length > 0;
    const hasUnmapped = Object.keys(unmapped).length > 0;
    if (hasDetails && !hasUnmapped) return null;

    const base = baseMessage(error) || FALLBACK_MESSAGE;
    const message = hasUnmapped ? `${base}: ${formatDetails(unmapped)}` : base;
    return { message, errorId: error.errorId };
}
