#!/usr/bin/env bash
# shellcheck disable=SC2329 # local helpers are called through expect and wait_until
# Prove that the credential service renews build credentials while a build
# waits, using a revoked token as the stand-in for an expired one.
#
#   prove-renewal.sh standalone       service only, hand-made pending builds
#   prove-renewal.sh pac              PaC handoff, build queued by PaC
#   prove-renewal.sh pac-unavailable  PaC refuses to launch without the service
#
# Needs GITHUB_REPOSITORY_ID and GITHUB_REPOSITORY_URL. Optional: REVISION
# (default main), OTHER_REPOSITORY_ID, GITHUB_API_URL. Run the service with
# RENEW_BEFORE=57m so it renews about every 3 minutes. Prints PASS/FAIL per
# check and exits non-zero on any failure. Never prints token values.
set -euo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
POC=$(dirname "$HERE")
CTRL_NS=${CONTROL_NAMESPACE:-pipelines-as-code}
NS=${OUTPUT_NAMESPACE:-pac-credentials-poc}
DEPLOY=pipelines-as-code-credentials
REVISION=${REVISION:-main}
P=pipelinesascode.tekton.dev

PASSED=0
FAILED=0

log() { echo "---- $(date -u +%T) $*"; }
pass() {
  echo "PASS $*"
  PASSED=$((PASSED + 1))
}
fail() {
  echo "FAIL $*"
  FAILED=$((FAILED + 1))
}
expect() {
  local desc=$1
  shift
  if "$@"; then pass "$desc"; else fail "$desc"; fi
}
summary() {
  echo "==== ${PASSED} passed, ${FAILED} failed"
  [[ $FAILED -eq 0 ]]
}
die() {
  echo "ERROR $*" >&2
  summary || true
  exit 1
}

# wait_until SECONDS CMD... polls CMD every 5 seconds.
wait_until() {
  local deadline=$(($(date +%s) + $1))
  shift
  until "$@"; do
    [[ $(date +%s) -lt $deadline ]] || return 1
    sleep 5
  done
}

esc() { echo "${1//./\\.}"; }
reqann() { kubectl -n "$CTRL_NS" get cm "$1" -o jsonpath="{.metadata.annotations.$(esc "$P/$2")}" 2>/dev/null || true; }
secann() { kubectl -n "$NS" get secret "$1" -o jsonpath="{.metadata.annotations.$(esc "$P/$2")}" 2>/dev/null || true; }
state_is() { [[ $(reqann "$1" credential-state) == "$2" ]]; }
thash() { "$HERE/token.sh" hash "$NS" "$1" 2>/dev/null || true; }
hash_differs() { local h; h=$(thash "$1"); [[ -n $h && $h != "$2" ]]; }
epoch() { jq -rn --arg t "$1" '$t | sub("\\.[0-9]+Z$"; "Z") | fromdateiso8601'; }
gone() { ! kubectl -n "$1" get "$2" "$3" >/dev/null 2>&1; }
pr_uid() { kubectl -n "$NS" get pipelinerun "$1" -o jsonpath='{.metadata.uid}'; }
secret_owner_uid() {
  kubectl -n "$NS" get secret "$1" -o jsonpath='{.metadata.ownerReferences[?(@.kind=="PipelineRun")].uid}' 2>/dev/null || true
}
owned_by() {
  local uid
  uid=$(pr_uid "$2")
  [[ -n $uid && $(secret_owner_uid "$1") == "$uid" ]]
}
pr_succeeded() {
  kubectl -n "$NS" get pipelinerun "$1" -o jsonpath='{.status.conditions[?(@.type=="Succeeded")].status}' 2>/dev/null || true
}
pr_done() {
  local status
  status=$(pr_succeeded "$1")
  [[ $status == True || $status == False ]]
}
pr_pending() { [[ $(kubectl -n "$NS" get pipelinerun "$1" -o jsonpath='{.spec.status}') == PipelineRunPending ]]; }
clone_taskrun() {
  kubectl -n "$NS" get taskrun -l "tekton.dev/pipelineRun=$1,tekton.dev/pipelineTask=clone" \
    -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true
}
taskrun_succeeded() {
  kubectl -n "$NS" get taskrun "$1" -o jsonpath='{.status.conditions[?(@.type=="Succeeded")].status}' 2>/dev/null || true
}
clone_done() {
  local tr status
  tr=$(clone_taskrun "$1")
  [[ -n $tr ]] || return 1
  status=$(taskrun_succeeded "$tr")
  [[ $status == True || $status == False ]]
}

