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
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/manifest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/pull"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/reconcile"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/sources"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/oauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/runner"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/tokencache"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samversion"
)

func confirmPrompt(cmd *cobra.Command, question string) (bool, error) {
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s [y/N] ", question)
	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && err != io.EOF {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

func defaultOAuthResolver() (target.OAuthTokenResolver, error) {
	store, err := tokencache.NewDefault()
	if err != nil {
		return nil, err
	}
	return tokencache.NewResolver(store, oauth.NewClient()), nil
}

func newConfigCmd() *cobra.Command {
	var envFiles []string
	var noDotenv bool
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Describe agents in YAML and apply them to the platform service",
		Long: `Describe your agents, tools, and skills in YAML files, then apply them
to the platform service.

Edit the YAML, run "sam config plan" to preview the changes, then
"sam config apply" to make them. "sam config pull" does the reverse,
exporting what is already running back into editable YAML.

Config commands auto-load a .env file from the nearest ancestor directory.
Unlike "sam run", variables already set in the environment win over it, so a
CI job's exported credentials are never replaced by a stray .env. Use
--env-file to supply additional files or to override specific variables. Pass
--no-dotenv to skip auto-loading.`,
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			if !noDotenv {
				if envPath := runner.LoadDotEnvKeepingEnv(slog.Default()); envPath != "" {
					slog.Default().With("logger_name", "samcli").Info("loaded environment", "path", envPath)
				}
			}
			for _, ef := range envFiles {
				if err := godotenv.Overload(ef); err != nil {
					return fmt.Errorf("loading --env-file %q: %w", ef, err)
				}
			}
			return nil
		},
	}
	cmd.PersistentFlags().StringArrayVar(&envFiles, "env-file", nil,
		"Load environment variables from this file before running (repeatable). "+
			"Loaded after the auto-detected .env, so values here override it.")
	cmd.PersistentFlags().BoolVar(&noDotenv, "no-dotenv", false,
		"Skip auto-loading the nearest .env file.")

	cmd.AddGroup(
		&cobra.Group{ID: "core", Title: "Core:"},
		&cobra.Group{ID: "setup", Title: "Setup and maintenance:"},
	)
	addSub := func(group string, c *cobra.Command) {
		c.GroupID = group
		cmd.AddCommand(c)
	}
	addSub("core", newConfigApplyCmd())
	addSub("core", newConfigPlanCmd())
	addSub("core", newConfigSchemaCmd())
	addSub("setup", newConfigPullCmd())
	addSub("setup", newConfigMigrateCmd())
	addSub("setup", newConfigRefreshCmd())
	addSub("setup", newConfigCacheCmd())

	return cmd
}

