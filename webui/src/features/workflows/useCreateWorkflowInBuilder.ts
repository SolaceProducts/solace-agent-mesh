// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback } from "react";
import { useNavigate } from "react-router-dom";
import { v7 as uuidv7 } from "uuid";

import { SESSION_STORAGE_KEYS } from "@/utils/sessionStorageKeys";

export function useCreateWorkflowInBuilder() {
    const navigate = useNavigate();

    return useCallback(() => {
        const context = "I want to create a workflow";
        sessionStorage.setItem(SESSION_STORAGE_KEYS.REDIRECT_CONTEXT, context);
        sessionStorage.setItem(SESSION_STORAGE_KEYS.REDIRECT_SESSION_ID, uuidv7());
        sessionStorage.setItem(SESSION_STORAGE_KEYS.REDIRECT_AUTO_SUBMIT, "true");
        navigate("/builder");
    }, [navigate]);
}
