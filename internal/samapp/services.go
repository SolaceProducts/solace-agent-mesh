// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samapp

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	azurestore "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/azure"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/filesystem"
	gcsstore "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/gcs"
	artifactmem "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	s3store "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/s3"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
	"github.com/SolaceProducts/solace-agent-mesh/internal/settings"
	"github.com/SolaceProducts/solace-agent-mesh/internal/volume"
)

func servicesLogger() *slog.Logger {
	return slog.Default().With("logger_name", "samapp-services")
}

func CreateSessionStore(cfg map[string]any) (session.Store, error) {
	svcType, _ := cfg["type"].(string)
	switch svcType {
	case "sql":
		return createSessionStoreFromURL(cfg)
	case "sqlite":
		dbPath, _ := cfg["path"].(string)
		if dbPath == "" {
			dbPath = "/tmp/sam-sessions.db"
		}
		return openSessionStore("sqlite:///" + dbPath)
	case "memory", "":
		return session.NewMemoryStore(), nil
	default:
		return nil, fmt.Errorf("unsupported session_service type %q (want sql, sqlite, or memory)", svcType)
	}
}

func createSessionStoreFromURL(cfg map[string]any) (session.Store, error) {
	dbURL, _ := cfg["database_url"].(string)
	if dbURL == "" {
		servicesLogger().Warn("no session database_url configured — using a volatile in-memory "+
			"session store; chat history will NOT survive a restart. Set DATABASE_URL "+
			"(shared Postgres) or a sqlite:/// path for durable sessions.",
			"configKey", "session_service.database_url")
		return session.NewMemoryStore(), nil
	}
	return openSessionStore(dbURL)
}

func openSessionStore(dbURL string) (session.Store, error) {
	db, err := dbutil.Open(dbURL, slog.Default(), dbutil.WithPoolName("sessions"))
	if err != nil {
		return nil, fmt.Errorf("open session db: %w", err)
	}
	store := session.NewSQLStore(db)
	if err := store.Migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate session db: %w", err)
	}
	return store, nil
}

func CreateArtifactService(cfg, appConfig map[string]any) *artifact.ScopedService {
	svcType, _ := cfg["type"].(string)
	basePath, _ := cfg["base_path"].(string)
	scope, _ := cfg["artifact_scope"].(string)
	namespace, _ := appConfig["namespace"].(string)
	namespace = artifact.NormalizeNamespace(namespace)

	if scope == "" {
		scope = artifact.ScopeNamespace
	}
	warnIfNamespaceUnroutable(scope, namespace)

	wrap := func(inner artifact.Service) artifact.Service {
		return observability.NewInstrumentedArtifactService(inner)
	}

	switch svcType {
	case "filesystem":
		if basePath == "" {
			if d := os.Getenv("SAM_DATA_DIR"); d != "" {
				basePath = d
			} else {
				basePath = "./sam-data"
			}
		}
		basePath += "/artifacts"
		return &artifact.ScopedService{
			Inner:     wrap(filesystem.New(basePath)),
			Scope:     scope,
			Namespace: namespace,
		}
	case "s3":
		store, err := createS3Store(cfg)
		if err != nil {
			servicesLogger().Error("failed to create S3 artifact store; artifact uploads/previews unavailable", "error", err)
			return nil
		}
		return &artifact.ScopedService{
			Inner:     wrap(store),
			Scope:     scope,
			Namespace: namespace,
		}
	case "azure":
		store, err := createAzureStore(cfg)
		if err != nil {
			servicesLogger().Error("failed to create Azure artifact store; artifact uploads/previews unavailable", "error", err)
			return nil
		}
		return &artifact.ScopedService{
			Inner:     wrap(store),
			Scope:     scope,
			Namespace: namespace,
		}
	case "gcs":
		store, err := createGCSStore(cfg)
		if err != nil {
			servicesLogger().Error("failed to create GCS artifact store; artifact uploads/previews unavailable", "error", err)
			return nil
		}
		return &artifact.ScopedService{
			Inner:     wrap(store),
			Scope:     scope,
			Namespace: namespace,
		}
	case "memory":
		return &artifact.ScopedService{
			Inner:     wrap(artifactmem.New()),
			Scope:     scope,
			Namespace: namespace,
		}
	case "":
		return nil
	default:
		servicesLogger().Warn("unsupported artifact type", "type", svcType)
		return nil
	}
}

