// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const withAuthorization = {
    configContext: {
        configUseAuthorization: true,
    },
};

export const asOwner = (username: string = "owner-user") => ({
    authContext: {
        userInfo: { username },
    },
});

export const asViewer = (username: string = "different-user") => ({
    authContext: {
        userInfo: { username },
    },
});

export const ownerWithAuthorization = (ownerUsername: string = "owner-user") => ({
    ...withAuthorization,
    ...asOwner(ownerUsername),
});

export const withProjectSharingEnabled = {
    configContext: {
        configUseAuthorization: true,
        identityServiceType: "okta",
    },
    featureContext: {
        project_sharing: true,
    },
};

export const withIdentityService = (type: "okta" | "azure" | string = "okta") => ({
    configContext: {
        identityServiceType: type,
    },
});

export const withoutIdentityService = {
    configContext: {
        identityServiceType: null,
    },
};

export const ownerWithProjectSharingEnabled = (ownerUsername: string = "owner-user") => ({
    ...withProjectSharingEnabled,
    ...asOwner(ownerUsername),
});

export const viewerWithProjectSharingEnabled = (viewerUsername: string = "different-user") => ({
    ...withProjectSharingEnabled,
    ...asViewer(viewerUsername),
});
