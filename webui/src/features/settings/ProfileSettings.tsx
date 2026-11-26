// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ReactNode } from "react";

import { useAuthContext } from "@/providers/contexts";

import { Badge } from "@/ui";

const stringField = (value: unknown): string | undefined => (typeof value === "string" && value ? value : undefined);

const stringArrayField = (value: unknown): string[] => (Array.isArray(value) ? value.filter((entry): entry is string => typeof entry === "string") : []);

interface ProfileFieldProps {
    label: string;
    children: ReactNode;
}

const ProfileField = ({ label, children }: ProfileFieldProps) => (
    <div className="flex flex-col gap-3">
        <span className="font-semibold">{label}</span>
        {children}
    </div>
);

export const ProfileSettings = () => {
    const { userInfo } = useAuthContext();

    const username = stringField(userInfo?.username) ?? "Unknown user";
    const email = stringField(userInfo?.email);
    const roles = stringArrayField(userInfo?.roles);

    const showEmail = email !== undefined && email !== username;

    return (
        <div className="flex flex-col gap-6">
            <ProfileField label="User">
                <span className="break-words">{username}</span>
            </ProfileField>

            {showEmail && (
                <ProfileField label="Email">
                    <span className="break-words">{email}</span>
                </ProfileField>
            )}

            <ProfileField label="Roles">
                {roles.length > 0 ? (
                    <div className="flex flex-wrap gap-1.5">
                        {roles.map(role => (
                            <Badge key={role}>{role}</Badge>
                        ))}
                    </div>
                ) : (
                    <span className="text-sm text-(--secondary-text-wMain)">No roles assigned</span>
                )}
            </ProfileField>
        </div>
    );
};
