#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${LOGFORGE_BASE_URL:-}"
API_KEY="${LOGFORGE_API_KEY:-}"

if [[ -z "${BASE_URL}" ]]; then
  echo "LOGFORGE_BASE_URL is required"
  exit 1
fi

echo "[1/4] GET /health"
curl -fsS "${BASE_URL}/health" >/tmp/logforge-health.out
cat /tmp/logforge-health.out

echo "[2/4] GET /ready"
curl -fsS "${BASE_URL}/ready" >/tmp/logforge-ready.out
cat /tmp/logforge-ready.out

echo "[3/4] GET /version"
curl -fsS "${BASE_URL}/version" >/tmp/logforge-version.out
cat /tmp/logforge-version.out

echo "[4/4] POST /logs"
if [[ -z "${API_KEY}" ]]; then
  echo "LOGFORGE_API_KEY not set; skipping authenticated /logs test"
  exit 0
fi

cat >/tmp/logforge-smoke-payload.json <<'JSON'
[
  {
    "timestamp": "2026-01-01T00:00:00Z",
    "path": "/smoke",
    "userAgent": "lab-smoke"
  }
]
JSON

curl -fsS -X POST "${BASE_URL}/logs" \
  -H "Content-Type: application/json" \
  -H "X-API-Key: ${API_KEY}" \
  --data-binary @/tmp/logforge-smoke-payload.json \
  -o /tmp/logforge-post.out -D /tmp/logforge-post.headers

status_code="$(awk 'toupper($1) ~ /^HTTP\// { code=$2 } END { print code }' /tmp/logforge-post.headers)"
echo "POST /logs status: ${status_code}"
if [[ "${status_code}" != "202" ]]; then
  echo "unexpected /logs status; expected 202"
  cat /tmp/logforge-post.headers
  exit 1
fi

echo "smoke test completed"
