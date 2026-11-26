// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type SetupWorker, setupWorker } from "msw/browser";

let worker: SetupWorker | undefined;

export function getWorker(): SetupWorker {
    worker ??= setupWorker();
    return worker;
}

export async function startWorker(): Promise<SetupWorker> {
    const instance = getWorker();
    await instance.start({
        onUnhandledFrame: "bypass",
        quiet: true,
    });
    return instance;
}
