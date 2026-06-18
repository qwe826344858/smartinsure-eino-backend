#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

CONTAINER_NAME="${CONTAINER_NAME:-smartinsure-eino-backend}"
IMAGE="${IMAGE:-smartinsure-eino-backend:latest}"
HOST_PORT="${HOST_PORT:-34567}"
CONTAINER_PORT="${CONTAINER_PORT:-34567}"
RESTART_POLICY="${RESTART_POLICY:-unless-stopped}"
HTTP_ADDR="${HTTP_ADDR:-0.0.0.0:${CONTAINER_PORT}}"
TARGETOS="${TARGETOS:-linux}"
TARGETARCH="${TARGETARCH:-amd64}"
BINARY_PATH="${BINARY_PATH:-${ROOT_DIR}/build/server}"
CONFIGS_PATH="${CONFIGS_PATH:-${ROOT_DIR}/configs}"
LOGS_PATH="${LOGS_PATH:-${ROOT_DIR}/logs}"
HEALTH_URL="${HEALTH_URL:-http://127.0.0.1:${HOST_PORT}/api/healthz}"
HEALTH_TIMEOUT_SECONDS="${HEALTH_TIMEOUT_SECONDS:-30}"
KEEP_OLD_CONTAINER="${KEEP_OLD_CONTAINER:-0}"

mkdir -p "$(dirname "${BINARY_PATH}")"
mkdir -p "${LOGS_PATH}"
chmod 0777 "${LOGS_PATH}"

echo "Building server binary: ${BINARY_PATH}"
(
	cd "${ROOT_DIR}"
	CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" \
		go build -buildvcs=false -trimpath -ldflags="-s -w" -o "${BINARY_PATH}" ./cmd/server
)
chmod 755 "${BINARY_PATH}"

env_file="$(mktemp)"
cleanup() {
	rm -f "${env_file}"
}
trap cleanup EXIT
chmod 600 "${env_file}"

if docker container inspect "${CONTAINER_NAME}" >/dev/null 2>&1; then
	docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "${CONTAINER_NAME}" |
		awk -F= '
			$1 == "LLM_API_KEY" {next}
			$1 == "HTTP_ADDR" {next}
			$1 == "MYSQL_DSN" {next}
			$1 == "REDIS_URL" {next}
			$1 == "DATABASE_URL" {next}
			$1 == "RAG_CONFIG_PATH" {next}
			$1 == "LOG_CONFIG_PATH" {next}
			$1 == "LOG_FILE_PATH" {next}
			$1 == "LOG_TO_CONSOLE" {next}
			$1 ~ /^EMBEDDING_/ {next}
			$1 ~ /^PRODUCT_DETAIL_RAG_/ {next}
			$1 ~ /^RAG_SEARCH_/ {next}
			{print}
		' >"${env_file}"
fi

printf 'HTTP_ADDR=%s\n' "${HTTP_ADDR}" >>"${env_file}"
if [[ -n "${LLM_API_KEY:-}" ]]; then
	printf 'LLM_API_KEY=%s\n' "${LLM_API_KEY}" >>"${env_file}"
fi

append_env_override() {
	local key="$1"
	local value="${!key:-}"
	if [[ -z "${value}" ]]; then
		return
	fi
	local filtered
	filtered="$(mktemp)"
	grep -v "^${key}=" "${env_file}" >"${filtered}" || true
	cat "${filtered}" >"${env_file}"
	rm -f "${filtered}"
	printf '%s=%s\n' "${key}" "${value}" >>"${env_file}"
}

append_env_override "RAG_CONFIG_PATH"
append_env_override "LOG_CONFIG_PATH"
append_env_override "LOG_FILE_PATH"
append_env_override "LOG_TO_CONSOLE"
append_env_override "MYSQL_DSN"
append_env_override "REDIS_URL"
append_env_override "DATABASE_URL"

for key in $(compgen -e | awk '/^(EMBEDDING_|PRODUCT_DETAIL_RAG_|RAG_SEARCH_)/'); do
	append_env_override "${key}"
done

old_container=""
if docker container inspect "${CONTAINER_NAME}" >/dev/null 2>&1; then
	old_container="${CONTAINER_NAME}-old-$(date +%Y%m%d%H%M%S)"
	echo "Stopping current container: ${CONTAINER_NAME}"
	docker stop "${CONTAINER_NAME}" >/dev/null
	docker rename "${CONTAINER_NAME}" "${old_container}"
fi

rollback() {
	echo "Restart failed; rolling back container." >&2
	docker rm -f "${CONTAINER_NAME}" >/dev/null 2>&1 || true
	if [[ -n "${old_container}" ]] && docker container inspect "${old_container}" >/dev/null 2>&1; then
		docker rename "${old_container}" "${CONTAINER_NAME}"
		docker start "${CONTAINER_NAME}" >/dev/null
	fi
}
trap rollback ERR

echo "Starting ${CONTAINER_NAME} with mounted binary."
docker run -d \
	--name "${CONTAINER_NAME}" \
	--restart "${RESTART_POLICY}" \
	--env-file "${env_file}" \
	-p "${HOST_PORT}:${CONTAINER_PORT}" \
	-v "${BINARY_PATH}:/app/server:ro" \
	-v "${CONFIGS_PATH}:/app/configs:ro" \
	-v "${LOGS_PATH}:/app/logs" \
	"${IMAGE}" >/dev/null

deadline=$((SECONDS + HEALTH_TIMEOUT_SECONDS))
until curl -fsS "${HEALTH_URL}" >/dev/null; do
	if ((SECONDS >= deadline)); then
		echo "Health check timed out: ${HEALTH_URL}" >&2
		exit 1
	fi
	sleep 1
done

trap - ERR
if [[ -n "${old_container}" && "${KEEP_OLD_CONTAINER}" != "1" ]]; then
	docker rm "${old_container}" >/dev/null || true
fi

echo "Container restarted: ${CONTAINER_NAME}"
echo "Mounted binary: ${BINARY_PATH} -> /app/server"
echo "Mounted configs: ${CONFIGS_PATH} -> /app/configs"
echo "Mounted logs: ${LOGS_PATH} -> /app/logs"
echo "Health check: ${HEALTH_URL}"
