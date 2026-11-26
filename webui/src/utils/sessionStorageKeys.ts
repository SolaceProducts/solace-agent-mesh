// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const SESSION_STORAGE_KEYS = {
    REDIRECT_PENDING_INPUT: "sam_redirect_pending_input",
    REDIRECT_PENDING_INPUT_SESSION: "sam_redirect_pending_input_session",
    REDIRECT_CONTEXT: "sam_redirect_context",
    REDIRECT_SESSION_ID: "sam_redirect_session_id",
    REDIRECT_PENDING_ARTIFACTS: "sam_redirect_pending_artifacts",
    REDIRECT_AUTO_SUBMIT: "sam_redirect_auto_submit",
    REDIRECT_DISPLAY_HTML: "sam_redirect_display_html",
    REDIRECT_CHIP: "sam_redirect_chip",
    ACTIVE_BUILDER_SESSION: "sam_builder_active_session",
} as const;