func newConfigRefreshCmd() *cobra.Command {
	var cacheRoot string
	cmd := &cobra.Command{
		Use:   "refresh",
		Short: "Force a fresh download of remote sources",
		Long: `Wipe the local source cache used by "sam config apply" / "sam config plan".

The cache lives at $XDG_CACHE_HOME/sam/sources (or ~/.cache/sam/sources
when XDG_CACHE_HOME is unset). Each entry is a clone of one git source at
one ref; pinned refs (tags, full SHAs) are reused forever, so this command
is the manual eviction lever for users who want a forced re-fetch.

Running on a missing cache directory is a no-op; the command never errors
on absent state.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root := cacheRoot
			if root == "" {
				r, err := sources.DefaultCacheRoot()
				if err != nil {
					return err
				}
				root = r
			}
			if err := sources.PurgeCache(root); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "source cache cleared: %s\n", root)
			return nil
		},
	}
	cmd.Flags().StringVar(&cacheRoot, "cache-root", "",
		"Override the cache root (defaults to $XDG_CACHE_HOME/sam/sources or ~/.cache/sam/sources).")
	return cmd
}

type configFlags struct {
	manifestPath     string
	url              string
	target           string
	noColor          bool
	verbose          bool
	allowFloating    bool
	noCache          bool
	noBuild          bool
	noInteractive    bool
	skipVersionCheck bool
	detailedExitCode bool
	format           string
}

func (f *configFlags) bind(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&f.manifestPath, "manifest", "m", "",
		"Path to the manifest YAML to apply. "+
			"Defaults to ./manifest.yaml (or ./manifest.yml) in the current directory when omitted.")
	cmd.Flags().StringVar(&f.url, "url", "",
		"Platform URL to apply to; overrides the manifest's target.url.")
	cmd.Flags().StringVar(&f.target, "target", "",
		"Named `sam auth login` target whose URL to apply to; overrides the manifest's target.url (alternative to --url).")
	cmd.Flags().BoolVar(&f.noColor, "no-color", false,
		"Disable ANSI color codes in plan output. Also set by the NO_COLOR env var.")
	cmd.Flags().BoolVarP(&f.verbose, "verbose", "v", false,
		"Show per-field diffs for resources that would be updated. Auth secrets render as \"(changed)\".")
	cmd.Flags().BoolVar(&f.allowFloating, "allow-floating-refs", false,
		"Permit manifest sources without a pinned ref (branch, short SHA, or HEAD). "+
			"Resolution becomes non-reproducible; a warning is printed for each floating ref.")
	cmd.Flags().BoolVar(&f.noCache, "no-cache", false,
		"Bypass the source cache; force a fresh clone for every git source.")
	cmd.Flags().BoolVar(&f.noBuild, "no-build", false,
		"Skip toolset build steps. On `plan`, cache misses surface as `[BUILD: skipped]` "+
			"without error. On `apply`, cache misses are a hard error — pair with `plan` "+
			"first to populate the cache, or run without `--no-build` for a one-shot build.")
	cmd.Flags().BoolVar(&f.noInteractive, "no-interactive", false,
		"Never prompt: fail instead of offering OAuth login, and require --force for --prune. "+
			"Implied when CI is set or stdin/stderr is not a terminal.")
	cmd.Flags().BoolVar(&f.skipVersionCheck, "skip-version-check", false,
		"Skip the CLI<->platform version compatibility preflight. Also settable via "+
			"SAM_SKIP_VERSION_CHECK=1. Break-glass escape hatch; the check is best-effort "+
			"and never fatal except when the platform advertises a minimum CLI version.")
	cmd.Flags().BoolVar(&f.detailedExitCode, "detailed-exit-code", false,
		"Exit 2 when the plan succeeds with changes pending, 0 when nothing is pending, 1 on error. "+
			"For pipelines that gate on drift. On apply, only with --dry-run.")
	addFormatFlag(cmd, &f.format)
}

func newConfigPlanCmd() *cobra.Command {
	flags := &configFlags{}
	var prune bool
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Preview what apply would change, without touching anything",
		Long: `Show changes a manifest would apply, without mutating state.

Loads the manifest, resolves declared resources, fetches current platform
state, and prints a per-resource diff plus summary counts.`,
		Example: `  sam config plan
  sam config plan -m ./manifest.yaml
  sam config plan --url https://platform.example.com
  sam config plan --target dev
  sam config plan --prune
  sam config plan --detailed-exit-code`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runConfigPlan(cmd, flags, prune)
		},
	}
	flags.bind(cmd)
	cmd.Flags().BoolVar(&prune, "prune", false,
		"Plan as apply --prune would: deletes count as pending changes. Nothing is deleted.")
	return cmd
}

func newConfigApplyCmd() *cobra.Command {
	flags := &configFlags{}
	var prune, dryRun, noDeploy, force bool
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply your config to the platform service, --prune to delete",
		Long: `Apply a manifest's resources to a running platform.

Computes the same diff "sam config plan" produces, then executes creates,
updates, and (with --prune) deletes via the platform's REST API.

For kinds that have a deploy phase, apply runs a second pass after config
sync that POSTs to the kind's deployment endpoint. --no-deploy skips this
phase entirely so a manifest can be reconciled without touching deployments.
Without --no-deploy, apply also deploys an agent, link agent, workflow or
entrypoint whose config is unchanged but whose deployment is out of sync or not
deployed, such as one an earlier --no-deploy run created, and a workflow or link
agent whose last deploy failed.

A failed operation does not abort the rest of the apply; the command
returns non-zero if any operation failed.`,
		Example: `  sam config apply
  sam config apply -m ./manifest.yaml
  sam config apply --dry-run
  sam config apply --prune
  sam config apply --no-deploy`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if flags.detailedExitCode && !dryRun {
				return errors.New("--detailed-exit-code applies only with --dry-run\nuse it with `sam config plan` or `sam config apply --dry-run`")
			}
			if prune && !dryRun && !force {
				if flags.noInteractive || !isInteractive() {
					return errors.New("refusing to --prune without confirmation: pass --force when not running interactively")
				}
				ok, err := confirmPrompt(cmd, "Prune deletes platform resources that are absent from the manifest. Continue?")
				if err != nil {
					return err
				}
				if !ok {
					return errors.New("prune not confirmed: nothing was applied\nre-run and confirm, or pass --force to skip the prompt")
				}
			}
			authResolver, err := defaultOAuthResolver()
			if err != nil {
				return err
			}
			return runConfigApply(cmd, flags, authResolver, reconcile.ApplyOptions{
				Prune:          prune,
				DryRun:         dryRun,
				NoDeploy:       noDeploy,
				Resolve:        resolveOptionsFor(cmd, flags),
				NoBuild:        flags.noBuild,
				BuildStdout:    cmd.ErrOrStderr(),
				BuildStderr:    cmd.ErrOrStderr(),
				DeployProgress: cmd.ErrOrStderr(),
				AuthResolver:   authResolver,
			})
		},
	}
	flags.bind(cmd)
	cmd.Flags().BoolVar(&prune, "prune", false,
		"Delete resources that exist on the platform but aren't in the manifest.")
	cmd.Flags().BoolVar(&force, "force", false,
		"Skip the --prune confirmation prompt; required when --prune runs non-interactively (e.g. CI).")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"Compute the plan but do not mutate the platform (equivalent to `sam config plan`).")
	cmd.Flags().BoolVar(&noDeploy, "no-deploy", false,
		"Run config sync (phase 1) but skip deployment (phase 2). "+
			"Use to reconcile config without redeploying running services.")
	return cmd
}

type configPullFlags struct {
	output             string
	url                string
	target             string
	authType           string
	authEnvVar         string
	force              bool
	merge              bool
	onlyKind           string
	name               string
	manifestName       string
	includeYAMLPreview bool
	referenceManifest  string
}

func newConfigPullCmd() *cobra.Command {
	flags := &configPullFlags{}
	cmd := &cobra.Command{
		Use:   "pull",
		Short: "Export the platform service's live config into editable YAML",
		Long: `Serialize a running platform's state into spec-format YAML.

Lists each in-scope kind (model, connector, toolset, skill, agent,
gateway, linkAgent, workflow, dataset, evaluator, experiment) from the platform,
runs the per-kind reverse translator, substitutes secret-typed fields
with ${VAR} placeholders, and writes one YAML per resource into
--output. A manifest enumerating what was pulled is generated alongside
under <output>/manifests/.

Default behavior refuses to write when --output is non-empty; pass
--force to wipe the directory first or --merge to update only files for
resources that exist on the platform.

Toolset pull in v1 emits each toolset's metadata + spec.config block
(secrets re-templated as ${TOOLSET_<NAME>_<KEY>}); the bundle zip
itself is not downloaded. Pulled values are literal — ${VAR} references
inside spec.config are not preserved across the round-trip, mirroring
the existing caveat for connector authConfig and model apiKey. Re-add
${VAR} references by hand if you want to re-template after a pull.`,
		Example: `  sam config pull -o ./pulled --url https://platform.example.com
  sam config pull -o ./pulled --target dev --only agent
  sam config pull -o ./pulled --url https://platform.example.com --force`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runConfigPull(cmd, flags)
		},
	}
	cmd.Flags().StringVarP(&flags.output, "output", "o", "",
		"Directory to write the pulled YAML repo into (required).")
	cmd.Flags().StringVar(&flags.url, "url", "",
		"Platform URL to pull from (e.g. https://platform.example.com).")
	cmd.Flags().StringVar(&flags.target, "target", "",
		"Named `sam auth login` target to pull from (alternative to --url).")
	cmd.Flags().StringVar(&flags.authType, "auth-type", "",
		"Auth type for the target (e.g. \"bearer\"). Optional.")
	cmd.Flags().StringVar(&flags.authEnvVar, "auth-env", "",
		"Env var holding the auth token. Optional.")
	cmd.Flags().BoolVar(&flags.force, "force", false,
		"Wipe the contents of --output before writing.")
	cmd.Flags().BoolVar(&flags.merge, "merge", false,
		"Keep files for resources that aren't on the platform; only rewrite files for resources that are pulled.")
	cmd.Flags().StringVar(&flags.onlyKind, "only", "",
		"Restrict the pull to one kind: "+strings.Join(pull.InScopeKinds(), " | ")+". "+
			"Accepts the bare kind name; the legacy `kind=X` form is also tolerated.")
	cmd.Flags().StringVar(&flags.name, "name", "",
		"Restrict the pull to a single resource by name (alias, for models).")
	cmd.Flags().StringVar(&flags.manifestName, "manifest-name", "",
		"Override the auto-generated manifest filename (default: pulled-<UTC timestamp>.yaml).")
	cmd.Flags().BoolVar(&flags.includeYAMLPreview, "include-yaml-preview", false,
		"For RBAC kinds, also emit operator-owned YAML-defined rows as commented-out preview "+
			"blocks. Preview files are excluded from the manifest and are not authorable through "+
			"`sam config apply`.")
	cmd.Flags().StringVar(&flags.referenceManifest, "reference", "",
		"Path to the manifest that drove the apply. When supplied, pull carries the manifest's "+
			"sources: block forward and emits imported resources as `name@source` / `{from, as}` "+
			"shapes, omitting their on-disk files (the source is the truth). Required for a clean "+
			"apply → pull → diff round-trip on manifests that aggregate imported resources.")
	_ = cmd.MarkFlagRequired("output")
	return cmd
}

