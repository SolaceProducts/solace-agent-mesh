// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export type LlmErrorCategory = "auth_failure" | "rate_limit" | "timeout" | "context_overflow" | "content_filtered" | "free_llm_terms_required" | "free_llm_paused" | "allowance_exhausted" | "budget_exhausted" | "unknown";

export type ProductAnalyticsErrorSource = "api" | "agent_task" | "client";

export type ProductAnalyticsSuggestionLabel = "write" | "learn" | "code" | "brainstorm" | "surprise_me" | "agent_custom";

export type ProductAnalyticsSetupPath = "free_model" | "own_model";

export type ProductAnalyticsSignInFailure = "canceled" | "denied" | "different_account" | "failed" | "grant_in_use" | "no_callback_port" | "no_refresh_token" | "not_saved" | "request_failed" | "timed_out" | "unavailable" | "other";

export type ProductAnalyticsFormName = "agent" | "connector" | "entrypoint" | "model";

export type { ProductAnalyticsFormField } from "./formFields";

export type ProductAnalyticsFormErrorType =
    "custom" | "invalid_element" | "invalid_format" | "invalid_key" | "invalid_type" | "invalid_union" | "invalid_value" | "max" | "min" | "not_multiple_of" | "required" | "too_big" | "too_small" | "unknown" | "unrecognized_keys" | "validate";

export type ProductAnalyticsEventName =
    | "app:launched"
    | "agent:creation_started"
    | "chat:first_response"
    | "builder:opened"
    | "builder:completed"
    | "form:validation_error"
    | "error:displayed"
    | "page_view"
    | "chat:suggestion_used"
    | "activation:sign_in_clicked"
    | "activation:sign_in_failed"
    | "activation:signed_in"
    | "activation:terms_shown"
    | "activation:terms_error"
    | "activation:replace_confirmed"
    | "activation:setup_chosen"
    | "activation:accepted"
    | "activation:accept_failed"
    | "free_model:credits_low"
    | "free_model:credits_out"
    | "free_model:sign_in_again_clicked";

export type ProductAnalyticsEventProperties = {
    "app:launched": never;
    "agent:creation_started": never;
    "chat:first_response": "traceId";
    "builder:opened": never;
    "builder:completed": never;
    "form:validation_error": "form" | "field" | "errorType";
    "error:displayed": "errorCategory" | "errorSource" | "page" | "traceId";
    page_view: never;
    "chat:suggestion_used": "suggestionLabel";
    "activation:sign_in_clicked": never;
    "activation:sign_in_failed": "signInFailure";
    "activation:signed_in": never;
    "activation:terms_shown": "alreadyAgreed";
    "activation:terms_error": never;
    "activation:replace_confirmed": never;
    "activation:setup_chosen": "setupPath";
    "activation:accepted": "setupPath";
    "activation:accept_failed": "setupPath";
    "free_model:credits_low": never;
    "free_model:credits_out": never;
    "free_model:sign_in_again_clicked": never;
};
