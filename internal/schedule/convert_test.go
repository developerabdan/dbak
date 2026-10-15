package schedule

import (
	"testing"
	"time"
)

func TestConvertClock(t *testing.T) {
	day := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		clock, from, to, want string
	}{
		{"02:00", "Asia/Jakarta", "UTC", "02:00 Asia/Jakarta = 19:00 UTC on the day before"},
		{"20:00", "Asia/Jakarta", "UTC", "20:00 Asia/Jakarta = 13:00 UTC"},
		{"22:30", "America/New_York", "UTC", "22:30 America/New_York = 02:30 UTC on the next day"},
		{"02:00", "UTC", "UTC", "02:00 UTC = 02:00 UTC"},
	}
	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) {
			got, err := ConvertClock(c.clock, c.from, c.to, day)
			if err != nil || got != c.want {
				t.Fatalf("got %q %v, want %q", got, err, c.want)
			}
		})
	}
	if _, err := ConvertClock("25:00", "UTC", "UTC", day); err == nil {
		t.Fatal("got no error for a bad time")
	}
}
