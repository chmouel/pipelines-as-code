---
title: Concurrency Flow
weight: 2
---
# Concurrency Flow

Ever wondered how Pipelines as Code handles things when you kick off a bunch of pipelines at once?  It's all about managing the flow so things don't get too crazy!  This is where "Concurrency Flow" comes in.  Think of it like this: you've got a bunch of tasks (pipelines) and you want to run them efficiently without overloading your system.

Here's a diagram that breaks down how it works under the hood:

{{< mermaid >}}

graph TD
    A1[Controller] --> B1(Validate & Process Event)
    B1 --> C1{Is concurrency defined?}
    C1 -->|Not Defined| D1[Create PipelineRun with state='started']
    C1 -->|Defined| E1[Create PipelineRun with pending status and state='queued']

    Z[Pipelines-as-Code]

    A[Watcher] --> B(PipelineRun Reconciler)
    B --> C{Check state}
    C --> |completed| F(Return, nothing to do!)
    C --> |queued| D(Create Queue for Repository)
    C --> |started| E{Is PipelineRun Done?}
    D --> O(Add PipelineRun in the queue)
    O --> P{If PipelineRuns running < concurrency_limit}
    P --> |Yes| Q(Start the top most PipelineRun in the Queue)
    Q --> P
    P --> |No| R[Return and wait for your turn]
    E --> |Yes| G(Report Status to provider)
    E --> |No| H(Requeue Request)
    H --> B
    G --> I(Update status in Repository)
    I --> J(Update state to 'completed')
    J --> K{Check if concurrency was defined?}
    K --> |Yes| L(Remove PipelineRun from Queue)
    L --> M(Start the next PipelineRun from Queue)
    M --> N[Done!]
    K --> |No| N

{{< /mermaid >}}

Let's walk through it step by step, shall we?

1.  **Someone triggers a pipeline (via the Controller).**  Think of this as you pushing code or creating a pull request – something happens that needs a pipeline to run.
2.  **We validate and process this event.**  Basically, we make sure everything looks good and we know what kind of pipeline needs to run.
3.  **"Is concurrency defined?"** - This is the key question. Have you set a limit on how many pipelines from this repository can run at the same time?
    *   **If no concurrency limit is set ("Not Defined"):** We just go ahead and **start the PipelineRun right away** with a status of "started".  No waiting, let's roll!
    *   **If concurrency is defined ("Defined"):**  Hold on a sec! We need to be a bit more careful. We create the PipelineRun, but we put it in a **"queued" state** with a "pending" status.  Think of it as waiting in line.

4.  **Now, there's a "Watcher" and a "PipelineRun Reconciler" working in the background.** These guys are the traffic controllers for your pipelines.
5.  **The Reconciler constantly checks the state of each PipelineRun.**  Is it done? Is it queued? Is it running?
6.  **Let's see what happens depending on the state:**
    *   **"completed":**  If a PipelineRun is finished, awesome! The Reconciler says, "Nothing to do here!" and moves on.
    *   **"queued":**  If a PipelineRun is waiting in line, the Reconciler makes sure there's a queue set up for pipelines from this specific repository (so we don't mix queues from different projects).
    *   **"started":**  If a PipelineRun is already running, the Reconciler asks, "Is it done yet?"
        *   **"Yes" (PipelineRun is done):**  Time to report back! We tell the provider (like GitHub, GitLab, etc.) the status of the pipeline. Then, we update the status in your repository and finally mark the PipelineRun as "completed".
        *   **"No" (PipelineRun is still running):**  Not finished yet? No worries, we just "requeue the request" – which basically means we'll check again soon.

7.  **For those "queued" PipelineRuns, we add them to the repository's queue.**
8.  **Then, we check: "Are there PipelineRuns already running, and are we below the concurrency limit?"**
    *   **"Yes" (We're under the limit):** Great! We take the PipelineRun at the front of the queue (the one that's been waiting the longest) and **start it up!**  Then we check again – maybe there's room to start even more?
    *   **"No" (We're at the limit):**  Too many pipelines running already!  This PipelineRun just has to **wait its turn** in the queue.  Patience is a virtue, right?

9.  **After a PipelineRun is completed, we check one last time: "Was concurrency defined for this repository?"**
    *   **"Yes":**  Since concurrency *was* defined, we need to manage the queue. We **remove the just-completed PipelineRun from the queue** and then **start the next PipelineRun in line**, if there is one.  Keeping things flowing smoothly!
    *   **"No":** If concurrency wasn't defined, we just mark it as "Done!" and move on.

And that's the gist of it!  Concurrency Flow helps Pipelines as Code manage your pipelines efficiently, especially when things get busy. It's all about keeping things organized and making sure everyone gets their turn without causing chaos.
