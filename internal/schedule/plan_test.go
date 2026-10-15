package schedule

import (
	"testing"
	"time"

	"dbak/internal/config"
)

func daily(clock, zone string) config.Schedule {
	return config.Schedule{Frequency: config.FrequencyDaily, Time: clock, Timezone: zone}
}

func weekly(day, clock, zone string) config.Schedule {
	return config.Schedule{Frequency: config.FrequencyWeekly, Weekday: day, Time: clock, Timezone: zone}
}

func everyHours(n int, zone string) config.Schedule {
	return config.Schedule{Frequency: config.FrequencyEveryNHours, EveryHours: n, Timezone: zone}
}

func mustPlan(t *testing.T, sc config.Schedule) Plan {
	t.Helper()
	p, err := NewPlan(sc)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestOnCalendarAndCron(t *testing.T) {
	cases := []struct {
		name     string
		schedule config.Schedule
		calendar string
		cron     string
	}{
		{"daily at 02:00", daily("02:00", "Asia/Jakarta"), "*-*-* 02:00:00 Asia/Jakarta", "0 2 * * *"},
		{"daily at 23:45", daily("23:45", "UTC"), "*-*-* 23:45:00 UTC", "45 23 * * *"},
		{"every 6 hours", everyHours(6, "Asia/Jakarta"), "*-*-* 00/6:00:00 Asia/Jakarta", "0 */6 * * *"},
		{"every hour", everyHours(1, "UTC"), "*-*-* 00/1:00:00 UTC", "0 */1 * * *"},
		{"weekly on Sunday", weekly("Sun", "02:00", "Asia/Jakarta"), "Sun *-*-* 02:00:00 Asia/Jakarta", "0 2 * * 0"},
		{"weekly on Saturday", weekly("Sat", "04:30", "Europe/Berlin"), "Sat *-*-* 04:30:00 Europe/Berlin", "30 4 * * 6"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := mustPlan(t, c.schedule)
			if got := p.OnCalendar(true); got != c.calendar {
				t.Fatalf("got OnCalendar %q, want %q", got, c.calendar)
			}
			if got := p.CronFields(); got != c.cron {
				t.Fatalf("got cron %q, want %q", got, c.cron)
			}
		})
	}
}

func TestOnCalendarWithoutZone(t *testing.T) {
	p := mustPlan(t, daily("02:00", "UTC"))
	if got := p.OnCalendar(false); got != "*-*-* 02:00:00" {
		t.Fatalf("got %q", got)
	}
}

func TestNewPlanRejectsBadValues(t *testing.T) {
	bad := []config.Schedule{
		daily("02:00", "Mars/Base"),
		daily("02:00", ""),
		daily("2:00", "UTC"),
		weekly("Sunday", "02:00", "UTC"),
		everyHours(5, "UTC"),
		{Frequency: "monthly", Timezone: "UTC"},
	}
	for _, sc := range bad {
		if _, err := NewPlan(sc); err == nil {
			t.Fatalf("got no error for %+v", sc)
		}
	}
}

func TestPlanIn(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		schedule config.Schedule
		zone     string
		calendar string
		cron     string
	}{
		{"daily moves to the day before", daily("02:00", "Asia/Jakarta"), "UTC", "*-*-* 19:00:00 UTC", "0 19 * * *"},
		{"daily in the same day", daily("20:00", "Asia/Jakarta"), "UTC", "*-*-* 13:00:00 UTC", "0 13 * * *"},
		{"weekly moves to Saturday", weekly("Sun", "02:00", "Asia/Jakarta"), "UTC", "Sat *-*-* 19:00:00 UTC", "0 19 * * 6"},
		{"weekly moves to Monday", weekly("Sun", "22:30", "America/New_York"), "UTC", "Mon *-*-* 02:30:00 UTC", "30 2 * * 1"},
		{"every 6 hours with an offset", everyHours(6, "Asia/Jakarta"), "UTC", "*-*-* 05/6:00:00 UTC", "0 5-23/6 * * *"},
		{"every 4 hours with a half hour", everyHours(4, "Asia/Kolkata"), "UTC", "*-*-* 02/4:30:00 UTC", "30 2-23/4 * * *"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := mustPlan(t, c.schedule).In(c.zone, now)
			if err != nil {
				t.Fatal(err)
			}
			if got := p.OnCalendar(true); got != c.calendar {
				t.Fatalf("got OnCalendar %q, want %q", got, c.calendar)
			}
			if got := p.CronFields(); got != c.cron {
				t.Fatalf("got cron %q, want %q", got, c.cron)
			}
		})
	}
	if _, err := mustPlan(t, daily("02:00", "UTC")).In("Bad/Zone", now); err == nil {
		t.Fatal("got no error for an unknown timezone")
	}
}

