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
	w.Write([]byte(s.renderMitmachenPage(r.Host)))
}

func (s *Service) renderMitmachenPage(host string) string {
	rpl := strings.NewReplacer(
		"{{HOST}}", html.EscapeString(host),
	)
	return rpl.Replace(mitmachenHTML)
}

const mitmachenHTML = `<!DOCTYPE html>
<html lang="de">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Mitmachen — FreeTetra</title>
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
<div class="container">
    <div class="hero">
        <a href="/">&larr; Start</a>
        <h1>Mit<span>machen</span></h1>
        <div class="tagline">FreeTetra ist offen — jeder lizenzierte Funkamateur kann mitmachen. So gehts.</div>
    </div>

    <div class="card">
        <h2>Drei Wege</h2>
        <div class="path">
            <div class="path-icon">📡</div>
            <div>
                <div class="path-name">Als User mit eigenem Hotspot</div>
                <div class="path-desc">Eine kleine BlueStation zu Hause, fuer dich + dein Funkgeraet. Haeufigster Fall.</div>
            </div>
        </div>
        <div class="path">
            <div class="path-icon">🗼</div>
            <div>
                <div class="path-name">Als Relais-Betreiber</div>
                <div class="path-desc">Hoehere Power, externe Antenne, BNetzA-Repeater-Genehmigung.</div>
            </div>
        </div>
        <div class="path">
            <div class="path-icon">🌐</div>
            <div>
                <div class="path-name">Als Server-Operator</div>
                <div class="path-desc">Eigenen FreeTetra-Server fuer einen lokalen Cluster (OV, Verein, Gruppe).</div>
            </div>
        </div>
    </div>

    <div class="card">
        <h2>Pfad 1: Hotspot mit BlueStation</h2>
        <p>Der einfachste Einstieg. Du brauchst keinen eigenen Server — nur eine BlueStation die sich mit <code>freetetra.de</code> verbindet.</p>

        <h3>Hardware</h3>
        <ul>
            <li><strong>Raspberry Pi</strong> (3B+ oder neuer)</li>
            <li><strong>SX1255-Funkboard</strong> als RPi-HAT — es gibt mehrere offene Designs (TetroPi, SXceiver von OH2EAT, weitere in der Community)</li>
            <li><strong>UHF-Antenne</strong> (fuer Hotspot reicht eine kleine Stub-Antenne)</li>
            <li>SD-Karte, Stromversorgung, ggf. Gehaeuse</li>
        </ul>
        <p style="font-size:0.88rem">Boards bekommt man entweder fix und fertig <strong>in der BlueStation-Telegram-Gruppe</strong> (dort handeln OMs gebrauchte und neue) oder man laesst sich die offenen Designs bei <strong>JLCPCB</strong> selbst fertigen.</p>

        <h3>Software</h3>
        <p><strong>BlueStation</strong> von <a href="https://github.com/MidnightBlueLabs/tetra-bluestation">MidnightBlueLabs</a> (Apache 2.0). Ein Hotspot-User braucht <strong>nichts extra</strong> — kein Fork, kein Build-System-Magic. Pre-Built Binaries oder selbst kompilieren wie im README beschrieben.</p>

        <h3>Account: RadioID</h3>
        <p>Du brauchst eine <a href="https://radioid.net">RadioID</a> (= DMR-ID). Wenn du noch keine hast: dort registrieren mit deinem Funkamateur-Rufzeichen. Dauert 1-2 Tage. Deine ISSI fuer TETRA ist die <code>RadioID + 2 Stellen SSID</code> (z.B. <code>2623563</code> + <code>00</code> = <code>262356300</code>).</p>

        <h3>Config — Brew-Host eintragen</h3>
        <p>In deiner <code>config.toml</code> nur diese Section anpassen:</p>
        <pre>[brew]
host = "freetetra.de"
port = 443
tls = true
username = DEINE_ISSI         # z.B. 262356300
password = "blafablafa"       # Shared Key fuer alle RadioID-User

[net_info]
mcc = 901
mnc = 8888</pre>
        <p>Plus SDR-Frequenzen + Cell-Info wie in der BlueStation-Doku. Beim Start meldet sich dein Funkgeraet automatisch an — keine Account-Registrierung noetig, RadioID wird gegen radioid.net verifiziert.</p>
    </div>

    <div class="card">
        <h2>Hilfe zur BlueStation-Hardware/-Software</h2>
        <p>Es gibt eine aktive <strong>BlueStation-Telegram-Gruppe</strong> in der OMs Boards anbieten, Probleme diskutieren und Setup-Fragen beantworten. Den Einladungslink findest du im MidnightBlueLabs-Repo bzw. ueber die Community.</p>
        <p>Fuer FreeTetra-spezifische Fragen (Server-Connect, TG-Schema): siehe Kontakt unten.</p>
    </div>

    <div class="card">
        <h2>Talkgroups</h2>
        <pre>TG 1-9      Lokal (nur dein Server, nie foederiert)
            -> Echo bei TG 9

TG 10-90    FreeTetra global (alle FreeTetra-Server)

TG 91+      BrandMeister-Kompatibilitaet (DMR-Bridge)
            -> TG 262   = DL
            -> TG 2621  = DL Cluster Nord
            -> TG 1     = Welt</pre>
        <p>Mehr Details auf der <a href="/">Startseite</a>.</p>
    </div>

    <div class="card">
        <h2>Pfad 2: Relais-Betreiber</h2>
        <p>Gleicher Software-Stack, aber:</p>
        <ul>
            <li>Hoehere TX-Power via PA (typisch 10-50W)</li>
            <li>Externe Antenne auf Mast/Dach</li>
            <li><strong>BNetzA-Genehmigung</strong> als Repeater-Standort + Relais-Rufzeichen</li>
        </ul>
        <p>Bei Interesse: melde dich, dann besprechen wir das im Detail.</p>
    </div>

    <div class="card">
        <h2>Pfad 3: Eigener Server</h2>
        <p>Wenn deine Gruppe (OV, Verein, Bastelrunde) einen eigenen lokalen Cluster will: du betreibst einen FreeTetra-Server, deine BlueStations connecten dort, ihr peert mit <code>freetetra.de</code> und anderen FreeTetra-Servern.</p>
        <p>Was du brauchst:</p>
        <ul>
            <li>Linux-VM oder kleiner Server (1 vCPU, 512 MB RAM reicht)</li>
            <li>Domain mit SSL</li>
            <li>FreeTetra-Server-Software (Go-Binary; das Repo wird oeffentlich sobald wir stabil sind)</li>
            <li>Federation-Peer-Setup mit Shared Key</li>
        </ul>
        <p>Bei Interesse: melde dich. Setup-Anleitung + Federation-Key gibt es per Mail.</p>
    </div>

    <div class="card">
        <h2>Kontakt</h2>
        <p>FreeTetra ist im Aufbau. Fuer Fragen, Mitmachen, oder Setup-Hilfe: <a href="mailto:do1xx@pm.me">do1xx@pm.me</a></p>
    </div>

    <div class="footer">
        <a href="/">Start</a> · <a href="/live">Live</a> · <a href="/map">Map</a>
    </div>
</div>
</body>
</html>`
