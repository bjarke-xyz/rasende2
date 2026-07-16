package lang

import (
	"time"

	"github.com/xeonx/timeago"
)

var danishTimeAgo = timeago.Config{
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

var daMsgs = map[string]string{
	"brand":         "Rasende",
	"nav.search":    "Søg",
	"nav.fakeNews":  "Fake News",
	"flash.close":   "Luk",
	"footer.login":  "Login",
	"footer.logout": "Logout",

	"page.index":            "Raseri i de danske medier",
	"page.search":           "Søg | Rasende",
	"page.fakeNews":         "Fake News | Rasende",
	"page.fakeNewsArticle":  "Fake News | Rasende",
	"page.titleGenerator":   "Overskriftsgenerator | Rasende",
	"page.articleGenerator": "Artikelgenerator | Rasende",
	"page.login":            "Login | Rasende",
	"page.error":            "Fejl | Rasende",

	"index.latest":  "Seneste raseri:",
	"index.none":    "Ingen raseri!",
	"index.earlier": "Tidligere raserier:",
	"footer.credit": "Inspireret af",

	"search.content":  "Søg i artikel indhold",
	"search.loadMore": "Hent flere",

	"chart.line.title":        "Den seneste uges raserier",
	"chart.line.dataset":      "Raseriudbrud",
	"chart.pie.title":         "Raseri i de forskellige medier",
	"chart.line.titleQuery":   "Den seneste uges brug af '%v'",
	"chart.line.datasetQuery": "Antal '%v'",
	"chart.pie.titleQuery":    "Brug af '%v' i de forskellige medier",

	"fakeNews.heading": "Falske Nyheder",
	"fakeNews.create":  "Opret en falsk nyhed",
	"fakeNews.sorting": "Sortering",
	"fakeNews.popular": "Mest populære",
	"fakeNews.newest":  "Nyeste",

	"common.showMore": "Vis mere",
	"common.readMore": "Læs mere",
	"vote.up":         "Stem op",
	"vote.down":       "Stem ned",

	"titleGenerator.site":       "Nyhedsmedie",
	"titleGenerator.choose":     "Vælg",
	"titleGenerator.generating": "Finder på overskrifter...",

	"articleGenerator.publish": "Udgiv falsk nyhed",

	"admin.toggleFeatured":   "Fremhæv",
	"admin.resetContent":     "Nulstil indhold",
	"admin.articleGenerator": "Artikelgenerator",

	"error.prefix":        "Fejl:",
	"error.unknown":       "ukendt fejl",
	"error.requiresAdmin": "Kræver admin",
	"error.tryAgainLater": "Prøv igen senere",

	"auth.invalidEmail": "Ugyldig email",
	"auth.userNotFound": "Bruger ikke fundet. Registrering er deaktiveret.",
	"auth.badCode":      "Koden virker ikke",
	"auth.badLink":      "Linket virker ikke",
	"auth.genericError": "der skete en fejl",
	"auth.loggedIn":     "Du er nu logget ind!",
	"auth.loggedOut":    "Du er nu logget ud!",
	"auth.checkMail":    "Tjek din mail!",

	// Args: name, sign-in url, minutes until expiry, formatted OTP.
	"mail.signIn.subject": "Dit link til at logge ind",
	"mail.signIn.body": `
Hej %v,

Klik her for at logge ind:

%v

Linket udløber om %v minutter.

Eller indtast denne engangskode (OTP):

%v

Hvis du ikke har bedt om dette, så bare ignorer det.

-  Rasende`,
}
