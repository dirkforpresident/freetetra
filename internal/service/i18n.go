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
)

// detectLang liest die User-Sprachpraeferenz aus Cookie (User-Override),
// dann Accept-Language Header (Browser-Default), Fallback DE.
//
// /api/site/config consumes this so the Vue SPA can preselect the right
// vue-i18n locale. The full /lang/de + /lang/en redirect handlers were
// removed when the Go-rendered HTML pages were deleted; the Vue
// LangSwitch component now writes the ft_lang cookie client-side.
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
