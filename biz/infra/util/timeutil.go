package util

import (
	"fmt"
	"time"
)

var CST = time.FixedZone("CST", 8*3600)

func DayToUTCRange(date string) (time.Time, time.Time, error) {
	dayStart, err := time.ParseInLocation("2006-01-02", date, CST)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("parse date %s: %w", date, err)
	}
	dayEnd := dayStart.Add(24 * time.Hour)
	return dayStart.UTC(), dayEnd.UTC(), nil
}

func FormatDateUTC8(t time.Time) string {
	return t.In(CST).Format("2006-01-02")
}
