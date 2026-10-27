# GitHub credential service POC

This directory holds a proof of concept for
[KONFLUX-16493](https://redhat.atlassian.net/browse/KONFLUX-16493). A
private-repository build that waits longer than the one-hour lifetime of
its GitHub App token fails to clone, because nothing renews the token in its
git auth Secret.

The `pipelines-as-code-credentials` binary (`cmd/pipelines-as-code-credentials`,
`pkg/credentials`) owns those Secrets for one GitHub App installation, one
repository and one output namespace. It mints `contents: read` tokens scoped
to that repository, records GitHub's expiry, renews `RENEW_BEFORE` ahead of
it (15 minutes by default), attaches the Secret to the intended PipelineRun,
stops renewing once that build and its finally tasks have finished, and
deletes the Secret and request once the build is deleted.

Nothing here is part of the standard installation. Apply it explicitly.

## How the proof stays short

GitHub installation tokens always last one hour, so the proof does not wait
for a real expiry:

- The service runs with `RENEW_BEFORE=57m`, so it renews about three
  minutes after each mint.
- `hack/token.sh revoke` revokes the original token. GitHub then answers
  401 for it, as it does for an expired token. The service itself never
  revokes tokens.
- Builds wait in a pending state: created with `PipelineRunPending` for the
  standalone check, and queued by PaC's own `concurrency_limit` for the PaC
  check.

`hack/prove-renewal.sh` runs each check and prints PASS or FAIL per step. A
stage takes about 15 minutes. No script prints token values; they compare
short hashes and HTTP status codes.

The clone task is Tekton catalog `git-clone` 0.10, pinned to catalog commit
`112374ce1262b133a77300bd8133445722605e04`. Its clone step copies
`.git-credentials` from the `basic-auth` workspace when the step starts, and
the pod only exists once the build leaves the pending state, so it always
reads the Secret as it is at release time.

## Limits

- A revoked token stands in for an expired one. Natural expiry after an
  hour is not observed by this proof.
- One repository and one output namespace per service instance.
- GitHub App events only. Incoming webhooks have no repository ID, so
  opted-in incoming-webhook runs fail preparation.
- Runs with extra repository token scoping (`secret-github-app-scope-extra-repos`
  or `github_app_token_scope_repos`) are refused, not narrowed.
- No exactly-once launch. If PaC fails after the request is created but
  before the build exists, the request stays unbound and is logged as a
  warning until someone deletes it.
- Credentials already copied into a running container are not refreshed.
- Direct token minting here is a stand-in for Konflux's shared issuer.
- PaC still holds the App key for its own GitHub calls.

## Inputs

Never paste the private key into chat or commit it.

```shell
export GITHUB_REPOSITORY_URL=https://github.com/OWNER/REPO
export GITHUB_REPOSITORY_ID=$(gh api repos/OWNER/REPO --jq .id)
# From https://github.com/settings/installations/<id> (or the org equivalent),
# or the installation.id of a webhook delivery.
export GITHUB_INSTALLATION_ID=...
export REVISION=main
# Only when using a key file instead of PaC's existing App Secret:
export GITHUB_APP_ID=...
export GITHUB_APP_KEY_FILE=/path/to/app.private-key.pem
```

The scripts also need `kubectl`, `jq`, `curl` and `envsubst`. With `gh`
logged in, the PaC stage also checks the GitHub check run status.

## Setup

After `startpaac -a`, `hack/setup.sh` runs steps 2 to 4 and the stage 2
setup in one go. It reads the repository, installation and a GitHub token from
`pass show pac/vars/scratchtest` (`PASS_KEY` overrides it), looks up the
repository ID, and takes the registry from `startpaac --show-config` and the
platform from the Kind node:

```shell
poc/github-credentials/hack/setup.sh all        # or: service, pac
poc/github-credentials/hack/setup.sh tekton     # push the PaC templates to main
eval "$(poc/github-credentials/hack/setup.sh env)"
poc/github-credentials/hack/setup.sh cleanup
```

The manual steps:

1. Create Kind, the registry and PaC from this worktree with startpaac
   (`startpaac -a` from this directory). Docker must be running on the Kind
   host. Note the registry from `startpaac --show-config` as `REGISTRY`, and
   set `KO_DEFAULTPLATFORMS` to the Kind node's platform, for example
   `linux/arm64` (ko builds `linux/amd64` by default).

2. Create the namespace and the service account and roles:

   ```shell
   kubectl apply -f poc/github-credentials/namespace.yaml
   kubectl apply -f poc/github-credentials/service/rbac.yaml
   ```

3. Configure the service. Either reuse PaC's App Secret
   (`pipelines-as-code-secret`, which carries the App ID and key):

   ```shell
   kubectl -n pipelines-as-code create configmap pipelines-as-code-credentials-config \
     --from-literal=GITHUB_INSTALLATION_ID="$GITHUB_INSTALLATION_ID" \
     --from-literal=GITHUB_REPOSITORY_ID="$GITHUB_REPOSITORY_ID" \
     --from-literal=GITHUB_REPOSITORY_URL="$GITHUB_REPOSITORY_URL" \
     --from-literal=OUTPUT_NAMESPACE=pac-credentials-poc \
     --from-literal=RENEW_BEFORE=57m
   ```

   or load a key file into a separate Secret:

   ```shell
   kubectl -n pipelines-as-code create secret generic pipelines-as-code-credentials-key \
     --from-file=github-private-key="$GITHUB_APP_KEY_FILE"
   kubectl -n pipelines-as-code create configmap pipelines-as-code-credentials-config \
     --from-literal=GITHUB_APP_ID="$GITHUB_APP_ID" \
     --from-literal=KEY_SECRET_NAME=pipelines-as-code-credentials-key \
     --from-literal=GITHUB_INSTALLATION_ID="$GITHUB_INSTALLATION_ID" \
     --from-literal=GITHUB_REPOSITORY_ID="$GITHUB_REPOSITORY_ID" \
     --from-literal=GITHUB_REPOSITORY_URL="$GITHUB_REPOSITORY_URL" \
     --from-literal=OUTPUT_NAMESPACE=pac-credentials-poc \
     --from-literal=RENEW_BEFORE=57m
   ```

   `RENEW_BEFORE=57m` is for the proof only; leave it unset (15m) otherwise.
   Set `GITHUB_API_URL` too for GitHub Enterprise.

4. Deploy the service:

   ```shell
   KO_DOCKER_REPO=$REGISTRY ko apply -B --sbom=none --insecure-registry \
     -f poc/github-credentials/service/deployment.yaml
   kubectl -n pipelines-as-code rollout status deploy/pipelines-as-code-credentials
   ```

## Stage 1: the service on its own

```shell
poc/github-credentials/hack/prove-renewal.sh standalone
```

The script:

1. Creates `examples/request.yaml` and waits for the request to be `ready`
   with a Secret whose expiry is about an hour ahead.
2. Copies the Secret into an unmanaged baseline Secret.
3. Creates the managed and baseline builds from `examples/private-clone.yaml`,
   both pending. The request becomes `bound` and the Secret gets an owner
   reference to the managed build.
4. Waits for the scheduled renewal: a new token, a later expiry, and the
   baseline still holding the original token.
5. Restarts the service before the renewal point (no new token), then stops
   it across the next renewal point and starts it again (renewed within a
   minute).
6. Revokes the original token from the baseline Secret. GitHub returns 401
   for it and 200 for the managed token, which only lists the configured
   repository.
7. Releases both builds. The managed clone succeeds on its first attempt;
   the baseline clone fails with an authentication error. Both clone pods
   start after the revocation.
8. Checks that the request stays `bound` while the finally task runs, turns
   `completed` afterwards, and gets no further renewal. Deleting the build
   removes the Secret and request.
9. Creates a request for another repository ID and checks that it is
   `rejected` with no Secret.

## Stage 2: PaC handoff

1. Let the PaC controller submit requests, opt the repository in, and create
   its Repository CR with `concurrency_limit: 1`:

   ```shell
   kubectl apply -f poc/github-credentials/pac/controller-rbac.yaml
   kubectl -n pipelines-as-code set env deploy/pipelines-as-code-controller \
     PAC_GITHUB_CREDENTIALS_SERVICE=true \
     PAC_GITHUB_CREDENTIALS_SERVICE_REPOSITORIES="$GITHUB_REPOSITORY_URL"
   envsubst '${GITHUB_REPOSITORY_URL}' < poc/github-credentials/pac/repository.yaml \
     | kubectl apply -f -
   ```

   `PAC_GITHUB_CREDENTIALS_SERVICE_REPOSITORIES` also accepts `*` or a
   comma-separated list. `PAC_GITHUB_CREDENTIALS_SERVICE_WAIT` (default
   `30s`) and `PAC_GITHUB_CREDENTIALS_SERVICE_NAMESPACE` (default the PaC
   namespace) are optional. Running `startpaac -p` again drops these
   variables. The App must be installed on the repository with no extra
   repository token scoping.

2. Copy `pac/tekton/blocker.yaml` and `pac/tekton/private-clone.yaml` to
   `.tekton/` on the test repository's `main` branch.

3. Run the check and follow its prompts:

   ```shell
   poc/github-credentials/hack/prove-renewal.sh pac
   ```

   Push a commit that changes a file under `hold/`. The blocker build takes
   the only concurrency slot. Then push a commit outside `hold/`. PaC submits
   the request, waits for credentials, creates `private-clone-*` with a fixed
   name, and queues it behind the blocker.

   The script checks the request, owner annotation, managed Secret and
   queued GitHub check. It then waits for a renewal, revokes the original
   token and cancels the blocker. PaC starts the queued build, the clone
   succeeds, GitHub shows success, the request completes, and deleting the
   build removes the Secret and request.

4. Check that PaC refuses to launch without credentials:

   ```shell
   poc/github-credentials/hack/prove-renewal.sh pac-unavailable
   ```

   The script stops the service and asks for a push outside `hold/`. PaC
   reports a failure after 30 seconds and creates no build. The script then
   restarts the service and deletes the abandoned request and its Secret,
   which is the expected manual step.

5. Optional legacy check: remove the opt-in variables
   (`kubectl -n pipelines-as-code set env deploy/pipelines-as-code-controller
   PAC_GITHUB_CREDENTIALS_SERVICE-`) and push again. PaC creates its own
   `pac-gitauth-*` Secret with no credential service label.

## Inspecting by hand

These print state, expiry, resource versions and owners, never token data.

```shell
req() { kubectl -n pipelines-as-code get cm "$1" -o json | jq '.metadata.annotations
  | with_entries(select(.key | startswith("pipelinesascode.tekton.dev/credential")))'; }
sec() { kubectl -n pac-credentials-poc get secret "$1" -o json | jq '{
  resourceVersion: .metadata.resourceVersion,
  expiresAt: .metadata.annotations["pipelinesascode.tekton.dev/credential-expires-at"],
  owners: [.metadata.ownerReferences[]? | .kind + "/" + .name]}'; }
svclogs() { kubectl -n pipelines-as-code logs deploy/pipelines-as-code-credentials "$@"; }
poc/github-credentials/hack/token.sh check pac-credentials-poc SECRET   # "<status> <repos>"
```

Useful log lines: `credentials renewed`, `request bound to PipelineRun`,
`build execution finished, renewal stopped`, `bound PipelineRun deleted,
removed credentials and request`, `rejecting credential request`.

## Results to send back

- The full output of each `prove-renewal.sh` run.
- `svclogs` covering the runs.
- For stage 2, the PaC controller logs around the two pushes.

## Cleanup

```shell
kubectl delete -f poc/github-credentials/service/deployment.yaml --ignore-not-found
kubectl -n pipelines-as-code delete cm -l pipelinesascode.tekton.dev/github-credential-request=true
kubectl -n pipelines-as-code delete cm pipelines-as-code-credentials-config --ignore-not-found
kubectl -n pipelines-as-code delete secret pipelines-as-code-credentials-key --ignore-not-found
kubectl delete -f poc/github-credentials/pac/controller-rbac.yaml --ignore-not-found
kubectl -n pipelines-as-code set env deploy/pipelines-as-code-controller \
  PAC_GITHUB_CREDENTIALS_SERVICE- PAC_GITHUB_CREDENTIALS_SERVICE_REPOSITORIES-
kubectl delete -f poc/github-credentials/service/rbac.yaml --ignore-not-found
kubectl delete -f poc/github-credentials/namespace.yaml
```