func warnIfNamespaceUnroutable(scope, namespace string) {
	if scope != artifact.ScopeNamespace {
		return
	}
	probe := artifact.Locator{AppName: namespace, UserID: "u", SessionID: "s", Filename: "probe"}
	if _, err := artifact.FormatURI(probe, 0); err != nil {
		servicesLogger().Error(
			"artifact namespace cannot address artifacts; every namespace-scoped upload will fail",
			"error", err)
	}
}

func createS3Store(cfg map[string]any) (*s3store.Store, error) {
	client, err := newObjectStoreOfType(cfg, "s3")
	if err != nil {
		return nil, err
	}
	store := s3store.New(client.S3(), client.BucketName())
	if err := waitForBucket(context.Background(), store.ValidateBucket); err != nil {
		return nil, err
	}
	return store, nil
}

var (
	bucketWaitTimeout  = time.Minute
	bucketWaitInterval = 2 * time.Second
	bucketWaitMaxDelay = 15 * time.Second
)

func waitForBucket(ctx context.Context, check func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, bucketWaitTimeout)
	defer cancel()
	delay := bucketWaitInterval
	var lastErr error
	for attempt := 1; ; attempt++ {
		err := check(ctx)
		if err == nil {
			if attempt > 1 {
				servicesLogger().Info("artifact bucket is reachable", "attempts", attempt)
			}
			return nil
		}
		if ctx.Err() == nil || lastErr == nil {
			lastErr = err
		}
		servicesLogger().Warn("artifact bucket not reachable yet; retrying", "attempt", attempt, "error", err)
		select {
		case <-ctx.Done():
			return lastErr
		case <-time.After(delay):
		}
		delay = min(delay*2, bucketWaitMaxDelay)
	}
}

func createGCSStore(cfg map[string]any) (*gcsstore.Store, error) {
	client, err := newObjectStoreOfType(cfg, "gcs")
	if err != nil {
		return nil, err
	}
	bucket := gcsstore.WrapBucket(client.GCSBucket())
	return gcsstore.New(bucket, client.BucketName()), nil
}

func createAzureStore(cfg map[string]any) (*azurestore.Store, error) {
	if _, hasBucket := cfg["bucket_name"]; !hasBucket {
		if name, ok := cfg["container_name"].(string); ok && name != "" {
			cfg["bucket_name"] = name
		}
	}
	client, err := newObjectStoreOfType(cfg, "azure")
	if err != nil {
		return nil, err
	}
	container := azurestore.WrapContainer(client.AzureContainer())
	return azurestore.New(container, client.BucketName()), nil
}

func newObjectStoreOfType(cfg map[string]any, want string) (*objectstore.Client, error) {
	if cfg == nil {
		cfg = map[string]any{}
	}
	cfg["type"] = want
	client, err := objectstore.NewFromConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("create %s artifact client: %w", want, err)
	}
	if client.StorageType() != want {
		return nil, fmt.Errorf("objectstore returned type %q, expected %q", client.StorageType(), want)
	}
	if client.BucketName() == "" {
		return nil, fmt.Errorf("%s artifact store requires bucket_name (or set OBJECT_STORAGE_BUCKET_NAME)", want)
	}
	return client, nil
}

func CreateVolumeService(cfg map[string]any) (volume.Service, error) {
	svcType, _ := cfg["type"].(string)
	switch svcType {
	case "filesystem", "":
		root, _ := cfg["root"].(string)
		if root == "" {
			home, err := settings.ResolveHomeDir()
			if err != nil {
				home = "/tmp/.sam"
			}
			root = filepath.Join(home, "volumes")
		}
		databaseURL, _ := cfg["database_url"].(string)
		store, err := volume.OpenFilesystemStore(context.Background(), volume.FilesystemConfig{
			Root:        root,
			DatabaseURL: strings.TrimSpace(databaseURL),
			Logger:      slog.Default(),
		})
		if err != nil {
			return nil, fmt.Errorf("create filesystem volume store at %s: %w", root, err)
		}
		if q := volumeQuotaBytes(cfg); q > 0 {
			store.SetPerUserQuotaBytes(q)
		}
		return store, nil
	case "memory":
		return volume.NewMemoryStore(), nil
	default:
		return nil, fmt.Errorf("unsupported volume_service.type %q (expected filesystem or memory)", svcType)
	}
}

func VolumeServiceConfigEnabled(cfg map[string]any) bool {
	if cfg == nil {
		return false
	}
	if v, ok := cfg["enabled"].(bool); ok {
		return v
	}
	return true
}

func volumeQuotaBytes(cfg map[string]any) int64 {
	switch v := cfg["per_user_quota_bytes"].(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case float64:
		return int64(v)
	default:
		return 0
	}
}
