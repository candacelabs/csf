# cron: the schedule grammar

`pkg/cron` is the schedule grammar of CSF's cron [service](../../csf/docs/generated/ontology_cgen.md#term-service): human-readable
trigger declarations, their canonical five-field form, and the pure value
model of triggers and occurrences that the scheduler, its store and the
Liquid Proto contract share. It starts no [goroutines](../../csf/docs/generated/ontology_cgen.md#term-goroutine) and crosses no boundary.
The scheduler that fires triggers and records occurrences is
[`services/cron`](../../services/cron).

## Declaring a schedule

Schedules are typed builders rather than strings. Each one normalizes to a
canonical five-field cron form, and that string, not the builder call, is
what a `TriggerDefinition` persists and a status snapshot reports. These pairs
are pinned by `schedule_test.go`:

| Declaration | `Canonical()` |
|---|---|
| `cron.Spec(cron.Daily(cron.At(3).PM()))` | `0 15 * * *` |
| `cron.Spec(cron.Weekly(time.Monday, cron.At(8, 30).AM()))` | `30 8 * * 1` |
| `cron.Spec(cron.Monthly(1, cron.Noon()))` | `0 12 1 * *` |
| `cron.Spec(cron.LastDayOfMonth(cron.Midnight()))` | `0 0 L * *` |
| `cron.Spec(cron.Every(15 * time.Minute))` | `@every 15m0s` |
| `cron.Spec(cron.Raw("*/15 2-3 * * 1,3"))` | `*/15 2-3 * * 1,3` |

`Schedule.String()` is the human rendering of the same declaration: the first
row reads `daily at 3:00 PM (UTC)`.

`At` returns a `MeridiemTime`, not a `TimeOfDay`, so a twelve-hour clock
declaration cannot reach a schedule until it says which half of the day it
means: the ambiguous spelling is a compile error rather than a trigger that
fires twelve hours off.

```go
cron.Daily(cron.At(3))        // does not compile: MeridiemTime is not a TimeOfDay
cron.Daily(cron.At(3).PM())   // 15:00
cron.Daily(cron.At24(15))     // the same instant on a 24-hour clock
```

`Spec` schedules in UTC. `Schedule.In(location)` returns a copy in another
location, and neither builder panics: an invalid declaration is carried until
`Validate`, `Canonical`, or `Next` reports it. An interval schedule is
anchored once, at the instant it is first reconciled, so its cadence survives
a restart; `Schedule.Anchor` states the anchor explicitly.

## The value model

`TriggerDefinition` is the persistence-neutral declaration of one trigger:
its name, which `ValidateTriggerName` holds to `^[a-z][a-z0-9._/-]*$` and
128 bytes, its `ScheduleDefinition`, and its `CatchUpPolicy` and
`OverlapPolicy`. `NormalizeTriggerDefinition` validates one and anchors an
interval schedule; `PreserveIntervalAnchor` keeps an established anchor across
a re-declaration. `TriggerState` is a trigger's durable cursor, and
`OccurrenceRecord` the durable record of one scheduled instant;
`OccurrenceID` is its stable identity, the operation's idempotency key.

## Contract

[`contract`](contract) maps the value model to the validated Liquid Proto
messages under [`v1`](v1): `ScheduleSpec`, `TriggerDefinition`,
`TriggerStatus` and `StatusSnapshot`. They are HTTP and messaging boundary
contracts; protobuf wire bytes are never stored in the database. Regenerate
them with `pkg/proto/generate.sh`.
