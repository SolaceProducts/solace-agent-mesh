// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useConfigContext } from "@/providers/contexts";

export function useIsPeopleSearchAvailable(): boolean {
    const { peopleSearchAvailable } = useConfigContext();
    return Boolean(peopleSearchAvailable);
}