func runConfigPull(cmd *cobra.Command, f *configPullFlags) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	if f.output == "" {
		return errors.New("--output is required")
	}
	pullURL, err := platformURLFromFlags(f.url, f.target)
	if err != nil {
		return err
	}
	if pullURL == "" {
		return errors.New("--url (or --target) is required for pull")
	}

	tg := target.Target{
		URL: pullURL,
		Auth: target.AuthSpec{
			Type:   target.AuthType(f.authType),
			EnvVar: f.authEnvVar,
		},
	}

	onlyKind := f.onlyKind
	if v, ok := strings.CutPrefix(onlyKind, "kind="); ok {
		onlyKind = v
	}
	if onlyKind == "systemUser" {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), legacyServiceAccountKindWarning)
	}

	authResolver, err := defaultOAuthResolver()
	if err != nil {
		return err
	}
	recordAddressedPlatform(ctx, tg)

	if verr := preflightVersionCheck(ctx, tg, authResolver, newDiagnostics(cmd.ErrOrStderr(), textFormat).warn, false); verr != nil {
		return verr
	}

	var refManifest *manifest.Manifest
	if f.referenceManifest != "" {
		refManifest, err = manifest.Load(f.referenceManifest)
		if err != nil {
			return fmt.Errorf("--reference: %w", err)
		}
	}

	res, err := pull.Run(ctx, tg, pull.Options{
		OutputDir:          f.output,
		Force:              f.force,
		Merge:              f.merge,
		OnlyKind:           onlyKind,
		Name:               f.name,
		ManifestName:       f.manifestName,
		AuthResolver:       authResolver,
		IncludeYAMLPreview: f.includeYAMLPreview,
		ReferenceManifest:  refManifest,
	})
	if err != nil {
		return err
	}
	pull.RenderResult(cmd.OutOrStdout(), res)
	return nil
}

