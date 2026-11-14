// wk - a small work-schedule keeper for the terminal.
//
// The first line of output always answers "when do I report next?".
// Data lives in a plain text file, one line per date:
//
//	2026-11-14 09:00-17:30
//	2026-11-15 off
//	2026-11-16 12:00-20:00 truck day
//
// A date with no line is an unknown day.
package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	dateFmt     = "2006-01-02"
	lateGrace   = 30 * time.Minute // after a shift starts, how long the headline says LATE
	todayCutoff = 18               // from this hour on, an unknown "today" is no longer asked about
	assumedLen  = 8 * 60           // minutes; used only when a shift has no end time
	askHorizon  = 14               // days that "wk ask" looks ahead

	// Exit codes describe the situation so scripts can react.
	codeOK      = 0
	codeSoon    = 1
	codeLate    = 2
	codeUnknown = 3
	codeError   = 10
)

const usage = `wk - when do I work?

  wk                 headline + the next 7 days; asks only if today/tomorrow is unknown
  wk -1              headline only (for shell profiles and scripts)
  wk week [next|N]   the Saturday-to-Friday work week (N = weeks from now, e.g. -1)
  wk ask [DAY]       fill in every unknown day ahead (from DAY), one question per day
  wk DAYS SHIFT...   set days:      wk mon 12-8 truck day     wk mon-wed,fri 9-5:30
  wk from DAY S S..  set a run of consecutive days, one shift each:
                                    wk from sat 9-5:30 = off off 12-8 12-8 =
  wk DAYS ?          forget days
  wk DAYS            show days

DAY    today, tom, sat..fri (the next one), +7 (days from today), 11/16, 2026-11-16
DAYS   one DAY, a range (mon-fri, 11/21-11/27), or a comma list of either
SHIFT  9-5:30   12-8   7a-3p   13:00-21:30   9 (end unknown)   off
       = means "same as the day before"; anything after the shift is a note.
       Bare hours are guessed: starts 7-11 are am, 12-6 are pm.

Exit code: 0 fine, 1 leave soon, 2 late, 3 today/tomorrow unknown.
Settings:  WK_FILE (data file path), WK_LEAD (minutes of warning, default 60),
           WK_COLOR=always|never (NO_COLOR is also honored), WK_CHEER=0 (no pep line).
`

// ---------------------------------------------------------------- data

type entry struct {
	off   bool
	start int // minutes after midnight
	end   int // minutes after midnight of the start day; >1440 if overnight; -1 if unknown
	note  string
}

type schedule map[string]entry

// Days are represented as UTC midnight of the local calendar date, so that
// day arithmetic is exact regardless of daylight saving time.
func dayOf(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// at returns the real local time that is mins minutes into day d.
func at(d time.Time, mins int) time.Time {
	y, m, dd := d.Date()
	return time.Date(y, m, dd, 0, mins, 0, 0, time.Local)
}

func addDays(d time.Time, n int) time.Time { return d.AddDate(0, 0, n) }
func daysBetween(a, b time.Time) int       { return int(b.Sub(a) / (24 * time.Hour)) }
func key(d time.Time) string               { return d.Format(dateFmt) }

func (e entry) span(d time.Time) (start, end time.Time) {
	start = at(d, e.start)
	if e.end < 0 {
		return start, at(d, e.start+assumedLen)
	}
	return start, at(d, e.end)
}

// ---------------------------------------------------------------- parsing

// parseClock reads one time of day. fixed reports whether am/pm is certain
// (an explicit suffix, or unmistakable 24-hour form such as 13:00 or 09:00).
// When not fixed, mins is the hour taken at face value (12 = noon).
func parseClock(s string, strict bool) (mins int, fixed bool, err error) {
	bad := fmt.Errorf("can't read time %q", s)
	s = strings.ToLower(s)
	suffix := ""
	for _, suf := range []string{"am", "pm", "a", "p"} {
		if strings.HasSuffix(s, suf) {
			suffix, s = suf[:1], strings.TrimSuffix(s, suf)
			break
		}
	}
	hs, ms := s, "0"
	if i := strings.Index(s, ":"); i >= 0 {
		hs, ms = s[:i], s[i+1:]
		if len(ms) != 2 {
			return 0, false, bad
		}
	} else if len(s) == 3 || len(s) == 4 { // 930, 1730
		hs, ms = s[:len(s)-2], s[len(s)-2:]
	}
	h, err1 := strconv.Atoi(hs)
	m, err2 := strconv.Atoi(ms)
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, false, bad
	}
	switch {
	case suffix != "":
		if strict || h < 1 || h > 12 {
			return 0, false, bad
		}
		h %= 12
		if suffix == "p" {
			h += 12
		}
		return h*60 + m, true, nil
	case strict || h == 0 || h > 12 || strings.HasPrefix(hs, "0"):
		return h*60 + m, true, nil
	}
	return h*60 + m, false, nil
}

