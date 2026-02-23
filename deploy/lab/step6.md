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

Ingress hostnames are enabled in env values for all three environments and for Argo CD UI. Add local host mappings once:

Linux/macOS (including WSL terminal access):

```bash
echo "127.0.0.1 dev.logforge.local staging.logforge.local production.logforge.local argocd.logforge.local" | sudo tee -a /etc/hosts
```

Windows browser access (when cluster runs in WSL): run PowerShell as Administrator:

```powershell
Add-Content -Path "$env:WINDIR\System32\drivers\etc\hosts" -Value "`n127.0.0.1 dev.logforge.local staging.logforge.local production.logforge.local argocd.logforge.local"
```

Then run staging checks:

```bash
curl -fsS http://staging.logforge.local:8080/health
curl -fsS http://staging.logforge.local:8080/ready
curl -fsS http://staging.logforge.local:8080/version

export LOGFORGE_BASE_URL="http://staging.logforge.local:8080"
export LOGFORGE_API_KEY="CHANGE_ME_STAGING_API_KEY"
bash deploy/lab/smoke-test.sh
```

Notes:

- k3d maps host port `8080` to ingress-nginx service port `80` via `deploy/k3d/config.yaml`.
- If you use WSL + Windows browser, Windows needs its own hosts-file entry; `/etc/hosts` in WSL is not used by Windows apps.
- If you cannot modify `/etc/hosts`, use host-header routing directly:

```bash
curl -fsS -H 'Host: staging.logforge.local' http://127.0.0.1:8080/health
```

Environment URLs:

- Local ingress URLs:
  - argocd: `http://argocd.logforge.local:8080`
  - dev: `http://dev.logforge.local:8080`
  - staging: `http://staging.logforge.local:8080`
  - production: `http://production.logforge.local:8080`
- In-cluster service DNS:
  - dev: `http://logforge-dev.dev.svc.cluster.local:8082`
  - staging: `http://logforge-staging.staging.svc.cluster.local:8082`
  - production: `http://logforge-production.production.svc.cluster.local:8082`
- Port-forward fallback:
  - dev: `kubectl -n dev port-forward svc/logforge-dev 28081:8082` then open `http://127.0.0.1:28081`
  - staging: `kubectl -n staging port-forward svc/logforge-staging 28082:8082` then open `http://127.0.0.1:28082`
  - production: `kubectl -n production port-forward svc/logforge-production 28083:8082` then open `http://127.0.0.1:28083`

## 4) Production preflight (MinIO + buckets)

Before promoting, verify MinIO is healthy and both v2 buckets exist:

```bash
kubectl -n storage get pods -l app=minio

export MINIO_USER="$(kubectl -n storage get secret minio-root-creds -o jsonpath='{.data.root-user}' | base64 -d)"
export MINIO_PASS="$(kubectl -n storage get secret minio-root-creds -o jsonpath='{.data.root-password}' | base64 -d)"

# Verify runtime secrets use the same MinIO credentials in all environments.
for ns in dev staging production; do
  case "$ns" in
    dev) s=logforge-dev-runtime; ak=dev_minio_access_key; sk=dev_minio_secret_key;;
    staging) s=logforge-staging-runtime; ak=minio_access_key; sk=minio_secret_key;;
    production) s=logforge-production-runtime; ak=production_minio_access_key; sk=production_minio_secret_key;;
  esac
  ACCESS="$(kubectl -n "$ns" get secret "$s" -o jsonpath="{.data.$ak}" | base64 -d)"
  SECRET="$(kubectl -n "$ns" get secret "$s" -o jsonpath="{.data.$sk}" | base64 -d)"
  [ "$ACCESS" = "$MINIO_USER" ] && [ "$SECRET" = "$MINIO_PASS" ] && echo "$ns: minio creds match" || echo "$ns: minio creds mismatch"
done

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
