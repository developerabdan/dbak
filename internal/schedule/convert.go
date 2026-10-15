package schedule

import (
	"fmt"
	"time"

	"github.com/developerabdan/dbak/internal/config"
)

func ConvertClock(clock, fromZone, toZone string, day time.Time) (string, error) {
	hour, minute, err := config.ParseClock(clock)
	if err != nil {
		return "", err
	}
	from, err := time.LoadLocation(fromZone)
	if err != nil {
		return "", fmt.Errorf("the timezone %q is not known", fromZone)
	}
	to, err := time.LoadLocation(toZone)
	if err != nil {
		return "", fmt.Errorf("the timezone %q is not known", toZone)
	}
	local := day.In(from)
	start := time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, from)
	converted := start.In(to)
	text := fmt.Sprintf("%s %s = %s %s", start.Format("15:04"), fromZone, converted.Format("15:04"), toZone)
	switch dayOffset(start, converted) {
	case -1:
		text += " on the day before"
	case 1:
		text += " on the next day"
	}
	return text, nil
}

func dayOffset(a, b time.Time) int {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	da := time.Date(ay, am, ad, 0, 0, 0, 0, time.UTC)
	db := time.Date(by, bm, bd, 0, 0, 0, 0, time.UTC)
	return int(db.Sub(da).Hours() / 24)
}
