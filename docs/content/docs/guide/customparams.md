---
title: Custom Parameters
weight: 50
---
## Custom Parameters

Ever wished you could tweak things inside your Pipeline templates in Pipelines as Code?  Well, custom parameters are here to help! Think of them as little placeholders, using the `{{ param }}` syntax, that get filled in with your own values or info from the event payload when your PipelineRun kicks off.

Pipelines as Code is already pretty smart and gives you a bunch of variables right out of the box, depending on what kind of event triggered it all.  If you're curious about those default variables, you can peek at the [Authoring PipelineRuns documentation](../authoringprs#default-parameters). It's got the lowdown on all the automatic goodies.

But sometimes, you need to bring your own variables to the party. That's where custom parameters come in! They let you define your own values that get swapped into your templates.

{{< hint warning >}}
Now, heads up! Tekton PipelineRun parameters are usually the way to go for most things. Custom params are more for those special cases where Tekton parameters just don't quite cut it. So, keep that in mind!
{{< /hint >}}

Let's see it in action. Imagine you want to use your company name in your PipelineRun. You can set up a custom parameter in your Repository CR like this:

```yaml
spec:
  params:
    - name: company
      value: "My Beautiful Company"
```

Now, anywhere in your `PipelineRun` (even in Tasks fetched from somewhere else!),  `{{ company }}` will magically become "My Beautiful Company". Pretty neat, huh?

But wait, there's more! You can also grab values from Kubernetes Secrets. Let's say your company name is stored securely in a Secret called `my-secret` under the key `companyname`.  You can pull it in like this:

```yaml
spec:
  params:
    - name: company
      secret_ref:
        name: my-secret
        key: companyname
```

{{< hint info >}}

Just a few things to remember when playing with custom parameters:

- If you give a `value` *and* a `secret_ref`, the `value` wins.
- If you forget to set either a `value` or a `secret_ref`,  `{{ param }}` will just chill there in your `PipelineRun` as is.  It won't break anything, but it won't be replaced either.
- No `name` in your `params`?  Yeah, that parameter won't get parsed. Make sure you name them!
- Got multiple `params` with the same `name`?  Pipelines as Code is a bit of a rebel and will only use the *last* one it finds.

{{< /hint >}}

### CEL Filtering:  Making Custom Parameters Smarter

Want even *more* control? You can use CEL (Common Expression Language) filters to decide *when* a custom parameter should be applied.  Think of it as saying, "Hey, only use this custom parameter if *this* condition is true."

Here’s an example:

```yaml
spec:
  params:
    - name: company
      value: "My Beautiful Company"
      filter: pac.event_type == "pull_request"
```

In this case, "My Beautiful Company" will *only* be used for the `{{ company }}` parameter if the event that triggered the PipelineRun is a pull request (`pac.event_type == "pull_request"`).

That `pac` thing? It's like a shortcut to all those default variables we talked about earlier.  You can find all the details on what's in `pac` in the [Authoring PipelineRuns](../authoringprs) docs.

And guess what? The whole payload of the event that triggered your pipeline is also available!  You can access it using `body`.

For instance, if you're using GitHub and a pull request happens, the payload might look something like this (in JSON format):

```json
{
  "action": "opened",
  "number": 79,
  // .... lots more data
}
```

So, you could get super specific with your filter:

```yaml
spec:
  params:
    - name: company
      value: "My Beautiful Company"
      filter: body.action == "opened" && pac.event_type == "pull_request"
```

This would *only* use "My Beautiful Company" if it's a pull request *and* the action was "opened".  You can get really granular!

The event payload has tons of info you can use in your CEL filters.  To see exactly what's in the payload for your specific provider (like GitHub or GitLab), you'll want to check out their API documentation. It's worth a look!

You can even set up multiple custom `params` with the same name but different filters.  Pipelines as Code will go through them in order and use the *first* one where the filter matches. This is super handy if you want to do different things based on different event types – like handling push events differently from pull requests.

{{< hint info >}}

Want to dive deeper into webhook events and payloads? Here are some helpful links:

- [GitHub Documentation for webhook events](https://docs.github.com/webhooks-and-events/webhooks/webhook-events-and-payloads?actionType=auto_merge_disabled#pull_request)
- [GitLab Documentation for webhook events](https://docs.gitlab.com/ee/user/project/integrations/webhook_events.html)

{{< /hint >}}
