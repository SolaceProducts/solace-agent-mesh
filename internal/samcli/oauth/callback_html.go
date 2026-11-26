// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oauth

import (
	"errors"
	"io"
	"os"
	"sync/atomic"
)

var ErrRefreshDenied = errors.New("oauth: refresh token denied")

var stderrWriter atomic.Pointer[io.Writer]

func stderr() io.Writer {
	if w := stderrWriter.Load(); w != nil {
		return *w
	}
	return os.Stderr
}

func SetStderr(w io.Writer) func() {
	prev := stderrWriter.Load()
	stderrWriter.Store(&w)
	return func() {
		stderrWriter.Store(prev)
	}
}

const callbackHTMLSuccess = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>SAM authentication complete</title>
<style>
body { font-family: system-ui, sans-serif; background: #f5f7fa; padding: 4em; color: #1a202c; }
.card { background: white; max-width: 32em; margin: 0 auto; padding: 2em; border-radius: 8px; box-shadow: 0 1px 3px rgba(0,0,0,0.1); }
h1 { font-size: 1.4em; margin-top: 0; }
.note { color: #4a5568; }
</style>
</head>
<body>
<div class="card">
<h1>You're authenticated</h1>
<p class="note">The SAM CLI now holds your access tokens. You can close this tab.</p>
</div>
</body>
</html>`

const callbackHTMLError = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>SAM authentication failed</title></head>
<body style="font-family: system-ui, sans-serif; padding: 4em; color: #2d3748;">
<h1>Authentication failed</h1>
<p>The identity provider returned an error: <strong>%s</strong></p>
<p>%s</p>
<p>You can close this tab and try ` + "`sam auth login`" + ` again.</p>
</body>
</html>`

const callbackHTMLStateMismatch = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>SAM authentication failed</title></head>
<body style="font-family: system-ui, sans-serif; padding: 4em; color: #2d3748;">
<h1>Authentication failed</h1>
<p>The login response did not match this CLI session (state mismatch).</p>
<p>This can happen when an old browser tab is reused. Close all SAM login tabs and run <code>sam auth login</code> again.</p>
</body>
</html>`

const callbackHTMLNoToken = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>SAM authentication failed</title></head>
<body style="font-family: system-ui, sans-serif; padding: 4em; color: #2d3748;">
<h1>Authentication failed</h1>
<p>The platform did not return a SAM access token. Check the platform logs and retry.</p>
</body>
</html>`
