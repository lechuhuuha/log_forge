# Manual Implementation Plan (End-to-End, Updated to Current Repo State)

## Summary
You will manually complete the remaining production-lab work in this order:
1. Finish and validate pinned local infra bootstrap from existing `deploy/lab/*` files.
2. Implement application hardening (`/ready`, `/version`, API key auth, shutdown safety, storage backend config).
3. Add MinIO-backed storage implementation and wire it behind config.
4. Add Helm chart + environment values for staging/production.
5. Add Argo CD Application manifests.
6. Add GitHub Actions CI/release/promotion workflows.
7. Configure secrets and run first release + rollback drill.

Current truth in repo:
- `deploy/lab/step2.md`, `deploy/lab/step3.md`, `deploy/lab/step4.md` exist.
- `deploy/lab/versions.env` exists and pins versions.
- `deploy/lab/namespaces.yaml` exists.
- `deploy/argocd/bootstrap/kustomization.yaml` and `deploy/strimzi/bootstrap/kustomization.yaml` exist.
- Kafka/MinIO/monitoring manifests/values exist.

---

## Important Public API / Interface / Type Changes You Must Implement
1. Add `GET /ready` endpoint in `cmd/bootstrap/app.go`.
2. Add `GET /version` endpoint in `cmd/bootstrap/app.go`.
3. Add API key auth for `POST /logs` in `internal/http/handler.go`.
4. Extend config in `config/config.go`:
- `auth.enabled`
- `auth.headerName`
- `auth.keys`
- `storage.backend` (`file|minio`)
- `storage.minio.endpoint`
- `storage.minio.bucket`
- `storage.minio.accessKey`
- `storage.minio.secretKey`
- `storage.minio.useSSL`
- `storage.minio.logsPrefix`
- `storage.minio.analyticsPrefix`
5. Keep `repo.Repository` interface unchanged (`SaveBatch` only).
6. Add MinIO repository implementation in `repo/minio_repo.go`.
7. Add build metadata exposure (`version`, `commit`, `buildDate`) for `/version` and release tagging.

---

## Step-by-Step Manual Execution

## Step 0: Freeze Current Baseline
1. Stay on `chore/prod-lab-bootstrap`.
2. Commit the existing `deploy/` files first so infra bootstrap has a checkpoint.
3. Run baseline checks:
```bash
go test ./... -count=1
go test -race ./service ./internal/http ./cmd/bootstrap ./internal/queue
go vet ./...
```

## Step 1: Run Pinned Infra Bootstrap Files
1. Execute Step 2 doc: `deploy/lab/step2.md`.
2. Execute Step 3 doc: `deploy/lab/step3.md`.
3. Before Step 4, set a strong value for `stringData.root-password` in `deploy/minio/minio-lab.yaml`.
4. Execute Step 4 doc: `deploy/lab/step4.md`.
5. Validate cluster:
```bash
kubectl get pods -A
kubectl get pvc -A
kubectl get kafka -n kafka
kubectl get kafkatopic -n kafka
```

## Step 2: Add Build Metadata Plumbing
1. Edit `cmd/main.go`.
2. Add package vars:
- `Version = "dev"`
- `Commit = "none"`
- `BuildDate = "unknown"`
3. Create `BuildInfo` struct in `cmd/bootstrap/app.go` or a small new package and pass metadata into app wiring.
4. Ensure metadata is available to `/version`.
5. Update tests in `cmd/bootstrap/app_test.go` for `/version` response.

## Step 3: Implement `/ready` and `/version` Endpoints
1. Edit `cmd/bootstrap/app.go`.
2. Add `/version` handler returning JSON with:
- `version`
- `commit`
- `buildDate`
- `pipelineMode` (`1` or `2`)
3. Add `/ready` handler with dependency checks:
- file backend: verify writable storage path.
- minio backend: verify bucket reachability.
- version 2 mode: also verify Kafka connectivity (`CheckConnectivity` with short timeout).
4. Return `200` when all checks pass, otherwise `503`.
5. Keep existing `/health` as simple liveness.
6. Add tests in `cmd/bootstrap/app_test.go` for success/failure readiness cases and version payload.

## Step 4: Implement API Key Auth for `POST /logs`
1. Edit `internal/http/handler.go`.
2. Add handler config fields:
- `authEnabled bool`
- `authHeader string`
- `allowedAPIKeys map[string]struct{}`
3. Add builder method (for example `WithAPIKeyAuth(enabled, header, keys)`).
4. In `handleLogs`, validate key before reading body.
5. Return `401` on missing/invalid key.
6. Do not require auth for `/health`, `/ready`, `/metrics`, `/version`.
7. Add table-driven tests in `internal/http/handler_test.go`:
- auth disabled accepts.
- auth enabled valid key accepts.
- missing key rejected.
- wrong key rejected.
- custom header name works.

## Step 5: Extend Config Schema and Defaults
1. Edit `config/config.go`.
2. Add `AuthSettings`, `StorageBackend`, `MinIOSettings` structs.
3. Extend `Config` struct with `Auth` and MinIO fields under `Storage`.
4. Apply safe defaults:
- `auth.enabled = false`
- `auth.headerName = "X-API-Key"`
- `storage.backend = "file"`
- MinIO prefixes default to `logs` and `analytics`
5. Update YAML examples in `config/examples/*` for new keys (keep backward compatibility).
6. Add/extend tests in `config/config_test.go` for:
- default values
- parsing custom auth header + keys
- parsing minio backend settings
- invalid backend handling.

