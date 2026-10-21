# Event stream

The machine-readable record of each run, for scripts and agents. Back to the [README](../README.md).

## Event stream

The log is written for people, and its wording changes. For scripts and agents, each run also appends its events to `.orchestra/run/events.jsonl` in the main checkout, out of git with the rest of `.orchestra/run/`: one JSON object per line, each appended in a single write, so a reader tailing the file never sees half a line. Like the log, the file keeps every run.

```
{"time":"2026-10-02T09:12:04.51+02:00","run":"2026-10-02T09:12:03.98+02:00","kind":"start","version":"v0.3.0","repo":"/Users/me/kinieta","branch":"batch/2026-10-02","concurrency":3}
{"time":"2026-10-02T09:12:06.2+02:00","run":"2026-10-02T09:12:03.98+02:00","kind":"dispatch","ticket":"kinieta-kco","title":"Open the property model","text":"[1/40] kinieta-kco dispatching: Open the property model","n":1,"limit":40,"queued":12,"unclosed":31}
{"time":"2026-10-02T09:31:40.07+02:00","run":"2026-10-02T09:12:03.98+02:00","kind":"closed","ticket":"kinieta-kco","detail":"ffd6ce4 merged into batch/2026-10-02","text":"  kinieta-kco closed (ffd6ce4 kinieta-kco: Open the property model); merged into batch/2026-10-02, worktree, branch and tab removed"}
{"time":"2026-10-02T11:02:13.6+02:00","run":"2026-10-02T09:12:03.98+02:00","kind":"done","text":"READY_EMPTY after 12 tickets"}
{"time":"2026-10-02T09:31:39.2+02:00","run":"2026-10-02T09:12:03.98+02:00","kind":"check","ticket":"kinieta-kco","suite":"merge","command":"scripts/check-fast.sh","commit":"ffd6ce4a10","seconds":133.2,"result":"passed"}
{"time":"2026-10-02T11:14:40.3+02:00","run":"2026-10-02T09:12:03.98+02:00","kind":"full_check","ticket":"kinieta-q1z","detail":"failed","text":"  FULL_CHECK_FAILED: 'scripts/check-full.sh' fails on batch/2026-10-02 at 3c4d5e6, in the suite e2e; the whole output is in /Users/me/kinieta/.orchestra/run/check-full.log; filed kinieta-q1z","suite":"e2e","output":"/Users/me/kinieta/.orchestra/run/check-full.log","commit":"3c4d5e6f70","seconds":611.8,"result":"failed"}
{"time":"2026-10-02T11:03:55.1+02:00","run":"2026-10-02T09:12:03.98+02:00","kind":"end","code":0}
```

Every record has `time`, when it was written, and `run`, when the run started: RFC 3339 with fractional seconds, in local time. `run` is the same in each of a run's records and in its lock's `started`, so it picks one run's records out of the file. `kind` says what the record is:

| `kind` | What happened | Its other fields |
|---|---|---|
| `start` | the run's first record, once its startup checks pass (for a feature, once it is filed, or isn't; for a spec, once its breakdown is over, or isn't needed) | `version`; `repo`, the main checkout; `branch`, where finished tickets land; `scope`, the ticket the run is scoped to (`--ticket`, the epic a feature was filed as, or a spec's root epic), absent for all of `bd ready`; `feature`, the feature request (`--feature`, or typed at the start); `spec`, the spec's path in the repository ([`--spec`](running.md#a-whole-app-from-a-spec), or chosen at the start); `overnight`, `true` for an [overnight run](running.md#overnight), absent otherwise; `concurrency` |
| `info` | progress: the `START` line, worktrees, how workers settled, a ticket closed in the tab its worker was left in after it was [set aside](running.md#tickets-set-aside) and merged now (`LATE_CLOSE`)… | `text`; `ticket` on some |
| `dispatch` | a ticket was picked up | `ticket`, `title`; `n` and `limit`, as in `[n/limit]`, `limit` left out in a run with no ticket limit (a run's unless `--limit` or `LIMIT` gives one), whose lines read `[n]`; `queued`, how many ready tickets wait for a slot; `held` and `held_by`, the ready tickets held back (see below); `unclosed`, how many tickets aren't closed (open, in progress, blocked or deferred; in a run scoped to a ticket, the scope's); `solo` |
| `queue` | the number of ready tickets waiting for a slot or held back, or of tickets not closed, changed: the dashboard's **In queue**, which the log doesn't have | `queued`, `held`, `held_by`, `unclosed`, `solo` |
| `closed` | a ticket closed and was merged, or closed with no change of its own to merge | `ticket`, `title`; `detail`, as in `ffd6ce4 merged into main`, or `no change to merge`; `phases`, `hand_backs` and `carried` (see below) |
| `deferred` | a ticket was set aside | `ticket`, `title`; `detail`, why: `by the worker`, `still in_progress, noted for review`, …; `phases`, `hand_backs` and `carried` |
| `asked` | a ticket waits on a question for you | `ticket`, `title`; `detail`, the question's ID and title |
| `answered` | its question was answered: it comes back, dispatched next | `ticket`, `title`, `detail` |
| `waits` | a ticket's worker stopped with nothing done, having found it blocked by a ticket not merged yet: it is back in the queue until that ticket merges (`WAITS_FOR_MERGE`) | `ticket`, `title`; `detail`, the tickets it waits for, as in `kinieta-kco` or `kinieta-kco, kinieta-q1z` |
| `reopened` | a ticket set aside for failing at once was put back in the queue by a hold for the environment: no longer set aside | `ticket`; `detail`, why: `the run held for the environment` |
| `triage` | the triage organ's verdict on a deferred ticket | `ticket`; `title`, the verdict's summary; `detail`, as in `environment · high` |
| `warn` | something to review, while the run goes on: `CHECKS_FAILED`, `MERGE_CONFLICT`, `LIKELY_CONFLICT`, `CLOSED_UNMERGED` (a closed ticket whose branch isn't on the base, holding the tickets it blocks), … | `ticket` when it is about one; `aside: true` when that ticket is left for review, out of this run, with `detail`, why: `checks failed`, `closed without a commit`, …, and `phases`, `hand_backs` and `carried`; `blocked` on a `MERGE_CONFLICT`: the ticket's work is done, but it can't merge until you act, as in `merge conflict in a.go, b.go`; `detail` on a warning about the base branch as the run starts, the word its line starts with: `BASE_DIVERGED`, `BASE_UNFETCHED` or `BASE_NOT_FAST_FORWARDED` ([Run](running.md#run)) |
| `hold` | something stopped the run: no new tickets while the running ones finish | `ticket` on some; `blocked` when that ticket's work is done but it can't merge until you act (`DIRTY_TREE`, `GIT_FAILED`, `MERGE_FAILED`): why, as the dashboard says it, as in `main checkout has uncommitted changes`, `does not fast-forward onto main` |
| `drain`, `resume` | the run was asked to stop after the running tickets, or that was taken back | |
| `probed` | a probe found the machine working after an environment hold: tickets start again | |
| `usage_limit` | a worker reached the account's [usage limit](running.md#the-usage-limit), or an organ call was refused for it: the run holds, starting no new tickets, until it resets; once per hold | `ticket`, the worker's (none for an organ's); `detail`, what Claude Code said of the limit, as in `You've hit your session limit · resets 7pm (Europe/Athens)`; `reset`, when it resets (RFC 3339), when Claude Code said |
| `usage_limit_lifted` | the limit reset, or a worker went on past it: tickets start again, and the workers still limited are told to carry on | |
| `shared_machine` | another orchestra run on the machine started (`SHARED_MACHINE: …`), or ended ([Runs that share a machine](running.md#runs-that-share-a-machine)) | `shared`, how many other runs are going, which can be 0 |
| `machine_hold` | a ready ticket waits for a free slot across the machine: the workers running across its runs are at `--machine-workers` (`HOLD: MACHINE: …`); once per hold. `machine_full: true` is on each `dispatch` and `queue` record meanwhile | |
| `stop` | the loop stopped and needs you: `PAUSED`, `MERGE_FAILED`, `USAGE_LIMIT`, `INTERRUPTED`, … | `detail`, the word its text starts with; `ticket`, the one it stopped over, if one did; `blocked`, as for `hold` |
| `done` | the loop finished: `READY_EMPTY`, `LIMIT_REACHED` or `DRAINED`, with `QUESTIONS_OPEN` naming the questions a `READY_EMPTY` run ended on with nothing else ready and the tickets each holds, if any, `SETUP_WAITS` naming the ready tickets whose worktree's setup failed, if any, then `SCOPE_DONE` or `SCOPE_OPEN` in a scoped run; or the run had nothing to run: `READY_EMPTY after 0 tickets: everything is done`, or `: nothing ready` | |
| `check` | a check or a setup orchestra ran ended; the log has no line for it | `suite`, which: `merge` (a ticket's merge check, `check_fast`, the merge train's included), `recheck` (the check [once more](running.md#tickets-set-aside) of a ticket set aside for its failed check), `full` (the full check), `base` (the [overnight](running.md#overnight) pre-flight's merge check of the base branch's head) or `setup` (the project's `setup` in a ticket's worktree, before its worker starts or before its check); `ticket`, whose check or setup it was, absent for `full` and `base`; `command`, what ran (for `full` and `base`, the check, the setup before it counted in its time); `commit`, the commit it ran on, as the log shortens it; `seconds`, from its start to its end, to a tenth; `result`: `passed`, `failed`, `timed_out` (stopped at its time limit) or `cut_short` (a stop or Ctrl+C ended it); `output`, where its whole output is, when it was kept: as it failed, timed out or was cut short; `train: true` when it ran ahead of its ticket's turn, as the second ticket of the merge train |
| `full_check` | the [full check](running.md#the-full-check) after a run that ended by itself and merged a ticket, before triage finishes and the run report | `detail`, how it went: `passed`, `failed`, `timed out`, `skipped` (stopped with Ctrl+C) or `not run` (no worktree could be made for it); when it failed or timed out, `suite`, the suite it failed in (none when the setup before it failed), `output`, where its whole output is, and `ticket`, the ticket filed for that suite or the one open for it already; when it ran, `commit`, `seconds` and `result`, as its `check` record gives them |
| `end` | the run's last record, as orchestra exits, after triage and the run report | `code`, the [exit code](running.md#exit-codes); `tokens` and `organs`, the run's totals (see [What a ticket's workers used](#what-a-tickets-workers-used)) |

Every record but `start`, `queue`, `check` and `end` also has `text`, its line in the log as it is there, without the time. A `stop` or `done` record's text starts with the word that says how the loop ended (`PAUSED: …`, `READY_EMPTY after 12 tickets`), a `hold` record's with `HOLD: ` and that word. `solo` is there while a ticket labelled `solo` runs (`{"ticket":"<id>"}`) or is next (`{"ticket":"<id>","next":true}`). `held` is how many ready tickets wait for something besides a free slot, none of them among `queued`, and `held_by` how many each reason holds back, as in `{"overlap":3,"merge":1}`: `overlap`, a footprint overlapping a running ticket's; `parent`, subtickets not all closed and merged; `merge`, a ticket blocking it, or the one it was filed from, not merged yet; `solo`, a solo ticket running or next; `setup`, the setup failed in its worktree (`SETUP_FAILED`), until a ticket merges. Both are there only while a ticket is held back; the log says why each one waits. A field without a value is left out, except `queued`, `unclosed`, `shared` and `code`, which can be 0; `unclosed` is left out only before the run could read it. Kinds and fields may be added, but those here keep their names: read the ones you know and skip the rest.

### Where a ticket's time went

The record that ends a ticket's part in the run, `closed`, `deferred` or a `warn` with `aside: true`, says how long it spent in each phase from when the run first took it up (dispatched it, adopted its worker, or took it up again to merge it or check it once more) to that record, in seconds with one decimal:

```
{"time":"2026-10-02T09:31:40.07+02:00","run":"2026-10-02T09:12:03.98+02:00","kind":"closed","ticket":"kinieta-kco","title":"Open the property model","detail":"ffd6ce4 merged into batch/2026-10-02","text":"…","phases":{"working":812.4,"waiting":41.0,"merging":133.2,"fixing":260.8},"hand_backs":{"resolve":0,"fix":1}}
```

- `phases` gives each phase the [dashboard](dashboard.md) shows for a running ticket, and leaves out those it never entered: `working`, its worker at it, from its dispatch until its worker is done, and again after a hand-back; `waiting`, in the merge queue for its turn (**to merge**); `merging`, its merge under way: the rebase, the worktree's setup and the merge check; `resolving`, handed back to its worker to resolve its rebase's conflicts; `fixing`, handed back to fix its failing check. Time the run held for the usage limit or for the machine counts in the phase the ticket was in, as the dashboard shows it. A ticket the run takes up more than once, as one that asks a question and comes back once it is answered, or one that waits for a ticket to merge and starts again, adds each time to the same phases; the wait between, for your answer or for the merge, counts in none of them, and nor does a wait for an answer that ends its part in the run, as when the run ends with the ticket still asking.
- `hand_backs` counts the ticket's hand-backs in this run: `resolve`, for conflicts, and `fix`, for a failing check; both are given, 0 included.
- `carried: true` marks a ticket carried over from an earlier run: its phases count from when this run first took it up.

A record about a ticket the run didn't take up has none of the three, and no other record has them.

### What a ticket's workers used

The same record gives `tokens`, what the ticket's Claude Code workers used from when the run first took it up, by model, as their sessions' transcripts say:

```
"tokens":{"claude-opus-5-5":{"input":1834,"output":52011,"cache_write":210334,"cache_read":8120443}}
```

- `input` is the input neither written to the prompt cache nor read from it, `cache_write` the input written to it, `cache_read` the input read from it, and `output` the output, thinking included; each is given, 0 included.
- Every session the ticket's workers had in the run counts: the first, one a worker cleared into (`/clear`), a worker's started again, and the one resumed for a hand-back or an answer, read once. So do the subagents they ran, the verifier included, whose transcripts Claude Code keeps beside the session's. A message is counted once, though Claude Code writes one of several parts (thinking, text, tool calls) as a line each, and nothing from before the run took the ticket up is: a session resumed from an earlier run counts from then.
- A transcript that is missing or can't be read is left out, and the log says so once. A record whose transcripts give nothing has no `tokens`: none could be read, the workers don't report through Claude Code's hooks, or the run didn't take the ticket up.

The `end` record gives the run's totals: `tokens`, its tickets' summed by model, and `organs`, what its [organ](organs.md) calls cost, as their results said, as in `{"calls":3,"turns":5,"cost_usd":0.0456}`; each is left out when there is none. orchestra gives tokens, not prices: a subscription doesn't pay by the token, and prices change, so price them from your own table.

A failing terminal, Herdr, tmux or cmux, stops the run as `TERMINAL_FAILED`; up to 0.6.0 it was `HERDR_FAILED`, so a script that matches the stop's word should look for both.

The overnight pre-flight's `base` check ends before the run's `start` record: its `check` record follows `start`, written once the stream opens, and a run that ends in its pre-flight has none. So does the `warn` record of the pre-flight's warning about the base branch, or a spec run's before it commits its spec, its `detail` the word its line starts with, as a run's start gives it.

A run with [nothing to run](running.md#nothing-to-run) writes three records: `start`, `done` and `end` with code 0. Its done text says why: `READY_EMPTY after 0 tickets: everything is done` when no ticket is left but epics and none is closed but not merged, or `READY_EMPTY after 0 tickets: nothing ready`; a scoped run adds its scope's `; SCOPE_DONE: …` or `; SCOPE_OPEN: …`.

Until its `end` record a run is still going, or was killed (`kill -9`, a crash): `lsof -t .git/orchestra.lock` in the main checkout says which. A feature run writes nothing there while the feature is talked through, nor a spec run while its spec is committed and broken down: the log has those steps, on its `FEATURE` and `SPEC:` lines. If the stream can't be written, the log says so once, and the run goes on.

With `jq`, in the main checkout:

```
E=.orchestra/run/events.jsonl
run=$(jq -r .started .git/orchestra.lock)   # the latest run's start, as its lock gives it (null after init)
jq -c --arg run "$run" 'select(.run == $run and (.kind | IN("hold", "stop", "done", "end")))' $E   # how it ended
jq -r --arg run "$run" 'select(.run == $run and .kind == "closed") | .ticket' $E               # what it closed
tail -f $E | jq -c 'select(.kind != "info" and .kind != "queue") | {kind, ticket, text}'      # follow a run
jq -r 'select(.kind == "start" and .spec) | .scope' $E | tail -n 1                             # the last spec run's root epic
jq -r --arg run "$run" 'select(.run == $run and .phases) | [.ticket, .kind, (.phases | to_entries | map("\(.key) \(.value)s") | join(", "))] | @tsv' $E   # its tickets' time per phase
jq -rs 'map(select(.kind == "check")) | group_by(.suite)[] | (map(.seconds) | sort) as $s
  | "\(.[0].suite): \(length) runs, median \($s[length / 2 | floor])s, slowest \($s[-1])s"' $E   # each suite's median and slowest run
jq -s --arg run "$run" '[.[] | select(.run == $run and .kind != "end") | .tokens // {} | to_entries[]] | group_by(.key) | map({(.[0].key): (map(.value) | {input: (map(.input) | add), output: (map(.output) | add), cache_write: (map(.cache_write) | add), cache_read: (map(.cache_read) | add)})}) | add' $E   # its tokens per model, its end record or not
```