// parseShift reads "off", "9-5:30", "7a-3p", "13:00-21:30" or a lone start
// time. With strict set (used for the data file) times are plain 24-hour and
// nothing is guessed.
func parseShift(s string, strict bool) (entry, error) {
	if strings.EqualFold(s, "off") {
		return entry{off: true}, nil
	}
	startS, endS, hasEnd := strings.Cut(s, "-")
	start, fixed, err := parseClock(startS, strict)
	if err != nil {
		return entry{}, err
	}
	if !fixed && start < 7*60 { // bare 1 through 6 as a start means afternoon
		start += 12 * 60
	}
	e := entry{start: start, end: -1}
	if !hasEnd {
		return e, nil
	}
	end, fixed, err := parseClock(endS, strict)
	if err != nil {
		return entry{}, err
	}
	switch {
	case fixed:
		if end <= start {
			end += 24 * 60 // overnight
		}
	default: // pick the first reading that falls after the start
		if end <= start && end < 12*60 {
			end += 12 * 60
		}
		if end <= start {
			end = end%(12*60) + 24*60 // overnight, ending in the morning
		}
	}
	e.end = end
	return e, nil
}

func clock24(mins int) string {
	mins %= 24 * 60
	return fmt.Sprintf("%02d:%02d", mins/60, mins%60)
}

func clock12(mins int) string {
	mins %= 24 * 60
	h, m, ap := mins/60, mins%60, "am"
	if h >= 12 {
		ap = "pm"
	}
	if h %= 12; h == 0 {
		h = 12
	}
	return fmt.Sprintf("%d:%02d%s", h, m, ap)
}

func (e entry) fileForm() string {
	s := "off"
	if !e.off {
		s = clock24(e.start)
		if e.end >= 0 {
			s += "-" + clock24(e.end)
		}
	}
	if e.note != "" {
		s += " " + e.note
	}
	return s
}

func (e entry) display() string {
	s := "off"
	if !e.off {
		s = clock12(e.start)
		if e.end >= 0 {
			s += "-" + clock12(e.end)
		}
	}
	if e.note != "" {
		s = fmt.Sprintf("%-16s %s", s, e.note)
	}
	return s
}

// ---------------------------------------------------------------- file

func dataPath() (string, error) {
	if p := os.Getenv("WK_FILE"); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "wk", "schedule.txt"), nil
}

func load(path string) (schedule, error) {
	db := schedule{}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return db, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		_, derr := time.Parse(dateFmt, f[0])
		if derr != nil || len(f) < 2 {
			return nil, fmt.Errorf("%s line %d: expected DATE SHIFT [note]", path, n)
		}
		e, err := parseShift(f[1], true)
		if err != nil {
			return nil, fmt.Errorf("%s line %d: %v", path, n, err)
		}
		e.note = strings.Join(f[2:], " ")
		db[f[0]] = e
	}
	return db, sc.Err()
}

