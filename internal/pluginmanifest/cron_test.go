package pluginmanifest

import (
	"testing"
	"time"
)

func TestCron(t *testing.T) {
	at := func(s string) time.Time {
		v, err := time.ParseInLocation("2006-01-02 15:04", s, time.Local)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, c := range []struct {
		expr string
		yes  []string
		no   []string
	}{
		{"*/10 * * * *", []string{"2026-10-27 09:00", "2026-10-27 09:50"}, []string{"2026-10-27 09:05"}},
		{"30 9 * * 1-5", []string{"2026-10-26 09:30", "2026-10-30 09:30"}, []string{"2026-10-25 09:30", "2026-10-26 09:31", "2026-10-26 10:30"}},
		{"0 0 1 * 0", []string{"2026-11-01 00:00", "2026-10-25 00:00"}, []string{"2026-10-27 00:00"}},
		{"0 12 * * 7", []string{"2026-10-25 12:00"}, []string{"2026-10-26 12:00"}},
		{"5,35 8-10/2 * 1,6 *", []string{"2026-01-05 08:05", "2026-06-05 10:35"}, []string{"2026-01-05 09:05", "2026-02-05 08:05"}},
		{"15/20 * * * *", []string{"2026-10-27 09:15", "2026-10-27 09:55"}, []string{"2026-10-27 09:00"}},
	} {
		cron, err := ParseCron(c.expr)
		if err != nil {
			t.Fatalf("%s：%v", c.expr, err)
		}
		for _, s := range c.yes {
			if !cron.Match(at(s)) {
				t.Errorf("%s 应在 %s 触发", c.expr, s)
			}
		}
		for _, s := range c.no {
			if cron.Match(at(s)) {
				t.Errorf("%s 不应在 %s 触发", c.expr, s)
			}
		}
	}
	for _, bad := range []string{"* * * *", "60 * * * *", "* 24 * * *", "* * 0 * *", "* * * 13 *", "* * * * 8", "*/0 * * * *", "a * * * *", "5-1 * * * *"} {
		if _, err := ParseCron(bad); err == nil {
			t.Errorf("%q 应不合法", bad)
		}
	}
}
