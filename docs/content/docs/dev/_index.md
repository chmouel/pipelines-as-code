---
title: Contributor Resources
weight: 15
---

# Want to Help Build Pipelines-as-Code? Awesome!

So you're thinking about contributing to Pipelines-as-Code? That's fantastic! We really appreciate your interest.  Here's a quick guide to get you up and running in our development world.

## First Things First: Code of Conduct

Seriously, please read our Code of Conduct. It's not just legal mumbo jumbo; it's how we keep things friendly and productive for everyone. You can find it right here: <https://github.com/openshift-pipelines/pipelines-as-code/blob/main/code-of-conduct.md>

## Get Your Dev Environment Ready with `make dev`

For local development, we've got this super handy "all-in-one" setup using `kind` (Kubernetes IN Docker).  Think of it as your personal Kubernetes playground, all tucked away in Docker.  Starting it up is a breeze:

```shell
make dev
```

Once that command is done doing its magic, you'll have a complete development environment spun up in your `kind` cluster, including:

- A `kind` Kubernetes cluster, naturally.
- An internal Docker registry. This is where `ko` (more on that later) pushes your built images.
- An ingress controller (nginx).  This is like a traffic cop for your cluster, directing web requests.
- Tekton Pipelines and the Tekton Dashboard, all installed and accessible via an ingress route.  Basically, Tekton is the engine that powers Pipelines-as-Code, and the Dashboard gives you a visual peek into what's going on.
- Pipelines-as-Code itself, deployed straight from your local code using `ko`.  This means you're testing the very code you're working on!
- Gitea – a lightweight Git service – running locally. We use this for our end-to-end (E2E) tests because it's got the most comprehensive test suite.

By default, `make dev` assumes your Pipelines-as-Code code lives in `$GOPATH/src/github.com/openshift-pipelines/pipelines-as-code`.  If it's somewhere else, no worries! Just tell it where to look by setting the `PAC_DIRS` environment variable.

-  URLs to check out once it's up and running (using nip.io, which is pretty neat for local testing):

  - Controller: <http://controller.paac-127-0-0-1.nip.io>
  - Dashboard: <http://dashboard.paac-127-0-0-1.nip.io>

-  **Secrets time!** You'll need to create a Kubernetes secret for your GitHub App credentials.  If you're using `pass` (the password store command-line tool – highly recommended!), you can point `make dev` to a folder containing your `github-application-id`, `github-private-key`, and `webhook.secret` files.  These are the secrets you set up when you created your GitHub Application. Just set the `PAC_PASS_SECRET_FOLDER` environment variable.

  For example, if you keep your GitHub app secrets under `github-app/` in pass:

  ```shell
  pass insert github-app/github-application-id
  pass insert github-app/webhook.secret
  pass insert -m github-app/github-private-key
  ```

- Need to redeploy just Pipelines-as-Code itself?  Easy peasy. You've got a few options:

  ```shell
  ./hack/dev/kind/install.sh -p
  ```

  or the shorter `make` version:

  ```shell
  make rdev
  ```

  Or, if you're feeling adventurous and want to use `ko` directly:

  ```shell
  env KO_DOCKER_REPO=localhost:5000 ko apply -f ${1:-"config"} -B
  ```

-  There are a few more flags you can use with `./hack/dev/kind/install.sh`:

  - `-b`:  Just creates the `kind` cluster, nginx ingress, and Docker image build setup – skips the rest.
  - `-r`: Installs Pipelines-as-Code from the latest stable release (you can override which release with the `PAC_RELEASE` environment variable) instead of building from your local code using `ko`.
  - `-c`: Only configures Pipelines-as-Code (creates secrets, ingress, etc.) – assumes it's already installed.

-  For all the nitty-gritty details and more flags, run:  `./hack/dev/kind/install.sh -h`.  It's got all the info!

## Gitea: Our "Unofficially" Official Sidekick

We "unofficially" support Gitea.  Basically, if you know how to set up webhooks for other Git providers, you're golden with Gitea too.  Just configure it with a token, just like you would for, say, GitHub or GitLab.

Here's a sample Kubernetes setup – Namespace, Repository CRD, and Secret – for Gitea (you'll need to fill in the blanks):

```yaml
---
apiVersion: v1
kind: Namespace
metadata:
  name: gitea

---
apiVersion: "pipelinesascode.tekton.dev/v1alpha1"
kind: Repository
metadata:
  name: gitea
  namespace: gitea
spec:
  url: "https://gitea.my.com/owner/repo"
  git_provider:
    user: "git"
    url: "Your gitea installation URL, i.e: https://gitea.my.com/"
    secret:
      name: "secret"
      key: token
    webhook_secret:
      name: "secret"
      key: "webhook"
---
apiVersion: v1
kind: Secret
metadata:
  name: gitea-home-chmouel
  namespace: gitea
type: Opaque
stringData:
  token: "your token has generated in gitea"
  webhook: "" # make sure it's empty when you set this up on the interface and here
```

