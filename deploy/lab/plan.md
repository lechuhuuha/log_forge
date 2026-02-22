# Manual Implementation Plan (Dev + Staging/Production Parity)

## Summary
You will run lab delivery in this order:
1. Bootstrap local cluster infra from pinned `deploy/lab/step2.md` to `deploy/lab/step4.md`.
2. Deploy three application environments with Argo CD:
   - `dev`: version 1 + file backend.
   - `staging`: version 2 + Kafka + MinIO.
   - `production`: version 2 + Kafka + MinIO.
3. Keep staging runtime-equivalent to production to reduce promotion risk.
4. Use release workflow to bump `dev` and `staging` image tags together.
5. Use manual production promotion workflow for controlled rollout/rollback.

Current repo truth:
- Helm chart is at `deploy/helm/logforge`.
- Env values are at `deploy/env/dev/values.yaml`, `deploy/env/staging/values.yaml`, and `deploy/env/production/values.yaml`.
- Argo applications are at:
  - `deploy/argocd/logforge-dev.yaml`
  - `deploy/argocd/logforge-staging.yaml`
  - `deploy/argocd/logforge-production.yaml`

---

## Environment Policy (Source of Truth)
1. `dev`
- pipeline version: `1`
- storage backend: `file`
- purpose: fast iteration and basic API validation
- sync policy: Argo auto-sync

2. `staging`
- pipeline version: `2`
- storage backend: `minio`
- Kafka enabled
- sync policy: Argo auto-sync

3. `production`
- pipeline version: `2`
- storage backend: `minio`
- Kafka enabled
- sync policy: Argo manual sync

4. Staging/production parity rule
- Must match runtime topology and behavioral config.
- Allowed differences only:
  - namespace and secret names/keys
  - MinIO bucket names (`logforge-staging`, `logforge-production`)
  - image tag during promotion window

---

## Step-by-Step Manual Execution

## Step 1: Bootstrap Cluster and Platform Components
1. Execute:
- `deploy/lab/step2.md`
- `deploy/lab/step3.md`
- `deploy/lab/step4.md`
2. Validate core infra:
```bash
kubectl get nodes -o wide
kubectl get pods -A
kubectl get pvc -A
kubectl get kafka -n kafka
kubectl get kafkatopic -n kafka
```

## Step 2: Apply Argo Applications
1. Apply all application manifests:
```bash
kubectl apply -f deploy/argocd/logforge-dev.yaml
kubectl apply -f deploy/argocd/logforge-staging.yaml
kubectl apply -f deploy/argocd/logforge-production.yaml
```
2. Verify:
```bash
kubectl -n argocd get application logforge-dev logforge-staging logforge-production
```

## Step 3: Configure Runtime Secrets and Pull Secrets
1. Follow `deploy/lab/step5.md`.
2. Must exist in `dev`, `staging`, and `production`:
- runtime secret (`logforge-<env>-runtime`)
- GHCR pull secret (`ghcr-pull`)

## Step 4: MinIO Readiness and Bucket Preflight
1. Ensure MinIO pod is healthy:
```bash
kubectl -n storage get pods -l app=minio
```
2. Ensure buckets exist:
- `logforge-staging`
- `logforge-production`
3. Use `deploy/lab/step6.md` production preflight command if buckets are missing.

## Step 5: Release Flow (Tag Push)
1. Tag and push:
```bash
git tag v0.1.0
git push origin v0.1.0
```
2. Expected release workflow behavior:
- build/push image to GHCR
- create PR that updates image tags in:
  - `deploy/env/dev/values.yaml`
  - `deploy/env/staging/values.yaml`

## Step 6: Merge Dev+Staging Tag Bump PR
1. Review and merge the PR created by release workflow.
2. Verify Argo auto-sync:
```bash
kubectl -n argocd get application logforge-dev logforge-staging
```

## Step 7: Smoke Test Staging
1. Use port-forward workflow from `deploy/lab/step6.md`.
2. Verify:
- `GET /health`
- `GET /ready`
- `GET /version`
- authenticated `POST /logs`

## Step 8: Promote Production (Manual Gate)
1. Trigger promotion workflow:
```bash
gh workflow run promote-production.yml -f version=v0.1.0
```
2. Merge production promotion PR.
3. Perform manual sync for `logforge-production` in Argo (policy-controlled).

## Step 9: Rollback Drill
1. Trigger promotion workflow using previous version (example):
```bash
gh workflow run promote-production.yml -f version=v0.0.9
```
2. Merge rollback PR.
3. Sync production and verify `/version` reverted.

---

## Validation Gate
1. Helm render:
```bash
helm template logforge-dev deploy/helm/logforge -f deploy/env/dev/values.yaml >/tmp/logforge-dev.yaml
helm template logforge-staging deploy/helm/logforge -f deploy/env/staging/values.yaml >/tmp/logforge-staging.yaml
helm template logforge-production deploy/helm/logforge -f deploy/env/production/values.yaml >/tmp/logforge-production.yaml
```

2. Argo manifest dry-run:
```bash
kubectl apply --dry-run=client -f deploy/argocd/logforge-dev.yaml
kubectl apply --dry-run=client -f deploy/argocd/logforge-staging.yaml
kubectl apply --dry-run=client -f deploy/argocd/logforge-production.yaml
```

3. Application status:
```bash
kubectl -n argocd get application logforge-dev logforge-staging logforge-production
```

4. Endpoint checks (port-forward based):
- dev `/health`, `/ready`, `/version`
- staging `/health`, `/ready`, `/version`

5. Workflow checks:
- release workflow updates dev+staging values in one PR
- production promote workflow updates only production values

6. MinIO checks:
- MinIO pod ready
- `logforge-staging` and `logforge-production` buckets exist

---

## Explicit Assumptions and Defaults
1. `dev` remains version 1 + file backend.
2. `staging` and `production` remain version 2 + Kafka + MinIO.
3. Staging/production parity excludes only env-specific identifiers and temporary promotion tag drift.
4. Local endpoint access standard is `kubectl port-forward` unless ingress is explicitly introduced.
5. `deploy/lab/versions.env` is the source of pinned infra versions.
