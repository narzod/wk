package main

import (
	"fmt"
	"time"
)

// cheer returns one line of encouragement fitted to where the user stands in
// the day and the Saturday-to-Friday work week, or "" when the headline is
// already a call to action. The wording varies by date but is stable all day.
func cheer(db schedule, now time.Time) string {
	today := dayOf(now)
	tomorrow := addDays(today, 1)
	te, todayKnown := db[key(today)]
	tme, tomorrowKnown := db[key(tomorrow)]
	tomorrowOff := tomorrowKnown && tme.off

	pick := func(options ...string) string {
		return options[today.YearDay()%len(options)]
	}

	// Where this work week stands.
	var done, left, unknown int // shifts finished, shifts not yet started, blank days
	laterShifts, laterUnknown := 0, 0
	ws := weekStart(today)
	for i := 0; i < 7; i++ {
		d := addDays(ws, i)
		e, ok := db[key(d)]
		switch {
		case !ok:
			unknown++
			if d.After(today) {
				laterUnknown++
			}
		case !e.off:
			s, en := e.span(d)
			if !en.After(now) {
				done++
			} else if s.After(now) {
				left++
			}
			if d.After(today) {
				laterShifts++
			}
		}
	}
	lastOfWeek := laterShifts == 0 && laterUnknown == 0
	toGo := func() string { // "3 down, 2 to go", hedged when the week has blanks
		if unknown > 0 {
			return fmt.Sprintf("%d down this week", done)
		}
		return fmt.Sprintf("%d down, %d to go this week", done, left)
	}

	// Length of the run of consecutive work days around today, and today's place in it.
	isShift := func(d time.Time) bool { e, ok := db[key(d)]; return ok && !e.off }
	back, fwd := 0, 0
	for isShift(addDays(today, -back-1)) {
		back++
	}
	for isShift(addDays(today, fwd+1)) {
		fwd++
	}
	run, runDay := back+1+fwd, back+1

	// 1. A shift is in progress (possibly last night's overnight one).
	if cd, ce, ok := current(db, now); ok {
		start, end := ce.span(cd)
		if now.Sub(start) < lateGrace {
			return pick(
				"Go safely. A few minutes late is recoverable; rushing is not.",
				"Breathe, then go. One late start does not define the day.",
				"Running late happens. Get there in one piece and start from where you are.",
			)
		}
		if ce.end < 0 {
			return pick("One hour at a time.", "Steady pace. The shift ends either way.")
		}
		remaining := end.Sub(now)
		mine := cd.Equal(today)
		switch {
		case remaining <= time.Hour:
			return pick(
				"Under an hour left. Finish clean.",
				"Home stretch: "+dur(remaining)+" to go.",
			)
		case mine && lastOfWeek:
			return fmt.Sprintf("Last shift of the work week. %s and it is behind you.", dur(remaining))
		case mine && tomorrowOff:
			return fmt.Sprintf("Off tomorrow. %s left today, then rest.", dur(remaining))
		case now.Sub(start) >= remaining:
			return pick(
				"Past the halfway mark: "+dur(remaining)+" to go.",
				"More behind you than ahead: "+dur(remaining)+" left.",
			)
		}
		return pick(
			dur(remaining)+" to go. One hour at a time.",
			"Settle in: "+dur(remaining)+" left, and each hour counts down.",
		)
	}

	switch {
	// 2. Today is a blank that still matters: the headline says what to do.
	case !todayKnown:
		return ""

	// 3. Day off.
	case te.off:
		switch {
		case tomorrowOff:
			return pick("Two days off in a row. Use them well.", "Off today and tomorrow. Rest properly.")
		case tomorrowKnown:
			return fmt.Sprintf("Day off. Tomorrow starts at %s, so tonight can be easy.", clock12(tme.start))
		}
		return pick("Day off. Nowhere to report to.", "Nothing on the schedule today. Enjoy it.")

	// 4. Shift still ahead today.
	case at(today, te.start).After(now):
		switch {
		case lastOfWeek && done > 0:
			return pick(
				fmt.Sprintf("Last shift of the work week. %d behind you, one to go.", done),
				"Last one this week. Then it is yours.",
			)
		case done == 0 && back == 0 && unknown == 0 && left > 1:
			return fmt.Sprintf("First of %d shifts this week. Start steady.", left)
		case tomorrowOff:
			return pick("Off tomorrow. Just this one to get through.", "One shift stands between you and a day off.")
		case run >= 5:
			return fmt.Sprintf("Day %d of a %d-day stretch. Pace yourself.", runDay, run)
		case done > 0:
			return capitalize(toGo()) + ". You know how to do this."
		}
		return pick(
			"You know when to be there. That is half of it.",
			"One shift, one day. You have this.",
			"Plenty of notice today. No need to rush.",
		)
	}

	// 5. Today's shift is finished.
	if tomorrowKnown && !tme.off && te.end >= 0 {
		_, end := te.span(today)
		if gap := at(tomorrow, tme.start).Sub(end); gap < 11*time.Hour {
			return fmt.Sprintf("Short turnaround: back at %s. Set things out tonight and sleep.", clock12(tme.start))
		}
	}
	switch {
	case lastOfWeek:
		return pick(
			fmt.Sprintf("Work week done: %d shifts behind you.", done),
			"That was the last one this week. Well earned.",
		)
	case tomorrowOff:
		return "Done for today, and tomorrow is off."
	}
	return "Done for today. " + capitalize(toGo()) + "."
}
