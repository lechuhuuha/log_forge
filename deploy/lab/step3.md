# Step 3 (cluster + namespaces)

## 0) Load pinned  (No need to do this if step2 load is done)

```bash
set -a
source deploy/lab/versions.env
set +a
```

## 1) Create k3d cluster (config-driven)

Cluster name, node counts, image, k3s args, and ports are defined in `deploy/k3d/config.yaml`.

```bash
k3d cluster create --config deploy/k3d/config.yaml
```

## 2) Verify cluster

```bash
kubectl config current-context
kubectl get nodes -o wide
```

## 3) Apply namespaces declaratively

```bash
kubectl apply -f deploy/kubectl/namespaces.yaml
kubectl get ns argocd ingress-nginx kafka storage monitoring staging production
```
