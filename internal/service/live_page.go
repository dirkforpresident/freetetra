package service

import (
	"net/http"
	"strings"
)

func (s *Service) registerLiveHandlers() {
	s.server.RegisterHTTPHandler("/live", s.handleLivePage)
	s.server.RegisterHTTPHandler("/api/live/last-heard", s.handleLastHeardAPI)
}

func (s *Service) handleLivePage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/live" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(s.renderLivePage(detectLang(r))))
}

func (s *Service) renderLivePage(lang Lang) string {
	out := translate(liveHTML, lang)
	return strings.NewReplacer(
		"{{LANG_HTML_ATTR}}", string(lang),
		"{{LANG_SWITCH}}", langSwitchHTML(lang),
	).Replace(out)
}

const liveHTML = `<!DOCTYPE html>
<html lang="{{LANG_HTML_ATTR}}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{T:live.title}}</title>
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
body { background: var(--bg); color: var(--text); font-family: 'Inter', system-ui, sans-serif; line-height: 1.5; min-height: 100vh; }
.container { max-width: 900px; margin: 0 auto; padding: 24px; }
.header { display: flex; align-items: baseline; justify-content: space-between; padding-bottom: 16px; margin-bottom: 24px; border-bottom: 1px solid var(--border); }
.header h1 { font-size: 1.6rem; font-weight: 800; letter-spacing: -0.01em; }
.header h1 span { color: var(--accent); }
.header .meta { font-size: 0.82rem; color: var(--text-muted); font-family: 'JetBrains Mono', monospace; }

.card { background: var(--bg-card); border: 1px solid var(--border); border-radius: 12px; padding: 20px; margin-bottom: 16px; }
.card h2 { font-size: 1rem; font-weight: 700; margin-bottom: 12px; text-transform: uppercase; letter-spacing: 0.05em; color: var(--text-dim); }

.row {
    display: flex; gap: 14px; padding: 10px 12px; border-radius: 8px;
    background: var(--bg-subtle); margin-bottom: 6px; font-size: 0.9rem; align-items: center;
    border: 1px solid transparent;
    transition: box-shadow 0.3s ease, border-color 0.3s ease;
}
.row .cs { font-family: 'JetBrains Mono', monospace; font-weight: 600; color: var(--text); min-width: 84px; }
.row .issi { font-family: 'JetBrains Mono', monospace; color: var(--text-muted); font-size: 0.8rem; min-width: 80px; }
.row .tg { font-family: 'JetBrains Mono', monospace; font-weight: 600; min-width: 60px; }
.row .dur { color: var(--text-muted); font-size: 0.82rem; min-width: 70px; }
.row .when { color: var(--text-muted); font-size: 0.82rem; margin-left: auto; }
.row .badge {
    font-size: 0.7rem; padding: 2px 8px; border-radius: 4px;
    text-transform: uppercase; letter-spacing: 0.05em; font-weight: 600;
}

/* Past calls: subtle border in network color */
.row.local       { border-color: rgba(107,114,128,0.25); }
.row.local .tg   { color: var(--text-muted); }
.row.local .badge{ background: rgba(107,114,128,0.12); color: var(--text-muted); }

.row.tetra       { border-color: rgba(5,150,105,0.25); }
.row.tetra .tg   { color: var(--accent); }
.row.tetra .badge{ background: rgba(5,150,105,0.12); color: var(--accent); }

.row.dmr         { border-color: rgba(217,119,6,0.3); }
.row.dmr .tg     { color: #d97706; }
.row.dmr .badge  { background: rgba(217,119,6,0.12); color: #d97706; }

/* Active calls: strong glow + pulse */
.row.live.local  {
    background: rgba(107,114,128,0.08);
    border-color: var(--text-muted);
    box-shadow: 0 0 24px rgba(107,114,128,0.5), 0 0 6px rgba(107,114,128,0.3);
    animation: glow-local 1.6s infinite;
}
.row.live.tetra  {
    background: var(--accent-dim);
    border-color: var(--accent);
    box-shadow: 0 0 24px rgba(5,150,105,0.6), 0 0 6px rgba(5,150,105,0.4);
    animation: glow-tetra 1.6s infinite;
}
.row.live.dmr    {
    background: rgba(217,119,6,0.08);
    border-color: #d97706;
    box-shadow: 0 0 24px rgba(217,119,6,0.6), 0 0 6px rgba(217,119,6,0.4);
    animation: glow-dmr 1.6s infinite;
}

@keyframes glow-tetra {
    0%, 100% { box-shadow: 0 0 24px rgba(5,150,105,0.6), 0 0 6px rgba(5,150,105,0.4); }
    50%      { box-shadow: 0 0 36px rgba(5,150,105,0.9), 0 0 12px rgba(5,150,105,0.6); }
}
@keyframes glow-dmr {
    0%, 100% { box-shadow: 0 0 24px rgba(217,119,6,0.6), 0 0 6px rgba(217,119,6,0.4); }
    50%      { box-shadow: 0 0 36px rgba(217,119,6,0.9), 0 0 12px rgba(217,119,6,0.6); }
}
@keyframes glow-local {
    0%, 100% { box-shadow: 0 0 16px rgba(107,114,128,0.4), 0 0 4px rgba(107,114,128,0.3); }
    50%      { box-shadow: 0 0 24px rgba(107,114,128,0.6), 0 0 8px rgba(107,114,128,0.4); }
}

.pulse-dot { display: inline-block; width: 8px; height: 8px; border-radius: 50%; animation: pulse 1s infinite; margin-right: 6px; }
.row.live.tetra .pulse-dot { background: var(--accent); }
.row.live.dmr   .pulse-dot { background: #d97706; }
.row.live.local .pulse-dot { background: var(--text-muted); }
@keyframes pulse { 0%, 100% { opacity: 1; transform: scale(1); } 50% { opacity: 0.5; transform: scale(1.3); } }

.empty { color: var(--text-muted); padding: 14px; text-align: center; font-size: 0.88rem; font-style: italic; }
a { color: var(--accent); text-decoration: none; }
a:hover { text-decoration: underline; }
.foot { text-align: center; padding: 24px 0; color: var(--text-muted); font-size: 0.78rem; }
.lang-toggle { position: absolute; top: 16px; right: 20px; font-size: 0.78rem; font-family: 'JetBrains Mono', monospace; color: var(--text-muted); }
.lang-link { color: var(--text-muted); text-decoration: none; padding: 2px 6px; border-radius: 4px; }
.lang-link:hover { color: var(--accent); }
.lang-link.lang-active { color: var(--accent); font-weight: 700; }

@media (max-width: 640px) {
    .container { padding: 14px; }
    .header { margin-bottom: 16px; padding-bottom: 12px; }
    .header h1 { font-size: 1.3rem; }
    .header .meta { font-size: 0.72rem; }
    .card { padding: 14px; margin-bottom: 12px; }
    .card h2 { font-size: 0.85rem; }
    .row {
        flex-wrap: wrap;
        gap: 4px 10px;
        font-size: 0.85rem;
        padding: 10px 12px;
    }
    .row .cs    { min-width: 0; flex: 1 1 auto; }
    .row .issi  { min-width: 0; font-size: 0.75rem; }
    .row .tg    { min-width: 0; }
    .row .dur   { min-width: 0; font-size: 0.78rem; }
    .row .badge { font-size: 0.65rem; padding: 1px 6px; }
    .row .when  {
        flex-basis: 100%;
        margin-left: 0;
        font-size: 0.72rem;
        text-align: right;
        margin-top: 2px;
    }
}
@media (max-width: 380px) {
    /* Sehr kleine Screens — ISSI weglassen wenn Callsign da ist */
    .row .issi { display: none; }
    .row.has-callsign .issi { display: none; }
}
</style>
</head>
<body>
<div class="lang-toggle">{{LANG_SWITCH}}</div>
<div class="container">
    <div class="header">
        <h1>Free<span>Tetra</span> Live</h1>
        <div class="meta"><span id="last-update">…</span></div>
    </div>

    <div class="card">
        <h2>{{T:live.active_calls}} <span id="active-count" style="float:right;font-family:'JetBrains Mono',monospace;color:var(--accent)">0</span></h2>
        <div id="active-list"><div class="empty">{{T:live.silent}}</div></div>
    </div>

    <div class="card">
        <h2>{{T:live.last_heard}}</h2>
        <div id="last-list"><div class="empty">{{T:live.no_calls}}</div></div>
    </div>

    <div class="foot">
        <a href="/">{{T:common.home}}</a> · <a href="/map">{{T:common.map}}</a> · <a href="/ui">{{T:common.dashboard}}</a>
    </div>
</div>

<script>
function fmtDuration(ms) {
    if (!ms) return '';
    if (ms < 1000) return ms + 'ms';
    const s = Math.floor(ms / 1000);
    if (s < 60) return s + 's';
    return Math.floor(s / 60) + 'm ' + (s % 60) + 's';
}
const I18N = {
    just_now: '{{T:live.just_now}}',
    ago_s:    '{{T:live.ago_s}}',
    ago_min:  '{{T:live.ago_min}}',
    ago_h:    '{{T:live.ago_h}}',
    ago_d:    '{{T:live.ago_d}}',
    tg_local: '{{T:live.tg_local}}',
    tg_tetra: '{{T:live.tg_tetra}}',
    tg_dmr:   '{{T:live.tg_dmr}}',
    silent:   '{{T:live.silent}}',
    no_calls: '{{T:live.no_calls}}',
    lang:     '{{LANG_HTML_ATTR}}',
};
function fmtAgo(iso) {
    const then = new Date(iso).getTime();
    const now = Date.now();
    const s = Math.floor((now - then) / 1000);
    if (s < 5) return I18N.just_now;
    if (s < 60) return I18N.ago_s.replace('%d', s);
    const m = Math.floor(s / 60);
    if (m < 60) return I18N.ago_min.replace('%d', m);
    const h = Math.floor(m / 60);
    if (h < 24) return I18N.ago_h.replace('%d', h);
    return I18N.ago_d.replace('%d', Math.floor(h / 24));
}
function networkClass(gssi) {
    if (gssi >= 91) return 'dmr';
    if (gssi >= 10) return 'tetra';
    return 'local';
}
function networkLabel(gssi) {
    if (gssi >= 91) return I18N.tg_dmr;
    if (gssi >= 10) return I18N.tg_tetra;
    return I18N.tg_local;
}
function renderRow(e, live) {
    const cs = e.callsign ? e.callsign : '';
    const dur = live ? fmtDuration(Date.now() - new Date(e.started_at).getTime()) : fmtDuration(e.duration_ms);
    const dot = live ? '<span class="pulse-dot"></span>' : '';
    const net = networkClass(e.dest_gssi);
    const lbl = networkLabel(e.dest_gssi);
    return '<div class="row ' + net + (live ? ' live' : '') + '">' +
        '<span class="cs">' + dot + (cs || '–') + '</span>' +
        '<span class="issi">' + e.source_issi + '</span>' +
        '<span class="tg">TG ' + e.dest_gssi + '</span>' +
        '<span class="dur">' + dur + '</span>' +
        '<span class="badge">' + lbl + '</span>' +
        '<span class="when">' + fmtAgo(e.started_at) + '</span>' +
        '</div>';
}
async function update() {
    try {
        const r = await fetch('/api/live/last-heard');
        const d = await r.json();
        const all = d.entries || [];
        const active = all.filter(e => !e.ended_at);
        const past = all.filter(e => !!e.ended_at).slice(0, 30);

        document.getElementById('active-count').textContent = active.length;
        document.getElementById('active-list').innerHTML = active.length
            ? active.map(e => renderRow(e, true)).join('')
            : '<div class="empty">' + I18N.silent + '</div>';
        document.getElementById('last-list').innerHTML = past.length
            ? past.map(e => renderRow(e, false)).join('')
            : '<div class="empty">' + I18N.no_calls + '</div>';
        document.getElementById('last-update').textContent = new Date().toLocaleTimeString(I18N.lang === 'en' ? 'en-US' : 'de-DE');
    } catch (e) {
        document.getElementById('last-update').textContent = 'offline';
    }
}
update();
setInterval(update, 2000);
</script>
</body>
</html>`