func runConfigPlan(cmd *cobra.Command, f *configFlags, prune bool) error {
	format, err := parseFormat(f.format)
	if err != nil {
		return err
	}
	diags := newDiagnostics(cmd.ErrOrStderr(), format)
	plan, err := computePlan(cmd, f, diags)
	if format == jsonFormat {
		report := newPlanReport(plan, append(slices.Clone(diags.items), plan.Diagnostics()...), err)
		if werr := writeJSON(cmd.OutOrStdout(), report); werr != nil {
			return werr
		}
	} else if err == nil {
		reconcile.RenderPlan(cmd.OutOrStdout(), plan, formatOptions(cmd, f, prune))
	}
	if err != nil {
		return err
	}
	return changesPendingErr(f, plan, prune, false)
}

func computePlan(cmd *cobra.Command, f *configFlags, diags *diagnostics) (*reconcile.PlanResult, error) {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	loadSecretsDir(func(format string, args ...any) {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), format+"\n", args...)
	})

	m, repoRoot, tg, err := loadConfigContext(cmd, f)
	if err != nil {
		return nil, err
	}

	authResolver, err := defaultOAuthResolver()
	if err != nil {
		return nil, err
	}

	if perr := preflightAuth(ctx, tg, authResolver, cmd.ErrOrStderr(), os.Stdin, f.noInteractive); perr != nil {
		return nil, perr
	}
	recordAddressedPlatform(ctx, tg)

	if verr := preflightVersionCheck(ctx, tg, authResolver, diags.warn, f.skipVersionCheck); verr != nil {
		return nil, verr
	}

	skew := reconcile.NewSchemaSkewSink()
	plan, err := reconcile.PlanWithOptions(ctx, tg, repoRoot, m, reconcile.PlanOptions{
		Resolve:        resolveOptionsFor(cmd, f),
		NoBuild:        f.noBuild,
		BuildStdout:    cmd.ErrOrStderr(),
		BuildStderr:    cmd.ErrOrStderr(),
		AuthResolver:   authResolver,
		SchemaSkewSink: skew,
	})
	diags.finish(skew)
	if err != nil {
		return nil, err
	}
	return plan, nil
}

