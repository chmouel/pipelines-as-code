#!/usr/bin/env bash
# Copyright 2024 Chmouel Boudjnah <chmouel@chmouel.com>
set -eufo pipefail
NS=registry
fpath=$(dirname "$0")
# shellcheck disable=SC1091
source "${fpath}"/../common.sh

if [[ ${1:-""} == "-r" ]]; then
	kubectl delete ns ${NS} || true
fi

kubectl create namespace ${NS} 2>/dev/null || true

{ helm repo list | grep -q twun.io; } || helm repo add twuni https://helm.twun.io
[[ -z $(helm status -n ${NS} docker-registry) ]] &&
	helm install --wait --set garbageCollect.enabled=true docker-registry twuni/docker-registry --namespace ${NS}
create_ingress ${NS} docker-registry registry.civuole.local 5000

show_step "Add annotations to the ingress controller"
for annotations in "nginx.ingress.kubernetes.io/proxy-body-size=0" \
	"nginx.ingress.kubernetes.io/proxy-read-timeout=600" \
	"nginx.ingress.kubernetes.io/proxy-send-timeout=600" \
	"kubernetes.io/tls-acme=true"; do
	kubectl annotate ingress -n ${NS} docker-registry "${annotations}"
done