func TestNext(t *testing.T) {
	jakarta, _ := time.LoadLocation("Asia/Jakarta")
	berlin, _ := time.LoadLocation("Europe/Berlin")
	cases := []struct {
		name     string
		schedule config.Schedule
		after    time.Time
		want     time.Time
	}{
		{"daily later today", daily("02:00", "Asia/Jakarta"), time.Date(2026, 10, 15, 1, 0, 0, 0, jakarta), time.Date(2026, 10, 15, 2, 0, 0, 0, jakarta)},
		{"daily at the exact time moves to tomorrow", daily("02:00", "Asia/Jakarta"), time.Date(2026, 10, 15, 2, 0, 0, 0, jakarta), time.Date(2026, 10, 16, 2, 0, 0, 0, jakarta)},
		{"daily over the end of the year", daily("02:00", "Asia/Jakarta"), time.Date(2026, 12, 31, 3, 0, 0, 0, jakarta), time.Date(2027, 1, 1, 2, 0, 0, 0, jakarta)},
		{"weekly next Sunday", weekly("Sun", "02:00", "Asia/Jakarta"), time.Date(2026, 10, 15, 12, 0, 0, 0, jakarta), time.Date(2026, 10, 18, 2, 0, 0, 0, jakarta)},
		{"weekly on the same day after the time", weekly("Thu", "02:00", "Asia/Jakarta"), time.Date(2026, 10, 15, 12, 0, 0, 0, jakarta), time.Date(2026, 10, 22, 2, 0, 0, 0, jakarta)},
		{"every 6 hours", everyHours(6, "Asia/Jakarta"), time.Date(2026, 10, 15, 13, 0, 0, 0, jakarta), time.Date(2026, 10, 15, 18, 0, 0, 0, jakarta)},
		{"every 6 hours after midnight", everyHours(6, "Asia/Jakarta"), time.Date(2026, 10, 15, 19, 0, 0, 0, jakarta), time.Date(2026, 10, 16, 0, 0, 0, 0, jakarta)},
		{"daily in the daylight saving change", daily("02:30", "Europe/Berlin"), time.Date(2026, 10, 24, 12, 0, 0, 0, berlin), time.Date(2026, 10, 25, 2, 30, 0, 0, berlin)},
		{"input in a different timezone", daily("02:00", "Asia/Jakarta"), time.Date(2026, 10, 15, 18, 30, 0, 0, time.UTC), time.Date(2026, 10, 16, 2, 0, 0, 0, jakarta)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mustPlan(t, c.schedule).Next(c.after)
			if !got.Equal(c.want) {
				t.Fatalf("got %s, want %s", got, c.want)
			}
		})
	}
}

func TestDescribe(t *testing.T) {
	cases := map[string]config.Schedule{
		"daily at 02:00 Asia/Jakarta":                             daily("02:00", "Asia/Jakarta"),
		"weekly on Sun at 02:00 UTC":                              weekly("Sun", "02:00", "UTC"),
		"every 6 hours (00:00, 06:00, 12:00, 18:00) Asia/Jakarta": everyHours(6, "Asia/Jakarta"),
	}
	for want, sc := range cases {
		if got := mustPlan(t, sc).Describe(); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
}

func TestUsesDaylightSaving(t *testing.T) {
	if !UsesDaylightSaving("Europe/Berlin", 2026) {
		t.Fatal("got false for Europe/Berlin")
	}
	if UsesDaylightSaving("Asia/Jakarta", 2026) || UsesDaylightSaving("UTC", 2026) {
		t.Fatal("got true for a timezone without daylight saving time")
	}
}
