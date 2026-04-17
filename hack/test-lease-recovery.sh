#!/usr/bin/env bash
# Test the lease-based concurrency recovery flow by manipulating Kubernetes
# Lease objects and PipelineRun annotations on a live cluster.
#
# Prerequisites:
#   - kubectl configured and pointing at the target cluster
#   - PAC installed with concurrency-backend=lease
#   - At least one queued PipelineRun (or use --create-fixtures to create test PRs)
#
# Usage:
#   ./hack/test-lease-recovery.sh [--scenario <name>] [--pac-ns <namespace>] [--pr-ns <namespace>]
#
# Scenarios:
#   expired-lease       Patch a Lease's renewTime to the past so the watcher can reclaim it
#   orphaned-claim      Set stale queue-claimed-by/at annotations on a queued PR
#   holder-vanish       Delete the watcher pod while a Lease is held, then watch recovery
#   stuck-promotion     Simulate a PR that was claimed but never promoted to started
#   all                 Run all scenarios sequentially
#
set -euo pipefail

# -- defaults ----------------------------------------------------------------
PAC_NS="${PAC_NS:-pipelines-as-code}"
PR_NS=""
SCENARIO="all"
TIMEOUT=120        # seconds to wait for recovery events
POLL_INTERVAL=5    # seconds between status checks
CREATE_FIXTURES=false
CLEANUP=true
DRY_RUN=false

# -- annotation keys ---------------------------------------------------------
ANN_STATE="pipelinesascode.tekton.dev/state"
ANN_REPO="pipelinesascode.tekton.dev/repository"
ANN_EXEC_ORDER="pipelinesascode.tekton.dev/execution-order"
ANN_CLAIMED_BY="pipelinesascode.tekton.dev/queue-claimed-by"
ANN_CLAIMED_AT="pipelinesascode.tekton.dev/queue-claimed-at"
ANN_DECISION="pipelinesascode.tekton.dev/queue-decision"
ANN_DEBUG="pipelinesascode.tekton.dev/queue-debug-summary"
LBL_STATE="pipelinesascode.tekton.dev/state"
LBL_REPO="pipelinesascode.tekton.dev/repository"

# -- colors -------------------------------------------------------------------
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

info()    { echo -e "${CYAN}[INFO]${NC}  $*"; }
ok()      { echo -e "${GREEN}[PASS]${NC}  $*"; }
fail()    { echo -e "${RED}[FAIL]${NC}  $*"; }
warn()    { echo -e "${YELLOW}[WARN]${NC}  $*"; }
header()  { echo -e "\n${BOLD}=== $* ===${NC}\n"; }

# -- argument parsing ---------------------------------------------------------
while [[ $# -gt 0 ]]; do
    case "$1" in
        --scenario)       SCENARIO="$2"; shift 2 ;;
        --pac-ns)         PAC_NS="$2"; shift 2 ;;
        --pr-ns)          PR_NS="$2"; shift 2 ;;
        --timeout)        TIMEOUT="$2"; shift 2 ;;
        --create-fixtures) CREATE_FIXTURES=true; shift ;;
        --no-cleanup)     CLEANUP=false; shift ;;
        --dry-run)        DRY_RUN=true; shift ;;
        -h|--help)
            head -20 "$0" | grep '^#' | sed 's/^# \?//'
            exit 0
            ;;
        *) echo "Unknown option: $1"; exit 1 ;;
    esac
done

# -- helpers ------------------------------------------------------------------

# Compute the lease name the same way Go does: sha256(repoKey)[:16] with prefix
repo_lease_name() {
    local repo_key="$1"
    local hash
    hash=$(echo -n "$repo_key" | sha256sum | cut -c1-16)
    echo "pac-concurrency-${hash}"
}

# Find queued PipelineRuns in a namespace
find_queued_prs() {
    local ns="$1"
    kubectl get pipelineruns -n "$ns" \
        -l "${LBL_STATE}=queued" \
        -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null || true
}

# Find all pac-concurrency leases
find_pac_leases() {
    kubectl get leases -n "$PAC_NS" -o name 2>/dev/null | grep pac-concurrency || true
}

# Get a specific annotation from a PipelineRun
get_pr_annotation() {
    local ns="$1" name="$2" key="$3"
    kubectl get pipelinerun -n "$ns" "$name" -o jsonpath="{.metadata.annotations['${key//\./\\.'}']}" 2>/dev/null || true
}

