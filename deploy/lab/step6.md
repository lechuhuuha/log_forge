# Step 6 (release, promote, rollback drill)

This step maps to `deploy/lab/plan.md` Step 13.

## 0) Preconditions

- Step 10 Argo applications are applied.
- Step 12 secrets are created in `staging` and `production`.
- CI/release workflows from Step 11 are enabled.

## 1) Create and push release tag

```bash
git tag v0.1.0
git push origin v0.1.0
```

Expected result:

- `release.yml` runs on the tag.
- GHCR image is published with tag `v0.1.0`.
- Workflow opens a PR that bumps `deploy/env/staging/values.yaml` image tag.

## 2) Merge staging tag bump PR

After release workflow opens the PR:

- review and merge the PR into default branch.
- Argo `logforge-staging` should auto-sync.

Check Argo status:

```bash
kubectl -n argocd get application logforge-staging
kubectl -n argocd describe application logforge-staging
```

## 3) Smoke test staging

Set staging endpoint and API key, then run smoke checks:

```bash
export LOGFORGE_BASE_URL="http://staging.example.com"
export LOGFORGE_API_KEY="CHANGE_ME_STAGING_API_KEY"
bash deploy/lab/smoke-test.sh
```

## 4) Promote to production via workflow

Use GitHub UI or CLI.

CLI example:

```bash
gh workflow run promote-production.yml -f version=v0.1.0
```

Expected result:

- workflow opens PR updating `deploy/env/production/values.yaml` image tag.
- PR approval and merge triggers Argo `logforge-production` sync (manual sync policy means sync must be approved/executed by operator policy).

Check production app status:

```bash
kubectl -n argocd get application logforge-production
kubectl -n argocd describe application logforge-production
```

## 5) Rollback drill

Pick previous version tag (example `v0.0.9`) and update production values via PR:

```bash
gh workflow run promote-production.yml -f version=v0.0.9
```

Merge rollback PR, then verify:

- Argo sync completes for `logforge-production`.
- `/version` reports rollback tag.

Example manual check:

```bash
curl -fsS "${LOGFORGE_BASE_URL}/version"
```

## 6) Evidence checklist

Capture and store:

- release workflow run URL and image digest.
- staging deploy PR and sync status.
- production promote PR and sync status.
- rollback PR and `/version` output before/after rollback.