func save(path string, db schedule) error {
	keys := make([]string, 0, len(db))
	for k := range db {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s %s\n", k, db[k].fileForm())
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ---------------------------------------------------------------- the headline

// todayOpen reports whether an unknown "today" is still worth worrying about.
func todayOpen(now time.Time) bool { return now.Hour() < todayCutoff }

// current returns the day whose shift is in progress right now, if any.
func current(db schedule, now time.Time) (time.Time, entry, bool) {
	today := dayOf(now)
	for _, d := range []time.Time{addDays(today, -1), today} {
		if e, ok := db[key(d)]; ok && !e.off {
			if s, en := e.span(d); !now.Before(s) && now.Before(en) {
				return d, e, true
			}
		}
	}
	return time.Time{}, entry{}, false
}

// upcoming scans forward from today and returns the first day that is either
// a shift that has not started yet (known=true) or an unknown day (known=false).
func upcoming(db schedule, now time.Time) (d time.Time, e entry, known bool) {
	today := dayOf(now)
	for d = today; ; d = addDays(d, 1) {
		e, ok := db[key(d)]
		switch {
		case !ok && d.Equal(today) && !todayOpen(now):
			continue // too late in the day to matter
		case !ok:
			return d, entry{}, false
		case e.off:
			continue
		case !at(d, e.start).After(now):
			continue // already started or finished
		}
		return d, e, true
	}
}

func dayLabel(d, today time.Time) string {
	switch daysBetween(today, d) {
	case 0:
		return "today"
	case 1:
		return "tomorrow"
	}
	return d.Format("Mon 2")
}

// dur formats a length of time, rounded up to the minute.
func dur(d time.Duration) string {
	mins := int((d + time.Minute - 1) / time.Minute)
	switch {
	case mins >= 48*60:
		return fmt.Sprintf("%d days", mins/(24*60))
	case mins >= 60:
		return fmt.Sprintf("%dh %02dm", mins/60, mins%60)
	}
	return fmt.Sprintf("%dm", mins)
}

func until(d time.Duration) string { return "in " + dur(d) }

func capitalize(s string) string { return strings.ToUpper(s[:1]) + s[1:] }

// head is the headline: the main text, an optional "(tomorrow unknown)" tail,
// the exit code for the situation, and the colors for the main text.
type head struct {
	text, tail string
	code       int
	style      []string
}

func (h head) plain() string  { return h.text + h.tail }
func (h head) String() string { return paint(h.text, h.style...) + paint(h.tail, yellow) }

func headline(db schedule, now time.Time, lead time.Duration) head {
	today := dayOf(now)
	tomorrow := addDays(today, 1)
	nd, ne, known := upcoming(db, now)
	urgentGap := !known && !nd.After(tomorrow)
	_, tomorrowKnown := db[key(tomorrow)]

	var text string
	code := codeOK
	style := []string{bold, cyan}

	if cd, ce, ok := current(db, now); ok {
		start, _ := ce.span(cd)
		switch {
		case now.Sub(start) < lateGrace:
			mins := int(now.Sub(start) / time.Minute)
			text = fmt.Sprintf("LATE: shift started %s, %dm ago.", clock12(ce.start), mins)
			code = codeLate
			style = []string{bold, red}
		case ce.end < 0:
			text = fmt.Sprintf("ON SHIFT since %s.", clock12(ce.start))
			style = []string{bold, green}
		default:
			text = fmt.Sprintf("ON SHIFT until %s.", clock12(ce.end))
			style = []string{bold, green}
		}
		switch {
		case known:
			text += fmt.Sprintf(" Next: %s %s", dayLabel(nd, today), clock12(ne.start))
		case urgentGap:
			text += fmt.Sprintf(" %s UNKNOWN", strings.ToUpper(dayLabel(nd, today)))
		default:
			text += fmt.Sprintf(" Next unknown (%s)", dayLabel(nd, today))
		}
	} else if known {
		wait := at(nd, ne.start).Sub(now)
		if wait <= lead {
			text = fmt.Sprintf("LEAVE SOON: %s, %s", clock12(ne.start), until(wait))
			code = codeSoon
			style = []string{bold, yellow}
		} else {
			text = fmt.Sprintf("NEXT: %s %s, %s", dayLabel(nd, today), clock12(ne.start), until(wait))
			if te, ok := db[key(today)]; ok && te.off {
				text = "Off today. " + text
			}
		}
	} else if urgentGap {
		text = fmt.Sprintf("%s UNKNOWN: find out your shift", strings.ToUpper(dayLabel(nd, today)))
		style = []string{bold, magenta}
	} else {
		text = fmt.Sprintf("No shift before %s, and %s is unknown", nd.Format("Mon 2"), nd.Format("Mon"))
		style = []string{bold}
	}

	// A known next shift must not hide the fact that tomorrow is a blank.
	tail := ""
	if !tomorrowKnown && !urgentGap {
		tail = "  (tomorrow unknown)"
		urgentGap = true
	}
	if urgentGap && code == codeOK {
		code = codeUnknown
	}
	return head{text, tail, code, style}
}

// urgentGaps lists the unknown days close enough to interrupt the user about.
func urgentGaps(db schedule, now time.Time) []time.Time {
	today := dayOf(now)
	var out []time.Time
	if _, ok := db[key(today)]; !ok && todayOpen(now) {
		out = append(out, today)
	}
	if _, ok := db[key(addDays(today, 1))]; !ok {
		out = append(out, addDays(today, 1))
	}
	return out
}

// ---------------------------------------------------------------- listing

func dayLine(db schedule, d time.Time, now time.Time) string {
	today := dayOf(now)
	lead := " "
	if d.Equal(today) {
		lead = ">"
	}
	date := fmt.Sprintf("%s %s %2d", lead, d.Format("Mon"), d.Day())
	body, bodyStyle := "?", []string{yellow}
	if e, ok := db[key(d)]; ok {
		body, bodyStyle = e.display(), nil
		if e.off {
			bodyStyle = []string{dim}
		}
	}
	mark, markStyle := "", []string{bold, cyan}
	if cd, _, ok := current(db, now); ok && cd.Equal(d) {
		mark, markStyle = "   <- now", []string{bold, green}
	} else if nd, _, known := upcoming(db, now); known && nd.Equal(d) {
		mark = "   <- next"
	}
	switch {
	case d.Before(today) && mark == "":
		return paint(date+"  "+body, dim) // the past is settled
	case d.Equal(today):
		date = paint(date, bold)
	}
	return date + "  " + paint(body, bodyStyle...) + paint(mark, markStyle...)
}

func list(w io.Writer, db schedule, from time.Time, n int, now time.Time) {
	for i := 0; i < n; i++ {
		fmt.Fprintln(w, dayLine(db, addDays(from, i), now))
	}
}

// weekStart returns the Saturday that begins d's work week.
func weekStart(d time.Time) time.Time {
	return addDays(d, -((int(d.Weekday()) + 1) % 7))
}

// ---------------------------------------------------------------- input

// parseAnswer turns what the user typed for day d into an entry.
// del is true when the answer means "forget this day".
func parseAnswer(db schedule, d time.Time, text string) (e entry, del bool, err error) {
	f := strings.Fields(text)
	if len(f) == 0 {
		return entry{}, false, errors.New("nothing to set")
	}
	switch f[0] {
	case "?":
		return entry{}, true, nil
	case "=":
		prev, ok := db[key(addDays(d, -1))]
		if !ok {
			return entry{}, false, errors.New("the day before is unknown, so = has nothing to copy")
		}
		e = prev
		e.note = ""
	default:
		if e, err = parseShift(f[0], false); err != nil {
			return entry{}, false, err
		}
	}
	if len(f) > 1 {
		e.note = strings.Join(f[1:], " ")
	}
	return e, false, nil
}

// ask questions the user about each day in turn. A blank answer ends the
// session; "?" skips one day. Every answer is saved immediately.
func ask(in *bufio.Reader, path string, db schedule, days []time.Time, now time.Time) (changed bool, err error) {
	for _, d := range days {
		for {
			fmt.Printf("%s %d > ", d.Format("Mon"), d.Day())
			line, rerr := in.ReadString('\n')
			line = strings.TrimSpace(line)
			if line == "" {
				if rerr != nil {
					fmt.Println()
				}
				fmt.Println("OK, I'll ask again later.")
				return changed, nil
			}
			e, del, perr := parseAnswer(db, d, line)
			if perr != nil {
				fmt.Printf("  %v\n  try: 9-5:30   12-8 truck day   off   =   ? to skip   Enter to stop\n", perr)
				if rerr != nil {
					return changed, nil
				}
				continue
			}
			if del {
				break
			}
			db[key(d)] = e
			if err := save(path, db); err != nil {
				return changed, err
			}
			changed = true
			fmt.Println(" ", dayLine(db, d, now))
			break
		}
	}
	return changed, nil
}

func parseDay(s string, today time.Time) (time.Time, error) {
	s = strings.ToLower(s)
	switch s {
	case "today", "tod":
		return today, nil
	case "tomorrow", "tom":
		return addDays(today, 1), nil
	}
	if strings.HasPrefix(s, "+") {
		if n, err := strconv.Atoi(s[1:]); err == nil && n <= 366 {
			return addDays(today, n), nil
		}
	}
	if len(s) >= 3 {
		for i := 0; i < 7; i++ {
			d := addDays(today, i)
			if strings.HasPrefix(strings.ToLower(d.Weekday().String()), s) {
				return d, nil
			}
		}
	}
	if d, err := time.Parse(dateFmt, s); err == nil {
		return d, nil
	}
	if ms, ds, ok := strings.Cut(s, "/"); ok {
		m, err1 := strconv.Atoi(ms)
		dd, err2 := strconv.Atoi(ds)
		if err1 == nil && err2 == nil && m >= 1 && m <= 12 && dd >= 1 && dd <= 31 {
			d := time.Date(today.Year(), time.Month(m), dd, 0, 0, 0, 0, time.UTC)
			if d.Day() != dd {
				return d, fmt.Errorf("no such date %q", s)
			}
			if daysBetween(d, today) > 180 { // long past: they mean next year
				d = d.AddDate(1, 0, 0)
			}
			return d, nil
		}
	}
	return today, fmt.Errorf("don't know the day %q", s)
}

// parseDays reads one day, a range (mon-fri), or a comma list of either.
// In a range, the end is looked for on or after the start.
func parseDays(s string, today time.Time) ([]time.Time, error) {
	var out []time.Time
	for _, part := range strings.Split(s, ",") {
		if d, err := parseDay(part, today); err == nil {
			out = append(out, d)
			continue
		}
		a, b, ok := strings.Cut(part, "-")
		if !ok {
			return nil, fmt.Errorf("don't know the day %q", part)
		}
		from, err := parseDay(a, today)
		if err != nil {
			return nil, err
		}
		to, err := parseDay(b, from)
		if err != nil {
			return nil, err
		}
		if n := daysBetween(from, to); n < 0 || n > 62 {
			return nil, fmt.Errorf("range %q runs backwards or is too long", part)
		}
		for d := from; !d.After(to); d = addDays(d, 1) {
			out = append(out, d)
		}
	}
	return out, nil
}

func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	if err != nil || st.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	// The null device is also a character device; it is not a person.
	if null, err := os.Stat(os.DevNull); err == nil && os.SameFile(st, null) {
		return false
	}
	return true
}

// ---------------------------------------------------------------- main

func run(args []string) (int, error) {
	now := time.Now()
	if s := os.Getenv("WK_NOW"); s != "" { // for testing: "2026-11-12 20:06"
		t, err := time.ParseInLocation("2006-01-02 15:04", s, time.Local)
		if err != nil {
			return codeError, fmt.Errorf("WK_NOW: %v", err)
		}
		now = t
	}
	lead := 60 * time.Minute
	if s := os.Getenv("WK_LEAD"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			return codeError, fmt.Errorf("WK_LEAD must be a number of minutes")
		}
		lead = time.Duration(n) * time.Minute
	}
	path, err := dataPath()
	if err != nil {
		return codeError, err
	}
	db, err := load(path)
	if err != nil {
		return codeError, err
	}

	initColor()
	today := dayOf(now)
	stdin := bufio.NewReader(os.Stdin)
	show := func() int {
		h := headline(db, now, lead)
		fmt.Println(h)
		return h.code
	}
	pep := func() {
		if os.Getenv("WK_CHEER") == "0" {
			return
		}
		if c := cheer(db, now); c != "" {
			fmt.Println(paint(c, dim))
		}
	}

	if len(args) == 0 {
		code := show()
		pep()
		fmt.Println()
		list(os.Stdout, db, today, 7, now)
		if gaps := urgentGaps(db, now); len(gaps) > 0 && isTerminal(os.Stdin) {
			fmt.Println()
			changed, err := ask(stdin, path, db, gaps, now)
			if err != nil {
				return codeError, err
			}
			if changed {
				fmt.Println()
				code = show()
			}
		}
		return code, nil
	}

	switch args[0] {
	case "-1":
		return show(), nil

	case "help", "-h", "--help":
		fmt.Print(usage)
		fmt.Println("Data file:", path)
		return codeOK, nil

	case "week":
		n := 0
		if len(args) > 1 {
			switch args[1] {
			case "next":
				n = 1
			case "last":
				n = -1
			default:
				if n, err = strconv.Atoi(args[1]); err != nil {
					return codeError, fmt.Errorf("wk week [next|last|N]")
				}
			}
		}
		code := show()
		pep()
		start := addDays(weekStart(today), 7*n)
		fmt.Printf("\nWeek of %s\n", start.Format("Mon Jan 2"))
		list(os.Stdout, db, start, 7, now)
		return code, nil

	case "ask":
		from := today
		if len(args) > 1 {
			if from, err = parseDay(args[1], today); err != nil {
				return codeError, err
			}
		}
		show()
		pep()
		fmt.Println()
		list(os.Stdout, db, from, 7, now)
		var days []time.Time
		for i := 0; i < askHorizon; i++ {
			d := addDays(from, i)
			if _, ok := db[key(d)]; !ok && (!d.Equal(today) || todayOpen(now)) {
				days = append(days, d)
			}
		}
		if len(days) == 0 {
			fmt.Printf("\nNothing unknown in those %d days.\n", askHorizon)
			return show(), nil
		}
		fmt.Println()
		if _, err := ask(stdin, path, db, days, now); err != nil {
			return codeError, err
		}
		fmt.Println()
		return show(), nil

	case "from":
		if len(args) < 3 {
			return codeError, fmt.Errorf("wk from DAY SHIFT SHIFT ...   (one shift per day; = repeats, ? forgets)")
		}
		start, err := parseDay(args[1], today)
		if err != nil {
			return codeError, err
		}
		// Nothing is saved unless every shift reads cleanly.
		for i, tok := range args[2:] {
			d := addDays(start, i)
			e, del, err := parseAnswer(db, d, tok)
			if err != nil {
				return codeError, fmt.Errorf("%s %d: %v (nothing saved)", d.Format("Mon"), d.Day(), err)
			}
			if del {
				delete(db, key(d))
			} else {
				db[key(d)] = e
			}
		}
		if err := save(path, db); err != nil {
			return codeError, err
		}
		list(os.Stdout, db, start, len(args)-2, now)
		return show(), nil
	}

	// wk DAYS [SHIFT...]
	days, err := parseDays(args[0], today)
	if err != nil {
		return codeError, fmt.Errorf("%v (try: wk help)", err)
	}
	if len(args) > 1 {
		answer := strings.Join(args[1:], " ")
		for _, d := range days {
			e, del, err := parseAnswer(db, d, answer)
			if err != nil {
				return codeError, err
			}
			if del {
				delete(db, key(d))
			} else {
				db[key(d)] = e
			}
		}
		if err := save(path, db); err != nil {
			return codeError, err
		}
	}
	for _, d := range days {
		fmt.Println(dayLine(db, d, now))
	}
	return show(), nil
}

func main() {
	code, err := run(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "wk:", err)
	}
	os.Exit(code)
}