# Go durations such as 57m, 1h or 56m30s, in seconds.
duration_seconds() {
  local d=$1 total=0
  while [[ $d =~ ^([0-9]+)([hms])(.*)$ ]]; do
    case ${BASH_REMATCH[2]} in
    h) total=$((total + BASH_REMATCH[1] * 3600)) ;;
    m) total=$((total + BASH_REMATCH[1] * 60)) ;;
    s) total=$((total + BASH_REMATCH[1])) ;;
    esac
    d=${BASH_REMATCH[3]}
  done
  [[ -z $d ]] || return 1
  echo "$total"
}

repo_full_name() {
  local u=${GITHUB_REPOSITORY_URL%/}
  u=${u%.git}
  u=${u#*://}
  echo "${u#*/}" | tr '[:upper:]' '[:lower:]'
}

copy_secret() {
  kubectl -n "$NS" get secret "$1" -o json |
    jq --arg name "$2" '{apiVersion, kind, type, data, metadata: {name: $name, namespace: .metadata.namespace}}' |
    kubectl create -f - -o name
}

seconds_until_due() {
  local expiry
  expiry=$(secann "$1" credential-expires-at)
  [[ -n $expiry ]] || return 1
  echo $(($(epoch "$expiry") - RENEW_BEFORE_S - $(date +%s)))
}

preflight() {
  [[ -n ${GITHUB_REPOSITORY_ID:-} && -n ${GITHUB_REPOSITORY_URL:-} ]] ||
    die "set GITHUB_REPOSITORY_ID and GITHUB_REPOSITORY_URL"
  export GITHUB_REPOSITORY_ID GITHUB_REPOSITORY_URL REVISION
  for c in kubectl jq curl envsubst; do command -v "$c" >/dev/null || die "$c is required"; done
  kubectl -n "$CTRL_NS" rollout status "deploy/$DEPLOY" --timeout=60s >/dev/null ||
    die "deploy/$DEPLOY is not ready in $CTRL_NS"
  local cfg rb cfg_changed pod_started
  cfg=$(kubectl -n "$CTRL_NS" get cm pipelines-as-code-credentials-config -o json) ||
    die "pipelines-as-code-credentials-config is missing in $CTRL_NS"
  [[ $(jq -r '.data.GITHUB_REPOSITORY_ID' <<<"$cfg") == "$GITHUB_REPOSITORY_ID" &&
    $(jq -r '.data.GITHUB_REPOSITORY_URL' <<<"$cfg") == "$GITHUB_REPOSITORY_URL" ]] ||
    die "pipelines-as-code-credentials-config does not match GITHUB_REPOSITORY_ID/URL; update it and restart deploy/$DEPLOY"
  cfg_changed=$(jq -r '[.metadata.managedFields[]?.time // empty] + [.metadata.creationTimestamp] | max' <<<"$cfg")
  pod_started=$(kubectl -n "$CTRL_NS" get pod -l "app.kubernetes.io/name=$DEPLOY" -o json |
    jq -r '[.items[] | select(.metadata.deletionTimestamp == null) | .metadata.creationTimestamp] | min // empty')
  [[ -n $pod_started && $(epoch "$pod_started") -ge $(epoch "$cfg_changed") ]] ||
    die "deploy/$DEPLOY started before its config last changed; run: kubectl -n $CTRL_NS rollout restart deploy/$DEPLOY"
  rb=$(jq -r '.data.RENEW_BEFORE // empty' <<<"$cfg")
  RENEW_BEFORE_S=$(duration_seconds "${rb:-15m}") || die "cannot parse RENEW_BEFORE=$rb"
  if [[ $RENEW_BEFORE_S -lt 3000 ]]; then
    die "RENEW_BEFORE=${rb:-15m} renews every $((3600 - RENEW_BEFORE_S))s; set it to 57m for this proof"
  fi
  log "service renews $((3600 - RENEW_BEFORE_S))s after each mint (RENEW_BEFORE=$rb)"
}

# Waits for the next renewal of SECRET and checks the expiry moved forward.
check_renewal() {
  local secret=$1 req=$2 h0 e0 wait
  h0=$(thash "$secret")
  e0=$(secann "$secret" credential-expires-at)
  wait=$(($(seconds_until_due "$secret") + 120))
  [[ $wait -gt 120 ]] || wait=120
  log "waiting up to ${wait}s for the scheduled renewal"
  if wait_until "$wait" hash_differs "$secret" "$h0"; then
    pass "scheduled renewal replaced the token while the build waits"
  else
    fail "scheduled renewal replaced the token while the build waits"
    return
  fi
  expect "expiry moved forward with the new token" \
    test "$(epoch "$(secann "$secret" credential-expires-at)")" -gt "$(epoch "$e0")"
  expect "request reports the same expiry as the Secret" \
    test "$(reqann "$req" credential-expires-at)" == "$(secann "$secret" credential-expires-at)"
}

check_restart() {
  local secret=$1 h0 due
  h0=$(thash "$secret")
  due=$(seconds_until_due "$secret")
  log "restarting the service"
  kubectl -n "$CTRL_NS" rollout restart "deploy/$DEPLOY" >/dev/null
  kubectl -n "$CTRL_NS" rollout status "deploy/$DEPLOY" --timeout=120s >/dev/null
  sleep 20
  if [[ $due -gt 60 ]]; then
    expect "restart before the renewal point does not mint a new token" test "$(thash "$secret")" == "$h0"
  else
    echo "SKIP restart without minting: renewal was due within a minute"
  fi

  log "stopping the service across the next renewal point"
  kubectl -n "$CTRL_NS" scale "deploy/$DEPLOY" --replicas=0 >/dev/null
  kubectl -n "$CTRL_NS" wait --for=delete pod -l "app.kubernetes.io/name=$DEPLOY" --timeout=120s >/dev/null 2>&1 || true
  h0=$(thash "$secret")
  due=$(seconds_until_due "$secret")
  if [[ $due -gt -15 ]]; then
    log "service down, sleeping $((due + 15))s until the renewal is overdue"
    sleep $((due + 15))
  fi
  expect "no renewal while the service is down" test "$(thash "$secret")" == "$h0"
  kubectl -n "$CTRL_NS" scale "deploy/$DEPLOY" --replicas=1 >/dev/null
  kubectl -n "$CTRL_NS" rollout status "deploy/$DEPLOY" --timeout=120s >/dev/null
  if wait_until 90 hash_differs "$secret" "$h0"; then
    pass "overdue credentials renewed right after the service restarted"
  else
    fail "overdue credentials renewed right after the service restarted"
  fi
}

# Revokes the original token held in BASELINE and checks GitHub's answers.
check_revocation() {
  local baseline=$1 managed=$2 code out
  code=$("$HERE/token.sh" revoke "$NS" "$baseline")
  expect "original token revoked (HTTP $code)" test "$code" == 204
  REVOKED_AT=$(date +%s)
  baseline_rejected() { [[ $("$HERE/token.sh" check "$NS" "$baseline") == 401* ]]; }
  expect "original token now rejected by GitHub (401, like an expired token)" wait_until 60 baseline_rejected
  out=$("$HERE/token.sh" check "$NS" "$managed")
  expect "renewed token accepted by GitHub (got ${out%% *})" test "${out%% *}" == 200
  expect "renewed token only reaches $(repo_full_name)" \
    test "$(echo "${out#* }" | tr '[:upper:]' '[:lower:]')" == "$(repo_full_name)"
}

check_clone() {
  local pr=$1 want=$2 desc=$3 tr pod created
  wait_until 600 clone_done "$pr" || {
    fail "$desc: clone task did not finish"
    return
  }
  tr=$(clone_taskrun "$pr")
  expect "$desc" test "$(taskrun_succeeded "$tr")" == "$want"
  pod=$(kubectl -n "$NS" get taskrun "$tr" -o jsonpath='{.status.podName}')
  created=$(kubectl -n "$NS" get pod "$pod" -o jsonpath='{.metadata.creationTimestamp}' 2>/dev/null || true)
  if [[ -n $created ]]; then
    expect "$desc: clone pod started after the original token was revoked" \
      test "$(epoch "$created")" -gt "$REVOKED_AT"
  fi
  if [[ $want == True ]]; then
    expect "$desc: on the first attempt" \
      test -z "$(kubectl -n "$NS" get taskrun "$tr" -o jsonpath='{.status.retriesStatus}')"
  else
    auth_error() {
      kubectl -n "$NS" logs "$pod" --all-containers 2>/dev/null |
        grep -qiE 'authentication failed|could not read username|invalid username or password|terminal prompts disabled|returned error: 40[13]'
    }
    expect "$desc: failure is an authentication error" auth_error
  fi
}

check_completion() {
  local req=$1 secret=$2 pr=$3 h0 due
  if pr_done "$pr"; then
    echo "SKIP renewal during finally tasks: build already finished"
  else
    expect "request still bound while finally tasks run" state_is "$req" bound
  fi
  wait_until 300 pr_done "$pr" || fail "build $pr did not finish"
  expect "request marked completed once the build finished" wait_until 90 state_is "$req" completed
  h0=$(thash "$secret")
  due=$(seconds_until_due "$secret")
  [[ $due -gt 0 ]] || due=0
  log "waiting $((due + 60))s past the next renewal point"
  sleep $((due + 60))
  expect "no renewal after completion" test "$(thash "$secret")" == "$h0"
}

check_cleanup() {
  local req=$1 secret=$2 pr=$3
  kubectl -n "$NS" delete pipelinerun "$pr" --wait >/dev/null
  cleaned() { gone "$CTRL_NS" cm "$req" && gone "$NS" secret "$secret"; }
  expect "deleting the build removed its Secret and request" wait_until 120 cleaned
}

check_rejection() {
  local other=${OTHER_REPOSITORY_ID:-$((GITHUB_REPOSITORY_ID + 1))} req=other-repo-request
  kubectl -n "$CTRL_NS" delete cm "$req" --ignore-not-found >/dev/null
  sed -e "s/\${GITHUB_REPOSITORY_ID}/$other/" -e "s/private-clone-request/$req/" \
    -e "s/private-clone-auth/other-repo-auth/" -e "s/pipelinerun-name: private-clone/pipelinerun-name: other-repo/" \
    "$POC/examples/request.yaml" | kubectl create -f - >/dev/null
  expect "request for another repository rejected" wait_until 60 state_is "$req" rejected
  expect "no Secret created for the rejected request" gone "$NS" secret other-repo-auth
  kubectl -n "$CTRL_NS" delete cm "$req" >/dev/null
}

standalone() {
  local req=private-clone-request sec=private-clone-auth bsec=private-clone-baseline-auth
  local pr=private-clone bpr=private-clone-baseline h0

  log "removing leftovers from a previous run"
  kubectl -n "$CTRL_NS" delete cm "$req" --ignore-not-found --wait >/dev/null
  kubectl -n "$NS" delete pipelinerun "$pr" "$bpr" --ignore-not-found --wait >/dev/null
  kubectl -n "$NS" delete secret "$sec" "$bsec" --ignore-not-found --wait >/dev/null

  log "1. creating the credential request"
  envsubst "\${GITHUB_REPOSITORY_ID}" <"$POC/examples/request.yaml" | kubectl create -f - >/dev/null
  expect "request ready with a managed Secret" wait_until 60 state_is "$req" ready
  state_is "$req" ready || die "request $(reqann "$req" credential-state): $(reqann "$req" credential-message)"
  local left
  left=$(($(epoch "$(secann "$sec" credential-expires-at)") - $(date +%s)))
  expect "Secret records GitHub's expiry (${left}s left)" test "$left" -gt 3000
  h0=$(thash "$sec")

  log "2. copying the initial credentials into an unmanaged baseline Secret"
  copy_secret "$sec" "$bsec" >/dev/null

  log "3. creating pending managed and baseline builds"
  for pair in "$pr:$sec" "$bpr:$bsec"; do
    NAME=${pair%%:*} SECRET=${pair#*:} REVISION=$REVISION \
      envsubst "\${NAME} \${SECRET} \${GITHUB_REPOSITORY_URL} \${REVISION}" \
      <"$POC/examples/private-clone.yaml" | kubectl create -f - >/dev/null
  done
  expect "request bound to the managed build" wait_until 60 state_is "$req" bound
  expect "managed Secret owned by the managed build" wait_until 30 owned_by "$sec" "$pr"
  both_pending() { pr_pending "$pr" && pr_pending "$bpr"; }
  expect "both builds still pending" both_pending

  log "4. waiting for renewal"
  check_renewal "$sec" "$req"
  expect "baseline still holds the original token" test "$(thash "$bsec")" == "$h0"

  log "5. restarting the service"
  check_restart "$sec"

  log "6. revoking the original token"
  check_revocation "$bsec" "$sec"

  log "7. releasing both builds"
  for p in "$pr" "$bpr"; do
    kubectl -n "$NS" patch pipelinerun "$p" --type=json -p '[{"op":"remove","path":"/spec/status"}]' >/dev/null
  done
  check_clone "$pr" True "managed clone succeeds"
  check_clone "$bpr" False "baseline clone fails"

  log "8. completion and cleanup"
  check_completion "$req" "$sec" "$pr"
  check_cleanup "$req" "$sec" "$pr"
  kubectl -n "$NS" delete pipelinerun "$bpr" --ignore-not-found >/dev/null
  kubectl -n "$NS" delete secret "$bsec" --ignore-not-found >/dev/null

  log "9. repository restriction"
  check_rejection
}

newest_pr() {
  kubectl -n "$NS" get pipelinerun -l "$P/original-prname=$1" --sort-by=.metadata.creationTimestamp \
    -o jsonpath='{range .items[*]}{.metadata.name}{" "}{.metadata.creationTimestamp}{"\n"}{end}' 2>/dev/null | tail -1
}
running_blocker() {
  local line
  line=$(newest_pr blocker)
  [[ -n $line ]] && ! pr_done "${line%% *}" && ! pr_pending "${line%% *}"
}
new_private_clone() {
  local line
  line=$(newest_pr private-clone)
  [[ -n $line && $(epoch "${line#* }") -ge $1 ]]
}
check_run() {
  # check_run SHA FIELD prints the status or conclusion of the private-clone check run.
  gh api "repos/$(repo_full_name)/commits/$1/check-runs" \
    --jq "[.check_runs[] | select(.name | contains(\"private-clone\")) | .$2] | first" 2>/dev/null || true
}

pac_preflight() {
  kubectl -n "$CTRL_NS" get deploy pipelines-as-code-controller -o json |
    jq -e '[.spec.template.spec.containers[].env[]? | select(.name == "PAC_GITHUB_CREDENTIALS_SERVICE" and .value == "true")] | length > 0' >/dev/null ||
    die "PAC_GITHUB_CREDENTIALS_SERVICE=true is not set on the PaC controller"
  [[ $(kubectl -n "$NS" get repository private-clone -o jsonpath='{.spec.concurrency_limit}') == 1 ]] ||
    die "Repository private-clone with concurrency_limit 1 not found in $NS"
}

pac() {
  pac_preflight
  local blocker pr sec req bsec sha start

  log "1. push a commit to main that changes a file under hold/ (starts the blocker)"
  wait_until 900 running_blocker || die "no running blocker build"
  blocker=$(newest_pr blocker)
  blocker=${blocker%% *}
  pass "blocker $blocker holds the only concurrency slot"

  start=$(date +%s)
  log "2. push a commit to main outside hold/ (starts private-clone)"
  wait_until 900 new_private_clone "$start" || die "no new private-clone build"
  pr=$(newest_pr private-clone)
  pr=${pr%% *}
  sec=$(kubectl -n "$NS" get pipelinerun "$pr" -o jsonpath="{.metadata.annotations.$(esc "$P/git-auth-secret")}")
  bsec=$sec-baseline
  copy_secret "$sec" "$bsec" >/dev/null
  req=$(kubectl -n "$CTRL_NS" get cm -l "$P/github-credential-request=true" -o json |
    jq -r --arg pr "$pr" '.items[] | select(.data["pipelinerun-name"] == $pr) | .metadata.name')
  sha=$(kubectl -n "$NS" get pipelinerun "$pr" -o jsonpath="{.metadata.annotations.$(esc "$P/sha")}")
  log "build $pr, secret $sec, request $req"

  expect "PaC submitted a request for the build" test -n "$req"
  expect "request submitted before the build was created (name fixed up front)" test \
    "$(epoch "$(kubectl -n "$CTRL_NS" get cm "$req" -o jsonpath='{.metadata.creationTimestamp}')")" \
    -le "$(epoch "$(kubectl -n "$NS" get pipelinerun "$pr" -o jsonpath='{.metadata.creationTimestamp}')")"
  expect "build marked as using service credentials" test \
    "$(kubectl -n "$NS" get pipelinerun "$pr" -o jsonpath="{.metadata.annotations.$(esc "$P/git-auth-secret-owner")}")" == credentials-service
  expect "Secret managed by the service" test \
    -n "$(kubectl -n "$NS" get secret "$sec" -o jsonpath="{.metadata.labels.$(esc "$P/credential-managed-by")}")"
  expect "build queued by PaC's concurrency limit" pr_pending "$pr"
  expect "request bound to the build" wait_until 60 state_is "$req" bound
  expect "Secret owned by the build" wait_until 30 owned_by "$sec" "$pr"
  if command -v gh >/dev/null; then
    expect "GitHub check shows the build as queued" test "$(check_run "$sha" status)" == queued
  else
    echo "SKIP GitHub check status: gh not installed"
  fi

  log "3. waiting for renewal"
  check_renewal "$sec" "$req"

  log "4. revoking the original token"
  check_revocation "$bsec" "$sec"

  log "5. cancelling the blocker to free the slot"
  kubectl -n "$NS" patch pipelinerun "$blocker" --type=merge -p '{"spec":{"status":"Cancelled"}}' >/dev/null
  started() { ! pr_pending "$pr"; }
  expect "PaC started the queued build" wait_until 180 started
  check_clone "$pr" True "PaC build clone succeeds"

  log "6. completion and cleanup"
  check_completion "$req" "$sec" "$pr"
  expect "PaC reported the build as succeeded" test "$(pr_succeeded "$pr")" == True
  if command -v gh >/dev/null; then
    reported() { [[ $(check_run "$sha" conclusion) == success ]]; }
    expect "GitHub check concluded success" wait_until 60 reported
  fi
  check_cleanup "$req" "$sec" "$pr"
  kubectl -n "$NS" delete secret "$bsec" --ignore-not-found >/dev/null
}

pac_unavailable() {
  pac_preflight
  local start req
  kubectl -n "$CTRL_NS" scale "deploy/$DEPLOY" --replicas=0 >/dev/null
  start=$(date +%s)
  new_request() {
    req=$(kubectl -n "$CTRL_NS" get cm -l "$P/github-credential-request=true" -o json |
      jq -r --argjson t "$start" '[.items[] | select((.metadata.creationTimestamp | fromdateiso8601) >= $t)] | last | .metadata.name // empty')
    [[ -n $req ]]
  }
  log "service stopped; push a commit to main outside hold/"
  wait_until 900 new_request || die "PaC submitted no request"
  local pr
  pr=$(kubectl -n "$CTRL_NS" get cm "$req" -o jsonpath='{.data.pipelinerun-name}')
  log "request $req for build $pr; waiting for PaC to give up"
  sleep 60
  expect "PaC did not create the build without credentials" gone "$NS" pipelinerun "$pr"
  kubectl -n "$CTRL_NS" scale "deploy/$DEPLOY" --replicas=1 >/dev/null
  kubectl -n "$CTRL_NS" rollout status "deploy/$DEPLOY" --timeout=120s >/dev/null
  log "deleting the abandoned request and its Secret by hand"
  local sec
  sec=$(kubectl -n "$CTRL_NS" get cm "$req" -o jsonpath='{.data.secret-name}')
  kubectl -n "$CTRL_NS" delete cm "$req" >/dev/null
  kubectl -n "$NS" delete secret "$sec" --ignore-not-found >/dev/null
}

case ${1:-} in
standalone | pac | pac-unavailable)
  preflight
  ${1//-/_}
  summary
  ;;
*)
  sed -n '3,13p' "$0" >&2
  exit 2
  ;;
esac
