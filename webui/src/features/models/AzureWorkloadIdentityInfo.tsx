// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useAzureWorkloadIdentity } from "@/api/models";

import { CopyButton, MessageBanner } from "@/common";

export function AzureWorkloadIdentityInfo() {
    const { data, isError, isLoading } = useAzureWorkloadIdentity();

    const rows = data
        ? [
              ["Cluster issuer URL", data.issuer],
              ["Namespace", data.namespace],
              ["Service account", data.serviceAccount],
              ["Subject identifier", `system:serviceaccount:${data.namespace}:${data.serviceAccount}`],
              ...data.audiences.map(audience => ["Audience", audience]),
          ]
        : [];
    const allValues = rows.map(([label, value]) => `${label}: ${value}`).join("\n");

    return (
        <MessageBanner
            variant="info"
            data-testid="azureWorkloadIdentityInfo"
            message={
                <div className="flex min-w-0 flex-1 flex-col gap-2">
                    <div className="flex items-start justify-between gap-2">
                        <span>Before testing the connection, the Azure administrator adds a federated credential with these values to this identity so that it trusts Agent Mesh.</span>
                        {rows.length > 0 && <CopyButton value={allValues} label="Copy All Values" className="shrink-0" />}
                    </div>
                    {isLoading && <span className="font-normal">Reading the values...</span>}
                    {isError && (
                        <span className="font-normal">
                            Unable to show the federated credential values. This deployment cannot read its Kubernetes service account token. Ask the administrator who deployed Agent Mesh to check its workload identity setup.
                        </span>
                    )}
                    {rows.length > 0 && (
                        <dl className="grid grid-cols-[auto_1fr] items-center gap-x-3 gap-y-1 font-normal">
                            {rows.map(([label, value]) => (
                                <div key={`${label}:${value}`} className="contents">
                                    <dt>{label}</dt>
                                    <dd className="min-w-0 truncate font-mono" title={value}>
                                        {value}
                                    </dd>
                                </div>
                            ))}
                        </dl>
                    )}
                </div>
            }
        />
    );
}
