#!/usr/bin/env bash
set -euo pipefail

readonly docker_command="${DOCKER_COMMAND:-docker}"
readonly required_containers=(nofx-trading nofx-frontend)

for container_name in "${required_containers[@]}"; do
  running_state="$("${docker_command}" inspect --format '{{.State.Running}}' "${container_name}")"
  if [[ "${running_state}" != "true" ]]; then
    printf 'Container is not running: %s\n' "${container_name}" >&2
    exit 1
  fi
done

backend_health="$("${docker_command}" inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' nofx-trading)"
frontend_health="$("${docker_command}" inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' nofx-frontend)"

printf 'backend_health=%s\n' "${backend_health}"
printf 'frontend_health=%s\n' "${frontend_health}"

if [[ "${backend_health}" != "healthy" ]]; then
  printf '%s\n' 'nofx backend has not become healthy.' >&2
  exit 1
fi

if [[ "${frontend_health}" != "healthy" ]]; then
  printf '%s\n' 'nofx frontend has not become healthy.' >&2
  exit 1
fi

curl --fail --silent --show-error --max-time 10 http://127.0.0.1:3100/health >/dev/null

