#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
umask 077

fail() { printf '%s\n' "$1" >&2; exit 1; }

[[ "$(uname -s)" == Linux ]] || fail '此方式需要 Linux Docker Engine。'
command -v docker >/dev/null || fail '请先安装 Docker Engine 和 Docker Compose。'
docker_cmd=(docker)
if ! docker info >/dev/null 2>&1; then
  docker_cmd=(sudo docker)
  "${docker_cmd[@]}" info >/dev/null
fi
compose=("${docker_cmd[@]}" compose --env-file .env.attach -f compose.attach.yaml)
"${compose[@]}" version >/dev/null

if [[ -e .env.attach ]]; then
  printf '%s\n' '使用现有 .env.attach 启动或更新 EXT。'
  "${compose[@]}" up -d --build
  exit
fi

for dependency in curl openssl; do
  command -v "$dependency" >/dev/null || fail "请先安装 $dependency。"
done

read -r -p '现有 CPA 地址 [http://127.0.0.1:8317] ' cpa_url
cpa_url="${cpa_url:-http://127.0.0.1:8317}"
cpa_url="${cpa_url%/}"
[[ "$cpa_url" =~ ^https?://[^/@?#[:space:]]+$ ]] || fail '请输入 CPA 的基础地址，例如 http://127.0.0.1:8317。'
read -r -s -p 'CPA 管理密钥 ' cpa_key
printf '\n'
[[ "${#cpa_key}" -ge 20 && "$cpa_key" != *$'\r'* ]] || fail '请输入至少 20 个字符的 CPA 管理密钥。'

cpa_api() {
  printf 'Authorization: Bearer %s\n' "$cpa_key" |
    curl --silent --show-error --max-time 8 --header @- "$@"
}
management_url="$cpa_url/v8/management"
if ! cpa_api --fail --output /dev/null "$management_url/config/access/api-keys"; then
  fail '连接 CPA v8 管理接口失败。请检查地址、管理密钥和 CPA 的管理访问设置。'
fi

usage_setting="$management_url/config/observability/usage/usage-statistics-enabled"
stats=$(cpa_api --write-out '\n%{http_code}' "$usage_setting")
case "${stats##*$'\n'}" in
  200) stats="${stats%$'\n'*}" ;;
  404) stats=false ;;
  *) fail '无法读取 CPA 的用量统计设置。' ;;
esac

if [[ "${stats//[[:space:]]/}" != true ]]; then
  read -r -p 'CPA 的用量统计尚未开启。用管理 API 开启它？[y/N] ' enable
  [[ "$enable" == [yY] ]] || fail '请在 CPA 开启用量统计后再运行。'
  if ! cpa_api --fail --output /dev/null --request PUT --header 'Content-Type: application/json' \
       --data-binary true "$usage_setting"; then
    fail 'CPA 未能保存用量统计设置。请在 CPA 配置中手动开启后重试。'
  fi
fi

admin_password=$(openssl rand -hex 32)
install -d -m 700 state/attach
# Compose treats single-quoted dotenv values literally and accepts escaped single quotes.
quoted_key="${cpa_key//\'/\\\'}"
(
  set -o noclobber
  {
    printf 'CPA_BASE_URL=%s\n' "$cpa_url"
    printf "CPA_MANAGEMENT_KEY='%s'\n" "$quoted_key"
    printf 'ADMIN_PASSWORD=%s\n' "$admin_password"
    printf 'CPA_EXT_UID=%s\nCPA_EXT_GID=%s\n' "$(id -u)" "$(id -g)"
  } > .env.attach
)

printf '%s\n' 'EXT 会读取 CPA 的用量队列。同一台 CPA 请只运行一个队列采集器。'
"${compose[@]}" up -d --build
printf '%s\n' \
  'EXT 管理页 http://127.0.0.1:8481/（密码见 .env.attach 中的 ADMIN_PASSWORD）' \
  '客户端基础地址 http://127.0.0.1:8480/v1（沿用原有 CPA 客户端密钥）'