func resolveOptionsFor(cmd *cobra.Command, f *configFlags) resolve.Options {
	return resolve.Options{
		AllowFloating: f.allowFloating,
		NoCache:       f.noCache,
		Logf: func(format string, args ...any) {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), format+"\n", args...)
		},
	}
}

func runConfigApply(cmd *cobra.Command, f *configFlags, authResolver target.OAuthTokenResolver, opts reconcile.ApplyOptions) error {
	format, err := parseFormat(f.format)
	if err != nil {
		return err
	}
	diags := newDiagnostics(cmd.ErrOrStderr(), format)
	res, err := runApply(cmd, f, authResolver, opts, diags)
	if format == jsonFormat {
		if werr := writeJSON(cmd.OutOrStdout(), newApplyReport(res, diags, err)); werr != nil {
			return werr
		}
	} else if res != nil {
		reconcile.RenderApply(cmd.OutOrStdout(), res, formatOptions(cmd, f, opts.Prune))
	}
	if err != nil {
		return err
	}
	if opts.DryRun {
		return changesPendingErr(f, res.Plan, opts.Prune, opts.NoDeploy)
	}
	return nil
}

func runApply(cmd *cobra.Command, f *configFlags, authResolver target.OAuthTokenResolver, opts reconcile.ApplyOptions, diags *diagnostics) (*reconcile.ApplyResult, error) {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	loadSecretsDir(func(format string, args ...any) {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), format+"\n", args...)
	})

	m, repoRoot, tg, err := loadConfigContext(cmd, f)
	if err != nil {
		return nil, err
	}

	if perr := preflightAuth(ctx, tg, authResolver, cmd.ErrOrStderr(), os.Stdin, f.noInteractive); perr != nil {
		return nil, perr
	}
	recordAddressedPlatform(ctx, tg)

	if verr := preflightVersionCheck(ctx, tg, authResolver, diags.warn, f.skipVersionCheck); verr != nil {
		return nil, verr
	}

	skew := reconcile.NewSchemaSkewSink()
	opts.SchemaSkewSink = skew
	res, err := reconcile.Apply(ctx, tg, repoRoot, m, opts)
	diags.finish(skew)
	return res, err
}

func preflightVersionCheck(ctx context.Context, tg target.Target, authResolver target.OAuthTokenResolver, warn func(string), skip bool) error {
	if skip || envFlagSet("SAM_SKIP_VERSION_CHECK") {
		return nil
	}
	c, err := reconcile.NewClientForTarget(ctx, tg, authResolver)
	if err != nil {
		slog.Default().With("logger_name", "samcli").Debug("version preflight: client construction failed", "error", err)
		return nil
	}
	pv, err := c.FetchVersion(ctx)
	if err != nil {
		warn(fmt.Sprintf("could not check CLI/platform version compatibility (%v); continuing without it", err))
		return nil
	}
	level, msg := reconcile.EvaluateVersionCompat(samversion.Version, pv)
	switch level {
	case reconcile.CompatFatal:
		return errors.New(msg)
	case reconcile.CompatWarn:
		warn(msg)
	case reconcile.CompatOK:
	}
	return nil
}

