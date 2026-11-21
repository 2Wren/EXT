#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
umask 077
mkdir -p state/{ext,cpa-auth,cpa-config,cpa-plugins,panel}
if [[ ! -e .env ]]; then
  {
    printf 'CPA_MANAGEMENT_KEY=%s\n' "$(openssl rand -hex 32)"
    printf 'ADMIN_PASSWORD=%s\n' "$(openssl rand -hex 32)"
    printf 'CPA_EXT_UID=%s\n' "$(id -u)"
    printf 'CPA_EXT_GID=%s\n' "$(id -g)"
  } > .env
fi
if [[ ! -e state/cpa-config/config.yaml ]]; then
  secret="$(sed -n 's/^CPA_MANAGEMENT_KEY=//p' .env)"
  [[ "$secret" =~ ^[a-f0-9]{64}$ ]]
  sed "s/__CPA_MANAGEMENT_KEY__/$secret/" deploy/cpa.example.yaml > state/cpa-config/config.yaml
fi
if [[ ! -f state/panel/management.html ]]; then
  tmp="$(mktemp state/panel/.cpamc.XXXXXX)"
  trap 'rm -f "$tmp"' EXIT
  curl --fail --silent --show-error --location --max-time 60 \
    https://github.com/router-for-me/Cli-Proxy-API-Management-Center/releases/download/v1.28.3/management.html -o "$tmp"
  printf '%s  %s\n' 6d4e005420ee18899ef115a4e68d61f700ddb372844f2317546a0d0aa4553dfc "$tmp" | sha256sum -c -
  mv "$tmp" state/panel/management.html
fi
bash deploy/build-panel-plugin.sh
