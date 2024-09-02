package config

import (
	"time"

	"github.com/xeonx/timeago"
)

var DanishTimeagoConfig = timeago.Config{
	PastPrefix:   "",
	PastSuffix:   " siden",
	FuturePrefix: "om ",
	FutureSuffix: "",

	Periods: []timeago.FormatPeriod{
		{D: time.Second, One: "cirka et sekund", Many: "%d sekunder"},
		{D: time.Minute, One: "cirka et minut", Many: "%d minutter"},
		{D: time.Hour, One: "cirka en time", Many: "%d timer"},
		{D: timeago.Day, One: "en dag", Many: "%d dage"},
		{D: timeago.Month, One: "en måned", Many: "%d måneder"},
		{D: timeago.Year, One: "et år", Many: "%d år"},
	},

	Zero: "cirka et sekund",

	Max:           73 * time.Hour,
	DefaultLayout: "2006-01-02",
}
