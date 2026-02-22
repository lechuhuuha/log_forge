# Step 5 (secrets and repo settings for staging/production)

This step maps to `deploy/lab/plan.md` Step 12.

## 0) Preconditions

- Argo CD and namespaces are already installed.
- Helm values from `deploy/env/staging/values.yaml` and `deploy/env/production/values.yaml` are in use.

## 1) GitHub repository settings checklist

Manually verify in repository settings:

- Actions are enabled.
- Default branch protection is enabled for `main`.
- Required status checks include CI.
- Pull request review is required before merge.

## 2) GHCR permissions checklist

The workflows already request:

- `packages: write` for release workflow.
- `contents: write` and `pull-requests: write` for update PR workflows.

Make sure repository-level policy allows `GITHUB_TOKEN` to write packages.

## 3) Create application runtime secrets

Option A (manifest template):

```bash
kubectl apply -f deploy/kubectl/logforge-runtime-secrets.example.yaml
```

Update placeholder values before using in non-lab environments.

Option B (CLI, no file edits):

```bash
kubectl -n staging create secret generic logforge-staging-runtime \
  --from-literal=auth_keys_json='["CHANGE_ME_STAGING_API_KEY"]' \
  --from-literal=minio_access_key='CHANGE_ME_STAGING_MINIO_ACCESS_KEY' \
  --from-literal=minio_secret_key='CHANGE_ME_STAGING_MINIO_SECRET_KEY' \
  --dry-run=client -o yaml | kubectl apply -f -

kubectl -n production create secret generic logforge-production-runtime \
  --from-literal=production_auth_keys_json='["CHANGE_ME_PRODUCTION_API_KEY"]' \
  --from-literal=production_minio_access_key='CHANGE_ME_PRODUCTION_MINIO_ACCESS_KEY' \
  --from-literal=production_minio_secret_key='CHANGE_ME_PRODUCTION_MINIO_SECRET_KEY' \
  --dry-run=client -o yaml | kubectl apply -f -
```

## 4) Create GHCR image pull secret in both namespaces

Set your GHCR credentials in shell first:

```bash
export GHCR_USERNAME="CHANGE_ME"
export GHCR_TOKEN="CHANGE_ME"
export GHCR_EMAIL="CHANGE_ME@example.com"
```

Then create pull secret `ghcr-pull`:

```bash
kubectl -n staging create secret docker-registry ghcr-pull \
  --docker-server=ghcr.io \
  --docker-username="${GHCR_USERNAME}" \
  --docker-password="${GHCR_TOKEN}" \
  --docker-email="${GHCR_EMAIL}" \
  --dry-run=client -o yaml | kubectl apply -f -

kubectl -n production create secret docker-registry ghcr-pull \
  --docker-server=ghcr.io \
  --docker-username="${GHCR_USERNAME}" \
  --docker-password="${GHCR_TOKEN}" \
  --docker-email="${GHCR_EMAIL}" \
  --dry-run=client -o yaml | kubectl apply -f -
```

## 5) Verify

```bash
kubectl get secret -n staging logforge-staging-runtime ghcr-pull
kubectl get secret -n production logforge-production-runtime ghcr-pull
```
