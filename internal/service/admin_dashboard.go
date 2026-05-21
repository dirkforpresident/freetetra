package service

import (
	"encoding/json"
	"net/http"
	"strings"
)

// freetetraAdminHTML is the clean admin dashboard for FreeTetra operators.
// Replaces cheetah's Vuetify callout desk with a simpler, amateur-radio-focused UI.
const freetetraAdminHTML = `<!DOCTYPE html>
<html lang="de">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>FreeTetra Admin — {{SERVER_NAME}}</title>
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&family=JetBrains+Mono:wght@400;500&display=swap" rel="stylesheet">
<style>
:root {
    --bg: #0a0d12;
    --bg-card: #111827;
    --bg-input: #1a2030;
    --border: #1f2937;
    --border-hover: #374151;
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
    font-size: 14px;
    line-height: 1.5;
    min-height: 100vh;
}
.container { max-width: 1100px; margin: 0 auto; padding: 24px; }

/* Header */
.header {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    margin-bottom: 24px;
    padding-bottom: 16px;
    border-bottom: 1px solid var(--border);
}
.header h1 { font-size: 1.5rem; font-weight: 700; }
.header h1 span { color: var(--accent); }
.header .server-name { color: var(--text-dim); font-family: 'JetBrains Mono', monospace; font-size: 0.85rem; }

/* Stats */
.stats {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(160px, 1fr));
    gap: 12px;
    margin-bottom: 24px;
}
.stat {
    background: var(--bg-card);
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 16px;
}
.stat-value {
    font-size: 1.8rem;
    font-weight: 700;
    color: var(--accent);
    font-family: 'JetBrains Mono', monospace;
    line-height: 1;
}
.stat-label {
    font-size: 0.72rem;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.08em;
    margin-top: 6px;
}

/* Cards */
.card {
    background: var(--bg-card);
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 20px;
    margin-bottom: 16px;
}
.card h2 {
    font-size: 0.95rem;
    font-weight: 600;
    margin-bottom: 14px;
    display: flex;
    align-items: center;
    justify-content: space-between;
}
.card h2 .count {
    font-size: 0.75rem;
    color: var(--text-muted);
    font-weight: 400;
    font-family: 'JetBrains Mono', monospace;
}

/* Tables */
table { width: 100%; border-collapse: collapse; font-size: 0.82rem; }
thead tr { border-bottom: 1px solid var(--border); }
th {
    text-align: left;
    padding: 8px 10px;
    color: var(--text-muted);
    font-weight: 500;
    font-size: 0.7rem;
    text-transform: uppercase;
    letter-spacing: 0.06em;
}
td { padding: 10px; border-bottom: 1px solid var(--border); }
tr:last-child td { border-bottom: 0; }
td.mono { font-family: 'JetBrains Mono', monospace; font-size: 0.78rem; }
.empty { color: var(--text-muted); text-align: center; padding: 20px; font-size: 0.82rem; }

/* Badges */
.badge {
    display: inline-block;
    padding: 2px 8px;
    border-radius: 4px;
    font-size: 0.7rem;
    font-weight: 500;
}
.badge-green { background: rgba(110,231,183,0.15); color: var(--accent); }
.badge-blue { background: rgba(96,165,250,0.15); color: var(--blue); }
.badge-gray { background: rgba(107,114,128,0.15); color: var(--text-muted); }

/* SDS Form */
.sds-form { display: flex; flex-wrap: wrap; gap: 10px; align-items: flex-end; }
.sds-form label {
    display: block;
    font-size: 0.7rem;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.06em;
    margin-bottom: 4px;
}
.sds-form input {
    padding: 9px 12px;
    background: var(--bg-input);
    border: 1px solid var(--border);
    border-radius: 6px;
    color: var(--text);
    font-family: inherit;
    font-size: 0.85rem;
    width: 100%;
}
.sds-form input:focus { outline: none; border-color: var(--accent); }
.sds-form input.mono { font-family: 'JetBrains Mono', monospace; }
.sds-form .field { flex: 1; min-width: 120px; }
.sds-form .field-text { flex: 3; min-width: 200px; }

.btn {
    padding: 9px 20px;
    border: 0;
    border-radius: 6px;
    font-weight: 500;
    font-size: 0.85rem;
    cursor: pointer;
    font-family: inherit;
}
.btn-accent { background: var(--accent); color: #0a0d12; }
.btn-accent:hover { opacity: 0.9; }

.msg { margin-top: 10px; font-size: 0.8rem; color: var(--text-dim); min-height: 1.2em; }

/* Activity Feed */
.activity { max-height: 400px; overflow-y: auto; }
.activity-item {
    display: grid;
    grid-template-columns: 80px 1fr;
    gap: 12px;
    padding: 6px 0;
    font-size: 0.82rem;
    border-bottom: 1px solid var(--border);
}
.activity-item:last-child { border-bottom: 0; }
.activity-time { color: var(--text-muted); font-family: 'JetBrains Mono', monospace; font-size: 0.75rem; }
.activity-text { color: var(--text-dim); }
.activity-text .mono { font-family: 'JetBrains Mono', monospace; color: var(--text); }

/* Live dot */
.live {
    display: inline-block;
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--accent);
    margin-right: 6px;
    animation: pulse 2s infinite;
}
@keyframes pulse { 0%, 100% { opacity: 1; } 50% { opacity: 0.4; } }

/* Tables scrollen horizontal auf schmalen Screens */
.table-wrap { overflow-x: auto; -webkit-overflow-scrolling: touch; }

/* Mobile */
@media (max-width: 700px) {
    .container { padding: 14px; }
    .header { margin-bottom: 16px; padding-bottom: 12px; flex-wrap: wrap; gap: 6px; }
    .header h1 { font-size: 1.2rem; }
    .header .server-name { font-size: 0.75rem; }
    .stats { grid-template-columns: repeat(2, 1fr); gap: 8px; margin-bottom: 16px; }
    .stat { padding: 12px; }
    .stat-value { font-size: 1.4rem; }
    .stat-label { font-size: 0.65rem; }
    .card { padding: 14px; margin-bottom: 12px; }
    .card h2 { font-size: 0.9rem; }
    table { font-size: 0.75rem; min-width: 480px; }
    th, td { padding: 6px 8px; }
    td.mono { font-size: 0.7rem; word-break: break-all; }
    .activity-item { grid-template-columns: 60px 1fr; gap: 8px; font-size: 0.75rem; }
    .activity-time { font-size: 0.68rem; }
}
</style>
</head>
<body>

<div class="container">

<div class="header">
    <h1>Free<span>Tetra</span> Admin</h1>
    <div class="server-name"><span class="live"></span><span id="server-name">...</span></div>
</div>

<div class="stats">
    <div class="stat">
        <div class="stat-value" id="s-repeaters">-</div>
        <div class="stat-label">{{T:admin.repeater}}</div>
    </div>
    <div class="stat">
        <div class="stat-value" id="s-subscribers">-</div>
        <div class="stat-label">{{T:admin.subscriber}}</div>
    </div>
    <div class="stat">
        <div class="stat-value" id="s-peers">-</div>
        <div class="stat-label">{{T:admin.peers}}</div>
    </div>
    <div class="stat">
        <div class="stat-value" id="s-positions">-</div>
        <div class="stat-label">{{T:admin.positions}}</div>
    </div>
</div>

<div class="card">
    <h2>{{T:admin.repeater}} <span class="count" id="repeater-count">0</span></h2>
    <div class="table-wrap"><table>
        <thead><tr><th>{{T:admin.col.name}}</th><th>{{T:admin.subscriber}}</th><th>{{T:admin.col.ip}}</th><th>{{T:admin.col.last_act}}</th></tr></thead>
        <tbody id="repeaters-body"></tbody>
    </table></div>
    <div id="repeaters-empty" class="empty">{{T:admin.empty.repeaters}}</div>
</div>

<div class="card">
    <h2>{{T:admin.subscriber}} <span class="count" id="subs-count">0</span></h2>
    <div class="table-wrap"><table>
        <thead><tr><th>{{T:admin.col.issi}}</th><th>{{T:admin.col.callsign}}</th><th>{{T:admin.col.source}}</th><th>{{T:admin.col.gssis}}</th></tr></thead>
        <tbody id="subs-body"></tbody>
    </table></div>
    <div id="subs-empty" class="empty">{{T:admin.empty.subs}}</div>
</div>

<div class="card">
    <h2>{{T:admin.h.talkgroups}} <span class="count" id="tg-count">0</span></h2>
    <div class="table-wrap"><table>
        <thead><tr><th>{{T:admin.col.tg}}</th><th>{{T:admin.col.count}}</th><th>{{T:admin.col.subs}}</th></tr></thead>
        <tbody id="tg-body"></tbody>
    </table></div>
    <div id="tg-empty" class="empty">{{T:admin.empty.tgs}}</div>
</div>

<div class="card">
    <h2>{{T:admin.peers}} <span class="count" id="peers-count">0</span></h2>
    <div class="table-wrap"><table>
        <thead><tr><th>{{T:admin.col.server}}</th><th>{{T:admin.col.direction}}</th><th>{{T:admin.col.remote_subs}}</th></tr></thead>
        <tbody id="peers-body"></tbody>
    </table></div>
    <div id="peers-empty" class="empty">{{T:admin.empty.peers}}</div>
</div>

<div class="card">
    <h2>{{T:admin.h.last_positions}} <span class="count" id="pos-count">0</span></h2>
    <div class="table-wrap"><table>
        <thead><tr><th>{{T:admin.col.issi}}</th><th>{{T:admin.col.lat}}</th><th>{{T:admin.col.lon}}</th><th>{{T:admin.col.time}}</th></tr></thead>
        <tbody id="positions-body"></tbody>
    </table></div>
    <div id="positions-empty" class="empty">{{T:admin.empty.positions}}</div>
</div>

<div class="card">
    <h2>{{T:admin.h.activity}} <span class="live"></span></h2>
    <div class="activity" id="activity"></div>
</div>

</div>

<script>
function fmt(ts) {
    if (!ts) return "-";
    const d = new Date(ts);
    return d.toLocaleTimeString("de-DE");
}
function fmtDate(ts) {
    if (!ts) return "-";
    const d = new Date(ts);
    return d.toLocaleString("de-DE");
}

async function update() {
    try {
        const [publicStatus, telemetry, peers, positions, snapshot] = await Promise.all([
            fetch("/api/public/status").then(r => r.json()),
            fetch("/api/telemetry/clients").then(r => r.ok ? r.json() : {clients:[]}),
            fetch("/api/peers").then(r => r.ok ? r.json() : {peers:[]}).catch(() => ({peers:[]})),
            fetch("/api/positions").then(r => r.ok ? r.json() : {positions:[]}),
            fetch("/api/dashboard/snapshot").then(r => r.ok ? r.json() : {subscribers:[],groups:[]}).catch(() => ({subscribers:[],groups:[]})),
        ]);

        document.getElementById("server-name").textContent = publicStatus.server || "FreeTetra";
        document.getElementById("s-repeaters").textContent = publicStatus.repeaters || 0;
        document.getElementById("s-subscribers").textContent = publicStatus.subscribers || 0;
        document.getElementById("s-peers").textContent = peers.count || (peers.peers || []).length || 0;
        document.getElementById("s-positions").textContent = publicStatus.positions || 0;

        // Repeaters
        const reps = telemetry.clients || [];
        document.getElementById("repeater-count").textContent = "(" + reps.length + ")";
        const rbody = document.getElementById("repeaters-body");
        const rempty = document.getElementById("repeaters-empty");
        if (reps.length === 0) {
            rbody.innerHTML = ""; rempty.style.display = "block";
        } else {
            rempty.style.display = "none";
            rbody.innerHTML = reps.map(r =>
                "<tr><td><span class=\"badge badge-green\">" + r.name + "</span></td>" +
                "<td class=\"mono\">" + r.subscriber_count + "</td>" +
                "<td class=\"mono\" style=\"color:var(--text-muted)\">" + (r.ip || "-") + "</td>" +
                "<td style=\"color:var(--text-muted)\">" + fmt(r.last_activity) + "</td></tr>"
            ).join("");
        }

        // Subscribers — local from dashboard-snapshot, remote from peers.
        // Each entry: { issi, source, gssis[] }
        const subsByIssi = new Map();
        for (const s of (snapshot.subscribers || [])) {
            subsByIssi.set(s.issi, { issi: s.issi, source: "local", gssis: s.groups || [] });
        }
        for (const p of (peers.peers || [])) {
            if (p.direction !== "outgoing") continue;
            for (const issi of (p.issis || [])) {
                if (subsByIssi.has(issi)) continue; // local wins
                const gssis = [];
                for (const [g, members] of Object.entries(p.gssis || {})) {
                    if ((members || []).includes(issi)) gssis.push(parseInt(g, 10));
                }
                subsByIssi.set(issi, { issi, source: p.name, gssis });
            }
        }
        const allSubs = Array.from(subsByIssi.values()).sort((a, b) => a.issi - b.issi);

        document.getElementById("subs-count").textContent = "(" + allSubs.length + ")";
        const sbody = document.getElementById("subs-body");
        const sempty = document.getElementById("subs-empty");
        if (allSubs.length === 0) {
            sbody.innerHTML = ""; sempty.style.display = "block";
        } else {
            sempty.style.display = "none";
            sbody.innerHTML = allSubs.map(s => {
                const sourceBadge = s.source === "local"
                    ? '<span class="badge badge-green">local</span>'
                    : '<span class="badge badge-blue">' + s.source + '</span>';
                const gssiBadges = s.gssis.length
                    ? s.gssis.sort((a,b)=>a-b).map(g => '<span class="badge badge-gray">TG ' + g + '</span>').join(" ")
                    : '<span style="color:var(--text-muted)">—</span>';
                return "<tr><td class=\"mono\">" + s.issi + "</td>" +
                    "<td style=\"color:var(--text-muted)\" id=\"call-" + s.issi + "\">...</td>" +
                    "<td>" + sourceBadge + "</td>" +
                    "<td>" + gssiBadges + "</td></tr>";
            }).join("");
            // Resolve callsigns
            for (const s of allSubs) {
                fetch("/api/radioid/lookup?issi=" + s.issi)
                    .then(r => r.ok ? r.json() : null)
                    .then(d => {
                        if (d && d.callsign) {
                            const el = document.getElementById("call-" + s.issi);
                            if (el) el.textContent = d.callsign;
                        }
                    }).catch(() => {});
            }
        }

        // Talkgroups — wer ist auf welcher TG (lokal + peer aggregiert)
        const tgMap = new Map();
        for (const s of allSubs) {
            for (const g of s.gssis) {
                if (!tgMap.has(g)) tgMap.set(g, []);
                tgMap.get(g).push(s.issi);
            }
        }
        const tgList = Array.from(tgMap.entries()).sort((a, b) => a[0] - b[0]);
        const tgEl = document.getElementById("tg-body");
        const tgEmpty = document.getElementById("tg-empty");
        if (tgEl) {
            document.getElementById("tg-count").textContent = "(" + tgList.length + ")";
            if (tgList.length === 0) {
                tgEl.innerHTML = ""; if (tgEmpty) tgEmpty.style.display = "block";
            } else {
                if (tgEmpty) tgEmpty.style.display = "none";
                tgEl.innerHTML = tgList.map(([g, issis]) =>
                    "<tr><td class=\"mono\" style=\"color:#2563eb;font-weight:600\">TG " + g + "</td>" +
                    "<td class=\"mono\">" + issis.length + "</td>" +
                    "<td>" + issis.map(i => '<span class="badge badge-gray mono" style="font-size:0.72rem">' + i + '</span>').join(" ") + "</td></tr>"
                ).join("");
            }
        }

        // Peers
        const peerList = peers.peers || [];
        document.getElementById("peers-count").textContent = "(" + peerList.length + ")";
        const pbody = document.getElementById("peers-body");
        const pempty = document.getElementById("peers-empty");
        if (peerList.length === 0) {
            pbody.innerHTML = ""; pempty.style.display = "block";
        } else {
            pempty.style.display = "none";
            pbody.innerHTML = peerList.map(p =>
                "<tr><td><span class=\"badge badge-blue\">" + p.name + "</span></td>" +
                "<td><span class=\"badge badge-gray\">" + (p.direction || "-") + "</span></td>" +
                "<td class=\"mono\">" + (p.issis ? p.issis.length : 0) + "</td></tr>"
            ).join("");
        }

        // Positions
        const pos = positions.positions || [];
        document.getElementById("pos-count").textContent = "(" + pos.length + ")";
        const posbody = document.getElementById("positions-body");
        const posempty = document.getElementById("positions-empty");
        if (pos.length === 0) {
            posbody.innerHTML = ""; posempty.style.display = "block";
        } else {
            posempty.style.display = "none";
            posbody.innerHTML = pos.map(p =>
                "<tr><td class=\"mono\">" + p.issi + "</td>" +
                "<td class=\"mono\">" + p.lat.toFixed(5) + "</td>" +
                "<td class=\"mono\">" + p.lon.toFixed(5) + "</td>" +
                "<td style=\"color:var(--text-muted)\">" + fmtDate(p.timestamp) + "</td></tr>"
            ).join("");
        }
    } catch (e) {
        console.error(e);
    }
}

// Load activity feed from dashboard snapshot
async function updateActivity() {
    try {
        const r = await fetch("/api/dashboard/snapshot?since_seq=0");
        if (!r.ok) return;
        const d = await r.json();
        const activity = (d.activity || []).slice(-30).reverse();
        const el = document.getElementById("activity");
        if (activity.length === 0) {
            el.innerHTML = "<div class=\"empty\">{{T:admin.empty.activity}}</div>";
            return;
        }
        el.innerHTML = activity.map(a =>
            "<div class=\"activity-item\"><div class=\"activity-time\">" +
            fmt(a.timestamp || a.time) + "</div>" +
            "<div class=\"activity-text\">" +
            "<span class=\"mono\">" + (a.kind || a.type || "") + "</span> " +
            (a.message || a.text || JSON.stringify(a.details || {})) +
            "</div></div>"
        ).join("");
    } catch (e) {}
}

update();
updateActivity();
setInterval(update, 5000);
setInterval(updateActivity, 3000);
</script>
</body>
</html>`

func (s *Service) handleAdminDashboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	serverName := s.cfg.Federation.Name
	if serverName == "" {
		serverName = "FreeTetra"
	}
	lang := detectLang(r)
	html := translate(freetetraAdminHTML, lang)
	html = strings.NewReplacer(
		"{{SERVER_NAME}}", serverName,
		"{{LANG_HTML_ATTR}}", string(lang),
		"{{LANG_SWITCH}}", langSwitchHTML(lang),
	).Replace(html)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(html))
}

// handlePeersAPI returns connected federation peers (for admin dashboard).
func (s *Service) handlePeersAPI(w http.ResponseWriter, r *http.Request) {
	peers := []any{}
	count := 0
	if s.federation != nil {
		snapshots := s.federation.PeerSnapshots()
		for _, p := range snapshots {
			peers = append(peers, p)
		}
		count = len(snapshots)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"peers": peers,
		"count": count,
	})
}
