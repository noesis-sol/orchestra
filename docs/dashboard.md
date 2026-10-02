# Dashboard

What the terminal shows while a run goes and after it ends, its keys, and the notifications. Back to the [README](../README.md).

## What you see

One dashboard, updated in place: nothing is printed above it while the loop runs.

```
 Orchestra  v0.2.0   batch/2026-09-28 · 18m40s
╭──────────────┬──────────────┬──────────────┬─────────────┬─────────────╮
│ Completed    │ Deferred     │ Needs you    │ Workers     │ In queue    │
│ ✓ 2          │ ↷ 1 ◆ 1      │ 0            │ 2 of 3      │ 11          │
╰──────────────┴──────────────┴──────────────┴─────────────┴─────────────╯
╭────────────┬─────────────┬─────────────────────────────────────────────╮
│ Tickets    │             │                                             │
├────────────┼─────────────┼─────────────────────────────────────────────┤
│ ✓ done     │ kinieta-dwv │ ffd6ce4 merged into batch/2026-09-28        │
│ ↷ deferred │ kinieta-vzg │ ◆ environment · high · prompt never submit… │
│ ✓ done     │ kinieta-y6j │ 6097367 merged into batch/2026-09-28        │
│ ▶ working  │ kinieta-kco │ Open the property model: Interpolatable p…  │
│ ▶ testing  │ kinieta-zq4 │ Add an undo stack for property edits        │
╰────────────┴─────────────┴─────────────────────────────────────────────╯
  Current
╭────────────────────────────────────────────────────────────────────────╮
│ 1 ⣾  kinieta-kco  working  4m52s                                       │
│   Open the property model: Interpolatable protocol and custom key-path │
│   / constraint-constant properties                                     │
│   ⏺ Bash(scripts/ci-local.sh lint ios)                                 │
╰────────────────────────────────────────────────────────────────────────╯
╭────────────────────────────────────────────────────────────────────────╮
│ 2 ⣾  kinieta-zq4  testing  1m07s                                       │
│   Add an undo stack for property edits                                 │
│   ⏺ Bash(scripts/ci-local.sh test core)                                │
╰────────────────────────────────────────────────────────────────────────╯
  1–2 to go to a worker's tab · s to stop after the current tickets
```

- **Totals** for the run, as one strip: completed, deferred (and how many triaged), questions for you, workers running out of how many may, and how many are still ready. The branch and how long the run has gone are on the title line.
- **Tickets**: one row per ticket, updated as it moves. A **picked-up** ticket (cyan) shows its title. A **completed** one (green) shows only the merged commit. A **deferred** one (yellow) shows why, replaced by the triage organ's verdict (purple `◆`) once it's in. One left for review (`CHECKS_FAILED`, `MERGE_CONFLICT`, closed without a commit, …) reads `! review` and says why in a few words (`checks failed`, `conflicts with main`); the log has the rest. A ticket that stopped the run is red. The table shows the most recent tickets that fit in the pane.
- **Current**: a box per running ticket, with its number (the key that goes to its tab, below), the worker's status, elapsed time, the ticket title and the worker's latest action (one line per worker, in a single box, when the pane is short). Each box's border is cyan while its worker runs and red when it's blocked; with several workers, each title takes at most two lines. With no worker running, a single grey box says `picking the next ticket…` (or `stopping…`). It updates every 3 seconds. In the smallest panes the `Current` label is the first thing left out.

