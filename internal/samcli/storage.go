// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	samlog "github.com/SolaceProducts/solace-agent-mesh/internal/logging"
	"github.com/spf13/cobra"

	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
)

var componentBucketEnvVars = []string{
	"",
	"STR_BUCKET_NAME",
	"ARTIFACT_BUCKET_NAME",
	"CONNECTOR_SPEC_BUCKET_NAME",
	"EVAL_DATA_BUCKET_NAME",
}

type storageCleanupOpts struct {
	namespace string
	force     bool

	storeFactory func(storageType, bucket string) (*objectstore.Store, error)
	logger       *slog.Logger
}

func newStorageCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "storage",
		Short:  "Manage the object storage behind an Agent Mesh deployment",
		Long:   `Operate on the object storage an Agent Mesh deployment writes to.`,
		Hidden: true,
	}
	cmd.AddCommand(newStorageCleanupCmd())
	return cmd
}

func newStorageCleanupCmd() *cobra.Command {
	o := &storageCleanupOpts{storeFactory: newEnvStore}
	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Delete every object a namespace owns in object storage",
		Long: `Delete every object whose key starts with "<namespace>/" from the object
storage configured in the environment.

Named "cleanup" rather than "delete": this is a namespace-scoped teardown
sweep (every object a decommissioned deployment left behind, across several
buckets), not the deletion of a single named object.

Sweeps the default bucket (OBJECT_STORAGE_BUCKET_NAME) plus every
per-component override bucket (STR_BUCKET_NAME, ARTIFACT_BUCKET_NAME,
CONNECTOR_SPEC_BUCKET_NAME, EVAL_DATA_BUCKET_NAME), deduplicated — removing
the namespace's artifacts, connector specs, skills, and toolsets.
Intended for operators decommissioning a deployment, for example a
Kubernetes uninstall hook running this command from the Agent Mesh image.
Credentials are ambient: each backend's default credential chain (workload
identity in Kubernetes). Cloud object stores only (s3, gcs, azure).

Deletion is idempotent — a namespace with no remaining objects succeeds
with a count of 0. On buckets with versioning or soft delete, deleted
objects remain recoverable per the bucket's retention policy. Results are
emitted as JSON log lines, one per bucket, on stderr.`,
		Example: `  OBJECT_STORAGE_TYPE=s3 OBJECT_STORAGE_BUCKET_NAME=my-sam-bucket \
  sam storage cleanup --namespace my-mesh --force`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if o.logger == nil {
				o.logger = slog.New(samlog.NewHandler(cmd.ErrOrStderr(), nil, "json"))
			}
			return runStorageCleanup(commandContext(cmd), o)
		},
	}
	cmd.Flags().StringVar(&o.namespace, "namespace", "",
		"Namespace whose objects to delete (required).")
	cmd.Flags().BoolVar(&o.force, "force", false,
		"Skip confirmation; force the permanent deletion (required).")
	_ = cmd.MarkFlagRequired("namespace")
	return cmd
}

func newEnvStore(storageType, bucket string) (*objectstore.Store, error) {
	client, err := objectstore.NewFromConfig(map[string]any{
		"type":        storageType,
		"bucket_name": bucket,
	})
	if err != nil {
		return nil, err
	}
	return objectstore.NewStore(client), nil
}

func resolveCleanupBuckets() []string {
	seen := make(map[string]bool, len(componentBucketEnvVars))
	var buckets []string
	for _, envVar := range componentBucketEnvVars {
		bucket := objectstore.ResolveBucket(envVar)
		if bucket == "" || seen[bucket] {
			continue
		}
		seen[bucket] = true
		buckets = append(buckets, bucket)
	}
	return buckets
}

func runStorageCleanup(ctx context.Context, o *storageCleanupOpts) error {
	namespace := strings.TrimSuffix(strings.TrimSpace(o.namespace), "/")
	if namespace == "" {
		return errors.New("--namespace must not be empty")
	}
	if strings.Contains(namespace, "/") {
		return fmt.Errorf("--namespace %q must not contain '/'", namespace)
	}
	if namespace == "." || namespace == ".." {
		return fmt.Errorf("--namespace %q is not a valid namespace", namespace)
	}
	if !o.force {
		return fmt.Errorf("sam storage cleanup permanently deletes every object under %q; re-run with --force to confirm", namespace+"/")
	}
	storageType := objectstore.StorageType()
	if storageType == "" {
		return errors.New("OBJECT_STORAGE_TYPE is not set; sam storage cleanup requires environment-configured object storage")
	}
	if storageType == "filesystem" || storageType == "memory" {
		return fmt.Errorf("sam storage cleanup supports cloud object stores (s3, gcs, azure); OBJECT_STORAGE_TYPE=%q is not supported", storageType)
	}
	buckets := resolveCleanupBuckets()
	if len(buckets) == 0 {
		return errors.New("no object-storage bucket resolved from the environment; set OBJECT_STORAGE_BUCKET_NAME")
	}

	prefix := namespace + "/"
	var errs []error
	total := 0
	for _, bucket := range buckets {
		if ctx.Err() != nil {
			errs = append(errs, fmt.Errorf("cleanup cancelled after %d of %d buckets: %w", len(errs), len(buckets), ctx.Err()))
			break
		}
		store, err := o.storeFactory(storageType, bucket)
		if err != nil {
			o.logger.Error("storage cleanup: store init failed",
				"bucket", bucket, "type", storageType, "prefix", prefix, "error", err.Error())
			errs = append(errs, fmt.Errorf("bucket %q: init: %w", bucket, err))
			continue
		}
		deleted, err := store.DeletePrefix(ctx, prefix)
		total += deleted
		if err != nil {
			o.logger.Error("storage cleanup: bucket sweep failed",
				"bucket", bucket, "prefix", prefix, "deleted", deleted, "error", err.Error())
			errs = append(errs, fmt.Errorf("bucket %q: %w", bucket, err))
			continue
		}
		o.logger.Info("storage cleanup: bucket swept",
			"bucket", bucket, "prefix", prefix, "deleted", deleted)
	}
	if err := errors.Join(errs...); err != nil {
		o.logger.Error("storage cleanup finished with errors",
			"namespace", namespace, "buckets", len(buckets), "deleted", total, "failedBuckets", len(errs))
		return fmt.Errorf("storage cleanup for namespace %q: %w", namespace, err)
	}
	o.logger.Info("storage cleanup finished",
		"namespace", namespace, "buckets", len(buckets), "deleted", total)
	return nil
}