One little thing to watch out for with Gitea: webhook validation secrets. Pipelines-as-Code is smart enough to know when you're using Gitea and lets you get away with an empty webhook secret (usually, we require one).

The `install.sh` script we talked about earlier? It actually spins up a fresh Gitea instance for you to play with and run our Gitea E2E tests.  Pretty handy, right?

To make the Gitea webhook work, you'll need a Hook URL. You can generate one at <https://hook.pipelinesascode.com/new> and then stick it into the `TEST_GITEA_SMEEURL` environment variable.

By default, our Gitea setup uses:

- URL: <https://localhost:3000/>
- Admin Username: pac
- Admin Password: pac

The E2E tests are clever – they'll automatically create a new repo for each test using that admin user.

## Debugging Those Tricky E2E Tests

If you've got your secrets all set up correctly, running the E2E tests should be smooth sailing. Gitea tests are the easiest to run since they're self-contained. For other Git providers, you'll need to set up a few environment variables.

Take a peek at our [e2e on kind workflow](https://github.com/openshift-pipelines/pipelines-as-code/blob/8f990bf5f348f6529deaa3693257907b42287a35/.github/workflows/kind-e2e-tests.yaml#L90) – it shows you all the environment variables we use for each provider.  It's a great reference!

By default, the E2E tests are tidy and clean up after themselves when they're done.  But, if you want to keep the Pull/Merge Request open and the namespace around for debugging, just set the `TEST_NOCLEANUP` environment variable to `true`.

## Diving into Controller Debugging

Want to get down and dirty with debugging the Pipelines-as-Code controller itself? Here's the scoop:

First, snag yourself a [hook](https://hook.pipelinesascode.com) URL.  Then, point your Git provider's webhook to that URL.  Now, use this cool tool called [gosmee](https://github.com/chmouel/gosmee) to forward those webhook requests from GitHub (or GitLab, etc.) to your locally running controller.  You can run your controller either directly in your debugger or inside that `kind` cluster we set up earlier.

`gosmee` has a neat trick: it can save webhook replays to a directory using `--saveDir /tmp/save`.  If you look in that directory, you'll find a shell script that lets you replay a specific webhook request directly to your controller – super handy for testing without triggering a whole new Git event!

For watching controller logs in style, check out [snazy](https://github.com/chmouel/snazy). It's designed to make logs easier to read, especially for Pipelines-as-Code, adding helpful context like which Git provider is involved.

![snazy screenshot](/images/pac-snazy.png)

##  Makefile Magic: Your Command-Line Toolkit

We've got a bunch of handy shortcuts in our `Makefile`.  To see what's available, just run:

```shell
make help
```

For example, to run Go tests and linting, it's as simple as:

```shell
make test lint-go
```

If you're adding new CLI commands with help text, you'll need to update the "golden files" (don't worry too much about what those are right now):

```shell
make update-golden
```

##  Pre-Push Git Checks: Keeping Things Shipshape

We're pretty serious about code quality and documentation around here. We use pre-commit hooks to help make sure everything you contribute is top-notch.  These checks run *before* you even push your code, catching potential issues early.

First, you gotta install pre-commit:

<https://pre-commit.com/>

It's probably available as a package for your system (like on Fedora or via Brew), or you can install it with `pip`.

Once pre-commit is installed, set it up in your Pipelines-as-Code repo:

```shell
pre-commit install
```

This sets up a bunch of "hooks" that will run on your changed files whenever you try to `git push`. If something fails a check, pre-commit will let you know.

Need to skip the checks for some reason? You can bypass them with:

```shell
git push --no-verify
```

Or, if you just want to skip a specific hook (say, `lint-md`), you can use the `SKIP` environment variable:

```shell
SKIP=lint-md git push
```

Want to manually run all the pre-commit checks on everything?  Just run:

```shell
make pre-commit
```

##  Docs are King (and Queen!): Developing Documentation

Documentation is super important to us.  If you're adding a new feature or changing how something works, including documentation in your Pull Request is usually a must.

We use [Hugo](https://gohugo.io) to build our website.  If you want to preview your doc changes locally while you're working, run this:

```shell
make dev-docs
```

This will download the same version of Hugo we use to build [pipelinesascode.com](https://pipelinesascode.com) (which is hosted on Cloudflare Pages) and start a local Hugo server with live previews at:

<https://localhost:1313>

When we release a new version, Cloudflare Pages automatically rebuilds the docs.

By default, [pipelinesascode.com](https://pipelinesascode.com) shows the "stable" documentation.  If you want to see the latest docs from the `main` branch (the "dev" docs), head over to:

<https://main.pipelines-as-code.pages.dev>

There's a dropdown at the bottom of the page where you can switch to older major versions of the documentation too.

##  Documentation During Releases

We have a whole process for releases, and documentation is part of it! You can read about it here: [release-process]({{< relref "/docs/dev/release-process.md" >}})

## Keeping Dependencies Fresh: Updating in Pipelines-as-Code

### Go Modules: Staying Up-to-Date

We try to keep our Go dependencies as up-to-date as possible, as long as they play nicely with the version of Tekton Pipelines that OpenShift Pipelines Operator ships with (we like to be a bit cautious there).

Whenever you update Go modules, it's a good idea to check if you can remove any `replace` directives in `go.mod`. These are used to pin dependencies to specific versions or commits, and we want to avoid them if we can, or at least match them to the Tekton Pipelines version if needed.

Here's how to update Go modules:

```shell
go get -u ./...
make vendor
```

Next, go to <https://github.com/google/go-github> and see what the latest Go version is (e.g., v59).  Then, open a Go file that uses the `go-github` library (like `pkg/provider/github/detect.go`) and check the *old* version (e.g., v56).

Run this `sed` command to update the import paths in your code:

```shell
find -name '*.go'|xargs sed -i 's,github.com/google/go-github/v56,github.com/google/go-github/v59,'
```

This should update most things. Sometimes, the `ghinstallation` library doesn't get updated right away, so you might need to keep the older version for that one. You'll know if you see errors like this:

```text
pkg/provider/github/parse_payload.go:56:33: cannot use &github.InstallationTokenOptions{…} (value of type *"github.com/google/go-github/v59/github".InstallationTokenOptions) as *"github.com/google/go-github/v57/github".InstallationTokenOptions value in assignment
```

After updating, make sure everything still builds and the tests pass:

```shell
make allbinaries test lint
```

Sometimes, structs in the libraries change, and things might break because of deprecations.  Don't just ignore these!  Figure out how to update your code to use the new versions correctly.  Don't be tempted to just add a `nolint` or pin to an older dependency – that just kicks the can down the road and makes things harder later.

### Go Version: Keeping Up with RHEL

We aim to use the latest Go version that's in RHEL (Red Hat Enterprise Linux).  To check this:

```shell
docker pull golang
docker run golang go version
```

If that version is newer than what we have in `go.mod`, you'll need to update `go.mod`.  For example, if the latest RHEL Go version is 1.20:

```shell
go mod tidy -go=1.20
```

Then, search the codebase for `go-toolset` images:

```shell
git grep golang:
```

and update the old version to the new one in those places.

### Pre-commit and Vale Rules: Keeping Linters Fresh

Update the pre-commit rules:

```shell
pre-commit autoupdate
```

Update the Vale (grammar checker) rules:

```shell
vale sync
make lint-md
```

##  Tools of the Trade:  What You'll Need

Here's a (not exhaustive) list of tools that we use in CI and pre-commit.  You'll probably want to have these on your system:

- [golangci-lint](https://github.com/golangci/golangci-lint) - For Go code linting.
- [yamllint](https://github.com/adrienverge/yamllint) - For YAML linting.
- [shellcheck](https://www.shellcheck.net/) - For shell script linting.
- [ruff](https://github.com/astral-sh/ruff) - Python code formatter and checker.
- [vale](https://github.com/errata-ai/vale) - For grammar and style checking in docs.
- [markdownlint](https://github.com/markdownlint/markdownlint) - For Markdown linting.
- [codespell](https://github.com/codespell-project/codespell) - For catching typos in code.
- [gitlint](https://github.com/jorisroovers/gitlint) - For linting Git commit messages (making them consistent).
- [hugo](https://gohugo.io) - For building the documentation website.
- [ko](https://github.com/google/ko) - To build and push container images to your Kubernetes cluster.
- [kind](https://kind.sigs.k8s.io/) - For local Kubernetes development.
- [snazy](https://github.com/chmouel/snazy) - To make JSON logs readable.
- [pre-commit](https://pre-commit.com/) - For running checks before you commit code.
- [pass](https://www.passwordstore.org/) - For managing secrets securely.
- [gosmee](https://github.com/chmouel/gosmee) - For replaying webhook events.

##  Target Architecture:  arm64 and amd64

We're building Pipelines-as-Code to run on both `arm64` and `amd64` architectures.  Our own dogfooding environment is on `arm64`, so it's important that all the jobs and Docker images we use in our Tekton Pipelines are built for `arm64`.

We use a GitHub Action and [ko](https://ko.build/) to automatically build both `amd64` and `arm64` images whenever there's a push to a branch or for a release.

# Helpful Links

- [Jira Backlog](https://issues.redhat.com/browse/SRVKP-2144?jql=component%20%3D%20%22Pipeline%20as%20Code%22%20%20AND%20status%20!%3D%20Done) -  See what we're currently working on and planning.
- [Bitbucket Server Rest API](https://docs.atlassian.com/bitbucket-server/rest/7.17.0/bitbucket-rest.html) - For working with Bitbucket Server.
- [GitHub API](https://docs.github.com/en/rest/reference) - For interacting with GitHub programmatically.
- [GitLab API](https://docs.gitlab.com/ee/api/api_resources.html) - For automating tasks in GitLab.
