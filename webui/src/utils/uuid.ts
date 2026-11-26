// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { v4 as uuidv4 } from "uuid";

export const uuid = (): string => uuidv4({});
