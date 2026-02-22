# Step 2 (tooling install, pinned)

## 0) Load pinned versions
You want everything inside .env exported
But you don’t want the rest of your script auto-exported
So you enable temporarily, then disable
```bash
set -a
source deploy/lab/versions.env
set +a
```

## 1) Base packages

```bash
sudo apt-get update
sudo apt-get install -y curl ca-certificates jq
```

## 2) Install k3d

```bash
curl -s https://raw.githubusercontent.com/k3d-io/k3d/main/install.sh | TAG="${K3D_VERSION}" bash
```

## 3) Install Helm

```bash
curl -fsSL -o get_helm.sh https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3
chmod 700 get_helm.sh
DESIRED_VERSION="${HELM_VERSION}" ./get_helm.sh
rm -f get_helm.sh
```

## 4) Install Argo CD CLI (optional but useful)

```bash
curl -sSL -o argocd-linux-amd64 \
  "https://github.com/argoproj/argo-cd/releases/download/${ARGOCD_VERSION}/argocd-linux-amd64"
sudo install -m 555 argocd-linux-amd64 /usr/local/bin/argocd
rm -f argocd-linux-amd64
```

## 5) Verify versions

```bash
docker --version
kubectl version --client
k3d version
helm version --short
argocd version --client || true
```
