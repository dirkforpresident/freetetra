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

	// Repeater count + subscriber count come from BlueStation telemetry (most accurate).
	// Falls back to heartbeat API for custom clients.
	repeaterCount := 0
	subscriberCount := 0
	if s.telemetry != nil && s.telemetry.ActiveCount() > 0 {
		repeaterCount = s.telemetry.ActiveCount()
		subscriberCount = s.telemetry.TotalSubscribers()
	} else if s.repeaters != nil {
		repeaterCount = s.repeaters.ActiveCount()
		subscriberCount = s.repeaters.TotalSubscribers()
	}
	_ = clients

	positions := s.positionStore.Latest()

	serverName := "FreeTetra"
	if s.cfg.Federation.Name != "" {
		serverName = "FreeTetra " + s.cfg.Federation.Name
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "public, max-age=5")
	json.NewEncoder(w).Encode(map[string]any{
		"server":      serverName,
		"version":     "1.0",
		"uptime":      time.Since(startTime).String(),
		"repeaters":   repeaterCount,
		"subscribers": subscriberCount,
		"positions":   len(positions),
	})
}

var startTime = time.Now()

func (s *Service) handleLandingPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	// If this is a WebSocket upgrade request, treat as telemetry connection
	if r.Header.Get("Upgrade") == "websocket" && s.telemetry != nil {
		s.telemetry.handleConnection(w, r)
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
    --bg: #f9fafb;
    --bg-card: #ffffff;
    --bg-subtle: #f3f4f6;
    --border: #e5e7eb;
    --border-strong: #d1d5db;
    --accent: #059669;
    --accent-bright: #10b981;
    --accent-dim: rgba(5,150,105,0.08);
    --blue: #2563eb;
    --purple: #7c3aed;
    --yellow: #d97706;
    --red: #dc2626;
    --text: #111827;
    --text-dim: #4b5563;
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
    box-shadow: 0 1px 3px rgba(17,24,39,0.04), 0 1px 2px rgba(17,24,39,0.03);
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
    background: var(--bg-subtle);
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
    background: var(--bg-subtle);
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
    background: var(--bg-subtle);
    border-radius: 8px;
    border: 1px solid var(--border);
}
.federation-info code {
    font-family: 'JetBrains Mono', monospace;
    color: var(--accent);
    font-size: 0.8rem;
}

