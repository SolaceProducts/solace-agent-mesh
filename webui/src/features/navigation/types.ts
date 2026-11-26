// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { NavItemConfig } from "@/types/nav";

export type NavItem = NavItemConfig & { hasSubmenu?: boolean };
