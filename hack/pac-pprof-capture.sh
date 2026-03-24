#!/usr/bin/env bash

set -euo pipefail

usage() {
	cat <<'EOF'
Capture pprof profiles from a PAC controller or watcher pod.

Usage:
  hack/pac-pprof-capture.sh [-n namespace] [-c component] [-p local_port] [-s seconds] [-o output_dir] [-l label]

Options:
  -n namespace   Kubernetes namespace. Default: pipelines-as-code
  -c component   PAC component: watcher or controller. Default: watcher
  -p local_port  Local port for kubectl port-forward. Default: 6060
  -s seconds     CPU profile duration in seconds. Default: 30
  -o output_dir  Output directory. Default: tmp/pprof-<component>-<timestamp>
  -l label       Optional filename label, e.g. baseline or after-load
  -h             Show this help

Requirements:
  - PAC_ENABLE_PPROF=true must be enabled on the target deployment
  - PAC_PPROF_ADDR should typically be 127.0.0.1:6060 in the pod
  - kubectl and go must be available in PATH
EOF
}

namespace="pipelines-as-code"
component="watcher"
local_port="6060"
seconds="30"
output_dir=""
label=""

while getopts ":n:c:p:s:o:l:h" opt; do
	case "${opt}" in
		n) namespace="${OPTARG}" ;;
		c) component="${OPTARG}" ;;
		p) local_port="${OPTARG}" ;;
		s) seconds="${OPTARG}" ;;
		o) output_dir="${OPTARG}" ;;
		l) label="${OPTARG}" ;;
		h)
			usage
			exit 0
			;;
		:)
			echo "missing argument for -${OPTARG}" >&2
			usage >&2
			exit 1
			;;
		\?)
			echo "unknown option: -${OPTARG}" >&2
			usage >&2
			exit 1
			;;
	esac
done

case "${component}" in
	watcher|controller) ;;
	*)
		echo "component must be watcher or controller, got: ${component}" >&2
		exit 1
		;;
esac

if [[ -z "${output_dir}" ]]; then
	timestamp="$(date +%Y%m%d-%H%M%S)"
	output_dir="tmp/pprof-${component}-${timestamp}"
fi

mkdir -p "${output_dir}"

deploy="pipelines-as-code-${component}"
base_url="http://127.0.0.1:${local_port}/debug/pprof"
suffix=""
if [[ -n "${label}" ]]; then
	suffix="-${label}"
fi

pf_log="${output_dir}/port-forward.log"

echo "starting port-forward to deploy/${deploy} in namespace ${namespace}"
kubectl -n "${namespace}" port-forward "deploy/${deploy}" "${local_port}:6060" >"${pf_log}" 2>&1 &
pf_pid=$!

cleanup() {
	if kill -0 "${pf_pid}" >/dev/null 2>&1; then
		kill "${pf_pid}" >/dev/null 2>&1 || true
		wait "${pf_pid}" >/dev/null 2>&1 || true
	fi
}
trap cleanup EXIT

echo "waiting for ${base_url}/ to become reachable"
ready=0
for _ in $(seq 1 30); do
	if curl -fsS "${base_url}/" >/dev/null; then
		ready=1
		break
	fi
	sleep 1
done

if [[ "${ready}" -ne 1 ]]; then
	echo "pprof endpoint did not become reachable; check ${pf_log}" >&2
	exit 1
fi

echo "capturing profiles into ${output_dir}"
curl -fsS -o "${output_dir}/index${suffix}.html" "${base_url}/"
curl -fsS -o "${output_dir}/heap${suffix}.pb.gz" "${base_url}/heap?gc=1"
curl -fsS -o "${output_dir}/goroutine${suffix}.pb.gz" "${base_url}/goroutine"
curl -fsS -o "${output_dir}/allocs${suffix}.pb.gz" "${base_url}/allocs"
curl -fsS -o "${output_dir}/block${suffix}.pb.gz" "${base_url}/block"
curl -fsS -o "${output_dir}/mutex${suffix}.pb.gz" "${base_url}/mutex"
curl -fsS -o "${output_dir}/threadcreate${suffix}.pb.gz" "${base_url}/threadcreate"
curl -fsS -o "${output_dir}/cpu${suffix}.pb.gz" "${base_url}/profile?seconds=${seconds}"

cat <<EOF
done

Profiles:
  ${output_dir}/heap${suffix}.pb.gz
  ${output_dir}/cpu${suffix}.pb.gz
  ${output_dir}/goroutine${suffix}.pb.gz
  ${output_dir}/allocs${suffix}.pb.gz
  ${output_dir}/block${suffix}.pb.gz
  ${output_dir}/mutex${suffix}.pb.gz
  ${output_dir}/threadcreate${suffix}.pb.gz

Inspect:
  go tool pprof -top ${output_dir}/heap${suffix}.pb.gz
  go tool pprof -http=:0 ${output_dir}/heap${suffix}.pb.gz
  go tool pprof -http=:0 ${output_dir}/cpu${suffix}.pb.gz

Compare two heap snapshots:
  go tool pprof -base baseline.pb.gz after.pb.gz
EOF
