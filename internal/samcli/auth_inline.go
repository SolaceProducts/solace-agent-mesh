// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/oauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/tokencache"
)

var isInteractive = func() bool {
	return !envFlagSet("CI") && isCharDeviceFD(os.Stdin) && isCharDeviceFD(os.Stderr)
}

func isCharDeviceFD(f *os.File) bool {
	if f == nil {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

var runInteractiveLogin = defaultRunInteractiveLogin

func defaultRunInteractiveLogin(ctx context.Context, tg target.Target, w io.Writer, in io.Reader) error {
	if w == nil {
		w = os.Stderr
	}
	if in == nil {
		in = os.Stdin
	}

	_, _ = fmt.Fprintf(w, "Not logged in to %s (%s). Open browser to authenticate? [Y/n] ",
		tg.CacheName(), tg.URL)

	reader := bufio.NewReader(in)
	line, readErr := reader.ReadString('\n')
	if errors.Is(readErr, io.EOF) && strings.TrimSpace(line) == "" {
		return fmt.Errorf("%w (target %q: stdin closed without a response)", target.ErrNotLoggedIn, tg.CacheName())
	}
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return fmt.Errorf("read response: %w", readErr)
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "y", "yes":
	case "n", "no":
		return fmt.Errorf("%w (target %q)", target.ErrNotLoggedIn, tg.CacheName())
	default:
		return fmt.Errorf("%w (target %q)", target.ErrNotLoggedIn, tg.CacheName())
	}

	store, err := tokencache.NewDefault()
	if err != nil {
		return err
	}

	client := oauth.NewClient()

	discoverCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cfg, err := client.Discover(discoverCtx, tg.URL)
	if err != nil {
		_, _ = fmt.Fprintf(w, "warning: cli-config discovery failed: %v (proceeding without it)\n", err)
		cfg = oauth.Config{PlatformURL: tg.URL}
	}

	tokens, err := client.Login(ctx, cfg)
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}

	cached := tokencache.FromTokens(cfg, tokens)
	if err := store.Save(tg.CacheName(), cached); err != nil {
		return fmt.Errorf("persist tokens: %w", err)
	}

	identity := jwtClaimString(tokens.SAMAccessToken, "email")
	if identity == "" {
		identity = jwtClaimString(tokens.SAMAccessToken, "name")
	}
	if identity == "" {
		identity = "(unknown user)"
	}
	_, _ = fmt.Fprintf(w, "Logged in to %s as %s\n", tg.CacheName(), identity)
	return nil
}

func preflightAuth(ctx context.Context, tg target.Target, resolver target.OAuthTokenResolver, errOut io.Writer, stdin io.Reader, noInteractive bool) error {
	if _, err := tg.Resolve(ctx, resolver); err == nil {
		return nil
	} else if !errors.Is(err, target.ErrNotLoggedIn) {
		return err
	}

	if noInteractive || !isInteractive() {
		return fmt.Errorf("%w (target %q: run `sam auth login %s`)",
			target.ErrNotLoggedIn, tg.CacheName(), tg.CacheName())
	}

	if err := runInteractiveLogin(ctx, tg, errOut, stdin); err != nil {
		return err
	}

	if _, err := tg.Resolve(ctx, resolver); err != nil {
		return err
	}
	return nil
}
