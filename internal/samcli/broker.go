// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	samlog "github.com/SolaceProducts/solace-agent-mesh/internal/logging"
	"github.com/spf13/cobra"
)

type queueClient interface {
	listQueues(ctx context.Context, prefix string) ([]string, error)
	deleteQueue(ctx context.Context, name string) error
}

type brokerCleanupOpts struct {
	namespace string
	force     bool
	dryRun    bool

	clientFactory func() (queueClient, error)
	logger        *slog.Logger
	out           io.Writer
}

func newBrokerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "broker",
		Short:  "Manage the event broker behind an Agent Mesh deployment",
		Long:   `Operate on the event broker an Agent Mesh deployment provisions queues on.`,
		Hidden: true,
	}
	cmd.AddCommand(newBrokerCleanupCmd())
	return cmd
}

func newBrokerCleanupCmd() *cobra.Command {
	o := &brokerCleanupOpts{}
	o.clientFactory = func() (queueClient, error) { return newSEMPClientFromEnv(o.logger) }
	cmd := &cobra.Command{
		Use:    "cleanup",
		Short:  "Deprovision every durable queue a namespace owns on the event broker",
		Hidden: true,
		Long: `Deprovision every durable queue whose name starts with "<namespace>/q/" on the
message VPN, over the broker SEMP v2 management API.

Agent Mesh leaves its durable queues in place on shutdown so a rolling upgrade
can hand consumption to the successor pod, which leaves permanent removal without
an owner: the queues accumulate against the VPN max-endpoints quota. This command
is that owner. It sweeps by prefix, so per-worker, per-gateway and per-alias names
are covered without enumerating them. Intended for decommissioning a deployment,
for example a Kubernetes post-delete hook running from the Agent Mesh image.

The namespace defaults to $NAMESPACE, the value the deployment runs with;
override it with --namespace.

SEMP connection comes from the environment: SOLACE_SEMP_URL (scheme://host:port,
the management endpoint, not the messaging URL), SOLACE_SEMP_USERNAME,
SOLACE_SEMP_PASSWORD and SOLACE_BROKER_VPN. Optionally SOLACE_SEMP_CA_FILE to
trust a private CA, or SOLACE_SEMP_INSECURE=true for a lab. Credentials are taken
from the environment, not flags, so they do not land in the process table or a
Job's rendered command; a Job injects them from a Secret.

Deletion is idempotent, so a teardown hook is safe to re-run. Run --dry-run first
to preview. Deprovisioning discards any messages still spooled on a queue; run
this only against a namespace that is being decommissioned.`,
		Example: `  SOLACE_SEMP_URL=https://broker.example.com:943 \
  SOLACE_SEMP_USERNAME=cleanup SOLACE_SEMP_PASSWORD=... SOLACE_BROKER_VPN=my-vpn \
  sam broker cleanup --namespace my-mesh --dry-run`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if o.logger == nil {
				o.logger = slog.New(samlog.NewHandler(cmd.ErrOrStderr(), nil, "json"))
			}
			if o.out == nil {
				o.out = cmd.OutOrStdout()
			}
			return runBrokerCleanup(commandContext(cmd), o)
		},
	}
	cmd.Flags().StringVar(&o.namespace, "namespace", "",
		"Namespace whose queues to deprovision. Defaults to $NAMESPACE.")
	cmd.Flags().BoolVar(&o.force, "force", false,
		"Skip confirmation; required unless --dry-run.")
	cmd.Flags().BoolVar(&o.dryRun, "dry-run", false,
		"Print the queues that would be deprovisioned and exit without changing the broker.")
	return cmd
}

func newSEMPClientFromEnv(logger *slog.Logger) (*sempClient, error) {
	insecure := strings.EqualFold(os.Getenv("SOLACE_SEMP_INSECURE"), "true")
	if insecure && logger != nil {
		logger.Warn("SEMP TLS verification disabled by SOLACE_SEMP_INSECURE; admin credentials are exposed to a network MITM, do not use in production")
	}
	return newSEMPClient(sempConfig{
		BaseURL:            os.Getenv("SOLACE_SEMP_URL"),
		VPN:                os.Getenv("SOLACE_BROKER_VPN"),
		Username:           os.Getenv("SOLACE_SEMP_USERNAME"),
		Password:           os.Getenv("SOLACE_SEMP_PASSWORD"),
		CACertPath:         os.Getenv("SOLACE_SEMP_CA_FILE"),
		InsecureSkipVerify: insecure,
	})
}

func resolveNamespace(flagValue string) (string, error) {
	ns := strings.Trim(strings.TrimSpace(flagValue), "/")
	if ns == "" {
		ns = strings.Trim(strings.TrimSpace(os.Getenv("NAMESPACE")), "/")
	}
	if ns == "" {
		return "", errors.New("no namespace to sweep: pass --namespace or set NAMESPACE")
	}
	for _, seg := range strings.Split(ns, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", fmt.Errorf("namespace %q is not valid", ns)
		}
		if strings.ContainsAny(seg, "*?%") {
			return "", fmt.Errorf("namespace %q contains a SEMP filter metacharacter", ns)
		}
	}
	return ns, nil
}

func runBrokerCleanup(ctx context.Context, o *brokerCleanupOpts) error {
	namespace, err := resolveNamespace(o.namespace)
	if err != nil {
		return err
	}
	prefix := namespace + "/q/"
	if !o.force && !o.dryRun {
		return fmt.Errorf("this permanently deprovisions every queue under %q, discarding any spooled messages; re-run with --force, or --dry-run to preview", prefix)
	}

	client, err := o.clientFactory()
	if err != nil {
		return fmt.Errorf("SEMP client: %w", err)
	}

	listed, err := client.listQueues(ctx, prefix)
	if err != nil {
		return fmt.Errorf("list queues under %q: %w", prefix, err)
	}
	names := make([]string, 0, len(listed))
	for _, name := range listed {
		if strings.HasPrefix(name, prefix) {
			names = append(names, name)
		}
	}

	if o.dryRun {
		for _, name := range names {
			if _, err := fmt.Fprintln(o.out, name); err != nil {
				return fmt.Errorf("write queue listing: %w", err)
			}
		}
		o.logger.Info("broker cleanup dry run", "namespace", namespace, "prefix", prefix, "queues", len(names))
		return nil
	}

	var deleted int
	var deleteErrs []error
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("cleanup under %q cancelled after %d of %d queues: %w", prefix, deleted, len(names), err)
		}
		if err := client.deleteQueue(ctx, name); err != nil {
			deleteErrs = append(deleteErrs, fmt.Errorf("queue %q: %w", name, err))
			continue
		}
		deleted++
	}
	if len(deleteErrs) > 0 {
		o.logger.Error("broker cleanup finished with errors",
			"namespace", namespace, "prefix", prefix, "deleted", deleted, "queues", len(names), "failed", len(deleteErrs))
		return fmt.Errorf("deprovisioned %d of %d queues under %q: %w", deleted, len(names), prefix, errors.Join(deleteErrs...))
	}
	o.logger.Info("broker cleanup finished", "namespace", namespace, "prefix", prefix, "deleted", deleted)
	return nil
}
