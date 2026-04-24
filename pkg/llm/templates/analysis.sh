#!/bin/sh
set -eu

prompt_file="/tmp/prompt.txt"
result_file="$(results.analysis.path)"
backend="${LLM_BACKEND}"
model="${LLM_MODEL}"
max_tokens="${LLM_MAX_TOKENS}"
timeout_secs="${LLM_TIMEOUT_SECONDS:-840}"

printf '%s' "${LLM_PROMPT_B64}" | base64 -d >"${prompt_file}"

for _skills_dir in ".claude/skills" ".agents/skills"; do
	[ -d "${_skills_dir}" ] || continue
	printf '\n\n## Project Skills (from %s)\n\n' "${_skills_dir}" >>"${prompt_file}"
	find "${_skills_dir}" -maxdepth 1 -type f | sort | while IFS= read -r _skill_file; do
		printf '### %s\n\n' "$(basename "${_skill_file}")" >>"${prompt_file}"
		cat "${_skill_file}" >>"${prompt_file}"
		printf '\n\n' >>"${prompt_file}"
	done
done

prompt_bytes=$(wc -c <"${prompt_file}")
echo "analysis: backend=${backend} model=${model} prompt_size=${prompt_bytes}B timeout=${timeout_secs}s" >&2

run_backend() {
	set -- # clear positional args

	case "${backend}" in
	codex) set -- codex exec ;;
	claude | claude-vertex) set -- claude --print --dangerously-skip-permissions --bare --max-turns 1 --tools "" ;;
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
{ run_backend 2>&1; echo $? > /tmp/analysis_exit_code.txt; } | tee /tmp/analysis_output.txt
set -e
finished_at="$(date +%s)"
exit_code=$(cat /tmp/analysis_exit_code.txt)
output="$(cat /tmp/analysis_output.txt)"
duration_ms=$(((finished_at - started_at) * 1000))

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
