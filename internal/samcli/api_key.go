// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apikey"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/reconcile"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const defaultRBACMode = "default_rbac"

var apiKeyNow = time.Now

func newAPIKeyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "api-key",
		Short: "Issue, list, rename and revoke API keys for service accounts",
		Long: `Manage API keys: long-lived credentials that let a pipeline run ` + "`sam config`" + `
and ` + "`sam eval run`" + ` without a person's sign-in. A key reaches only the
platform's configuration and evaluation routes; every other route refuses it.

Each key belongs to a service account and acts as it, limited to the
scopes chosen when the key was created. Those scopes can only narrow what the
service account's roles grant, and are re-checked on every request.

Present a key as an HTTP header: "Authorization: Bearer <key>".

Managing keys requires a super administrator signed in with ` + "`sam auth login`" + `;
an API key can never create, rename or delete a key. The key itself is printed
once, by ` + "`sam api-key create`" + `, and cannot be retrieved again.`,
		Example: `  sam api-key create --user ci-deployer --label deploy-pipeline \
    --scopes 'agent_builder:*:read,agent_builder:*:create,agent_builder:*:update,agent_builder:*:deploy,skill:*:read,connector:*:read,toolset:*:read' \
    --expires-in 90d
  sam api-key list --user ci-deployer
  sam api-key get <id>
  sam api-key update <id> --label nightly-deploy
  sam api-key delete <id>`,
	}
	cmd.AddCommand(newAPIKeyCreateCmd())
	cmd.AddCommand(newAPIKeyListCmd())
	cmd.AddCommand(newAPIKeyGetCmd())
	cmd.AddCommand(newAPIKeyUpdateCmd())
	cmd.AddCommand(newAPIKeyDeleteCmd())
	return cmd
}

type systemUserIndex struct {
	byID     map[string]wire.SystemUserResponse
	listPath string
}

func loadSystemUserIndex(ctx context.Context, c *reconcile.Client) (systemUserIndex, error) {
	users, err := c.ListSystemUsers(ctx)
	if err != nil {
		return systemUserIndex{}, err
	}
	listPath, err := c.ServiceAccountsAPIPath(ctx)
	if err != nil {
		return systemUserIndex{}, err
	}
	idx := systemUserIndex{byID: make(map[string]wire.SystemUserResponse, len(users)), listPath: listPath}
	for _, u := range users {
		idx.byID[u.ID] = u
	}
	return idx, nil
}

func (idx systemUserIndex) resolve(value string) (wire.SystemUserResponse, error) {
	if strings.TrimSpace(value) == "" {
		return wire.SystemUserResponse{}, errors.New("--user is empty\n  name a service account by its name, system: subject or id")
	}
	if id, err := uuid.Parse(value); err == nil {
		if u, ok := idx.byID[id.String()]; ok {
			return u, nil
		}
	}
	sub := rbac.SystemUserPrincipal(value)
	for _, u := range idx.byID {
		if u.Sub == sub {
			return u, nil
		}
	}
	return wire.SystemUserResponse{}, fmt.Errorf("no service account %q\n  list them with `sam api GET %s`", value, idx.listPath)
}

func (idx systemUserIndex) owner(userID string) string {
	if u, ok := idx.byID[userID]; ok {
		return u.Sub
	}
	return userID
}

func ownerNames(ctx context.Context, c *reconcile.Client) systemUserIndex {
	idx, err := loadSystemUserIndex(ctx, c)
	if err != nil {
		return systemUserIndex{}
	}
	return idx
}

type platformErrorBody struct {
	Message           string              `json:"message"`
	ErrorID           string              `json:"errorId"`
	ValidationDetails map[string][]string `json:"validationDetails"`
}

type apiKeyOperation int

const (
	apiKeyRead apiKeyOperation = iota
	apiKeyManage
)

func explainAPIKeyError(err error, op apiKeyOperation) error {
	he, ok := errors.AsType[*reconcile.HTTPError](err)
	if !ok {
		return err
	}
	var body platformErrorBody
	enveloped := json.Unmarshal([]byte(he.Body), &body) == nil && body.Message != ""
	explained := explainRefusal(he.StatusCode, body, enveloped, op)
	if explained == nil {
		return err
	}
	if body.ErrorID != "" && he.StatusCode < http.StatusInternalServerError {
		return fmt.Errorf("%w\n  errorId %s identifies this refusal in the platform's logs", explained, body.ErrorID)
	}
	return explained
}

