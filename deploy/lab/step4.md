# Step 4 (pinned and file-based)

Everything here is pinned to explicit versions from `deploy/lab/versions.env`.

## 0) Load pinned  (No need to do this if step2 load is done)

```bash
set -a
source deploy/lab/versions.env
set +a
```

## 1) Install ingress-nginx (pinned chart version + local values)

```bash
helm repo add ingress-nginx https://kubernetes.github.io/ingress-nginx
helm repo update

helm upgrade --install ingress-nginx ingress-nginx \
  --repo https://kubernetes.github.io/ingress-nginx \
  --version "${INGRESS_NGINX_CHART_VERSION}" \
  -n ingress-nginx \
  --create-namespace \
  -f deploy/ingress-nginx/values-lab.yaml
```

## 2) Install Argo CD (pinned release manifest via kustomize)

```bash
kubectl apply -n argocd --server-side --force-conflicts -k deploy/argocd/bootstrap
kubectl -n argocd rollout status deploy/argocd-server --timeout=300s
```

## 3) Install Strimzi operator (pinned release manifest via kustomize)

```bash
kubectl apply -k deploy/strimzi/bootstrap
kubectl -n kafka rollout status deployment/strimzi-cluster-operator --timeout=300s
```

## 4) Apply Kafka cluster manifest (single broker, 8Gi PVC, 24h retention)

```bash
kubectl apply -n kafka -f deploy/kafka/lab-single-broker.yaml
kubectl wait kafka/my-cluster --for=condition=Ready --timeout=600s -n kafka
```

### Pitfall: Strimzi leader-election RBAC

`deploy/strimzi/bootstrap/kustomization.yaml` includes patches that force Strimzi binding subjects to namespace `kafka`.

If the wait command times out and operator logs show `leases.coordination.k8s.io ... forbidden`, re-apply Strimzi bootstrap and restart the operator:

```bash
kubectl apply -k deploy/strimzi/bootstrap
kubectl -n kafka rollout restart deployment/strimzi-cluster-operator
kubectl -n kafka rollout status deployment/strimzi-cluster-operator --timeout=300s
kubectl wait kafka/my-cluster --for=condition=Ready --timeout=600s -n kafka
```

## 5) Create `logs` topic

```bash
kubectl apply -n kafka -f deploy/kafka/logs-topic.yaml
```

## 6) Install MinIO from manifest (pinned image + 10Gi PVC)

Before applying, set a strong password in `deploy/minio/minio-lab.yaml`.

```bash
kubectl apply -f deploy/minio/minio-lab.yaml
kubectl -n storage rollout status statefulset/minio --timeout=300s
```

## 7) Install monitoring (pinned chart version + local values)

```bash
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm repo update

helm upgrade --install monitoring prometheus-community/kube-prometheus-stack \
  --version "${KUBE_PROM_STACK_CHART_VERSION}" \
  -n monitoring \
  --create-namespace \
  -f deploy/monitoring/values-lab.yaml
```

## 8) Verify

```bash
kubectl get pods -A
kubectl get pvc -A
```