## Step 6: Consumer Shutdown Safety Fix
1. Edit `service/consumer.go`.
2. Remove unbounded forced flush behavior.
3. Ensure both persist and commit operations are bounded by timeout during shutdown.
4. Keep DLQ write path for persist failures.
5. Add table-driven tests in `service/consumer_service_test.go`:
- forced close returns quickly when repo blocks.
- commit path timeout does not hang.
- normal flush behavior unchanged.

## Step 7: Add MinIO Repository
1. Add dependency `github.com/minio/minio-go/v7` in `go.mod`.
2. Create `repo/minio_repo.go`.
3. Implement `SaveBatch(ctx, records)` with hour-grouped NDJSON keys matching file layout:
- `logs/<YYYY-MM-DD>/<HH>.log.json`
4. Use per-object lock map to avoid concurrent append races in-process.
5. Add readiness helper method on MinIO repo (bucket exists/reachable).
6. Add tests in `repo/minio_repo_test.go` with mocked client interfaces:
- grouped object key behavior
- append semantics
- timeout/cancel handling
- empty batch no-op.

## Step 8: Wire Backend Selection in Bootstrap
1. Edit `cmd/bootstrap/app.go`.
2. Instantiate repository by `storage.backend`:
- `file` -> existing `repo.NewFileRepo`.
- `minio` -> `repo.NewMinioRepo`.
3. Wire auth settings into HTTP handler config.
4. Wire readiness checks according to selected backend + pipeline version.
5. Extend app bootstrap tests for:
- file backend startup.
- minio backend startup.
- invalid backend fails fast.

## Step 9: Create Helm Chart + Env Values
1. Add chart at `deploy/helm/logforge`.
2. Required templates:
- `deployment.yaml`
- `service.yaml`
- `ingress.yaml`
- `configmap.yaml`
- `secret.yaml`
- `serviceaccount.yaml`
- `pdb.yaml`
- `networkpolicy.yaml`
3. Enforce:
- liveness `/health`
- readiness `/ready`
- non-root runtime
- read-only root fs
- dropped capabilities
- resource requests `100m/256Mi`, limits `500m/512Mi`
4. Add:
- `deploy/env/staging/values.yaml`
- `deploy/env/production/values.yaml`
5. Include env differences:
- image tag
- replica count
- secret refs
- auth keys ref
- backend settings.

## Step 10: Add Argo CD Application Manifests
1. Add `deploy/argocd/logforge-staging.yaml`:
- auto-sync enabled
- self-heal enabled
- target namespace `staging`
2. Add `deploy/argocd/logforge-production.yaml`:
- manual sync
- target namespace `production`
3. Validate manifests with:
```bash
kubectl apply --dry-run=client -f deploy/argocd/logforge-staging.yaml
kubectl apply --dry-run=client -f deploy/argocd/logforge-production.yaml
```

## Step 11: Add GitHub Workflows
1. Add `.github/workflows/ci.yml` for PRs:
- `go test ./...`
- `go test -race` critical packages
- `go vet ./...`
- static analysis
- Docker build
- Helm lint/template
2. Add `.github/workflows/release.yml` for `v*` tags:
- build image
- ldflags inject metadata
- push to GHCR
- update staging values image tag
3. Add `.github/workflows/promote-production.yml` manual dispatch:
- input `version`
- update production values image tag
- create PR for approval.

## Step 12: Secrets and Repo Settings
1. GitHub:
- enable Actions
- protect `main`
- require CI checks + review
2. GHCR:
- ensure workflow has package write perms
3. Cluster secrets:
- API key secret in `staging`, `production`
- MinIO creds secret
- GHCR image pull secret in both namespaces.

## Step 13: Release, Promote, Rollback Run
1. Tag first release:
```bash
git tag v0.1.0
git push origin v0.1.0
```
2. Verify staging auto-deploy in Argo.
3. Smoke test:
- `GET /health`
- `GET /ready`
- `GET /version`
- authenticated `POST /logs`
4. Promote to production via workflow.
5. Rollback drill:
- change prod image tag to previous
- sync app
- verify `/version` reverted.

---

## Test Cases and Scenarios (Completion Gate)
1. Unit:
- config defaults/parsing for new auth/storage fields.
- API key auth matrix.
- `/ready` pass/fail for file/minio and v1/v2.
- `/version` metadata shape/values.
- consumer forced shutdown timeout.
- minio repo batch grouping/append.
2. Integration:
- ingest -> Kafka -> persistence -> aggregation path.
- readiness fails when Kafka unavailable in v2.
- invalid API key returns `401`.
3. Deployment:
- Helm template render success for both env files.
- Argo staging auto-sync applies tagged image.
- production manual promotion uses exact tag.
4. Operability:
- rollback to previous tag within 10 minutes.
- no shutdown hang during rolling restart.

---

## Explicit Assumptions and Defaults
1. You will run this in single-node `k3d` lab mode, not HA production.
2. `deploy/lab/versions.env` is the source of truth for pinned versions.
3. Kafka CR validation requires Strimzi CRDs first.
4. MinIO credentials are provided via Kubernetes secret, not hardcoded.
5. Aggregation behavior must remain functionally equivalent after backend wiring.
6. Existing table-driven test style remains mandatory for new tests.