type diagnostics struct {
	w     io.Writer
	json  bool
	items []reconcile.Diagnostic
}

func newDiagnostics(w io.Writer, format string) *diagnostics {
	return &diagnostics{w: w, json: format == jsonFormat, items: []reconcile.Diagnostic{}}
}

func (d *diagnostics) warn(msg string) {
	if d.json {
		d.items = append(d.items, reconcile.Warning(msg))
		return
	}
	_, _ = fmt.Fprintln(d.w, "warning: "+msg)
}

func (d *diagnostics) finish(skew *reconcile.SchemaSkewSink) {
	if msg := reconcile.FormatSchemaSkew(skew); msg != "" {
		d.warn(msg)
	}
}

type planReport struct {
	Changes     []reconcile.PlanOp     `json:"changes"`
	Summary     reconcile.PlanSummary  `json:"summary"`
	Diagnostics []reconcile.Diagnostic `json:"diagnostics"`
}

type applyReport struct {
	planReport
	Results []reconcile.ApplyOpResult `json:"results"`
}

func newPlanReport(plan *reconcile.PlanResult, diags []reconcile.Diagnostic, runErr error) planReport {
	if runErr != nil {
		diags = append(diags, reconcile.Diagnostic{Level: reconcile.DiagnosticError, Message: runErr.Error()})
	}
	ops := plan.Ops()
	return planReport{Changes: ops, Summary: reconcile.Summarize(ops), Diagnostics: diags}
}

func newApplyReport(res *reconcile.ApplyResult, d *diagnostics, runErr error) applyReport {
	var plan *reconcile.PlanResult
	if res != nil {
		plan = res.Plan
	}
	return applyReport{
		planReport: newPlanReport(plan, append(slices.Clone(d.items), res.Diagnostics()...), runErr),
		Results:    res.Results(),
	}
}

var errChangesPending = errors.New("changes pending")

func changesPendingErr(f *configFlags, plan *reconcile.PlanResult, prune, noDeploy bool) error {
	if f.detailedExitCode && plan.Pending(prune, noDeploy) {
		return errChangesPending
	}
	return nil
}

func envFlagSet(name string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	return v != "" && v != "0" && v != "false"
}

func loadConfigContext(cmd *cobra.Command, f *configFlags) (*manifest.Manifest, string, target.Target, error) {
	manifestPath := f.manifestPath
	if manifestPath == "" {
		discovered, err := discoverDefaultManifest(".")
		if err != nil {
			return nil, "", target.Target{}, err
		}
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "using auto-detected manifest: %s\n", discovered)
		manifestPath = discovered
	}
	absManifest, err := filepath.Abs(manifestPath)
	if err != nil {
		return nil, "", target.Target{}, fmt.Errorf("resolve manifest path: %w", err)
	}
	m, err := manifest.Load(absManifest)
	if err != nil {
		return nil, "", target.Target{}, err
	}
	for _, w := range m.Warnings {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), w)
	}

	manifestDir := filepath.Dir(absManifest)
	repoRoot := manifestDir
	if filepath.Base(manifestDir) == "manifests" {
		repoRoot = filepath.Dir(manifestDir)
	}

	override, err := platformURLFromFlags(f.url, f.target)
	if err != nil {
		return nil, "", target.Target{}, err
	}
	tg, err := resolveManifestTarget(absManifest, m.Target, override)
	if err != nil {
		return nil, "", target.Target{}, err
	}
	return m, repoRoot, tg, nil
}

func discoverDefaultManifest(dir string) (string, error) {
	for _, name := range []string{"manifest.yaml", "manifest.yml"} {
		p := filepath.Join(dir, name)
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, nil
		}
	}
	return "", errors.New("--manifest is required (no manifest.yaml or manifest.yml in current directory)")
}

func formatOptions(cmd *cobra.Command, f *configFlags, prune bool) reconcile.FormatOptions {
	noColor := f.noColor || os.Getenv("NO_COLOR") != ""
	if !noColor {
		if out, ok := cmd.OutOrStdout().(*os.File); ok {
			if !isCharDevice(out) {
				noColor = true
			}
		} else {
			noColor = true
		}
	}
	return reconcile.FormatOptions{NoColor: noColor, Verbose: f.verbose, Prune: prune}
}

func isCharDevice(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}
