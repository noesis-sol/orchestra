# Event stream

The machine-readable record of each run, for scripts and agents. Back to the [README](../README.md).

## Event stream

The log is written for people, and its wording changes. For scripts and agents, each run also appends its events to `.orchestra/run/events.jsonl` in the main checkout, out of git with the rest of `.orchestra/run/`: one JSON object per line, each appended in a single write, so a reader tailing the file never sees half a line. Like the log, the file keeps every run.

```
{"time":"2026-10-02T09:12:04.51+02:00","run":"2026-10-02T09:12:03.98+02:00","kind":"start","version":"v0.3.0","repo":"/Users/me/kinieta","branch":"batch/2026-10-02","concurrency":3}
{"time":"2026-10-02T09:12:06.2+02:00","run":"2026-10-02T09:12:03.98+02:00","kind":"dispatch","ticket":"kinieta-kco","title":"Open the property model","text":"[1/40] kinieta-kco dispatching: Open the property model","n":1,"limit":40,"queued":12}
{"time":"2026-10-02T09:31:40.07+02:00","run":"2026-10-02T09:12:03.98+02:00","kind":"closed","ticket":"kinieta-kco","detail":"ffd6ce4 merged into batch/2026-10-02","text":"  kinieta-kco closed (ffd6ce4 kinieta-kco: Open the property model); merged into batch/2026-10-02, worktree, branch and tab removed"}
{"time":"2026-10-02T11:02:13.6+02:00","run":"2026-10-02T09:12:03.98+02:00","kind":"done","text":"READY_EMPTY after 12 tickets"}
{"time":"2026-10-02T11:03:55.1+02:00","run":"2026-10-02T09:12:03.98+02:00","kind":"end","code":0}
```

Every record has `time`, when it was written, and `run`, when the run started: RFC 3339 with fractional seconds, in local time. `run` is the same in each of a run's records and in its lock's `started`, so it picks one run's records out of the file. `kind` says what the record is:

| `kind` | What happened | Its other fields |
|---|---|---|
| `start` | the run's first record, once its startup checks pass (with `--feature`, once the plan is filed, or isn't) | `version`; `repo`, the main checkout; `branch`, where finished tickets land; `scope`, the ticket the run is scoped to (`--ticket`, or the epic a `--feature` request was filed as), absent for all of `bd ready`; `feature`, the `--feature` request; `concurrency` |
| `info` | progress: the `START` line, worktrees, how workers settled… | `text`; `ticket` on some |
| `dispatch` | a ticket was picked up | `ticket`, `title`; `n` and `limit`, as in `[n/limit]`; `queued`, how many ready tickets wait for a slot; `solo` |
| `queue` | the number of ready tickets waiting for a slot changed: the dashboard's **In queue**, which the log doesn't have | `queued`, `solo` |
| `closed` | a ticket closed and was merged | `ticket`, `title`; `detail`, as in `ffd6ce4 merged into main` |
| `deferred` | a ticket was set aside | `ticket`, `title`; `detail`, why: `by the worker`, `still in_progress, noted for review`, … |
| `asked` | a ticket waits on a question for you | `ticket`, `title`; `detail`, the question's ID and title |
| `answered` | its question was answered: it comes back, dispatched next | `ticket`, `title`, `detail` |
| `triage` | the triage organ's verdict on a deferred ticket | `ticket`; `title`, the verdict's summary; `detail`, as in `environment · high` |
| `warn` | something to review, while the run goes on: `CHECKS_FAILED`, `MERGE_CONFLICT`, `LIKELY_CONFLICT`, … | `ticket` when it is about one; `aside: true` when that ticket is left for review, out of this run, with `detail`, why: `checks failed`, `closed without a commit`, … |
| `hold` | something stopped the run: no new tickets while the running ones finish | `ticket` on some |
| `drain`, `resume` | the run was asked to stop after the running tickets, or that was taken back | |
| `probed` | a probe found the machine working after an environment hold: tickets start again | |
| `stop` | the loop stopped and needs you: `PAUSED`, `MERGE_FAILED`, `INTERRUPTED`, … | `detail`, the word its text starts with; `ticket`, the one it stopped over, if one did |
| `done` | the loop finished: `READY_EMPTY`, `LIMIT_REACHED` or `DRAINED`, with `SCOPE_DONE` or `SCOPE_OPEN` in a scoped run | |
| `end` | the run's last record, as orchestra exits, after triage and the run report | `code`, the [exit code](running.md#exit-codes) |

Every record but `start`, `queue` and `end` also has `text`, its line in the log as it is there, without the time. A `stop` or `done` record's text starts with the word that says how the loop ended (`PAUSED: …`, `READY_EMPTY after 12 tickets`), a `hold` record's with `HOLD: ` and that word. `solo` is there while a ticket labelled `solo` runs (`{"ticket":"<id>"}`) or is next (`{"ticket":"<id>","next":true}`). A field without a value is left out, except `queued` and `code`, which can be 0. Kinds and fields may be added, but those here keep their names: read the ones you know and skip the rest.

Until its `end` record a run is still going, or was killed (`kill -9`, a crash): `lsof -t .orchestra/run/orchestra.lock` says which. A `--feature` run writes nothing there while it screens and plans the request. If the stream can't be written, the log says so once, and the run goes on.

With `jq`, in the main checkout:

```
E=.orchestra/run/events.jsonl
run=$(jq -r .started .orchestra/run/orchestra.lock)   # the latest run's start, as its lock gives it
jq -c --arg run "$run" 'select(.run == $run and (.kind | IN("hold", "stop", "done", "end")))' $E   # how it ended
jq -r --arg run "$run" 'select(.run == $run and .kind == "closed") | .ticket' $E               # what it merged
tail -f $E | jq -c 'select(.kind != "info" and .kind != "queue") | {kind, ticket, text}'      # follow a run
```