- **Keys**: the hint along the bottom names each key and what it does, the key brighter than its words: `1–3 to go to a worker's tab · s to stop after the current tickets`, or in a narrow pane `1–3 worker tab · s stop after current`, then `s` alone. **1** to **9** switch Herdr to that worker's tab (`herdr tab focus`): the running workers are numbered in Current, oldest first, up to the ninth, and when one finishes those after it move up. The hint names the numbers there are. They do nothing while the stop question is open; if Herdr can't switch, the log says why and the dashboard carries on. **s** asks, in a box over the dashboard (a line above the hint in a small pane), whether to stop after the running tickets: `Stop after the running tickets? Stopping after the 2 running tickets finish (kinieta-kco, kinieta-y6j): no new tickets will start. They merge as usual, then the run ends.` **y** confirms, **n** or **Esc** closes it; the question names y and n as the hint names its keys. The run then starts no new ticket, from any path; the running ones carry on as usual, merges, rebases and re-checks included, and when the last one returns the run ends normally (`Stopped after the running tickets, as asked` on screen, `DRAINED after 12 tickets` in the log, exit code 0), with triage and the report. The log says `DRAIN: stopping after the 2 running tickets finish (…): no new tickets will start, asked from the dashboard`, and the report's first sentence mentions it. With nothing running, it ends at once. Meanwhile a line above the hint says the same, `■ Stopping after the 2 running tickets finish (…): no new tickets will start`, naming the tickets left as they finish (in a narrow pane it wraps and shortens the IDs only); the totals mark the queue `held`; and the hint reads `s to keep taking tickets`: **s** offers `Keep taking tickets?` to take it back (logged as `DRAIN cancelled`). A run stopping for another reason (see [Several tickets at once](running.md#several-tickets-at-once)) has no **s** to offer: the hint names only the number keys, and nothing once no worker runs. **Ctrl+C**, which the hint leaves out, stops at once at any time, the question open or not, leaving the workers running; the run then waits for a merge already under way, and a second Ctrl+C quits without waiting (see [Run](running.md#run)). Outside the dashboard (`-plain`, scripts), `kill -USR1 <pid>` asks the same without a question.

When the loop stops, the dashboard stays on screen as the run's summary, followed by its closing line and the run report (see [Organs](organs.md#organs)). A run that ended by itself closes in bold green with how it ended, then, quieter, how many tickets and how long it took: `♪ Completed the Run  12 tickets · 1h12m` when the queue is empty, `♪ Reached the ticket limit (40)`, or `♪ Stopped after the running tickets, as asked` after **s**. A run scoped to a ticket adds its scope on a line of its own: `SCOPE_DONE: …` in green, or `SCOPE_OPEN: …` in yellow, as work is left. A run something stopped ends with its red line instead, saying why (`■ PAUSED: …`). The organ phase's lines follow in light purple after a `◆`: `Finishing triage…`, `Writing the run report with Claude… (Ctrl+C skips)`, `Report saved to …`. The log, the event stream and plain output keep the loop's own words (`READY_EMPTY after 12 tickets`, `finishing triage…`).

Everything is also appended to `.orchestra/orchestra.log` in plain text, so `tail -f` works too. When output isn't a terminal, or with `-plain`, it prints those log lines instead of the live view. Scripts and agents should read the [event stream](events.md#event-stream) instead, whose records don't change with the log's wording.

On macOS, a notification tells you each outcome that matters, in a few words, titled with the project's folder name (`jswallet`): which ticket was closed, set aside or needs your answer, or that the run stopped or finished.

| When | It says |
|---|---|
| a ticket is merged | `Closed jswallet-12 · Add a --json flag to list` |
| a ticket is deferred, or left for review (`CHECKS_FAILED`, `MERGE_CONFLICT`, closed without a commit, …) | `Set aside jswallet-12 · checks failed` |
| a ticket waits on a question for you | `jswallet-12 needs your answer · Which name should the flag have?` |
| the run stops | `Stopped: PAUSED on jswallet-12`, or `Stopped: INTERRUPTED` when no ticket stopped it |
| the run finishes | `Finished the run · 12 tickets closed · 2 set aside` |

A title or reason longer than 60 characters is cut with `…`; the log has the rest. Holds, probes, the warnings that set no ticket aside (`LIKELY_CONFLICT`, `LONG_RUNNING`, …), triage and the run report don't notify: they are in the log, the event stream and the dashboard. `NOTIFY=0`, or `-notify=false`, turns notifications off.
