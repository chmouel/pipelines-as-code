#!/bin/sh
set -eu

prompt_file="/tmp/prompt.txt"
result_file="$(results.analysis.path)"
backend="${LLM_BACKEND}"
model="${LLM_MODEL}"
max_tokens="${LLM_MAX_TOKENS}"
timeout_secs="${LLM_TIMEOUT_SECONDS:-840}"
repo_dir="${LLM_REPO_DIR:-$(pwd)}"

cat > /tmp/parse_stream.py << 'PYEOF'
import json
import sys

def main():
    result_text = ""
    exit_code = 0
    collected_text = []
    block_types = {}

    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            event = json.loads(line)
        except (json.JSONDecodeError, ValueError):
            continue

        etype = event.get("type")

        if etype == "stream_event":
            inner = event.get("event", {})
            inner_type = inner.get("type")

            if inner_type == "content_block_start":
                block = inner.get("content_block", {})
                idx = inner.get("index", 0)
                block_types[idx] = block.get("type")
                if block_types[idx] == "thinking":
                    sys.stderr.write("\n--- thinking ---\n")
                    sys.stderr.flush()

            elif inner_type == "content_block_delta":
                delta = inner.get("delta", {})
                delta_type = delta.get("type")
                if delta_type == "thinking_delta":
                    sys.stderr.write(delta.get("thinking", ""))
                    sys.stderr.flush()
                elif delta_type == "text_delta":
                    text = delta.get("text", "")
                    sys.stderr.write(text)
                    sys.stderr.flush()
                    collected_text.append(text)

            elif inner_type == "content_block_stop":
                idx = inner.get("index", 0)
                if block_types.get(idx) == "thinking":
                    sys.stderr.write("\n--- end thinking ---\n\n")
                    sys.stderr.flush()
                block_types.pop(idx, None)

        elif etype == "result":
            if event.get("is_error"):
                exit_code = 1
            result_text = event.get("result", "")

    if not result_text and collected_text:
        result_text = "".join(collected_text)

    sys.stdout.write(result_text)
    sys.exit(exit_code)

if __name__ == "__main__":
    main()
PYEOF

use_stream_json=false
case "${backend}" in
claude|claude-vertex)
	if command -v python3 >/dev/null 2>&1 && claude --help 2>&1 | grep -q 'stream-json'; then
		use_stream_json=true
	fi
	;;
esac

printf '%s' "${LLM_PROMPT_B64}" | base64 -d >"${prompt_file}"

# shellcheck disable=SC2016
{
	printf '\n\n## Repository Checkout\n\n'
	printf 'The repository checkout is at `%s` and the analysis command runs from that directory.\n' "${repo_dir}"
	printf 'When you identify a safe concrete fix, edit files in this checkout. Do not only provide a diff snippet.\n'
	printf 'Do not commit or push changes. The runner captures `git diff` after you finish.\n\n'
	printf 'This task is running inside Pipelines-as-Code CI in a Kubernetes pod.\n'
	printf 'Environment variables `CI=true`, `PAC_LLM_EXECUTION_CONTEXT=ci`, and `PAC_LLM_PIPELINERUN_KIND=analysis` are available to project skills and scripts.\n'
	printf 'If a project skill says it should always run during CI-based review or pipeline failure investigation, and the current task matches, run it.\n'
	printf 'In the final response, include a "Skills used" section that names each relevant project skill and states whether it was executed, skipped, or blocked, with a short reason.\n\n'
} >>"${prompt_file}"

for _skills_dir in ".claude/skills" ".agents/skills"; do
	[ -d "${_skills_dir}" ] || continue
	printf '\n\n## Project Skills (from %s)\n\n' "${_skills_dir}" >>"${prompt_file}"
	# Support flat files (e.g. .claude/skills/foo.md) and subdirectory convention
	# (e.g. .claude/skills/foo/SKILL.md used by Claude Code).
	{ find "${_skills_dir}" -maxdepth 1 -type f;
	  find "${_skills_dir}" -mindepth 2 -maxdepth 2 -name "SKILL.md" -type f; } | sort | \
	while IFS= read -r _skill_file; do
		_skill_name="$(basename "${_skill_file}")"
		_skill_name="${_skill_name%.md}"
		if [ "${_skill_name}" = "SKILL" ]; then
			_skill_name="$(basename "$(dirname "${_skill_file}")")"
		fi
		{
			printf '### %s\n\n' "${_skill_name}"
			cat "${_skill_file}"
			printf '\n\n'
		} >>"${prompt_file}"
	done