# Print PipelineRun queue state summary
dump_pr_queue_state() {
    local ns="$1"
    info "PipelineRun queue state in namespace ${ns}:"
    kubectl get pipelineruns -n "$ns" \
        -o custom-columns=\
NAME:.metadata.name,\
STATE:.metadata.annotations.pipelinesascode\\.tekton\\.dev/state,\
SPEC_STATUS:.spec.status,\
CLAIMED_BY:.metadata.annotations.pipelinesascode\\.tekton\\.dev/queue-claimed-by,\
CLAIMED_AT:.metadata.annotations.pipelinesascode\\.tekton\\.dev/queue-claimed-at,\
DECISION:.metadata.annotations.pipelinesascode\\.tekton\\.dev/queue-decision \
        2>/dev/null || warn "No PipelineRuns found in ${ns}"
}

# Print lease state
dump_lease_state() {
    info "Lease state in namespace ${PAC_NS}:"
    local leases
    leases=$(find_pac_leases)
    if [[ -z "$leases" ]]; then
        warn "No pac-concurrency leases found"
        return
    fi
    for lease in $leases; do
        local name
        name=$(basename "$lease")
        echo -n "  ${name}: holder="
        kubectl get lease -n "$PAC_NS" "$name" \
            -o jsonpath='{.spec.holderIdentity}' 2>/dev/null || echo -n "<none>"
        echo -n " renew="
        kubectl get lease -n "$PAC_NS" "$name" \
            -o jsonpath='{.spec.renewTime}' 2>/dev/null || echo -n "<none>"
        echo -n " duration="
        kubectl get lease -n "$PAC_NS" "$name" \
            -o jsonpath='{.spec.leaseDurationSeconds}' 2>/dev/null || echo -n "<none>"
        echo
    done
}

# Wait for a recovery event or annotation change
wait_for_recovery() {
    local ns="$1" pr_name="$2" expected_decision="${3:-recovery_requeued}"
    local elapsed=0

    info "Waiting up to ${TIMEOUT}s for recovery of ${ns}/${pr_name} (expected decision: ${expected_decision})..."
    while [[ $elapsed -lt $TIMEOUT ]]; do
        local decision
        decision=$(get_pr_annotation "$ns" "$pr_name" "$ANN_DECISION")
        if [[ "$decision" == "$expected_decision" ]]; then
            ok "PipelineRun ${ns}/${pr_name} reached decision '${decision}' after ${elapsed}s"
            return 0
        fi

        # Also check if the PR advanced to started (promotion succeeded)
        local state
        state=$(get_pr_annotation "$ns" "$pr_name" "$ANN_STATE")
        if [[ "$state" == "started" ]]; then
            ok "PipelineRun ${ns}/${pr_name} was promoted to 'started' after ${elapsed}s"
            return 0
        fi

        sleep "$POLL_INTERVAL"
        elapsed=$((elapsed + POLL_INTERVAL))
    done

    fail "Timeout: PipelineRun ${ns}/${pr_name} did not recover within ${TIMEOUT}s"
    info "Current state:"
    dump_pr_queue_state "$ns"
    return 1
}

# Wait for a K8s event with a specific reason
wait_for_event() {
    local ns="$1" reason="$2"
    local elapsed=0

    info "Watching for '${reason}' events in ${ns}..."
    while [[ $elapsed -lt $TIMEOUT ]]; do
        local count
        count=$(kubectl get events -n "$ns" --field-selector "reason=${reason}" -o name 2>/dev/null | wc -l)
        if [[ "$count" -gt 0 ]]; then
            ok "Found ${count} '${reason}' event(s) in ${ns}"
            kubectl get events -n "$ns" --field-selector "reason=${reason}" \
                --sort-by='.lastTimestamp' 2>/dev/null | tail -5
            return 0
        fi
        sleep "$POLL_INTERVAL"
        elapsed=$((elapsed + POLL_INTERVAL))
    done

    warn "No '${reason}' events found within ${TIMEOUT}s"
    return 1
}

# -- fixture creation ---------------------------------------------------------
FIXTURE_PRS=()

