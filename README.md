# wk

A small terminal program that answers one question first: **when do I report to work next?**

It keeps a work schedule in a plain text file, asks about the days it doesn't know, and treats a blank tomorrow as an emergency. The work week runs Saturday to Friday.

```
$ wk
NEXT: today 9:00am, in 2h 41m
First of 5 shifts this week. Start steady.

> Sat 14  9:00am-5:30pm   <- next
  Sun 15  9:00am-5:30pm
  Mon 16  12:00pm-8:00pm
  Tue 17  off
  Wed 18  off
  Thu 19  9:00am-5:30pm
  Fri 20  ?
```

## Build

Standard library only; any recent Go.

```
go build -o wk .        # wk.exe on Windows
go test ./...
```

## Commands

| Command | What it does |
|---|---|
| `wk` | Headline and the next 7 days. Asks a question only if today or tomorrow is unknown. |
| `wk -1` | Headline only, for shell profiles and scripts. Never prompts. |
| `wk week [next\|last\|N]` | The Saturday-to-Friday work week. |
| `wk ask [DAY]` | Asks about every unknown day in the 14 days from DAY (default today). Enter stops. |
| `wk DAYS SHIFT [note]` | Sets one or more days: `wk mon 12-8 truck day`, `wk mon-wed,fri 9-5:30`. |
| `wk from DAY SHIFT SHIFT ...` | Sets a run of consecutive days, one shift each. Saves nothing if any shift is unreadable. |
| `wk DAYS ?` | Forgets days. |
| `wk DAYS` | Shows days. |

Entering a whole posted week in one line:

```
wk from sat 9-5:30 = 12-8 off off 9-5:30 =
```

**DAY** is `today`, `tom`, a weekday name (`sat`..`fri`, meaning the next one), `+7` (days from today), `11/16`, or `2026-11-16`.
**DAYS** is one DAY, a range (`mon-fri`, `11/21-11/27`), or a comma list of either.

**SHIFT** is `9-5:30`, `12-8`, `7a-3p`, `13:00-21:30`, `9` (end unknown), or `off`. `=` copies the day before. Bare hours are guessed: starts of 7 to 11 are am, 12 to 6 are pm, and the end is the first matching time after the start. `10p-6` is an overnight shift.

## Headline states

| Headline | Exit code |
|---|---|
| `NEXT: today 12:00pm, in 3h 10m` | 0 |
| `Off today. NEXT: ...` | 0 |
| `ON SHIFT until 8:00pm. Next: ...` | 0 |
| `LEAVE SOON: 12:00pm, in 40m` | 1 |
| `LATE: shift started 12:00pm, 15m ago.` | 2 |
| `TOMORROW UNKNOWN: find out your shift` | 3 |
| any of the above with `(tomorrow unknown)` | 3 unless already 1 or 2 |

The exit code lets a scheduled task raise a notification:

```
wk -1 > /dev/null; [ $? -ne 0 ] && notify-send "$(wk -1)"
```

## Settings

| Variable | Meaning |
|---|---|
| `WK_FILE` | Path of the data file. Default: `wk/schedule.txt` in the user config folder (`wk help` prints it). |
| `WK_LEAD` | Minutes before a shift when the headline becomes `LEAVE SOON`. Default 60. |
| `WK_COLOR` | `always` or `never`. By default color is used only on a terminal; `NO_COLOR` is honored. |
| `WK_CHEER` | `0` turns off the line of encouragement under the headline. |
| `WK_NOW` | Pretend the time is e.g. `2026-11-13 08:20`, for trying things out. |

## Data file

One line per date; a date with no line is an unknown day. It is safe to edit by hand.

```
2026-11-14 09:00-17:30
2026-11-15 off
2026-11-16 12:00-20:00 truck day
```
