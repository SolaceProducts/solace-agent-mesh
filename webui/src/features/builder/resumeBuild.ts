// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { FormEvent } from "react";

import type { ChatContextValue } from "@/providers/contexts";

const RESUME_BUILD_MESSAGE = "Please continue the build from where it stopped.";
const RESUME_BUILD_CHIP = `<span style="display:inline-flex;align-items:center;gap:4px;padding:4px 12px;border-radius:6px;font-size:13px;font-weight:500;opacity:0.7">↻ Resuming build</span>`;

export function submitResumeBuild(handleSubmit: ChatContextValue["handleSubmit"]): void {
    const fakeEvent = { preventDefault: () => {} } as FormEvent;
    void handleSubmit(fakeEvent, null, RESUME_BUILD_MESSAGE, null, RESUME_BUILD_CHIP, null, null, null, { resume_build: true });
}
