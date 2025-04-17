---
title: Custom Parameters
weight: 40
---

# Custom Parameters

Using the `{{ param }}` syntax, Pipelines-as-Code lets you expand variables or payload body content inside templates within your PipelineRun.

By default, several variables are exposed according to the event type. To view
all the variables exposed by default, refer to the documentation on [Authoring
PipelineRuns](../authoringprs#dynamic-variables).

With custom parameters, you can specify additional values to be
replaced inside the template.

{{< hint warning >}}
Utilizing the Tekton PipelineRun parameters feature may generally be the
preferable approach, and custom params expansion should only be used in specific
scenarios where Tekton params cannot be used.
{{< /hint >}}

As an example, here is a custom variable in the Repository CR `spec`:

```yaml
spec:
  params:
    - name: company
      value: "My Beautiful Company"
```

The variable name `{{ company }}` will be replaced by `My Beautiful Company`
anywhere inside your `PipelineRun` (including the remotely fetched task).

Alternatively, the value can be retrieved from a Kubernetes Secret.

For instance, the following code will retrieve the value for the company
`parameter` from a secret named `my-secret` and the key `companyname`:

```yaml
spec:
  params:
    - name: company
      secret_ref:
        name: my-secret
        key: companyname
```

Lastly, if no default value makes sense for a custom param, it can be defined
without a value:

```yaml
spec:
  params:
    - name: start_time
```

If the custom parameter is not defined with any value, it is only expanded
if a value is supplied via [a GitOps command]({{< relref "/docs/guide/gitops_commands#passing-parameters-to-gitops-commands-as-arguments" >}}).

{{< hint info >}}

- If you have a `value` and a `secret_ref` defined, the `value` will be used and a warning will be emitted to the repository namespace.
- If you don't have a `value` or a `secret_ref`, and the parameter is not
  [overridden by a GitOps command]({{< relref "/docs/guide/gitops_commands#passing-parameters-to-gitops-commands-as-arguments" >}}),
  the parameter will not be parsed, and it will be shown as `{{ param }}` in
  the `PipelineRun`.
- If you don't have a `name` in the `params`, the parameter will not be parsed.
- If you have multiple `params` with the same `name` without filters, the last one will be used.
{{< /hint >}}

### CEL filtering on custom parameters

You can define a `param` to only apply the custom parameters expansion when some
conditions have been matched on a `filter`:

```yaml
spec:
  params:
    - name: company
      value: "My Beautiful Company"
      filter: pac.event_type == "pull_request"
```

The `pac` prefix contains all the values as set by default in the templates
variables. Refer to the [Authoring PipelineRuns](../authoringprs) documentation
for all the variables exposed by default.

The body of the payload is exposed inside the `body` prefix.

For example, if you are running a Pull Request on GitHub, pac will receive a
payload that has this kind of JSON:

```json
{
  "action": "opened",
  "number": 79,
  // .... more data
}
```

The filter can then do something like this:

```yaml
spec:
  params:
    - name: company
      value: "My Beautiful Company"
      filter: body.action == "opened" && pac.event_type == "pull_request"
```

The payload of the event contains much more information that can be used with
the CEL filter. To see the specific payload content for your provider, refer to
the API documentation

You can have multiple `params` with the same name and different filters; the
first param that matches the filter will be picked up. This lets you have
different output according to different events, and for example, combine a push
and a pull request event.

```yaml
spec:
  params:
    - name: environment
      value: "staging"
      filter: pac.event_type == "pull_request"
    - name: environment
      value: "production"
      filter: pac.event_type == "push" && pac.target_branch == "main"
```

{{< hint info >}}

- If a CEL filter evaluation fails, the parameter will be skipped and an error message will be logged.
- If a filter is provided but does not evaluate to a boolean, the parameter will be skipped.
- If multiple parameters with the same name have filters that match, only the first match will be used.

- [GitHub Documentation for webhook events](https://docs.github.com/webhooks-and-events/webhooks/webhook-events-and-payloads?actionType=auto_merge_disabled#pull_request)
- [GitLab Documentation for webhook events](https://docs.gitlab.com/ee/user/project/integrations/webhook_events.html)
{{< /hint >}}

### Accessing Changed Files

Within CEL filters, you can access information about changed files through the `files` object:

```yaml
spec:
  params:
    - name: build_frontend
      value: "true"
      filter: "files.modified.exists(file, file.startsWith('frontend/'))"
```

The `files` object provides the following collections:

- `files.all`: All changed files (union of added, modified, deleted, and renamed)
- `files.added`: Files that were added in this event
- `files.deleted`: Files that were deleted in this event
- `files.modified`: Files that were modified in this event
- `files.renamed`: Files that were renamed in this event

For example, to check if any JavaScript files were modified:

```yaml
spec:
  params:
    - name: run_js_tests
      value: "true"
      filter: "files.all.exists(file, file.endsWith('.js'))"
```

Or to check if specific directories were affected:

```yaml
spec:
  params:
    - name: deploy_type
      value: "frontend"
      filter: "files.all.exists(file, file.startsWith('frontend/'))"
    - name: deploy_type
      value: "backend"
      filter: "files.all.exists(file, file.startsWith('backend/'))"
    - name: deploy_type
      value: "all"
```

{{< hint info >}}

- File path matching uses the full repository-relative path
- The `files` information is only available for pull request and push events
- For other event types, the files collections will be empty
- If file information is not available, the filter will evaluate but the files collections will be empty
{{< /hint >}}

### Parameters from incoming payload

When using the incoming webhooks feature, you can define parameters to be overridden by the incoming payload. For example, if the incoming payload contains:

```json
{
  "params": {
    "the_best_superhero_is": "superman"
  }
}
```

These parameters will be applied and can override values defined in the Repository CR.
