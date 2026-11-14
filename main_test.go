package main

import (
	"strings"
	"testing"
	"time"
)

func TestParseShift(t *testing.T) {
	cases := []struct {
		in, want string // want is the file form
	}{
		{"9-5:30", "09:00-17:30"},
		{"12-8", "12:00-20:00"},
		{"7a-3p", "07:00-15:00"},
		{"2-10", "14:00-22:00"},
		{"8-12", "08:00-12:00"},
		{"13:00-21:30", "13:00-21:30"},
		{"930-6", "09:30-18:00"},
		{"10p-6", "22:00-06:00"},
		{"9", "09:00"},
		{"OFF", "off"},
	}
	for _, c := range cases {
		e, err := parseShift(c.in, false)
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if got := e.fileForm(); got != c.want {
			t.Errorf("%q: got %s, want %s", c.in, got, c.want)
		}
		// What is written to the file must read back identically.
		back, err := parseShift(strings.Fields(e.fileForm())[0], true)
		if err != nil || back != e {
			t.Errorf("%q: round trip gave %+v (%v), want %+v", c.in, back, err, e)
		}
	}
	for _, bad := range []string{"bogus", "25-9", "9-5:3", "13p-2", ""} {
		if _, err := parseShift(bad, false); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
}

func mk(t *testing.T, lines ...string) schedule {
	t.Helper()
	db := schedule{}
	for _, l := range lines {
		f := strings.Fields(l)
		e, err := parseShift(f[1], false)
		if err != nil {
			t.Fatal(err)
		}
		db[f[0]] = e
	}
	return db
}

func clockAt(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, time.Local)
	if err != nil {
		panic(err)
	}
	return t
}

func TestHeadline(t *testing.T) {
	db := mk(t,
		"2026-11-13 9-5:30", "2026-11-14 9-5:30", "2026-11-15 off",
		"2026-11-16 12-8", "2026-11-20 10p-6")
	cases := []struct {
		now, want string
		code      int
	}{
		{"2026-11-12 20:06", "NEXT: tomorrow 9:00am, in 12h 54m", codeOK},
		{"2026-11-13 08:20", "LEAVE SOON: 9:00am, in 40m", codeSoon},
		{"2026-11-13 09:15", "LATE: shift started 9:00am, 15m ago. Next: tomorrow 9:00am", codeLate},
		{"2026-11-13 13:00", "ON SHIFT until 5:30pm. Next: tomorrow 9:00am", codeOK},
		{"2026-11-15 10:00", "Off today. NEXT: tomorrow 12:00pm, in 26h 00m", codeOK},
		{"2026-11-16 21:00", "TOMORROW UNKNOWN: find out your shift", codeUnknown},
		{"2026-11-17 09:00", "TODAY UNKNOWN: find out your shift", codeUnknown},
		{"2026-11-20 12:00", "NEXT: today 10:00pm, in 10h 00m  (tomorrow unknown)", codeUnknown},
		{"2026-11-21 02:00", "ON SHIFT until 6:00am. TODAY UNKNOWN", codeUnknown},
	}
	for _, c := range cases {
		h := headline(db, clockAt(c.now), time.Hour)
		if h.plain() != c.want || h.code != c.code {
			t.Errorf("%s:\n got  %q (%d)\n want %q (%d)", c.now, h.plain(), h.code, c.want, c.code)
		}
	}
}

func TestWeekStartIsSaturday(t *testing.T) {
	for i := 0; i < 14; i++ {
		d := addDays(time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC), i)
		ws := weekStart(d)
		if ws.Weekday() != time.Saturday || ws.After(d) || daysBetween(ws, d) > 6 {
			t.Errorf("%s: week start %s", key(d), key(ws))
		}
	}
}

func TestParseDays(t *testing.T) {
	today := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC) // a Saturday
	cases := []struct {
		in   string
		want []string
	}{
		{"today", []string{"2026-11-14"}},
		{"sat", []string{"2026-11-14"}},
		{"+7", []string{"2026-11-21"}},
		{"mon-wed", []string{"2026-11-16", "2026-11-17", "2026-11-18"}},
		{"mon,fri", []string{"2026-11-16", "2026-11-20"}},
		{"thu-sat", []string{"2026-11-19", "2026-11-20", "2026-11-21"}},
		{"11/21-11/22,tom", []string{"2026-11-21", "2026-11-22", "2026-11-15"}},
		{"2026-12-01", []string{"2026-12-01"}},
	}
	for _, c := range cases {
		days, err := parseDays(c.in, today)
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		var got []string
		for _, d := range days {
			got = append(got, key(d))
		}
		if strings.Join(got, " ") != strings.Join(c.want, " ") {
			t.Errorf("%q: got %v, want %v", c.in, got, c.want)
		}
	}
	for _, bad := range []string{"xyz", "fri-xyz", "9-5"} {
		if _, err := parseDays(bad, today); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
}

func TestCheer(t *testing.T) {
	// A full work week, Sat Nov 14 to Fri Nov 20: five shifts, Tue and Wed off.
	db := mk(t,
		"2026-11-14 9-5:30", "2026-11-15 9-5:30", "2026-11-16 12-8",
		"2026-11-17 off", "2026-11-18 off", "2026-11-19 9-5:30", "2026-11-20 9-5:30")
	cases := []struct{ now, want string }{
		{"2026-11-14 07:00", "First of 5 shifts"},
		{"2026-11-14 09:10", "late"},
		{"2026-11-14 14:00", "3h 30m"},
		{"2026-11-15 19:00", "2 down, 3 to go"},
		{"2026-11-16 09:00", "Off tomorrow"},
		{"2026-11-17 10:00", "tomorrow"},
		{"2026-11-18 10:00", "Tomorrow starts at 9:00am"},
		{"2026-11-20 07:00", "Last"},
		{"2026-11-20 12:00", "Last shift of the work week"},
	}
	for _, c := range cases {
		got := cheer(db, clockAt(c.now))
		if !strings.Contains(strings.ToLower(got), strings.ToLower(c.want)) {
			t.Errorf("%s: got %q, want it to mention %q", c.now, got, c.want)
		}
	}
	// A blank today is a call to action, not a moment for cheer.
	if got := cheer(schedule{}, clockAt("2026-11-14 07:00")); got != "" {
		t.Errorf("blank day: got %q, want nothing", got)
	}
}
