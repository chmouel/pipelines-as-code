#!/usr/bin/env bash
# Use the GitHub token stored in a build Secret without printing it.
#
#   token.sh check  NAMESPACE SECRET   print "<http status> <repo,...>"
#   token.sh revoke NAMESPACE SECRET   revoke the token, print the http status
#   token.sh hash   NAMESPACE SECRET   print a short sha256 of the token
#
# The token goes from kubectl straight into curl's header input. Set
# GITHUB_API_URL for GitHub Enterprise (default https://api.github.com).
set -euo pipefail

api=${GITHUB_API_URL:-https://api.github.com}
api=${api%/}

usage() {
  sed -n '2,9p' "$0" >&2
  exit 2
}

[[ $# -eq 3 ]] || usage
action=$1 ns=$2 secret=$3

token_field() {
  kubectl -n "$ns" get secret "$secret" -o jsonpath='{.data.git-provider-token}'
}

auth_header() {
  token_field | base64 -d | sed 's/^/Authorization: token /'
}

case $action in
check)
  body=$(mktemp)
  trap 'rm -f "$body"' EXIT
  code=$(auth_header | curl -sS -o "$body" -w '%{http_code}' -H @- \
    -H 'Accept: application/vnd.github+json' "$api/installation/repositories")
  repos=$(jq -r '[.repositories[]?.full_name] | join(",")' "$body" 2>/dev/null || true)
  echo "$code ${repos:--}"
  ;;
revoke)
  auth_header | curl -sS -o /dev/null -w '%{http_code}\n' -X DELETE -H @- \
    -H 'Accept: application/vnd.github+json' "$api/installation/token"
  ;;
hash)
  field=$(token_field)
  [[ -n $field ]] || exit 1
  printf '%s' "$field" | sha256sum | cut -c1-12
  ;;
*)
  usage
  ;;
esac
