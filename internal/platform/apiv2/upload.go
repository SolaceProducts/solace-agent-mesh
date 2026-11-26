// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

type caller struct {
	BaseURL       string
	Authorization string
}

const multipartEnvelopeSlack = 64 << 10

func readCaller(w http.ResponseWriter, r *http.Request) (caller, bool) {
	baseURL, err := entplatform.EvalGenConfiguredGatewayURL(r)
	if err == nil {
		err = checkBaseURL(baseURL)
	}
	if err != nil {
		entplatform.WriteErrorWithCause(w, http.StatusBadRequest, "Unable to tell where the request was sent",
			map[string][]string{"Host": {"must name the host the request was sent to"}}, err)
		return caller{}, false
	}
	return caller{BaseURL: baseURL, Authorization: r.Header.Get("Authorization")}, true
}

func checkBaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
		u.Opaque != "" || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return errors.New("base URL is not a scheme and host")
	}
	return nil
}

func init() {
	apicontract.RegisterFieldReader(readCaller)
}
