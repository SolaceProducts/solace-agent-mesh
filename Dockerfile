# Stage 1: Build Go binaries
#
# The frontend must be pre-built and placed at internal/desktop/frontend/dist/
# BEFORE running docker build: run `npm ci && npm run build` in webui/, then
# copy webui/static/ to internal/desktop/frontend/dist/static/.
FROM golang:1.27-bookworm AS builder
ARG MAIN_VERSION=dev
ARG STR_VERSION=dev
ENV VERSION_LDFLAGS="-X github.com/SolaceProducts/solace-agent-mesh/internal/samversion.Version=${MAIN_VERSION} -X github.com/SolaceProducts/solace-agent-mesh/internal/samversion.STRVersion=${STR_VERSION}"
WORKDIR /src
COPY go.mod go.sum ./
# go.mod has `replace github.com/jmespath/go-jmespath => ./third_party/jmespath`.
# `go mod download` resolves the replace target and
# needs the directory present in the build context before it runs.
COPY third_party/ ./third_party/
RUN go mod download

# Copy source in layers ordered by change frequency (least → most).
# Unchanged layers stay cached even when other directories change.
COPY common/ ./common/
COPY tools/ ./tools/
COPY pkg/ ./pkg/
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY configs/ ./configs/
COPY skills/ ./skills/

# Generate the connector schema BEFORE the main go build so that
# //go:embed schemas/*.yaml inside pkg/schema/ picks it up. The file is
# .gitignore'd (it's the build output of cmd/generate-connector-schema),
# so a fresh checkout doesn't have it on disk — and without it on disk
# at compile time, pkg/schema's embed has 5 of 6 schemas. At runtime the
# platform binary's reconcileBundledAgentSkills then aborts with
# `schema "connector" not found`, silently dropping the entire 14-skill
# design+schema upload that the Builder agent depends on. Mirrors the
# `make generate` step the local developer Makefile runs before any
# `go build` of the platform / sam binaries.
RUN --mount=type=cache,target=/root/.cache/go-build \
    go run ./cmd/generate-connector-schema -o pkg/schema/schemas/connector.schema.yaml

# Generate the public OpenAPI description BEFORE the main go build so that
# //go:embed all:spec in internal/platform/openapispec picks it up. The file is
# .gitignore'd (it's the build output of cmd/gen-openapi), and without it the
# image serves 503 at /api/v2/openapi.yaml instead of its description.
# Mirrors the openapi-embed step of `make generate`.
RUN --mount=type=cache,target=/root/.cache/go-build \
    go run -tags nosolace ./cmd/gen-openapi -out /tmp/openapi && \
    cp /tmp/openapi/sam-openapi.yaml internal/platform/openapispec/spec/sam-openapi.yaml

# Build the sam-test-harness skill's bundled Go tool BEFORE the main go build
# so that //go:embed files inside internal/builtinskills/ picks it up. The
# binary is .gitignore'd (its bytes drift per-host and per-rebuild) so a
# fresh checkout requires this step before any platform / sam build that
# embeds builtinskills. CGO_ENABLED=0 +
# -tags nosolace match the Makefile's build-test-harness-skill linux target.
RUN --mount=type=cache,target=/root/.cache/go-build \
    mkdir -p /src/internal/builtinskills/files/sam-test-harness/tools/test_harness && \
    CGO_ENABLED=0 GOOS=linux go build -tags nosolace \
        -o /src/internal/builtinskills/files/sam-test-harness/tools/test_harness/test_harness \
        ./tools/go/test_harness

# Build the sam-excel skill's bundled xlsx tool the same way (and for the same
# reason) so //go:embed picks up the Linux binary in builtinskills builds.
RUN --mount=type=cache,target=/root/.cache/go-build \
    mkdir -p /src/internal/builtinskills/files/sam-excel/tools/xlsx && \
    CGO_ENABLED=0 GOOS=linux go build -tags nosolace \
        -o /src/internal/builtinskills/files/sam-excel/tools/xlsx/xlsx \
        ./tools/go/xlsx

# Build the sam-docx skill's bundled docx tool the same way (and for the same
# reason) so //go:embed picks up the Linux binary in builtinskills builds.
RUN --mount=type=cache,target=/root/.cache/go-build \
    mkdir -p /src/internal/builtinskills/files/sam-docx/tools/docx && \
    CGO_ENABLED=0 GOOS=linux go build -tags nosolace \
        -o /src/internal/builtinskills/files/sam-docx/tools/docx/docx \
        ./tools/go/docx