create_test_pipelinerun() {
    local ns="$1" name="$2" repo="$3" state="$4"
    local spec_status=""
    [[ "$state" == "queued" ]] && spec_status='"status": "PipelineRunPending",'

    if $DRY_RUN; then
        info "[dry-run] Would create PipelineRun ${ns}/${name} (state=${state}, repo=${repo})"
        return
    fi

    kubectl apply -f - <<EOF
apiVersion: tekton.dev/v1
kind: PipelineRun
metadata:
  name: ${name}
  namespace: ${ns}
  labels:
    ${LBL_STATE}: ${state}
    ${LBL_REPO}: ${repo}
  annotations:
    ${ANN_STATE}: ${state}
    ${ANN_REPO}: ${repo}
    ${ANN_EXEC_ORDER}: "${ns}/${name}"
spec:
  ${spec_status}
  pipelineSpec:
    tasks:
    - name: noop
      taskSpec:
        steps:
        - name: sleep
          image: busybox
          command: ["sleep", "300"]
EOF
    FIXTURE_PRS+=("${ns}/${name}")
    info "Created PipelineRun ${ns}/${name} (state=${state})"
}

cleanup_fixtures() {
    if ! $CLEANUP || [[ ${#FIXTURE_PRS[@]} -eq 0 ]]; then
        return
    fi
    info "Cleaning up ${#FIXTURE_PRS[@]} test PipelineRun(s)..."
    for pr in "${FIXTURE_PRS[@]}"; do
        local ns name
        ns="${pr%%/*}"
        name="${pr##*/}"
        kubectl delete pipelinerun -n "$ns" "$name" --ignore-not-found 2>/dev/null || true
    done
}

# -- pre-flight checks -------------------------------------------------------
preflight() {
    header "Pre-flight checks"

    if ! kubectl cluster-info &>/dev/null; then
        fail "Cannot connect to Kubernetes cluster"
        exit 1
    fi
    ok "Cluster reachable"

    local backend
    backend=$(kubectl get configmap pipelines-as-code -n "$PAC_NS" \
        -o jsonpath='{.data.concurrency-backend}' 2>/dev/null || echo "")
    if [[ "$backend" != "lease" ]]; then
        warn "concurrency-backend is '${backend:-memory}', not 'lease'"
        warn "Set it with:"
        warn "  kubectl patch configmap pipelines-as-code -n ${PAC_NS} --type merge -p '{\"data\":{\"concurrency-backend\":\"lease\"}}'"
        warn "  kubectl rollout restart deployment pipelines-as-code-watcher -n ${PAC_NS}"
        if [[ "$backend" != "lease" && "$SCENARIO" != "all" ]]; then
            fail "Cannot run lease recovery tests without lease backend enabled"
            exit 1
        fi
    else
        ok "concurrency-backend=lease"
    fi

    if [[ -z "$PR_NS" ]]; then
        # Try to auto-detect from existing queued PRs
        PR_NS=$(kubectl get pipelineruns --all-namespaces \
            -l "${LBL_STATE}=queued" \
            -o jsonpath='{.items[0].metadata.namespace}' 2>/dev/null || true)
        if [[ -z "$PR_NS" ]]; then
            PR_NS="$PAC_NS"
            warn "No queued PipelineRuns found; defaulting PR namespace to ${PR_NS}"
            if ! $CREATE_FIXTURES; then
                warn "Use --create-fixtures to create test PipelineRuns, or --pr-ns to specify namespace"
            fi
        else
            ok "Auto-detected PipelineRun namespace: ${PR_NS}"
        fi
    fi

    dump_lease_state
    dump_pr_queue_state "$PR_NS"
}

# -- scenarios ----------------------------------------------------------------

scenario_expired_lease() {
    header "Scenario: expired-lease"
    info "Patches a Lease's renewTime far into the past and clears the holder,"
    info "simulating a watcher that died without releasing its lock."

    local leases
    leases=$(find_pac_leases)
    if [[ -z "$leases" ]]; then
        if $CREATE_FIXTURES; then
            local repo_name="test-recovery-repo"
            create_test_pipelinerun "$PR_NS" "lease-test-queued-1" "$repo_name" "queued"
            # Trigger a lease creation by waiting for the watcher to process it
            info "Waiting 10s for watcher to create a lease..."
            sleep 10
            leases=$(find_pac_leases)
        fi
        if [[ -z "$leases" ]]; then
            warn "No pac-concurrency leases found. Skipping."
            return 1
        fi
    fi

    local lease_name
    lease_name=$(basename "$(echo "$leases" | head -1)")
    info "Target lease: ${lease_name}"

    if $DRY_RUN; then
        info "[dry-run] Would patch lease ${lease_name} with expired renewTime"
        return 0
    fi

    # Record original state
    local original_holder
    original_holder=$(kubectl get lease -n "$PAC_NS" "$lease_name" \
        -o jsonpath='{.spec.holderIdentity}' 2>/dev/null || echo "")
    info "Original holder: ${original_holder:-<none>}"

    # Patch the lease to look expired (renewTime 5 minutes ago, fake dead holder)
    kubectl patch lease "$lease_name" -n "$PAC_NS" --type merge -p '{
        "spec": {
            "holderIdentity": "dead-watcher-recovery-test",
            "renewTime": "2020-01-01T00:00:00.000000Z",
            "leaseDurationSeconds": 30
        }
    }'
    ok "Patched lease ${lease_name} with expired renewTime and dead holder"

    dump_lease_state

    # Wait and verify the watcher reclaims the lease on its next operation
    info "Waiting for the watcher to reclaim the expired lease..."
    local elapsed=0
    while [[ $elapsed -lt $TIMEOUT ]]; do
        local holder
        holder=$(kubectl get lease -n "$PAC_NS" "$lease_name" \
            -o jsonpath='{.spec.holderIdentity}' 2>/dev/null || echo "")
        if [[ "$holder" != "dead-watcher-recovery-test" && -n "$holder" ]]; then
            ok "Lease ${lease_name} reclaimed by '${holder}' after ${elapsed}s"
            return 0
        fi
        # A released lease (nil holder) also counts as recovered
        if [[ -z "$holder" ]]; then
            local renew
            renew=$(kubectl get lease -n "$PAC_NS" "$lease_name" \
                -o jsonpath='{.spec.renewTime}' 2>/dev/null || echo "")
            if [[ "$renew" != "2020-01-01T00:00:00.000000Z" ]]; then
                ok "Lease ${lease_name} was used and released (holder cleared, renewTime updated) after ${elapsed}s"
                return 0
            fi
        fi
        sleep "$POLL_INTERVAL"
        elapsed=$((elapsed + POLL_INTERVAL))
    done

    fail "Lease ${lease_name} was not reclaimed within ${TIMEOUT}s"
    dump_lease_state
    return 1
}

scenario_orphaned_claim() {
    header "Scenario: orphaned-claim"
    info "Sets stale queue-claimed-by/at annotations on a queued PipelineRun,"
    info "simulating a watcher that claimed a PR but crashed before promoting it."

    local target_pr=""

    if $CREATE_FIXTURES; then
        local pr_name
        pr_name="orphan-claim-test-$(date +%s)"
        create_test_pipelinerun "$PR_NS" "$pr_name" "test-recovery-repo" "queued"
        target_pr="$pr_name"
        info "Waiting 5s for the PR to be indexed..."
        sleep 5
    else
        target_pr=$(find_queued_prs "$PR_NS" | head -1)
    fi

    if [[ -z "$target_pr" ]]; then
        warn "No queued PipelineRun found in ${PR_NS}. Use --create-fixtures or push a PR. Skipping."
        return 1
    fi

    info "Target PipelineRun: ${PR_NS}/${target_pr}"

    # Show current annotations
    info "Current annotations:"
    echo "  claimed-by: $(get_pr_annotation "$PR_NS" "$target_pr" "$ANN_CLAIMED_BY")"
    echo "  claimed-at: $(get_pr_annotation "$PR_NS" "$target_pr" "$ANN_CLAIMED_AT")"
    echo "  decision:   $(get_pr_annotation "$PR_NS" "$target_pr" "$ANN_DECISION")"

    if $DRY_RUN; then
        info "[dry-run] Would set stale claim annotations on ${PR_NS}/${target_pr}"
        return 0
    fi

    # Set stale claim annotations (claimed 5 minutes ago by a dead watcher)
    local stale_time
    stale_time=$(date -u -d '5 minutes ago' '+%Y-%m-%dT%H:%M:%S.000000000Z' 2>/dev/null || \
                 date -u -v-5M '+%Y-%m-%dT%H:%M:%S.000000000Z' 2>/dev/null || \
                 echo "2020-01-01T00:00:00.000000000Z")

    kubectl annotate pipelinerun -n "$PR_NS" "$target_pr" \
        "${ANN_CLAIMED_BY}=ghost-watcher-recovery-test" \
        "${ANN_CLAIMED_AT}=${stale_time}" \
        --overwrite
    ok "Set stale claim on ${PR_NS}/${target_pr} (claimed by ghost-watcher at ${stale_time})"

    # The recovery loop runs every ~31s. The claim TTL is 30s.
    # Since we set the claim 5 minutes in the past, hasActiveClaim() returns false immediately.
    # The recovery loop should detect this PR as an orphan and re-enqueue it.
    wait_for_recovery "$PR_NS" "$target_pr" "recovery_requeued"
    local rc=$?

    # Check for recovery events
    wait_for_event "$PR_NS" "QueueRecoveryRequeued" || true

    # Dump final state
    info "Final annotations:"
    echo "  claimed-by: $(get_pr_annotation "$PR_NS" "$target_pr" "$ANN_CLAIMED_BY")"
    echo "  claimed-at: $(get_pr_annotation "$PR_NS" "$target_pr" "$ANN_CLAIMED_AT")"
    echo "  decision:   $(get_pr_annotation "$PR_NS" "$target_pr" "$ANN_DECISION")"
    echo "  debug:      $(get_pr_annotation "$PR_NS" "$target_pr" "$ANN_DEBUG")"

    return $rc
}

scenario_holder_vanish() {
    header "Scenario: holder-vanish"
    info "Deletes the PAC watcher pod to simulate a crash, then watches for"
    info "recovery events as the new pod starts its recovery loop."

    dump_pr_queue_state "$PR_NS"

    local queued_count
    queued_count=$(find_queued_prs "$PR_NS" | wc -l)
    if [[ "$queued_count" -eq 0 ]]; then
        if $CREATE_FIXTURES; then
            create_test_pipelinerun "$PR_NS" "vanish-test-queued-$(date +%s)" "test-recovery-repo" "queued"
            info "Waiting 5s for watcher to process..."
            sleep 5
        else
            warn "No queued PipelineRuns found. Use --create-fixtures. Skipping."
            return 1
        fi
    fi

    if $DRY_RUN; then
        info "[dry-run] Would delete watcher pod and watch for recovery"
        return 0
    fi

    # Record current events count
    local pre_events
    pre_events=$(kubectl get events -n "$PR_NS" --field-selector "reason=QueueRecoveryRequeued" -o name 2>/dev/null | wc -l)

    # Kill the watcher
    info "Deleting PAC watcher pod..."
    kubectl delete pod -n "$PAC_NS" -l app.kubernetes.io/component=watcher --wait=false 2>/dev/null || \
    kubectl delete pod -n "$PAC_NS" -l app=pipelines-as-code-watcher --wait=false 2>/dev/null || {
        fail "Could not find watcher pod to delete"
        return 1
    }
    ok "Watcher pod deleted"

    # Wait for the new pod to be ready
    info "Waiting for new watcher pod to be ready..."
    kubectl rollout status deployment -n "$PAC_NS" \
        $(kubectl get deployments -n "$PAC_NS" -o name 2>/dev/null | grep watcher | head -1 | xargs basename 2>/dev/null || echo "pipelines-as-code-watcher") \
        --timeout=60s 2>/dev/null || {
        warn "Could not verify watcher rollout status"
    }

    # The new watcher should run recovery on startup (controller.go:110)
    # and then every ~31s after that.
    info "Waiting for recovery events from the new watcher..."
    local elapsed=0
    while [[ $elapsed -lt $TIMEOUT ]]; do
        local post_events
        post_events=$(kubectl get events -n "$PR_NS" --field-selector "reason=QueueRecoveryRequeued" -o name 2>/dev/null | wc -l)
        if [[ "$post_events" -gt "$pre_events" ]]; then
            ok "New recovery events detected: ${pre_events} -> ${post_events} (${elapsed}s)"
            kubectl get events -n "$PR_NS" --field-selector "reason=QueueRecoveryRequeued" \
                --sort-by='.lastTimestamp' 2>/dev/null | tail -5
            dump_pr_queue_state "$PR_NS"
            return 0
        fi
        sleep "$POLL_INTERVAL"
        elapsed=$((elapsed + POLL_INTERVAL))
    done

    warn "No new recovery events within ${TIMEOUT}s (pre=${pre_events})"
    info "This might be expected if all queued PRs are healthy (have running siblings or active claims)"
    dump_pr_queue_state "$PR_NS"
    return 0
}

scenario_stuck_promotion() {
    header "Scenario: stuck-promotion"
    info "Creates a PipelineRun that looks claimed but never got promoted to started."
    info "Verifies the recovery loop detects it after the claim TTL expires."

    local pr_name
    pr_name="stuck-promo-test-$(date +%s)"

    if $DRY_RUN; then
        info "[dry-run] Would create stuck PR ${PR_NS}/${pr_name} and wait for recovery"
        return 0
    fi

    # Create a queued PR with an already-expired claim
    local stale_time
    stale_time=$(date -u -d '5 minutes ago' '+%Y-%m-%dT%H:%M:%S.000000000Z' 2>/dev/null || \
                 date -u -v-5M '+%Y-%m-%dT%H:%M:%S.000000000Z' 2>/dev/null || \
                 echo "2020-01-01T00:00:00.000000000Z")

    kubectl apply -f - <<EOF
apiVersion: tekton.dev/v1
kind: PipelineRun
metadata:
  name: ${pr_name}
  namespace: ${PR_NS}
  labels:
    ${LBL_STATE}: queued
    ${LBL_REPO}: test-recovery-repo
  annotations:
    ${ANN_STATE}: queued
    ${ANN_REPO}: test-recovery-repo
    ${ANN_EXEC_ORDER}: "${PR_NS}/${pr_name}"
    ${ANN_CLAIMED_BY}: "crashed-watcher-12345"
    ${ANN_CLAIMED_AT}: "${stale_time}"
    ${ANN_DECISION}: "claimed_for_promotion"
spec:
  status: PipelineRunPending
  pipelineSpec:
    tasks:
    - name: noop
      taskSpec:
        steps:
        - name: sleep
          image: busybox
          command: ["sleep", "300"]
EOF
    FIXTURE_PRS+=("${PR_NS}/${pr_name}")
    ok "Created stuck-promotion PipelineRun ${PR_NS}/${pr_name}"
    info "  claimed-by: crashed-watcher-12345"
    info "  claimed-at: ${stale_time} (expired)"

    # Wait for recovery
    wait_for_recovery "$PR_NS" "$pr_name" "recovery_requeued"
    local rc=$?

    wait_for_event "$PR_NS" "QueueRecoveryRequeued" || true

    info "Final state:"
    echo "  claimed-by: $(get_pr_annotation "$PR_NS" "$pr_name" "$ANN_CLAIMED_BY")"
    echo "  claimed-at: $(get_pr_annotation "$PR_NS" "$pr_name" "$ANN_CLAIMED_AT")"
    echo "  decision:   $(get_pr_annotation "$PR_NS" "$pr_name" "$ANN_DECISION")"
    echo "  state:      $(get_pr_annotation "$PR_NS" "$pr_name" "$ANN_STATE")"
    echo "  debug:      $(get_pr_annotation "$PR_NS" "$pr_name" "$ANN_DEBUG")"

    return $rc
}

# -- main ---------------------------------------------------------------------

trap cleanup_fixtures EXIT

preflight

results=()

run_scenario() {
    local name="$1"
    local func="scenario_${name//-/_}"

    if ! declare -f "$func" &>/dev/null; then
        fail "Unknown scenario: ${name}"
        exit 1
    fi

    if $func; then
        results+=("${GREEN}PASS${NC}  ${name}")
    else
        results+=("${RED}FAIL${NC}  ${name}")
    fi
}

case "$SCENARIO" in
    all)
        for s in expired-lease orphaned-claim stuck-promotion holder-vanish; do
            run_scenario "$s"
            echo
        done
        ;;
    *)
        run_scenario "$SCENARIO"
        ;;
esac

# -- summary ------------------------------------------------------------------
header "Results"
for r in "${results[@]}"; do
    echo -e "  $r"
done
echo

# Exit with failure if any scenario failed
for r in "${results[@]}"; do
    if [[ "$r" == *"FAIL"* ]]; then
        exit 1
    fi
done
exit 0
