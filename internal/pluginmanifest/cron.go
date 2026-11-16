package pluginmanifest

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Cron is the five-field cron of the manifest's schedule[]: minute hour day-of-month month day-of-week, matched
// against the server's local time. Each field can be *, a number, an a-b range, a comma-separated list, and */n or
// a-b/n steps; both 0 and 7 in day-of-week are Sunday. When neither day-of-month nor day-of-week is *, matching
// either one is enough (as in common cron implementations).
type Cron struct {
	minute, hour, dom, month, dow uint64
	domAny, dowAny                bool
}

// ParseCron parses a five-field cron expression.
func ParseCron(expr string) (Cron, error) {
	f := strings.Fields(expr)
	if len(f) != 5 {
		return Cron{}, fmt.Errorf("cron 须是五段（分 时 日 月 周）：%q", expr)
	}
	var c Cron
	var err error
	for _, part := range []struct {
		field    string
		min, max int
		bits     *uint64
	}{{f[0], 0, 59, &c.minute}, {f[1], 0, 23, &c.hour}, {f[2], 1, 31, &c.dom}, {f[3], 1, 12, &c.month}, {f[4], 0, 7, &c.dow}} {
		if *part.bits, err = cronField(part.field, part.min, part.max); err != nil {
			return Cron{}, err
		}
	}
	if c.dow&(1<<7) != 0 {
		c.dow |= 1
	}
	c.domAny, c.dowAny = f[2] == "*", f[4] == "*"
	return c, nil
}

func cronField(field string, min, max int) (uint64, error) {
	var bits uint64
	for _, item := range strings.Split(field, ",") {
		rng, step := item, 1
		if r, s, ok := strings.Cut(item, "/"); ok {
			n, err := strconv.Atoi(s)
			if err != nil || n < 1 {
				return 0, fmt.Errorf("cron 步长不合法：%q", item)
			}
			rng, step = r, n
		}
		lo, hi := min, max
		if rng != "*" {
			a, b, isRange := strings.Cut(rng, "-")
			var err error
			if lo, err = strconv.Atoi(a); err != nil {
				return 0, fmt.Errorf("cron 字段不合法：%q", item)
			}
			hi = lo
			if isRange {
				if hi, err = strconv.Atoi(b); err != nil {
					return 0, fmt.Errorf("cron 字段不合法：%q", item)
				}
			} else if step > 1 {
				hi = max
			}
		}
		if lo < min || hi > max || lo > hi {
			return 0, fmt.Errorf("cron 字段超出范围 %d-%d：%q", min, max, item)
		}
		for v := lo; v <= hi; v += step {
			bits |= 1 << v
		}
	}
	return bits, nil
}

// Match reports whether the minute containing t should fire.
func (c Cron) Match(t time.Time) bool {
	if c.minute&(1<<t.Minute()) == 0 || c.hour&(1<<t.Hour()) == 0 || c.month&(1<<int(t.Month())) == 0 {
		return false
	}
	dom, dow := c.dom&(1<<t.Day()) != 0, c.dow&(1<<int(t.Weekday())) != 0
	if c.domAny || c.dowAny {
		return dom && dow
	}
	return dom || dow
}
