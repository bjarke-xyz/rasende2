package lang

var enMsgs = map[string]string{
	"brand":         "Outrage",
	"nav.search":    "Search",
	"nav.fakeNews":  "Fake News",
	"flash.close":   "Close",
	"footer.login":  "Login",
	"footer.logout": "Logout",

	"page.index":            "Outrage in the media",
	"page.search":           "Search | Outrage",
	"page.fakeNews":         "Fake News | Outrage",
	"page.fakeNewsArticle":  "Fake News | Outrage",
	"page.titleGenerator":   "Title Generator | Outrage",
	"page.articleGenerator": "Article Generator | Outrage",
	"page.login":            "Login | Outrage",
	"page.error":            "Error | Outrage",

	"index.latest":  "Latest outrage:",
	"index.none":    "No outrage!",
	"index.earlier": "Earlier outrages:",
	"footer.credit": "Inspired by",

	"search.content":  "Search article content",
	"search.loadMore": "Load more",

	"chart.line.title":        "This week's outrages",
	"chart.line.dataset":      "Outbursts",
	"chart.pie.title":         "Outrage across the media",
	"chart.line.titleQuery":   "This week's use of '%v'",
	"chart.line.datasetQuery": "Number of '%v'",
	"chart.pie.titleQuery":    "Use of '%v' across the media",

	"fakeNews.heading": "Fake News",
	"fakeNews.create":  "Create a fake news article",
	"fakeNews.sorting": "Sorting",
	"fakeNews.popular": "Most popular",
	"fakeNews.newest":  "Newest",

	"common.showMore": "Show more",
	"common.readMore": "Read more",
	"vote.up":         "Vote up",
	"vote.down":       "Vote down",

	"titleGenerator.site":       "News site",
	"titleGenerator.choose":     "Choose",
	"titleGenerator.generating": "Coming up with headlines...",

	"articleGenerator.publish": "Publish fake news",

	"admin.toggleFeatured":   "Toggle featured",
	"admin.resetContent":     "Reset content",
	"admin.articleGenerator": "Article generator",

	"login.heading":          "Login",
	"login.email":            "Email",
	"login.password":         "Password",
	"login.otp":              "One-Time Password (OTP)",
	"login.emailPlaceholder": "Enter your email",
	"login.submit":           "Login",

	"error.prefix":        "Error:",
	"error.unknown":       "unknown error",
	"error.requiresAdmin": "Requires admin",
	"error.tryAgainLater": "Try again later",

	"auth.invalidEmail": "Invalid email",
	"auth.userNotFound": "User not found. Sign-up is disabled.",
	"auth.badCode":      "That code does not work",
	"auth.badLink":      "That link does not work",
	"auth.genericError": "something went wrong",
	"auth.loggedIn":     "You are now logged in!",
	"auth.loggedOut":    "You are now logged out!",
	"auth.checkMail":    "Check your mail!",

	// Args: name, sign-in url, minutes until expiry, formatted OTP.
	"mail.signIn.subject": "Your link to sign in",
	"mail.signIn.body": `
Hey %v,

Click here to sign in:

%v

This link expires in %v minutes.

Or enter this One-Time-Password (OTP):

%v

If you didn't ask for this, just ignore it.

-  Outrage`,
}