/* Mobile */
@media (max-width: 640px) {
    .container { padding: 0 16px; }
    .hero { padding: 48px 0 24px; }
    .hero h1 { font-size: 2rem; }
    .hero .tagline { font-size: 0.95rem; margin-bottom: 28px; }
    .stats { gap: 16px; margin-bottom: 40px; }
    .stat { min-width: 90px; }
    .stat-value { font-size: 1.6rem; }
    .stat-label { font-size: 0.7rem; }
    .card { padding: 20px; margin-bottom: 16px; }
    .card h2 { font-size: 1.1rem; }
    .card p { font-size: 0.9rem; }
    .step { min-width: 100%; padding: 16px; }
    .svc { min-width: 100%; padding: 10px 14px; }
    .btn { padding: 9px 18px; font-size: 0.85rem; }
    .footer { padding: 28px 0; font-size: 0.75rem; }
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
            <div class="stat-label">Repeater</div>
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
        <p>Foederierter TETRA-Brew-Server fuer Amateurfunk. Mehrere unabhaengige Server peeren ueber das Brew-Protokoll (binary WebSocket) — wie SMTP zwischen Mailservern. Subscriber (ISSI) sind serverlokal, ausgewaehlte Talkgroups (GSSI) werden zwischen Servern geroutet. Authentifizierung ueber RadioID, keine zentrale Registrierung.</p>
        <p><strong>FreeTetra</strong> ist sowohl das Projekt als auch der erste Server: was hier auf <code>freetetra.de</code> laeuft, kannst du genauso selber hosten und mit allen anderen FreeTetra-Servern peeren.</p>

        <div class="federation-info">
            Basiert auf <code>BlueStation</code> (Open Source TETRA-Basisstation) und dem <code>Brew</code>-Protokoll.
        </div>
    </div>

    <div class="card">
        <h2>Was los ist im Netz</h2>
        <p>Live mitschauen wer am Funken ist und wo Coverage besteht:</p>
        <div class="services" style="margin-top:12px">
            <a href="/live" class="svc" style="text-decoration:none;color:inherit;cursor:pointer">
                <div class="svc-tg">/live</div>
                <div>
                    <div class="svc-name">Last Heard</div>
                    <div class="svc-desc">Wer hat zuletzt gesprochen — Callsign, ISSI, Talkgroup, Dauer. Live-Update alle 2 Sek.</div>
                </div>
            </a>
            <a href="/map" class="svc" style="text-decoration:none;color:inherit;cursor:pointer">
                <div class="svc-tg">/map</div>
                <div>
                    <div class="svc-name">Coverage-Map</div>
                    <div class="svc-desc">Wo Funkgeraete erfolgreich gesehen werden — automatisch aus LIP-Positions, in H3-Hexagons aggregiert (Street/City/Region je nach Zoom).</div>
                </div>
            </a>
            <a href="/ui" class="svc" style="text-decoration:none;color:inherit;cursor:pointer">
                <div class="svc-tg">/ui</div>
                <div>
                    <div class="svc-name">Dashboard</div>
                    <div class="svc-desc">Volle Uebersicht: Repeater, Subscriber, Federation-Peers, SDS-Console.</div>
                </div>
            </a>
        </div>
    </div>

    <div class="card">
        <h2>Talkgroups (GSSI-Schema)</h2>
        <div class="services">
            <div class="svc">
                <div class="svc-tg">TG 1-9</div>
                <div>
                    <div class="svc-name">Server-lokal</div>
                    <div class="svc-desc">Bleibt auf diesem Server — wird NICHT zu anderen FreeTetra-Servern foederiert. Innerhalb des Servers an alle verbundenen Cells. Per Konvention: TG 7-9 fuer Service-Bots (Echo, Wetter, etc.) — jeder Server-Operator hostet die eigenen.</div>
                </div>
            </div>
            <div class="svc">
                <div class="svc-tg">TG 10-90</div>
                <div>
                    <div class="svc-name">FreeTetra global</div>
                    <div class="svc-desc">Alle FreeTetra-Server weltweit, ueber Brew-Federation.</div>
                </div>
            </div>
            <div class="svc">
                <div class="svc-tg">TG 91+</div>
                <div>
                    <div class="svc-name">BrandMeister-Bruecke</div>
                    <div class="svc-desc">Wie 10-90 + DMR-Bruecke zu BrandMeister. TG-Nummern 1:1 (z.B. TG 262 = DL, TG 2621 = DL Cluster Nord).</div>
                </div>
            </div>
        </div>
        <p style="margin-top:16px;font-size:0.85rem;color:var(--text-muted)">Aktuell aktive Services: <strong>TG 9</strong> Echo/Papagei (server-lokal — jeder Server-Operator sollte einen eigenen Echo auf TG 9 betreiben, um Federation-Ping-Pong zu vermeiden).</p>
    </div>


    <div class="card">
        <h2>Server verbinden</h2>
        <p>BlueStation-Config — einfach den Brew-Host auf diesen Server zeigen:</p>
        <pre style="background:var(--bg);padding:16px;border-radius:8px;border:1px solid var(--border);font-family:'JetBrains Mono',monospace;font-size:0.82rem;color:var(--accent);overflow-x:auto;margin-top:8px">[brew]
host = "freetetra.de"
port = 443
tls = true
username = DEINE_DIGITALFUNK_ID
password = "blafablafa"</pre>
        <p style="margin-top:12px;font-size:0.82rem">Keine Registrierung noetig! Deine <a href="https://radioid.net" style="color:var(--blue)">Digitalfunk-ID</a> wird automatisch verifiziert. Passwort: <code style="color:var(--accent);font-family:'JetBrains Mono',monospace">blafablafa</code></p>
    </div>

    <div class="footer">
        <p>FreeTetra — Betrieben von DO0RAM (DO1XX)</p>
        <p style="margin-top:4px">Powered by <a href="https://github.com/MidnightBlueLabs/tetra-bluestation">BlueStation</a></p>
    </div>
</div>

<script>
function update() {
    fetch("/api/public/status")
        .then(r => r.json())
        .then(d => {
            document.getElementById("s-clients").textContent = d.repeaters || 0;
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
