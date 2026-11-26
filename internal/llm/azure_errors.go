// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/maximhq/bifrost/core/schemas"
)

const ErrorTypeAzureSignIn = "azure_signin_failed"

const azureAdminHint = "An administrator needs to check this model's Azure setup."

type azureSetupProblem struct {
	cause string
	fix   string
}

var noMatchingFederatedCredential = azureSetupProblem{
	cause: "No federated credential on the Azure identity matches this deployment",
	fix: "Make the identity's federated credential match this deployment's issuer, namespace, service account, and audience exactly. " +
		"A new or changed credential can take a few minutes to apply.",
}

var azureSetupProblems = map[string]azureSetupProblem{
	"AADSTS700016": {
		cause: "Azure has no application with this model's client ID in its tenant",
		fix:   "Check the client ID and tenant ID on the model.",
	},
	"AADSTS90002": {
		cause: "Azure has no tenant with this model's tenant ID",
		fix:   "Check the tenant ID on the model.",
	},
	"AADSTS70025": {
		cause: "The Azure identity has no federated credentials",
		fix:   "Add a federated credential for this deployment's issuer, namespace, service account, and audience to the identity.",
	},
	"AADSTS70021":  noMatchingFederatedCredential,
	"AADSTS700211": noMatchingFederatedCredential,
	"AADSTS700212": noMatchingFederatedCredential,
	"AADSTS700213": noMatchingFederatedCredential,
	"AADSTS50166": {
		cause: "Microsoft Entra is unable to reach this cluster's token issuer",
		fix:   "Make the cluster's issuer URL public so Microsoft Entra can fetch its signing keys.",
	},
	"PermissionDenied": {
		cause: "The Azure identity is not allowed to call this Azure resource",
		fix: "Grant the identity the Cognitive Services OpenAI User role on the resource for Azure OpenAI models, " +
			"or Cognitive Services User for other Microsoft Foundry models. " +
			"A new role assignment can take a few minutes to apply.",
	},
	"DeploymentNotFound": {
		cause: "Azure OpenAI has no deployment with this model name at this endpoint",
		fix:   "Check the model's endpoint and deployment name, and that the deployment still exists.",
	},
}

var azureSignInFailed = azureSetupProblem{
	cause: "Unable to sign in to Azure with the model's workload identity",
	fix:   "Check the client ID and tenant ID on the model, and the federated credential on the Azure identity.",
}

const (
	SignInTokenFileUnreadable = "token_file_unreadable"
	SignInEntraUnreachable    = "entra_unreachable"
)

var azureLocalSignInProblems = map[string]azureSetupProblem{
	SignInTokenFileUnreadable: {
		cause: "Unable to read this deployment's service account token",
		fix:   "Check that " + AzureFederatedTokenFileEnv + " points at the service account token mounted in the pod.",
	},
	SignInEntraUnreachable: {
		cause: "Unable to reach Microsoft Entra to sign in",
		fix:   "Check that this deployment can reach login.microsoftonline.com. If it can, Microsoft Entra may be briefly unavailable. Please try again later.",
	},
}

func (e *Error) AzureSetupProblem() (cause, fix string, ok bool) {
	if e == nil {
		return "", "", false
	}
	signIn := e.Type == ErrorTypeAzureSignIn
	if !signIn && e.Provider != string(schemas.Azure) {
		return "", "", false
	}
	if problem, known := azureSetupProblems[e.Code]; known {
		return fmt.Sprintf("%s (%s)", problem.cause, e.Code), problem.fix, true
	}
	if !signIn {
		return "", "", false
	}
	if problem, known := azureLocalSignInProblems[e.Code]; known {
		return problem.cause, problem.fix, true
	}
	return azureSignInFailed.cause, azureSignInFailed.fix, true
}

func azureSignInError(err error) *schemas.BifrostError {
	field := &schemas.ErrorField{Type: new(ErrorTypeAzureSignIn), Message: err.Error()}
	code, description, replied := entraError(err)
	switch {
	case replied:
		field.Message = description
	case !federatedTokenReadable():
		code = SignInTokenFileUnreadable
	default:
		code = SignInEntraUnreachable
	}
	if code != "" {
		field.Code = &code
	}
	return &schemas.BifrostError{IsBifrostError: true, Error: field}
}

func federatedTokenReadable() bool {
	f, err := os.Open(os.Getenv(AzureFederatedTokenFileEnv))
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

func entraError(err error) (code, description string, ok bool) {
	failed, isAuth := errors.AsType[*azidentity.AuthenticationFailedError](err)
	if !isAuth || failed.RawResponse == nil {
		return "", "", false
	}
	payload, readErr := runtime.Payload(failed.RawResponse)
	var body struct {
		Description string `json:"error_description"`
		Codes       []int  `json:"error_codes"`
	}
	if readErr != nil || json.Unmarshal(payload, &body) != nil || body.Description == "" {
		return "", "", false
	}
	description, _, _ = strings.Cut(body.Description, "Trace ID:")
	if len(body.Codes) > 0 {
		code = fmt.Sprintf("AADSTS%d", body.Codes[0])
	}
	return code, strings.TrimSpace(description), true
}
