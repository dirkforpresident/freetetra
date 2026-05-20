package service

import (
	"html"
	"net/http"
	"strings"
)

func (s *Service) registerMitmachenHandlers() {
	s.server.RegisterHTTPHandler("/mitmachen", s.handleMitmachenPage)
}

func (s *Service) handleMitmachenPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/mitmachen" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(s.renderMitmachenPage(r.Host, detectLang(r))))
}

func (s *Service) renderMitmachenPage(host string, lang Lang) string {
	out := translate(mitmachenHTML, lang)
	return strings.NewReplacer(
		"{{HOST}}", html.EscapeString(host),
		"{{LANG_HTML_ATTR}}", string(lang),
		"{{LANG_SWITCH}}", langSwitchHTML(lang),
	).Replace(out)
}

const mitmachenHTML = `<!DOCTYPE html>
<html lang="{{LANG_HTML_ATTR}}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{T:join.title}} — FreeTetra</title>
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700;800&family=JetBrains+Mono:wght@400;500&display=swap" rel="stylesheet">
<style>
:root {
    --bg: #f9fafb; --bg-card: #ffffff; --bg-subtle: #f3f4f6;
    --border: #e5e7eb; --accent: #059669; --accent-dim: rgba(5,150,105,0.08);
    --blue: #2563eb; --red: #dc2626;
    --text: #111827; --text-dim: #4b5563; --text-muted: #6b7280;
}
* { margin: 0; padding: 0; box-sizing: border-box; }
body { background: var(--bg); color: var(--text); font-family: 'Inter', system-ui, sans-serif; line-height: 1.6; min-height: 100vh; }
.container { max-width: 900px; margin: 0 auto; padding: 0 24px; }

.hero { padding: 56px 0 24px; }
.hero a { color: var(--text-muted); text-decoration: none; font-size: 0.85rem; }
.hero a:hover { color: var(--accent); }
.hero h1 { font-size: 2.2rem; font-weight: 800; letter-spacing: -0.02em; margin: 16px 0 8px; }
.hero h1 span { color: var(--accent); }
.hero .tagline { font-size: 1rem; color: var(--text-dim); margin-bottom: 32px; }

.card {
    background: var(--bg-card); border: 1px solid var(--border);
    border-radius: 12px; padding: 28px; margin-bottom: 20px;
    box-shadow: 0 1px 3px rgba(17,24,39,0.04);
}
.card h2 { font-size: 1.2rem; font-weight: 700; margin-bottom: 14px; }
.card h3 { font-size: 0.95rem; font-weight: 600; margin: 14px 0 6px; color: var(--text-dim); }
.card p { color: var(--text-dim); margin-bottom: 10px; }
.card ul { margin: 8px 0 12px 22px; color: var(--text-dim); }
.card li { margin-bottom: 4px; }
.card code { font-family: 'JetBrains Mono', monospace; font-size: 0.84rem; color: var(--accent); background: var(--accent-dim); padding: 1px 5px; border-radius: 3px; }
.card a { color: var(--accent); text-decoration: none; }
.card a:hover { text-decoration: underline; }

pre {
    background: var(--bg-subtle); padding: 14px 16px; border-radius: 8px;
    border: 1px solid var(--border); font-family: 'JetBrains Mono', monospace;
    font-size: 0.82rem; overflow-x: auto; margin: 8px 0; color: var(--text);
    line-height: 1.55;
}

.path {
    display: flex; gap: 12px; padding: 14px; border-radius: 8px;
    background: var(--bg-subtle); border: 1px solid var(--border); margin: 8px 0;
}
.path-icon { font-size: 1.4rem; line-height: 1.2; }
.path-name { font-weight: 600; font-size: 0.95rem; }
.path-desc { font-size: 0.85rem; color: var(--text-muted); margin-top: 2px; }

.warn {
    border-left: 3px solid var(--accent); background: var(--accent-dim);
    padding: 10px 14px; border-radius: 6px; font-size: 0.88rem; margin: 12px 0;
}
.warn strong { color: var(--accent); }

.footer { text-align: center; padding: 32px 0; color: var(--text-muted); font-size: 0.8rem; }
.footer a { color: var(--accent); text-decoration: none; margin: 0 8px; }
.lang-toggle { position: absolute; top: 16px; right: 20px; font-size: 0.78rem; font-family: 'JetBrains Mono', monospace; color: var(--text-muted); }
.lang-link { color: var(--text-muted); text-decoration: none; padding: 2px 6px; border-radius: 4px; }
.lang-link:hover { color: var(--accent); }
.lang-link.lang-active { color: var(--accent); font-weight: 700; }

@media (max-width: 640px) {
    .container { padding: 0 16px; }
    .hero { padding: 32px 0 16px; }
    .hero h1 { font-size: 1.6rem; }
    .card { padding: 18px; }
    pre { font-size: 0.75rem; }
}
</style>
</head>
<body>
<div class="lang-toggle">{{LANG_SWITCH}}</div>
<div class="container">
    <div class="hero">
        <a href="/">{{T:common.back_to_start}}</a>
        <h1>{{T:join.title}}</h1>
        <div class="tagline">{{T:join.tagline}}</div>
    </div>

    <div class="card">
        <h2>{{T:join.two_ways.title}}</h2>
        <div class="path">
            <div class="path-icon">📡</div>
            <div>
                <div class="path-name">{{T:join.path_hotspot}}</div>
                <div class="path-desc">{{T:join.path_hotspot.desc}}</div>
            </div>
        </div>
        <div class="path">
            <div class="path-icon">🌐</div>
            <div>
                <div class="path-name">{{T:join.path_server}}</div>
                <div class="path-desc">{{T:join.path_server.desc}}</div>
            </div>
        </div>
    </div>

    <div class="card">
        <h2>{{T:join.path1.title}}</h2>
        <p>{{T:join.path1.intro}}</p>

        <h3>{{T:join.path1.hardware_h}}</h3>
        <ul>
            <li>{{T:join.path1.hw_pi}}</li>
            <li>{{T:join.path1.hw_sx}}</li>
            <li>{{T:join.path1.hw_ant}}</li>
            <li>{{T:join.path1.hw_misc}}</li>
        </ul>
        <p style="font-size:0.88rem">{{T:join.path1.boards}}</p>

        <h3>{{T:join.path1.sw_h}}</h3>
        <p>{{T:join.path1.sw_body}}</p>

        <h3>{{T:join.path1.acct_h}}</h3>
        <p>{{T:join.path1.acct_body}}</p>

        <h3>{{T:join.path1.cfg_h}}</h3>
        <p>{{T:join.path1.cfg_intro}}</p>
        <pre>[brew]
host = "freetetra.de"
port = 443
tls = true
username = DEINE_ISSI         # {{T:join.path1.cfg_comment_issi}}
password = "blafablafa"       # {{T:join.path1.cfg_comment_pw}}</pre>
        <p>{{T:join.path1.cfg_rest}}</p>
        <p>{{T:join.path1.cfg_autoauth}}</p>
    </div>

    <div class="card">
        <h2>{{T:join.help.title}}</h2>
        <p>{{T:join.help.body1}}</p>
        <p>{{T:join.help.body2}}</p>
    </div>

    <div class="card">
        <h2>{{T:join.tgs.title}}</h2>
        <pre>TG 1-9      {{T:landing.tgs.local.name}}
TG 10-90    {{T:landing.tgs.global.name}}
TG 91+      {{T:landing.tgs.bm.name}}
            -> TG 262   = DL
            -> TG 2621  = DL Cluster Nord
            -> TG 1     = World</pre>
        <p>{{T:join.tgs.more}}</p>
    </div>

    <div class="card">
        <h2>{{T:join.path2.title}}</h2>
        <p>{{T:join.path2.intro}}</p>
        <p>{{T:join.path2.need}}</p>
        <ul>
            <li>{{T:join.path2.need_vm}}</li>
            <li>{{T:join.path2.need_dom}}</li>
            <li>{{T:join.path2.need_sw}}</li>
        </ul>

        <h3>{{T:join.path2.fed_h}}</h3>
        <p>{{T:join.path2.fed_body}}</p>
        <pre>FEDERATION_KEY=freetetra-federation-2026
FEDERATION_PEERS=wss://freetetra.de/peer/</pre>
        <p style="font-size:0.88rem">{{T:join.path2.fed_note}}</p>
    </div>

    <div class="card">
        <h2>{{T:join.contact.title}}</h2>
        <p>{{T:join.contact.body}}</p>
    </div>

    <div class="footer">
        <a href="/">{{T:common.home}}</a> · <a href="/live">{{T:common.live}}</a> · <a href="/map">{{T:common.map}}</a>
    </div>
</div>
</body>
</html>`