func explainRefusal(status int, body platformErrorBody, enveloped bool, op apiKeyOperation) error {
	envToken := envFirstNonEmpty("SAM_AUTH_TOKEN", "SAM_PLATFORM_TOKEN")

	switch {
	case status == http.StatusUnauthorized && envToken != "":
		return errors.New("the platform did not accept the token in SAM_AUTH_TOKEN or SAM_PLATFORM_TOKEN\n  replace it with a valid token, or unset both and sign in with `sam auth login`")
	case status == http.StatusUnauthorized:
		return errors.New("the platform did not accept your sign-in\n  run `sam auth login` and try again")
	case status == http.StatusForbidden && strings.Contains(body.Message, defaultRBACMode):
		return fmt.Errorf("%s\n  an administrator must run the platform with the %s authorization mode before it can issue keys", body.Message, defaultRBACMode)
	case status == http.StatusForbidden && op == apiKeyManage && apikey.HasMarker(envToken):
		return fmt.Errorf("%s\n  an API key cannot manage keys: unset SAM_AUTH_TOKEN and SAM_PLATFORM_TOKEN, then sign in with `sam auth login`", refusal(body))
	case status == http.StatusForbidden && apikey.HasMarker(envToken):
		return fmt.Errorf("%s\n  reading API keys needs the *:*:* grant: use a key that holds it, or sign in as a super administrator with `sam auth login`", refusal(body))
	case status == http.StatusForbidden && envToken != "":
		return fmt.Errorf("%s\n  SAM_AUTH_TOKEN or SAM_PLATFORM_TOKEN overrides your sign-in: put a super administrator's token there, or unset both and sign in as a super administrator with `sam auth login`", refusal(body))
	case status == http.StatusForbidden:
		return fmt.Errorf("%s\n  sign in as a super administrator with `sam auth login`", refusal(body))
	case status == http.StatusNotFound && !enveloped:
		return errors.New("API keys are not enabled on this deployment, or --url does not point at its platform\n  an administrator who turned off the machine_tokens feature must turn it back on")
	case status == http.StatusNotFound:
		return fmt.Errorf("%s\n  list the keys that exist with `sam api-key list`", body.Message)
	case status == http.StatusConflict:
		return fmt.Errorf("%s\n  delete a key the service account no longer needs with `sam api-key delete <id>`", body.Message)
	case enveloped && len(body.ValidationDetails) > 0:
		return errors.New(body.Message + formatValidationDetails(body.ValidationDetails))
	case enveloped && status >= http.StatusInternalServerError && body.ErrorID != "":
		return fmt.Errorf("%s\n  report errorId %s to the operator; it identifies the failure in the platform's logs", body.Message, body.ErrorID)
	case enveloped:
		return errors.New(body.Message)
	}
	return nil
}

func canonicalKeyID(id string) (string, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return "", fmt.Errorf("%q is not an API key id\n  list the keys and their ids with `sam api-key list`", id)
	}
	return parsed.String(), nil
}

func refusal(body platformErrorBody) string {
	return cmp.Or(body.Message, "the platform refused the request")
}

func formatValidationDetails(details map[string][]string) string {
	var b strings.Builder
	for _, field := range slices.Sorted(maps.Keys(details)) {
		for _, problem := range details[field] {
			fmt.Fprintf(&b, "\n  %s: %s", field, problem)
		}
	}
	return b.String()
}

func writeAPIKeyDetail(w io.Writer, key wire.APIKeyResponse, owners systemUserIndex) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	rows := [][2]string{
		{"ID", key.ID},
		{"Label", key.Label},
		{"Owner", owners.owner(key.UserID)},
		{"Prefix", displayKeyPrefix(key.Prefix)},
		{"Scopes", strings.Join(key.Scopes, ", ")},
		{"Status", key.Status},
		{"Expires", formatKeyTime(key.ExpiresTime, "never")},
		{"Last used", formatKeyTime(key.LastUsedTime, "never")},
		{"Created", formatKeyTime(key.CreatedTime, "") + " by " + key.CreatedBy},
		{"Updated", formatKeyTime(key.UpdatedTime, "") + " by " + key.UpdatedBy},
	}
	for _, row := range rows {
		_, _ = fmt.Fprintf(tw, "%s:\t%s\n", row[0], row[1])
	}
	return tw.Flush()
}

func displayKeyPrefix(prefix string) string { return apikey.Marker + prefix + "…" }

func formatKeyTime(t apiv2.Instant, unset string) string {
	if t == 0 {
		return unset
	}
	return entplatform.RESTTimestamp(t).String()
}