done

# Append machine-patch protocol instructions. The backend may edit the checkout;
# this script captures the resulting git diff and stores it as the machine patch.
cat >>"${prompt_file}" << 'PATCHINSTRUCTIONS'

---

## Machine Patch

Your task includes producing a reusable patch when the issue has a clean,
concrete fix. Inspect the checked-out repository and modify the repository files
directly. Do not commit or push changes. The surrounding PipelineRun script will
capture `git diff` after you finish and store it as the machine-readable patch.

Rules:
- Leave the working tree unchanged if you are not confident or the fix involves multiple steps.
- Do not modify files for broad refactors or binary files.
- Do not print patch markers or raw diff text in your response.
- Do not say you cannot modify files unless you first inspect the repository checkout.
- Explain the root cause, cite the supporting evidence, and describe the intended fix in normal prose.
- Describe the fix as a proposed pull request change, not as an already deployed or verified runtime outcome.

PATCHINSTRUCTIONS

prompt_bytes=$(wc -c <"${prompt_file}")
echo "analysis: backend=${backend} model=${model} prompt_size=${prompt_bytes}B timeout=${timeout_secs}s" >&2
echo "analysis: repository checkout=${repo_dir} pwd=$(pwd)" >&2
git config --global --add safe.directory "${repo_dir}" 2>/dev/null || true
git config --global --add safe.directory "${repo_dir}/" 2>/dev/null || true
if git -C "${repo_dir}" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
	echo "analysis: repository head=$(git -C "${repo_dir}" rev-parse --short HEAD 2>/dev/null || true)" >&2
	echo "analysis: repository files=$(find "${repo_dir}" -mindepth 1 -maxdepth 2 -not -path "${repo_dir}/.git/*" | head -20 | wc -l)" >&2
else
	echo "analysis: repository checkout is not a git worktree" >&2
fi

run_backend() {
	set -- # clear positional args

	case "${backend}" in
	codex) set -- codex exec ;;
	claude | claude-vertex)
		if [ "${use_stream_json}" = "true" ]; then
			set -- claude --print --dangerously-skip-permissions --bare --verbose --output-format stream-json --include-partial-messages --add-dir "${repo_dir}"
		else
			set -- claude --print --dangerously-skip-permissions --bare --add-dir "${repo_dir}"
		fi
		;;
	gemini) set -- gemini ;;
	opencode) set -- opencode run --dangerously-skip-permissions ;;
	*)
		echo "unsupported backend: ${backend}" >&2
		return 1
		;;
	esac

	if [ -n "${model}" ]; then
		set -- "$@" --model "${model}"
	fi
	if [ -n "${max_tokens}" ] && [ "${max_tokens}" != "0" ]; then
		case "${backend}" in
		claude | claude-vertex | opencode) ;; # these CLIs do not support max tokens flag
		*) set -- "$@" --max-tokens "${max_tokens}" ;;
		esac
	fi

	case "${backend}" in
	opencode) timeout "${timeout_secs}" "$@" -- "$(cat "${prompt_file}")" ;;
	*) timeout "${timeout_secs}" "$@" <"${prompt_file}" ;;
	esac
}

started_at="$(date +%s)"
set +e
if [ "${use_stream_json}" = "true" ]; then
	{ run_backend 2>/tmp/claude_stderr.txt; echo $? > /tmp/analysis_exit_code.txt; } | python3 /tmp/parse_stream.py > /tmp/analysis_output.txt
	parser_exit=$?
else
	{ run_backend 2>&1; echo $? > /tmp/analysis_exit_code.txt; } | tee /tmp/analysis_output.txt
	parser_exit=0
fi
set -e
finished_at="$(date +%s)"
exit_code=$(cat /tmp/analysis_exit_code.txt)
if [ "${exit_code}" -eq 0 ] && [ "${parser_exit}" -ne 0 ]; then
	exit_code="${parser_exit}"
fi
output="$(cat /tmp/analysis_output.txt)"
if [ "${use_stream_json}" = "true" ] && [ -z "${output}" ] && [ -s /tmp/claude_stderr.txt ]; then
	output="$(cat /tmp/claude_stderr.txt)"
fi
duration_ms=$(((finished_at - started_at) * 1000))

# Strip any legacy machine patch block from the backend output before building the envelope.
if printf '%s' "${output}" | grep -qF "===LLM_PATCH_BEGIN==="; then
	output="$(printf '%s\n' "${output}" | \
		awk '/^===LLM_PATCH_BEGIN===/{skip=1} !skip{print} /^===LLM_PATCH_END===/{skip=0}')"
	output="$(printf '%s' "${output}" | sed 's/[[:space:]]*$//')"
fi

capture_worktree_diff() {
	repo="${repo_dir:-.}"
	if ! git -C "${repo}" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
		echo "analysis: not inside a git worktree, skipping machine patch" >&2
		return
	fi
	git -C "${repo}" add -N . 2>/dev/null || true
	git -C "${repo}" diff --binary --no-ext-diff
}

reset_worktree() {
	repo="${repo_dir:-.}"
	git -C "${repo}" reset --hard HEAD >/dev/null 2>&1 || true
	git -C "${repo}" clean -fd >/dev/null 2>&1 || true
}

emit_patch_block() {
	clean_diff="$(capture_worktree_diff)"
	reset_worktree
	if [ -z "${clean_diff}" ]; then
		echo "analysis: working tree has no changes, skipping machine patch" >&2
		return
	fi
	encoded="$(printf '%s\n' "${clean_diff}" | gzip | base64 | tr -d '\n')"
	if [ -n "${encoded}" ]; then
		printf '\n===LLM_PATCH_BEGIN===\n'
		printf 'version: 1\n'
		printf 'format: git-diff\n'
		printf 'encoding: gzip+base64\n'
		printf 'base_sha: %s\n' "${LLM_COMMIT_SHA:-}"
		printf 'role: %s\n' "${LLM_ROLE_NAME:-unknown}"
		printf 'chunks: 1\n'
		printf 'chunk: 1/1\n'
		printf '%s\n' "${encoded}"
		printf '===LLM_PATCH_END===\n'
	fi
}

if [ "${exit_code}" -eq 124 ]; then
	echo "===ANALYSIS_BEGIN==="
	jq -n --arg backend "${backend}" \
		--arg model "${model}" \
		--argjson duration_ms "${duration_ms}" \
		--argjson timeout "${timeout_secs}" \
		'{status: "error", backend: $backend, model: $model, duration_ms: $duration_ms, error: {provider: $backend, type: "timeout", message: ("CLI timed out after " + ($timeout | tostring) + "s"), retryable: true}}'
	echo "===ANALYSIS_END==="
	echo '{"status":"complete"}' >"${result_file}"
	echo "analysis: completed in ${duration_ms}ms exit_code=${exit_code}" >&2
	exit 1
elif [ "${exit_code}" -eq 0 ]; then
	echo "===ANALYSIS_BEGIN==="
	jq -n --arg backend "${backend}" \
		--arg model "${model}" \
		--arg content "${output}" \
		--argjson duration_ms "${duration_ms}" \
		'{status: "success", backend: $backend, model: $model, content: $content, tokens_used: 0, duration_ms: $duration_ms}'
	echo "===ANALYSIS_END==="
	emit_patch_block
	echo '{"status":"complete"}' >"${result_file}"
else
	echo "===ANALYSIS_BEGIN==="
	jq -n --arg backend "${backend}" \
		--arg model "${model}" \
		--argjson duration_ms "${duration_ms}" \
		--arg message "${output}" \
		'{status: "error", backend: $backend, model: $model, duration_ms: $duration_ms, error: {provider: $backend, type: "cli_error", message: $message, retryable: false}}'
	echo "===ANALYSIS_END==="
	echo '{"status":"complete"}' >"${result_file}"
	echo "analysis: completed in ${duration_ms}ms exit_code=${exit_code}" >&2
	exit "${exit_code}"
fi
echo "analysis: completed in ${duration_ms}ms exit_code=${exit_code}" >&2
