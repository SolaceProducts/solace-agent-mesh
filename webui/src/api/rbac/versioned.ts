// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import {
    createClaimMappingV2,
    createRoleV2,
    deleteClaimMappingV2,
    deleteRoleV2,
    getDefaultRolesV2,
    getRoleV2,
    getUserRoleGrantsV2,
    getUserV2,
    listClaimMappingsV2,
    listComponentAccessUsersV2,
    listRolesV2,
    listSystemUsersV2,
    listUsersV2,
    setDefaultRolesV2,
    setUserRoleGrantsV2,
    updateClaimMappingV2,
    updateRoleV2,
} from "./v2";

export const componentAccessApi = { listUsers: listComponentAccessUsersV2 };

export const rbacApi = {
    listRoles: listRolesV2,
    getRole: getRoleV2,
    createRole: createRoleV2,
    updateRole: updateRoleV2,
    deleteRole: deleteRoleV2,
    getDefaultRoles: getDefaultRolesV2,
    setDefaultRoles: setDefaultRolesV2,
    listUsers: listUsersV2,
    getUser: getUserV2,
    getUserRoleGrants: getUserRoleGrantsV2,
    setUserRoleGrants: setUserRoleGrantsV2,
    listSystemUsers: listSystemUsersV2,
    listClaimMappings: listClaimMappingsV2,
    createClaimMapping: createClaimMappingV2,
    updateClaimMapping: updateClaimMappingV2,
    deleteClaimMapping: deleteClaimMappingV2,
};
