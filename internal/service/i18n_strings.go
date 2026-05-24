package service

// translations holds the few server-side strings still consumed by
// /api/site/config when it renders the "About this server" card. All
// other translation keys live in the Vue SPA's web/src/i18n/{de,en}.json
// and never reach Go.
//
// Keep this map in sync with the corresponding entries in the SPA JSON
// files so the inline info card matches the rest of the page.
var translations = map[Lang]map[string]string{
	LangDE: {
		"landing.about.title":    "Ueber diesen Server",
		"landing.about.cluster":  "Cluster",
		"landing.about.operator": "Betreiber",
		"landing.about.contact":  "Kontakt",
	},
	LangEN: {
		"landing.about.title":    "About this server",
		"landing.about.cluster":  "Cluster",
		"landing.about.operator": "Operator",
		"landing.about.contact":  "Contact",
	},
}
