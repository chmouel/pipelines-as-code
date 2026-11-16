#!/usr/bin/env bash
# Set up the credential service POC from the test variables stored in pass.
#
#   setup.sh service   namespace, RBAC, config, build and deploy the service
#   setup.sh pac       PaC controller RBAC, opt-in env vars, Repository CR
#   setup.sh all       service then pac
#   setup.sh tekton    push the blocker and private-clone templates to .tekton/ on main
#   setup.sh env       print exports for prove-renewal.sh: eval "$(setup.sh env)"
#   setup.sh cleanup   remove everything the other commands created
#
# Reads TEST_GITHUB_REPO_OWNER_GITHUBAPP, TEST_GITHUB_REPO_INSTALLATION_ID,
# TEST_GITHUB_API_URL and TEST_GITHUB_TOKEN from `pass show $PASS_KEY`
# (default pac/vars/scratchtest). Uses the current kubectl context. REGISTRY
# defaults to the one from `startpaac --show-config`, KO_DEFAULTPLATFORMS to
# the Kind node's platform. Never prints token or key values.
set -euo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
POC=$(dirname "$HERE")
ROOT=$(cd "$POC/../.." && pwd)
PASS_KEY=${PASS_KEY:-pac/vars/scratchtest}
CTRL_NS=pipelines-as-code
REQ_NS=pac-credentials-requests
NS=pac-credentials-poc
DEPLOY=pipelines-as-code-credentials
RENEW_BEFORE=${RENEW_BEFORE:-57m}

log() { echo "---- $*" >&2; }
die() {
  echo "ERROR $*" >&2
  exit 1
}

