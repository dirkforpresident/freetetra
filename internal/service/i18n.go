package service

import (
	"net/http"
	"strings"
)

// Lang ist ein Sprach-Code, "de" oder "en".
type Lang string

const (
	LangDE Lang = "de"
	LangEN Lang = "en"

	langCookieName = "ft_lang"
	langCookieTTL  = 365 * 24 * 3600 // 1 Jahr
)

// detectLang liest die User-Sprachpraeferenz aus Cookie (User-Override),
// dann Accept-Language Header (Browser-Default), Fallback DE.
func detectLang(r *http.Request) Lang {
	if c, err := r.Cookie(langCookieName); err == nil {
		switch Lang(c.Value) {
		case LangDE, LangEN:
			return Lang(c.Value)
		}
	}
	if al := r.Header.Get("Accept-Language"); al != "" {
		al = strings.ToLower(al)
		// Sehr simple Heuristik: erste Sprache im Header gewinnt.
		for _, part := range strings.Split(al, ",") {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(part, "de") {
				return LangDE
			}
			if strings.HasPrefix(part, "en") {
				return LangEN
			}
		}
	}
	return LangDE
}

// setLangCookie setzt das Sprach-Cookie.
func setLangCookie(w http.ResponseWriter, lang Lang) {
	http.SetCookie(w, &http.Cookie{
		Name:     langCookieName,
		Value:    string(lang),
		Path:     "/",
		MaxAge:   langCookieTTL,
		HttpOnly: false,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Service) handleLangSwitch(w http.ResponseWriter, r *http.Request) {
	// /lang/de oder /lang/en
	path := strings.TrimPrefix(r.URL.Path, "/lang/")
	var lang Lang
	switch path {
	case "de":
		lang = LangDE
	case "en":
		lang = LangEN
	default:
		http.NotFound(w, r)
		return
	}
	setLangCookie(w, lang)
	// Redirect zum Referer wenn vorhanden, sonst zur Startseite.
	ref := r.Header.Get("Referer")
	if ref == "" {
		ref = "/"
	}
	http.Redirect(w, r, ref, http.StatusSeeOther)
}

func (s *Service) registerLangHandlers() {
	s.server.RegisterHTTPHandler("/lang/de", s.handleLangSwitch)
	s.server.RegisterHTTPHandler("/lang/en", s.handleLangSwitch)
}

// translate ersetzt {{T:key}} Platzhalter im HTML mit dem Wert aus der
// Translations-Map fuer die gewuenschte Sprache. Wenn ein Key fehlt:
// Fallback auf DE. Wenn auch dort fehlt: leere Zeichenkette.
func translate(htmlText string, lang Lang) string {
	m := translations[lang]
	if m == nil {
		m = translations[LangDE]
	}
	// Simple textual replacement: every "{{T:foo}}" becomes m["foo"].
	for key, val := range m {
		htmlText = strings.ReplaceAll(htmlText, "{{T:"+key+"}}", val)
	}
	return htmlText
}

// langSwitchHTML ist der kleine Sprach-Toggle der oben rechts auf jeder Page
// angezeigt wird. Aktive Sprache wird hervorgehoben.
func langSwitchHTML(active Lang) string {
	deClass := ""
	enClass := ""
	if active == LangDE {
		deClass = " lang-active"
	} else {
		enClass = " lang-active"
	}
	return `<a href="/lang/de" class="lang-link` + deClass + `">DE</a> · <a href="/lang/en" class="lang-link` + enClass + `">EN</a>`
}
