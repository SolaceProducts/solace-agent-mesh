// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tlsutil"
)

func main() {
	target := os.Getenv("TLSCHECK_TARGET_URL")
	if target == "" {
		target = "https://target.invalid/"
	}

	client, err := tlsutil.BuildHTTPClient("", false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tlscheck: BuildHTTPClient: %v\n", err)
		os.Exit(2)
	}

	resp, err := client.Get(target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tlscheck: GET %s: %v\n", target, err)
		msg := err.Error()
		if strings.Contains(msg, "x509") || strings.Contains(msg, "tls: bad certificate") {
			os.Exit(3)
		}
		os.Exit(0)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	fmt.Fprintln(os.Stderr, "tlscheck: ok")
}
