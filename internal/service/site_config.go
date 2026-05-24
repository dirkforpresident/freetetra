package service

import (
	"encoding/json"
	"html"
	"net/http"
	"strings"
)

// registerSiteConfigHandlers exposes the static substitutions that were
// previously injected into rendered HTML ({{HOST}}, {{SERVER_NAME}},
// {{OPERATOR}}, {{SERVER_INFO_CARD}}). The Vue SPA fetches /api/site/config
// once on boot and renders the values itself.
func (s *Service) registerSiteConfigHandlers() {
	s.server.RegisterHTTPHandler("/api/site/config", s.handleSiteConfig)
}

type siteConfigResponse struct {
	ServerName     string `json:"server_name"`
	Operator       string `json:"operator"`
	Host           string `json:"host"`
	ServerInfoHTML string `json:"server_info_html"`
	Lang           string `json:"lang"`
}

func (s *Service) handleSiteConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	serverName := s.cfg.Federation.Name
	if serverName == "" {
		serverName = "FreeTetra"
	}
	op := s.cfg.Operator
	operator := op.Name
	if operator == "" {
		operator = serverName
	}
	lang := detectLang(r)

	infoHTML := ""
	if op.Name != "" || op.Contact != "" || op.Description != "" {
		t := translations[lang]
		if t == nil {
			t = translations[LangDE]
		}
		var b strings.Builder
		b.WriteString(`<div class="card"><h2>` + t["landing.about.title"] + `</h2>`)
		b.WriteString(`<p><strong>` + html.EscapeString(r.Host) + `</strong> — ` + t["landing.about.cluster"] + ` <code>` + html.EscapeString(serverName) + `</code></p>`)
		if op.Description != "" {
			b.WriteString(`<p>` + html.EscapeString(op.Description) + `</p>`)
		}
		b.WriteString(`<p style="font-size:0.88rem;color:var(--text-muted)">`)
		if op.Name != "" {
			b.WriteString(t["landing.about.operator"] + `: <strong>` + html.EscapeString(op.Name) + `</strong>`)
		}
		if op.Contact != "" {
			if op.Name != "" {
				b.WriteString(` · `)
			}
			b.WriteString(t["landing.about.contact"] + `: <code>` + html.EscapeString(op.Contact) + `</code>`)
		}
		b.WriteString(`</p></div>`)
		infoHTML = b.String()
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(siteConfigResponse{
		ServerName:     serverName,
		Operator:       operator,
		Host:           r.Host,
		ServerInfoHTML: infoHTML,
		Lang:           string(lang),
	})
}
