# shellcheck shell=sh
# PATCH_DATA_B64GZ is injected by PAC at PipelineRun creation time (see buildFixScript).
result_file="$(results.analysis.path)"
# shellcheck disable=SC2153
repo_url="${REPO_URL}"
repo_dir="${REPO_DIR:-/workspace/source}"
# shellcheck disable=SC2153
pr_branch="${PR_BRANCH}"
expected_sha="${EXPECTED_SHA:-}"
role_name="${ROLE_NAME:-AI analysis}"

emit_envelope() {
    envelope_file="$1"
    echo "===ANALYSIS_BEGIN==="
    cat "${envelope_file}"
    echo "===ANALYSIS_END==="
    cat "${envelope_file}" > "${result_file}"
}

# Setup git credentials for push
if [ -d /workspace/basic-auth ]; then
    cp /workspace/basic-auth/.gitconfig "${HOME}/.gitconfig" 2>/dev/null || true
    cp /workspace/basic-auth/.git-credentials "${HOME}/.git-credentials" 2>/dev/null || true
    chmod 600 "${HOME}/.git-credentials" 2>/dev/null || true
fi

emit_missing_checkout() {
    echo "fix: repository checkout is missing at ${repo_dir}" >&2
    jq -n \
        --arg repo_dir "${repo_dir}" \
        '{status: "error", backend: "patch-apply", model: "", duration_ms: 0, error: {provider: "patch-apply", type: "missing_checkout", message: ("Repository checkout is missing at " + $repo_dir + ". Please re-run analysis."), retryable: false}}' > /tmp/fix-envelope.json
    emit_envelope /tmp/fix-envelope.json
    exit 1
}

if ! cd "${repo_dir}"; then
    emit_missing_checkout
fi
git config --global --add safe.directory "${repo_dir}"
git config --global --add safe.directory "${repo_dir}/"
if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    emit_missing_checkout
fi
if git remote get-url origin >/dev/null 2>&1; then
    git remote set-url origin "${repo_url}"
else
    git remote add origin "${repo_url}"
fi
git fetch --depth=50 origin "${pr_branch}"
git checkout -B "${pr_branch}" "origin/${pr_branch}"
git config user.name "Pipelines as Code AI"
git config user.email "noreply@pipelinesascode.dev"

# Decode and decompress the stored patch
printf '%s' "${PATCH_DATA_B64GZ}" | base64 -d | gunzip > /tmp/fix.patch

# Verify branch hasn't moved since the analysis was run
if [ -n "${expected_sha}" ]; then
    git fetch --depth=1 origin "${pr_branch}"
    current_remote="$(git rev-parse "origin/${pr_branch}")"
    if [ "${current_remote}" != "${expected_sha}" ]; then
        echo "fix: branch has moved (expected ${expected_sha}, got ${current_remote}), aborting" >&2
        jq -n \
            --arg expected "${expected_sha}" \
            --arg current "${current_remote}" \
            '{status: "error", backend: "patch-apply", model: "", duration_ms: 0, error: {provider: "patch-apply", type: "branch_moved", message: ("Branch has advanced since analysis (expected " + $expected + ", got " + $current + "). Please re-run analysis on the latest commit."), retryable: false}}' > /tmp/fix-envelope.json
        emit_envelope /tmp/fix-envelope.json
        exit 1
    fi
fi

# Validate and apply the stored patch
if ! git apply --check /tmp/fix.patch 2>/tmp/apply_check_err.txt; then
    echo "fix: patch validation failed: $(cat /tmp/apply_check_err.txt)" >&2
    echo "fix: first 30 lines of decoded patch:" >&2
    head -30 /tmp/fix.patch >&2
    jq -n \
        '{status: "error", backend: "patch-apply", model: "", duration_ms: 0, error: {provider: "patch-apply", type: "patch_apply_failed", message: "The stored patch could not be applied cleanly. The branch may have diverged from the analyzed commit. Please re-run analysis.", retryable: false}}' > /tmp/fix-envelope.json
    emit_envelope /tmp/fix-envelope.json
    exit 1
fi
if git apply --index --3way /tmp/fix.patch; then
    git add -A
    changed_files="$(git diff --cached --name-only)"
    cat > /tmp/fix-commit-message.txt << EOF
fix: apply AI fix from ${role_name}

Pipelines-as-Code applied the AI-generated patch for role "${role_name}".
Analyzed commit: ${expected_sha:-unknown}

Files changed:
${changed_files}
EOF
    git commit -F /tmp/fix-commit-message.txt
    git push origin "HEAD:${pr_branch}"

    commit_sha="$(git rev-parse HEAD)"
    commit_short_sha="$(git rev-parse --short HEAD)"

    jq -n \
        --arg content "Stored patch applied and pushed to branch ${pr_branch} (${commit_short_sha}).

Files modified:
${changed_files}" \
        --arg commit_sha "${commit_sha}" \
        --arg commit_short_sha "${commit_short_sha}" \
        --arg branch "${pr_branch}" \
        --arg changed_files "${changed_files}" \
        '{status: "success", backend: "patch-apply", model: "", content: $content, tokens_used: 0, duration_ms: 0, metadata: {commit_sha: $commit_sha, commit_short_sha: $commit_short_sha, branch: $branch, changed_files: $changed_files}}' > /tmp/fix-envelope.json
    emit_envelope /tmp/fix-envelope.json
else
    echo "fix: git apply failed" >&2
    jq -n \
        '{status: "error", backend: "patch-apply", model: "", duration_ms: 0, error: {provider: "patch-apply", type: "patch_apply_failed", message: "The stored patch could not be applied cleanly. The branch may have diverged from the analyzed commit. Please re-run analysis.", retryable: false}}' > /tmp/fix-envelope.json
    emit_envelope /tmp/fix-envelope.json
    exit 1
fi
