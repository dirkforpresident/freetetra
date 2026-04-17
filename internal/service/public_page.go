package service

import (
	"encoding/json"
	"net/http"
	"time"
)

// registerPublicHandlers adds HTTP handlers for the public landing page.
func (s *Service) registerPublicHandlers() {
	s.server.RegisterHTTPHandler("/api/public/status", s.handlePublicStatus)
	s.server.RegisterHTTPHandler("/", s.handleLandingPage)
}

func (s *Service) handlePublicStatus(w http.ResponseWriter, r *http.Request) {
	clients := s.server.SnapshotClients()

	// Count real subscribers (exclude bot ISSIs like 900001)
	subscriberCount := 0
	for _, c := range clients {
		for _, sub := range c.Subscribers {
			if sub.Number < 800000 { // Bot ISSIs are 800000+
				subscriberCount++
			}
		}
	}

	// Count real clients (exclude webradio/echo modules)
	clientCount := 0
	for _, c := range clients {
		hasRealSub := false
		for _, sub := range c.Subscribers {
			if sub.Number < 800000 {
				hasRealSub = true
				break
			}
		}
		if hasRealSub || len(c.Subscribers) == 0 {
			clientCount++
		}
	}

	positions := s.positionStore.Latest()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "public, max-age=5")
	json.NewEncoder(w).Encode(map[string]any{
		"server":      "FreeTetra DO0RAM",
		"version":     "1.0",
		"uptime":      time.Since(startTime).String(),
		"clients":     clientCount,
		"subscribers": subscriberCount,
		"positions":   len(positions),
		"services": []map[string]any{
			{"tg": 6, "name": "Webradio", "description": "Deutschlandfunk (DLF)"},
			{"tg": 22, "name": "Echo", "description": "Papagei / Loopback Test"},
		},
	})
}

var startTime = time.Now()

func (s *Service) handleLandingPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(landingPageHTML))
}

