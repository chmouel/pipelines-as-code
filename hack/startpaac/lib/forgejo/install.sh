#!/usr/bin/env bash
# Copyright 2024 Chmouel Boudjnah <chmouel@chmouel.com>
set -eufo pipefail
NS=forgejo
fpath=$(dirname "$0")
# shellcheck disable=SC1091
source "${fpath}"/../common.sh
FORGE_HOST=${FORGE_HOST:-gitea.civuole.local}
forge_secret_name=forge-tls

kubectl create namespace ${NS} 2>/dev/null || true
create_tls_secret $FORGE_HOST ${forge_secret_name} ${NS}

helm install --wait -f ${fpath}/values.yaml \
	--set ingress.hosts[0].host=${FORGE_HOST} \
	--set ingress.tls[0].hosts[0]=${FORGE_HOST} \
	--set ingress.tls[0].secretName=${forge_secret_name} \
	--create-namespace -n ${NS} forgejo oci://codeberg.org/forgejo-contrib/forgejo
