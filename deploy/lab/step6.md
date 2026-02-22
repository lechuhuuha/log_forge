# Step 6 (release, promote, rollback drill)

This step maps to `deploy/lab/plan.md` Step 13.

## 0) Preconditions

- Step 10 Argo applications are applied (`logforge-dev`, `logforge-staging`, `logforge-production`).
- Step 12 secrets are created in `dev`, `staging`, and `production`.
- CI/release workflows from Step 11 are enabled.

## 1) Create and push release tag

```bash
git tag v0.1.0
git push origin v0.1.0
```

Expected result:

- `release.yml` runs on the tag.
- GHCR image is published with tag `v0.1.0`.
- Workflow opens a PR that bumps image tags in:
  - `deploy/env/dev/values.yaml`
  - `deploy/env/staging/values.yaml`

## 2) Merge dev+staging tag bump PR

After release workflow opens the PR:

- review and merge the PR into default branch.
- Argo `logforge-dev` and `logforge-staging` should auto-sync.

Check Argo status:

```bash
kubectl -n argocd get application logforge-dev logforge-staging
kubectl -n argocd describe application logforge-dev
kubectl -n argocd describe application logforge-staging
```

## 3) Smoke test staging

Use port-forward in one dedicated terminal:

```bash
kubectl -n staging port-forward svc/logforge-staging 18082:8082
```

In another terminal:

```bash
curl -fsS http://127.0.0.1:18082/health
curl -fsS http://127.0.0.1:18082/ready
curl -fsS http://127.0.0.1:18082/version

export LOGFORGE_BASE_URL="http://127.0.0.1:18082"
export LOGFORGE_API_KEY="CHANGE_ME_STAGING_API_KEY"
bash deploy/lab/smoke-test.sh
```

Notes:

- `http://127.0.0.1:18082` only works while the port-forward process is running.
- If `18082` is busy, use another local port (for example `28082:8082`) and update `LOGFORGE_BASE_URL`.

## 4) Production preflight (MinIO + buckets)

Before promoting, verify MinIO is healthy and both v2 buckets exist:

```bash
kubectl -n storage get pods -l app=minio

export MINIO_USER="$(kubectl -n storage get secret minio-root-creds -o jsonpath='{.data.root-user}' | base64 -d)"
export MINIO_PASS="$(kubectl -n storage get secret minio-root-creds -o jsonpath='{.data.root-password}' | base64 -d)"

kubectl -n storage run minio-bucket-bootstrap --restart=Never \
  --image=minio/mc:RELEASE.2025-08-13T08-35-41Z \
  --env MC_HOST_local="http://${MINIO_USER}:${MINIO_PASS}@minio.storage.svc.cluster.local:9000" \
  --command -- sh -ec 'mc mb -p local/logforge-staging; mc mb -p local/logforge-production; mc ls local/logforge-staging; mc ls local/logforge-production'

kubectl -n storage wait --for=jsonpath='{.status.phase}'=Succeeded pod/minio-bucket-bootstrap --timeout=120s
kubectl -n storage logs minio-bucket-bootstrap
kubectl -n storage delete pod minio-bucket-bootstrap --ignore-not-found
```

## 5) Promote to production via workflow

Use GitHub UI or CLI.

CLI example:

```bash
gh workflow run promote-production.yml -f version=v0.1.0
```

Expected result:

- workflow opens PR updating `deploy/env/production/values.yaml` image tag.
- PR approval and merge prepares production deployment.
- Argo `logforge-production` stays manual-sync by policy and must be synced by operator.

Check production app status:

```bash
kubectl -n argocd get application logforge-production
kubectl -n argocd describe application logforge-production
```

## 6) Rollback drill

Pick previous version tag (example `v0.0.9`) and update production values via PR:

```bash
gh workflow run promote-production.yml -f version=v0.0.9
```

Merge rollback PR, sync production app, then verify:

- Argo sync completes for `logforge-production`.
- `/version` reports rollback tag.

Example manual check:

```bash
curl -fsS "${LOGFORGE_BASE_URL}/version"
```

## 7) Evidence checklist

Capture and store:

- release workflow run URL and image digest.
- dev+staging deploy PR and sync status.
- production promote PR and sync status.
- rollback PR and `/version` output before/after rollback.