const landingPageHTML = `<!DOCTYPE html>
<html lang="de">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>FreeTetra — Freies TETRA-Netz</title>
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700;800&family=JetBrains+Mono:wght@400;500&display=swap" rel="stylesheet">
<style>
:root {
    --bg: #0a0d12;
    --bg-card: #111827;
    --border: #1f2937;
    --accent: #6ee7b7;
    --accent-dim: rgba(110,231,183,0.1);
    --blue: #60a5fa;
    --purple: #a78bfa;
    --yellow: #fbbf24;
    --red: #f87171;
    --text: #e5e7eb;
    --text-dim: #9ca3af;
    --text-muted: #6b7280;
}
* { margin: 0; padding: 0; box-sizing: border-box; }
body {
    background: var(--bg);
    color: var(--text);
    font-family: 'Inter', system-ui, sans-serif;
    line-height: 1.6;
    min-height: 100vh;
}
.container { max-width: 900px; margin: 0 auto; padding: 0 24px; }

/* Hero */
.hero {
    text-align: center;
    padding: 80px 0 40px;
}
.hero h1 {
    font-size: 2.8rem;
    font-weight: 800;
    letter-spacing: -0.02em;
    margin-bottom: 8px;
}
.hero h1 span { color: var(--accent); }
.hero .tagline {
    font-size: 1.15rem;
    color: var(--text-dim);
    margin-bottom: 40px;
}

/* Live Stats */
.stats {
    display: flex;
    justify-content: center;
    gap: 32px;
    flex-wrap: wrap;
    margin-bottom: 60px;
}
.stat {
    text-align: center;
    min-width: 120px;
}
.stat-value {
    font-size: 2.2rem;
    font-weight: 700;
    color: var(--accent);
    font-family: 'JetBrains Mono', monospace;
}
.stat-label {
    font-size: 0.8rem;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.08em;
}
.stat-dot {
    display: inline-block;
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--accent);
    margin-right: 6px;
    animation: pulse 2s infinite;
}
@keyframes pulse {
    0%, 100% { opacity: 1; }
    50% { opacity: 0.4; }
}

/* Cards */
.card {
    background: var(--bg-card);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 32px;
    margin-bottom: 24px;
}
.card h2 {
    font-size: 1.3rem;
    font-weight: 700;
    margin-bottom: 16px;
    color: var(--text);
}
.card p {
    color: var(--text-dim);
    margin-bottom: 12px;
}

/* Steps */
.steps {
    display: flex;
    gap: 16px;
    flex-wrap: wrap;
    margin-top: 16px;
}
.step {
    flex: 1;
    min-width: 220px;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 20px;
}
.step-num {
    display: inline-block;
    width: 28px;
    height: 28px;
    line-height: 28px;
    text-align: center;
    border-radius: 50%;
    background: var(--accent-dim);
    color: var(--accent);
    font-weight: 700;
    font-size: 0.85rem;
    margin-bottom: 10px;
}
.step h3 {
    font-size: 1rem;
    font-weight: 600;
    margin-bottom: 6px;
}
.step p {
    font-size: 0.85rem;
    color: var(--text-muted);
    margin: 0;
}

/* Services */
.services {
    display: flex;
    gap: 12px;
    flex-wrap: wrap;
    margin-top: 12px;
}
.svc {
    display: flex;
    align-items: center;
    gap: 10px;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 12px 18px;
    flex: 1;
    min-width: 200px;
}
.svc-tg {
    font-family: 'JetBrains Mono', monospace;
    font-weight: 600;
    color: var(--blue);
    font-size: 0.9rem;
}
.svc-name { font-weight: 600; font-size: 0.9rem; }
.svc-desc { font-size: 0.75rem; color: var(--text-muted); }

/* Footer */
.footer {
    text-align: center;
    padding: 40px 0;
    color: var(--text-muted);
    font-size: 0.8rem;
}
.footer a {
    color: var(--accent);
    text-decoration: none;
}
.footer a:hover { text-decoration: underline; }
.btn {
    display: inline-block;
    padding: 10px 24px;
    border-radius: 8px;
    font-weight: 600;
    font-size: 0.9rem;
    text-decoration: none;
    margin: 4px;
}
.btn-accent {
    background: var(--accent);
    color: #0a0d12;
}
.btn-outline {
    border: 1px solid var(--border);
    color: var(--text-dim);
}
.btn-outline:hover {
    border-color: var(--accent);
    color: var(--accent);
}

/* Federation */
.federation-info {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 0.85rem;
    color: var(--text-muted);
    margin-top: 16px;
    padding: 12px;
    background: var(--bg);
    border-radius: 8px;
    border: 1px solid var(--border);
}
.federation-info code {
    font-family: 'JetBrains Mono', monospace;
    color: var(--accent);
    font-size: 0.8rem;
}
</style>
</head>
<body>

<div class="container">
    <div class="hero">
        <h1>Free<span>Tetra</span></h1>
        <div class="tagline">Freies, foederiertes TETRA-Netz fuer Amateurfunk</div>
    </div>

    <div class="stats">
        <div class="stat">
            <div class="stat-value"><span class="stat-dot"></span><span id="s-clients">-</span></div>
            <div class="stat-label">BlueStations</div>
        </div>
        <div class="stat">
            <div class="stat-value" id="s-subs">-</div>
            <div class="stat-label">Subscriber</div>
        </div>
        <div class="stat">
            <div class="stat-value" id="s-positions">-</div>
            <div class="stat-label">Positionen</div>
        </div>
    </div>

    <div class="card">
        <h2>Was ist FreeTetra?</h2>
        <p>FreeTetra ist ein offenes TETRA-Funknetz fuer Amateurfunk. Jeder kann mitmachen —
           entweder ueber einen bestehenden Server oder mit einem eigenen.</p>
        <p>Das Netz ist <strong>foederiert</strong>: Mehrere unabhaengige Server sind untereinander
           verbunden, wie bei E-Mail. Egal bei welchem Server du bist — du erreichst alle.
           Kein zentraler Betreiber, kein Machthaber.</p>

        <div class="federation-info">
            Basiert auf <code>BlueStation</code> (Open Source TETRA-Basisstation) und dem <code>Brew</code>-Protokoll.
            Offener Code (GPLv3) — jeder kann pruefen, aendern, mitmachen.
        </div>
    </div>

    <div class="card">
        <h2>Wie mache ich mit?</h2>
        <p style="margin-bottom:20px;color:var(--text-dim)">Du brauchst keinen eigenen Server. Such dir einen bestehenden aus und buche dein Funkgeraet dort ein. Wenn du willst, kannst du spaeter selbst einen betreiben.</p>
        <div class="steps">
            <div class="step">
                <div class="step-num">1</div>
                <h3>Einbuchen</h3>
                <p>TETRA-Funkgeraet auf eine BlueStation in deiner Naehe einbuchen. Fertig — du bist im Netz.</p>
            </div>
            <div class="step">
                <div class="step-num">2</div>
                <h3>Abdeckung erweitern</h3>
                <p>Eigene BlueStation aufstellen (Raspberry Pi + SDR + Antenne) und an einen bestehenden Server anbinden.</p>
            </div>
            <div class="step">
                <div class="step-num">3</div>
                <h3>Server betreiben</h3>
                <p>Optional: Eigenen FreeTetra-Server aufsetzen und mit dem Netz verbinden. Fuer Leute die ihre eigene Infrastruktur wollen.</p>
            </div>
        </div>
    </div>

    <div class="card">
        <h2>Services</h2>
        <p>Verfuegbare Dienste auf diesem Server:</p>
        <div class="services" id="services">
            <div class="svc">
                <div>
                    <div class="svc-tg">TG 6</div>
                </div>
                <div>
                    <div class="svc-name">Webradio</div>
                    <div class="svc-desc">Deutschlandfunk (DLF) — Dauersendung</div>
                </div>
            </div>
            <div class="svc">
                <div>
                    <div class="svc-tg">TG 22</div>
                </div>
                <div>
                    <div class="svc-name">Echo / Papagei</div>
                    <div class="svc-desc">Spricht zurueck was du sendest</div>
                </div>
            </div>
        </div>
    </div>

    <div class="card">
        <h2>Server verbinden</h2>
        <p>BlueStation-Config — einfach den Brew-Host auf diesen Server zeigen:</p>
        <pre style="background:var(--bg);padding:16px;border-radius:8px;border:1px solid var(--border);font-family:'JetBrains Mono',monospace;font-size:0.82rem;color:var(--accent);overflow-x:auto;margin-top:8px">[brew]
host = "freetetra.1xx.is"
port = 443
tls = true
username = DEINE_DIGITALFUNK_ID
password = "blafablafa"</pre>
        <p style="margin-top:12px;font-size:0.82rem">Keine Registrierung noetig! Deine <a href="https://radioid.net" style="color:var(--blue)">Digitalfunk-ID</a> wird automatisch verifiziert. Passwort: <code style="color:var(--accent);font-family:'JetBrains Mono',monospace">blafablafa</code></p>
    </div>

    <div style="text-align:center;margin:32px 0">
        <a href="/ui" class="btn btn-accent">Server Dashboard</a>
        <a href="https://github.com/dirkforpresident/freetetra" class="btn btn-outline">GitHub</a>
    </div>

    <div class="footer">
        <p>FreeTetra — GPLv3 | Betrieben von DO0RAM (DO1XX)</p>
        <p style="margin-top:4px">Powered by <a href="https://github.com/MidnightBlueLabs/tetra-bluestation">BlueStation</a></p>
    </div>
</div>

<script>
function update() {
    fetch("/api/public/status")
        .then(r => r.json())
        .then(d => {
            document.getElementById("s-clients").textContent = d.clients || 0;
            document.getElementById("s-subs").textContent = d.subscribers || 0;
            document.getElementById("s-positions").textContent = d.positions || 0;
        })
        .catch(() => {});
}
update();
setInterval(update, 10000);
</script>
</body>
</html>`