load_vars() {
  local vars
  vars=$(pass show "$PASS_KEY") || die "cannot read pass $PASS_KEY"
  # shellcheck disable=SC1090
  source <(grep -E '^export (TEST_GITHUB_REPO_OWNER_GITHUBAPP|TEST_GITHUB_REPO_INSTALLATION_ID|TEST_GITHUB_API_URL|TEST_GITHUB_TOKEN)=' <<<"$vars")
  OWNER_REPO=${TEST_GITHUB_REPO_OWNER_GITHUBAPP:?missing in $PASS_KEY}
  GITHUB_INSTALLATION_ID=${TEST_GITHUB_REPO_INSTALLATION_ID:?missing in $PASS_KEY}
  : "${TEST_GITHUB_TOKEN:?missing in $PASS_KEY}"

  local host=${TEST_GITHUB_API_URL:-api.github.com}
  host=${host#https://}
  host=${host%/}
  API=https://$host
  GITHUB_API_URL=""
  [[ $host == api.github.com ]] || GITHUB_API_URL=$API
  local web=github.com
  [[ $host == api.github.com ]] || web=${host%%/*}
  GITHUB_REPOSITORY_URL=https://$web/$OWNER_REPO

  local repo
  repo=$(printf 'Authorization: token %s\n' "$TEST_GITHUB_TOKEN" |
    curl -fsS -H @- -H 'Accept: application/vnd.github+json' "$API/repos/$OWNER_REPO") ||
    die "cannot read $OWNER_REPO from $API with the token from $PASS_KEY"
  GITHUB_REPOSITORY_ID=$(jq -r .id <<<"$repo")
  [[ $(jq -r .private <<<"$repo") == true ]] ||
    log "WARNING $OWNER_REPO is public: git clones it without credentials, so the clone checks prove nothing"
  export GITHUB_REPOSITORY_URL GITHUB_REPOSITORY_ID GITHUB_INSTALLATION_ID
}

check_cluster() {
  for c in kubectl jq curl envsubst pass; do command -v "$c" >/dev/null || die "$c is required"; done
  kubectl -n "$CTRL_NS" get secret pipelines-as-code-secret >/dev/null ||
    die "no pipelines-as-code-secret in $CTRL_NS on context $(kubectl config current-context); run startpaac -a first"
  log "context $(kubectl config current-context), repository $OWNER_REPO ($GITHUB_REPOSITORY_ID), installation $GITHUB_INSTALLATION_ID"
}

do_service() {
  command -v ko >/dev/null || die "ko is required"
  if [[ -z ${REGISTRY:-} ]]; then
    REGISTRY=$(startpaac --show-config 2>/dev/null | sed -n 's|^REGISTRY: *https\?://||p' | tr -d ' ')
    [[ -n $REGISTRY ]] || die "set REGISTRY, startpaac --show-config did not give one"
  fi
  if [[ -z ${KO_DEFAULTPLATFORMS:-} ]]; then
    KO_DEFAULTPLATFORMS=linux/$(kubectl get nodes -o jsonpath='{.items[0].status.nodeInfo.architecture}')
  fi
  export KO_DEFAULTPLATFORMS

  log "namespace and service RBAC"
  kubectl apply -f "$POC/namespace.yaml" -f "$POC/service/rbac.yaml"
  # Earlier versions kept requests in $CTRL_NS and gave the service write
  # access to every ConfigMap there.
  kubectl -n "$CTRL_NS" delete role,rolebinding pipelines-as-code-credentials-control --ignore-not-found
  kubectl -n "$CTRL_NS" delete cm -l pipelinesascode.tekton.dev/github-credential-request=true

  log "service config (RENEW_BEFORE=$RENEW_BEFORE)"
  local args=(
    --from-literal=GITHUB_INSTALLATION_ID="$GITHUB_INSTALLATION_ID"
    --from-literal=GITHUB_REPOSITORY_ID="$GITHUB_REPOSITORY_ID"
    --from-literal=GITHUB_REPOSITORY_URL="$GITHUB_REPOSITORY_URL"
    --from-literal=OUTPUT_NAMESPACE="$NS"
    --from-literal=CONTROL_NAMESPACE="$REQ_NS"
    --from-literal=KEY_SECRET_NAMESPACE="$CTRL_NS"
    --from-literal=RENEW_BEFORE="$RENEW_BEFORE"
  )
  [[ -z $GITHUB_API_URL ]] || args+=(--from-literal=GITHUB_API_URL="$GITHUB_API_URL")
  kubectl -n "$CTRL_NS" create configmap pipelines-as-code-credentials-config "${args[@]}" \
    --dry-run=client -o yaml | kubectl apply -f -

  log "building for $KO_DEFAULTPLATFORMS and pushing to $REGISTRY"
  (cd "$ROOT" && KO_DOCKER_REPO=$REGISTRY ko apply -B --sbom=none --insecure-registry \
    -f "$POC/service/deployment.yaml")
  # The service reads its config only at startup.
  kubectl -n "$CTRL_NS" rollout restart "deploy/$DEPLOY"
  kubectl -n "$CTRL_NS" rollout status "deploy/$DEPLOY" --timeout=180s
}

do_pac() {
  log "PaC controller RBAC, opt-in and Repository CR"
  kubectl apply -f "$POC/pac/controller-rbac.yaml"
  kubectl -n "$CTRL_NS" set env deploy/pipelines-as-code-controller \
    PAC_GITHUB_CREDENTIALS_SERVICE=true \
    PAC_GITHUB_CREDENTIALS_SERVICE_REPOSITORIES="$GITHUB_REPOSITORY_URL" \
    PAC_GITHUB_CREDENTIALS_SERVICE_NAMESPACE="$REQ_NS"
  # shellcheck disable=SC2016
  envsubst '${GITHUB_REPOSITORY_URL}' <"$POC/pac/repository.yaml" | kubectl apply -f -
  kubectl -n "$CTRL_NS" rollout status deploy/pipelines-as-code-controller --timeout=180s
}

do_tekton() {
  command -v git >/dev/null || die "git is required"
  echo "This pushes .tekton/blocker.yaml and .tekton/private-clone.yaml to main of $OWNER_REPO." >&2
  echo "Other PaC setups using that repository (such as e2e runs) will see them too." >&2
  read -r -p "Continue? [y/N] " ok
  [[ $ok == [yY] ]] || die "aborted"
  # The token reaches git through a credential helper reading the environment,
  # never through the URL or the command line.
  # shellcheck disable=SC2016
  local helper='!f() { echo username=x-access-token; echo "password=$TEST_GITHUB_TOKEN"; }; f'
  local repo
  repo=$(mktemp -d)
  git -c credential.helper= -c credential.helper="$helper" \
    clone -q --depth 1 --branch main "$GITHUB_REPOSITORY_URL.git" "$repo"
  mkdir -p "$repo/.tekton"
  cp "$POC/pac/tekton/blocker.yaml" "$POC/pac/tekton/private-clone.yaml" "$repo/.tekton/"
  git -C "$repo" add .tekton
  if git -C "$repo" diff --cached --quiet; then
    log ".tekton templates already up to date on main"
  else
    git -C "$repo" commit -q -m "Add credential service POC PipelineRuns"
    git -C "$repo" -c credential.helper= -c credential.helper="$helper" push -q origin main
    log "pushed .tekton templates to main of $OWNER_REPO"
  fi
  rm -rf "$repo"
}

do_env() {
  printf 'export GITHUB_REPOSITORY_ID=%q GITHUB_REPOSITORY_URL=%q GITHUB_INSTALLATION_ID=%q REVISION=main\n' \
    "$GITHUB_REPOSITORY_ID" "$GITHUB_REPOSITORY_URL" "$GITHUB_INSTALLATION_ID"
  [[ -z $GITHUB_API_URL ]] || printf 'export GITHUB_API_URL=%q\n' "$GITHUB_API_URL"
}

do_cleanup() {
  log "removing the credential service POC"
  kubectl delete -f "$POC/service/deployment.yaml" --ignore-not-found
  kubectl -n "$REQ_NS" delete cm -l pipelinesascode.tekton.dev/github-credential-request=true --ignore-not-found
  kubectl -n "$CTRL_NS" delete cm pipelines-as-code-credentials-config --ignore-not-found
  kubectl -n "$CTRL_NS" delete secret pipelines-as-code-credentials-key --ignore-not-found
  kubectl delete -f "$POC/pac/controller-rbac.yaml" --ignore-not-found
  kubectl -n "$CTRL_NS" set env deploy/pipelines-as-code-controller \
    PAC_GITHUB_CREDENTIALS_SERVICE- PAC_GITHUB_CREDENTIALS_SERVICE_REPOSITORIES- \
    PAC_GITHUB_CREDENTIALS_SERVICE_NAMESPACE-
  kubectl delete -f "$POC/service/rbac.yaml" --ignore-not-found
  kubectl delete -f "$POC/namespace.yaml" --ignore-not-found
}

case ${1:-} in
service | pac | all | tekton | env | cleanup) ;;
*)
  sed -n '3,15p' "$0" >&2
  exit 2
  ;;
esac

if [[ $1 == cleanup ]]; then
  do_cleanup
  exit
fi
load_vars
case $1 in
env) do_env ;;
tekton) do_tekton ;;
service)
  check_cluster
  do_service
  ;;
pac)
  check_cluster
  do_pac
  ;;
all)
  check_cluster
  do_service
  do_pac
  ;;
esac
[[ $1 == env ]] || log "done; run: eval \"\$($0 env)\" && $HERE/prove-renewal.sh standalone"
