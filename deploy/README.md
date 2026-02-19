# Deploy Folder Review and Plan

## Current Repository Status

There is currently **no existing deployment manifest set** (for example Helm charts or raw Kubernetes YAML) in this repo.

What exists today:

- `docker-compose.yaml` for local infrastructure and app containers
- `Makefile` targets for easy stack spin up/tear down (`stack-up`, `stack-down`, `infra-up`, `infra-down`)
- runtime configs under `config/examples/*`

## Does the current setup fit local development?

**Yes (good baseline).**

- Compose gives quick startup and teardown.
- Local config uses `localhost:19092` Kafka and local directories.
- This is suitable for developer iteration and smoke testing.

## Does the current setup fit production?

**Not by itself.**

Gaps for production readiness:

1. No Kubernetes deployment resources (Deployments, Services, HPA/KEDA, PDBs, network policies).
2. No production secret/config strategy (secret manager integration, sealed/external secrets).
3. No object storage deployment wiring (S3 bucket config/credentials path).
4. No release packaging for cluster deployment (Helm/Kustomize or GitOps structure).

## Recommended deploy folder structure

A practical structure that fits both production and local development:

```text
deploy/
  README.md
  k8s/
    base/
      namespace.yaml
      ingestion-api-deployment.yaml
      consumer-writer-deployment.yaml
      aggregation-worker-cronjob.yaml
      services.yaml
      serviceaccounts.yaml
      pdb.yaml
      networkpolicy.yaml
    overlays/
      dev/
      staging/
      prod/
  local/
    compose/
      docker-compose.minio.yaml
```

## Suggested execution order

1. Keep Docker Compose as-is for local speed.
2. Add `deploy/k8s/base` manifests for the three workloads:
   - ingestion API
   - consumer writer
   - aggregation worker
3. Add autoscaling:
   - HPA for API
   - lag-driven scaler (KEDA) for consumer
4. Add production overlays for env-specific replicas/resources/secrets.
5. Add optional local MinIO compose override for S3 parity tests.

## Bottom line

- **Local development:** current setup is already good.
- **Production deployment:** needs Kubernetes manifests/packaging in this `deploy/` folder before it is production-fit.
