# Step 3 (cluster + namespaces)

## 0) Load pinned versions

```bash
set -a
source deploy/lab/versions.env
set +a
```

## 1) Create k3d cluster (pinned k3s image)

```bash
k3d cluster create logforge-lab \
  --image "rancher/k3s:${K3S_IMAGE_TAG}" \
  --servers 1 \
  --agents 1 \
  --k3s-arg "--disable=traefik@server:0" \
  --port "8080:80@loadbalancer" \
  --port "8443:443@loadbalancer"
```

## 2) Verify cluster

```bash
kubectl config current-context
kubectl get nodes -o wide
```

## 3) Apply namespaces declaratively

```bash
kubectl apply -f deploy/lab/namespaces.yaml
kubectl get ns argocd ingress-nginx kafka storage monitoring staging production
```
