// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { AddOwnModelButton } from "./AddOwnModelButton";
import { freeModelErrorKind } from "./freeModelErrors";
import { FreeModelSignInAgain } from "./FreeModelSignInAgain";
import { FreeModelTermsPrompt } from "./FreeModelTermsPrompt";
import { useFreeModel } from "./useFreeModel";

export function FreeModelErrorActions({ message }: Readonly<{ message: string }>) {
    const kind = freeModelErrorKind(message);
    const { chosen, inUse } = useFreeModel();
    if (!kind || !chosen) return null;
    if (kind === "terms") return <FreeModelTermsPrompt />;
    if (kind === "sign_in") {
        return (
            <div className="mt-2">
                <FreeModelSignInAgain variant="outline" signedInMessage="Signed in again. Send the message again." />
            </div>
        );
    }
    if (!inUse) return null;
    return (
        <div className="mt-2">
            <AddOwnModelButton variant="outline" />
        </div>
    );
}
