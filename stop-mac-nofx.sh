#!/usr/bin/env bash
set -euo pipefail

readonly project_directory="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
readonly docker_command="${DOCKER_COMMAND:-docker}"

cd "${project_directory}"
"${docker_command}" compose \
  --env-file .env.mac \
  -f docker-compose.mac.yml \
  down
