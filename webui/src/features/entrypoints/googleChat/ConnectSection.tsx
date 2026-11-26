// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { CredentialFileField, NO_BROWSER_FILL, TextField } from "@/forms";

import { serviceAccountKeyProblem } from "./serviceAccountKey";

interface ConnectSectionProps {
    hasStoredKey: boolean;
}

function ServiceAccountKeyField({ hasStoredKey }: Readonly<ConnectSectionProps>) {
    return (
        <CredentialFileField
            name="values.service_account_key"
            label="Service account key"
            description="The JSON key of the project's own service account, not the add-on service account."
            required
            accept=".json,application/json"
            validate={serviceAccountKeyProblem}
            hasStoredValue={hasStoredKey}
        />
    );
}

export function ConnectSection({ hasStoredKey }: Readonly<ConnectSectionProps>) {
    return (
        <>
            <TextField
                name="values.addon_service_account_email"
                label="Add-on service account"
                description="Google Cloud shows this address in the Connection settings of the Chat app. (e.g. service-<project number>@gcp-sa-gsuiteaddons.iam.gserviceaccount.com)."
                required
                autoComplete={NO_BROWSER_FILL}
            />
            <ServiceAccountKeyField hasStoredKey={hasStoredKey} />
        </>
    );
}