# Build the sam-build-apply skill's bundled tool the same way, for the same
# reason.
RUN --mount=type=cache,target=/root/.cache/go-build \
    mkdir -p /src/internal/builtinskills/files/sam-build-apply/tools/build_apply && \
    CGO_ENABLED=0 GOOS=linux go build -tags nosolace \
        -o /src/internal/builtinskills/files/sam-build-apply/tools/build_apply/build_apply \
        ./tools/go/build_apply

RUN --mount=type=cache,target=/root/.cache/go-build \
    go build -tags embedui -ldflags "${VERSION_LDFLAGS}" -o /bin/sam ./cmd/sam && \
    go build -ldflags "${VERSION_LDFLAGS}" -o /bin/sam-platform ./cmd/platform && \
    go build -ldflags "${VERSION_LDFLAGS}" -o /bin/sam-awe ./cmd/awe && \
    go build -ldflags "${VERSION_LDFLAGS}" -o /bin/sam-gwe ./cmd/gateway && \
    go build -ldflags "${VERSION_LDFLAGS}" -o /bin/sam-str ./cmd/str && \
    go build -ldflags "${VERSION_LDFLAGS}" -o /bin/sam-doctor ./cmd/sam-doctor

# NOTE: This all-in-one image runs `sam run --embedded` (see CMD below), which
# serves Go STR tools IN-PROCESS — the embedded worker registers them directly
# (internal/embedded/inprocess_tools.go). It does not read a tools dir:
# SAM_TOOLS_DIR is unset and str.yaml's toolsets_dir is force-overridden to the
# platform write path in embedded mode. So the previously-baked
# /opt/sam/builtin-tools (8 hand-listed binaries + a hand-written manifest whose
# names — sql_tools, etc. — did not match the execute_sql_query the connectors
# request) was dead weight and a silent name-mismatch trap; it has been removed.
# The standalone STR pod image (internal/container/Dockerfile.str) is unaffected
# — it still builds the full tool set with the correct per-tool manifests.

# Stage 2: Runtime
FROM debian:bookworm-20261123-slim

LABEL org.opencontainers.image.licenses="LicenseRef-Solace-Proprietary" \
      org.opencontainers.image.vendor="Solace Corporation" \
      org.opencontainers.image.title="solace-agent-mesh" \
      org.opencontainers.image.source="https://github.com/SolaceProducts/solace-agent-mesh" \
      org.opencontainers.image.documentation="https://solace.com/legal/"

# chromium is Debian's largest CVE surface in this image and the source of the
# vulnerabilities Prisma flags. Debian ships the patched build
# via bookworm-security, but BuildKit caches the apt layer, so a stale build
# keeps shipping the old chromium. Pinning the required floor asserts the
# patched version is present and, because bumping it changes this RUN's text,
# busts the apt-layer cache and forces a fresh pull the next time a chromium CVE
# is disclosed — replacing the base-image-date bump used when a newer Debian
# snapshot is available.
ARG CHROMIUM_MIN_VERSION=156.0.8078.47-1~deb12u1
# libgbm1 comes from the mesa source package (a chromium dependency) and is the
# CVE surface Prisma flags for mesa (CVE-2026-40393). It is
# floored the same way as chromium: the version floor asserts the patched build
# is present and, because all mesa binaries share the source version, checking
# one package validates the whole set. Bumping the floor busts the cached apt
# layer to force a fresh pull.
ARG MESA_MIN_VERSION=22.3.6-1+deb12u2
# python3.11 bundles its own expat (pyexpat), the CVE surface Prisma flags
# (CVE-2026-4224 — C stack overflow on deeply nested content
# models). Debian ships the patched build via bookworm-security; the floor
# asserts it is installed and, because bumping it changes this RUN's text,
# busts the cached apt layer the next time a python3.11 CVE is disclosed.
ARG PYTHON311_MIN_VERSION=3.11.2-6+deb12u8
# openssl (libssl3), pcre2 (libpcre2-8-0) and xz-utils (liblzma5) are Prisma
# CVE surfaces. libpcre2-8-0 and liblzma5 ship in the base
# image, so apt only upgrades them when named explicitly; all are floored like
# chromium.
ARG OPENSSL_MIN_VERSION=3.0.22-1~deb12u1
ARG PCRE2_MIN_VERSION=10.42-1+deb12u1
ARG XZ_MIN_VERSION=5.4.1-1+deb12u2
ARG PERL_MIN_VERSION=5.36.0-7+deb12u4
ARG CA_CERTIFICATES_MIN_VERSION=20250419~deb12u2
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    chromium \
    libgnutls30 \
    liblzma5 \
    libpcre2-8-0 \
    libssl3 \
    perl-base \
    python3 \
    && rm -rf /var/lib/apt/lists/* \
    && installed="$(dpkg-query -W chromium | cut -f2)" \
    && dpkg --compare-versions "$installed" ge "$CHROMIUM_MIN_VERSION" \
       || { echo "chromium $installed is below required floor $CHROMIUM_MIN_VERSION" >&2; exit 1; } \
    && installed="$(dpkg-query -W libgbm1 | cut -f2)" \
    && dpkg --compare-versions "$installed" ge "$MESA_MIN_VERSION" \
       || { echo "mesa (libgbm1) $installed is below required floor $MESA_MIN_VERSION" >&2; exit 1; } \
    && installed="$(dpkg-query -W python3.11 | cut -f2)" \
    && dpkg --compare-versions "$installed" ge "$PYTHON311_MIN_VERSION" \
       || { echo "python3.11 $installed is below required floor $PYTHON311_MIN_VERSION" >&2; exit 1; } \
    && installed="$(dpkg-query -W libssl3 | cut -f2)" \
    && dpkg --compare-versions "$installed" ge "$OPENSSL_MIN_VERSION" \
       || { echo "openssl (libssl3) $installed is below required floor $OPENSSL_MIN_VERSION" >&2; exit 1; } \
    && installed="$(dpkg-query -W libpcre2-8-0 | cut -f2)" \
    && dpkg --compare-versions "$installed" ge "$PCRE2_MIN_VERSION" \
       || { echo "pcre2 (libpcre2-8-0) $installed is below required floor $PCRE2_MIN_VERSION" >&2; exit 1; } \
    && installed="$(dpkg-query -W liblzma5 | cut -f2)" \
    && dpkg --compare-versions "$installed" ge "$XZ_MIN_VERSION" \
       || { echo "xz-utils (liblzma5) $installed is below required floor $XZ_MIN_VERSION" >&2; exit 1; } \
    && installed="$(dpkg-query -W perl-base | cut -f2)" \
    && dpkg --compare-versions "$installed" ge "$PERL_MIN_VERSION" \
       || { echo "perl-base $installed is below required floor $PERL_MIN_VERSION" >&2; exit 1; } \
    && installed="$(dpkg-query -W ca-certificates | cut -f2)" \
    && dpkg --compare-versions "$installed" ge "$CA_CERTIFICATES_MIN_VERSION" \
       || { echo "ca-certificates $installed is below required floor $CA_CERTIFICATES_MIN_VERSION" >&2; exit 1; }

RUN useradd -r -u 10001 -g nogroup samuser

# License attribution must precede any binary COPY.
COPY LICENSE /usr/share/doc/solace-agent-mesh/LICENSE
COPY licenses/solace-agent-mesh-source-third-party-licenses.txt /usr/share/doc/solace-agent-mesh/solace-agent-mesh-source-third-party-licenses.txt

COPY --from=builder /bin/sam /usr/local/bin/
COPY --from=builder /bin/sam-platform /usr/local/bin/
COPY --from=builder /bin/sam-awe /usr/local/bin/
COPY --from=builder /bin/sam-gwe /usr/local/bin/
COPY --from=builder /bin/sam-str /usr/local/bin/
COPY --from=builder /bin/sam-doctor /usr/local/bin/
# Copy frontend assets for GWE static serving
COPY --from=builder --chown=10001:nogroup /src/internal/desktop/frontend/dist/static/ /opt/sam/frontend/

# Go STR tools are served in-process by the embedded worker (see the builder
# stage note above); no /opt/sam/builtin-tools is baked into this image.

# Copy only the runtime config sets into the image: gwe/ (gateway+platform) and
# str/ (used by the GWE and STR pods, and by the single-container default CMD
# below). awe/ is baked separately just below. We deliberately do NOT copy the
# rest of configs/ (examples/, dev-users.json, awe-extras/) — those are samples
# and dev/mount scaffolding, not runtime config. Copying only what we run is
# deny-by-default: a new sample added to configs/ can never leak into the image
# or get auto-loaded. (Before the edition collapse (#1987) the image baked the
# curated configs-enterprise/ set for the same reason; the collapse merged the
# community samples into configs/ and the bake started COPYing the whole tree,
# which is what crash-looped the recursive default CMD.)
COPY --from=builder /src/configs/gwe/ /etc/sam/configs/gwe/
COPY --from=builder /src/configs/str/ /etc/sam/configs/str/

# Bake the AWE base config into /etc/sam/configs/awe/sam.yaml — the path the
# Helm chart loads as the AWE pod's first --config. It carries the framework
# blocks + broker connection but NO chat agent: the editable
# Orchestrator is seeded into the platform DB on first run (SeedBuiltinAgents)
# and deployed to this AWE over the control plane. Replaces the retired
# Concierge (onboard_agent.yaml). Source-of-truth: internal/settings/files
# (the desktop binary embeds it via //go:embed and can't reach
# configs/ from there). Same yaml content everywhere, single git source.
COPY --from=builder /src/internal/settings/files/awe_base.yaml /etc/sam/configs/awe/sam.yaml

# Bake the Builder agent. Desktop / samcli `run` paths load it via
# settings.AdditionalConfigYAML() when --system-env is false; in container mode
# (--system-env), that branch is skipped, so the binary needs the YAML on disk
# under /etc/sam/configs/awe/ to discover it. Frontend visibility stays gated
# by SAM_FEATURE_BUILDER — baking the YAML is harmless when the flag is off.
COPY --from=builder /src/internal/settings/files/builder_agent.yaml /etc/sam/configs/awe/builder_agent.yaml

# Copy image-baked built-in skills to /opt/sam/builtin-skills/. This path is
# distinct from the runtime-mirrored skills dir (/opt/sam/skills/) so charts
# can mount an emptyDir at the mirror target without shadowing baked content
# on read-only-rootfs pods. SAM_BUILTIN_SKILLS_DIR defaults to this path via
# str.yaml; old charts that don't set SAM_BUILTIN_SKILLS_DIR fall back to
# SAM_SKILLS_DIR for one release.
# chmod=0555 → read-only + traversable; baked skills are static content.
# The cluster built-in skills (sam-knowledge, sam-docs, sam-build-redirect,
# sam-test-harness) are NOT baked here — they live in internal/builtinskills/,
# are //go:embed'd into the platform binary, and reconciled to the object
# store on startup (cluster mode) or written via settings.WriteSkills
# (desktop mode). sam-test-harness ships a bundled Go binary built in the
# RUN step above; the other three are pure markdown.
COPY --from=builder --chown=10001:nogroup --chmod=0555 /src/skills/ /opt/sam/builtin-skills/

RUN mkdir -p /tmp/sam-data && chown 10001:nogroup /tmp/sam-data

USER samuser
WORKDIR /tmp/sam-data

EXPOSE 8800 8001 8090
ENTRYPOINT ["sam"]
# Single-container embedded default. This is the batteries-included all-in-one
# image (one --embedded process running every component), distinct from the
# multi-pod Kubernetes split. It loads the SAME runtime config files the
# pods load — gwe.yaml + platform.yaml (GWE pod), str.yaml (STR pod), awe/sam.yaml
# (AWE pod) — plus builder_agent.yaml so the out-of-box experience ships the
# Builder, matching the pre-collapse sam-enterprise image (Builder is opt-in in
# K8s via extraConfigs, but the demo container bundles it; FE visibility stays
# gated by SAM_FEATURE_BUILDER). Naming the files explicitly — rather than
# loading the whole /etc/sam/configs tree — keeps this in lockstep with the chart
# and is drift proof: a stray config baked into the dir can never be auto-loaded
# into the embedded process. Orchestrator + Sentinel are seeded by the platform
# and deployed to the embedded AWE at runtime.
CMD ["run", "--embedded", "--system-env", "--", \
     "/etc/sam/configs/gwe/gwe.yaml", "/etc/sam/configs/gwe/platform.yaml", \
     "/etc/sam/configs/str/str.yaml", "/etc/sam/configs/awe/sam.yaml", \
     "/etc/sam/configs/awe/builder_agent.yaml"]
