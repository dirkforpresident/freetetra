package service

// translations enthaelt alle Strings fuer die public-facing Pages
// (Landing, Live, Map, Mitmachen) in DE und EN.
//
// Konvention fuer Keys: page.section.element. z.B. "landing.what_is.title".
var translations = map[Lang]map[string]string{
	LangDE: {
		// Common
		"common.lang_label":     "Sprache",
		"common.home":           "Start",
		"common.map":            "Karte",
		"common.live":           "Live",
		"common.dashboard":      "Dashboard",
		"common.join":           "Mitmachen",
		"common.back_to_start":  "← Start",
		"common.powered_by":     "Powered by",
		"common.operated_by":    "Betrieben von",

		// Landing — Hero
		"landing.tagline": "Freies, foederiertes TETRA-Netz fuer Amateurfunk",

		// Landing — Was ist FreeTetra
		"landing.what_is.title": "Was ist FreeTetra?",
		"landing.what_is.body1": "Freies, foederiertes TETRA-Netz fuer Amateurfunk. Jeder Operator kann einen eigenen Server betreiben — die Server verbinden sich untereinander und teilen ausgewaehlte Talkgroups. Anmeldung mit deiner RadioID, kein zentraler Account.",
		"landing.what_is.body2": "<strong>FreeTetra</strong> ist sowohl das Projekt als auch der erste Server: was hier auf <code>{{HOST}}</code> laeuft, kannst du genauso selber hosten und mit anderen FreeTetra-Servern peeren.",
		"landing.what_is.based": "Basiert auf <code>BlueStation</code> (Open Source TETRA-Basisstation, Apache 2.0).",

		// Landing — Was los
		"landing.whats_up.title":     "Was los ist im Netz",
		"landing.whats_up.intro":     "Live mitschauen wer am Funken ist und wo Coverage besteht:",
		"landing.whats_up.live.name": "Last Heard",
		"landing.whats_up.live.desc": "Wer hat zuletzt gesprochen — Callsign, ISSI, Talkgroup, Dauer. Live-Update alle 2 Sek.",
		"landing.whats_up.map.name":  "Coverage-Map",
		"landing.whats_up.map.desc":  "Wo Funkgeraete erfolgreich gesehen werden — automatisch aus LIP-Positions, in H3-Hexagons aggregiert (Street/City/Region je nach Zoom).",
		"landing.whats_up.ui.name":   "Dashboard",
		"landing.whats_up.ui.desc":   "Volle Uebersicht: Repeater, Subscriber, Federation-Peers, SDS-Console.",

		// Landing — TG-Schema
		"landing.tgs.title":         "Talkgroups (GSSI-Schema)",
		"landing.tgs.local.name":    "Server-lokal",
		"landing.tgs.local.desc":    "Bleibt auf diesem Server — wird NICHT zu anderen FreeTetra-Servern foederiert. Innerhalb des Servers an alle verbundenen Cells. Per Konvention: TG 7-9 fuer Service-Bots (Echo, Wetter, etc.) — jeder Server-Operator hostet die eigenen.",
		"landing.tgs.global.name":   "FreeTetra global",
		"landing.tgs.global.desc":   "Alle FreeTetra-Server weltweit, ueber Brew-Federation.",
		"landing.tgs.bm.name":       "BrandMeister-Bruecke",
		"landing.tgs.bm.desc":       "Wie 10-90 + DMR-Bruecke zu BrandMeister. TG-Nummern 1:1 (z.B. TG 262 = DL, TG 2621 = DL Cluster Nord).",
		"landing.tgs.services":      "Aktuell aktive Services: <strong>TG 9</strong> Echo/Papagei (server-lokal — jeder Server-Operator sollte einen eigenen Echo auf TG 9 betreiben, um Federation-Ping-Pong zu vermeiden).",

		// Landing — Server verbinden
		"landing.connect.title":     "Server verbinden",
		"landing.connect.intro":     "BlueStation-Config — einfach den Brew-Host auf diesen Server zeigen:",
		"landing.connect.note":      "Keine Registrierung noetig! Deine <a href=\"https://radioid.net\" style=\"color:var(--blue)\">Digitalfunk-ID</a> wird automatisch verifiziert. Passwort: <code style=\"color:var(--accent);font-family:'JetBrains Mono',monospace\">blafablafa</code>",
		"landing.connect.full_doc":  "→ Vollständige Anleitung: Mitmachen mit eigener BlueStation",

		// Landing — Server-Info
		"landing.about.title":  "Ueber diesen Server",
		"landing.about.cluster": "Cluster",
		"landing.about.operator": "Betreiber",
		"landing.about.contact":  "Kontakt",

		// Live
		"live.title":         "FreeTetra Live",
		"live.active_calls":  "Aktive Calls",
		"live.last_heard":    "Last Heard",
		"live.silent":        "Stille auf der Frequenz.",
		"live.no_calls":      "Noch keine Calls aufgezeichnet.",
		"live.just_now":      "gerade eben",
		"live.ago_s":         "vor %ds",
		"live.ago_min":       "vor %dmin",
		"live.ago_h":         "vor %dh",
		"live.ago_d":         "vor %dd",
		"live.tg_local":      "LOKAL",
		"live.tg_tetra":      "TETRA",
		"live.tg_dmr":        "DMR",

		// Map
		"map.title":          "FreeTetra Map",
		"map.repeater":       "Repeater",
		"map.devices":        "Geräte",
		"map.filter_title":   "Zeitraum der gezeigten Samples",
		"map.filter_all":     "Alles",

		// Mitmachen
		"join.title":           "Mitmachen",
		"join.tagline":         "FreeTetra ist offen — jeder lizenzierte Funkamateur kann mitmachen. So gehts.",
		"join.two_ways.title":  "Zwei Wege",
		"join.path_hotspot":    "Als User mit eigenem Hotspot",
		"join.path_hotspot.desc": "Eine kleine BlueStation zu Hause, fuer dich + dein Funkgeraet. Haeufigster Fall.",
		"join.path_server":    "Als Server-Operator",
		"join.path_server.desc": "Eigenen FreeTetra-Server fuer einen lokalen Cluster (OV, Verein, Funkrunde).",
		"join.path1.title":    "Pfad 1: Hotspot mit BlueStation",
		"join.path2.title":    "Pfad 2: Eigener Server",
		"join.contact.title":  "Kontakt",
	},
	LangEN: {
		// Common
		"common.lang_label":     "Language",
		"common.home":           "Home",
		"common.map":            "Map",
		"common.live":           "Live",
		"common.dashboard":      "Dashboard",
		"common.join":           "Join",
		"common.back_to_start":  "← Home",
		"common.powered_by":     "Powered by",
		"common.operated_by":    "Operated by",

		// Landing — Hero
		"landing.tagline": "Free, federated TETRA network for amateur radio",

		// Landing — What is FreeTetra
		"landing.what_is.title": "What is FreeTetra?",
		"landing.what_is.body1": "Free, federated TETRA network for amateur radio. Anyone can run their own server — servers connect to each other and share selected talkgroups. Login with your RadioID, no central account needed.",
		"landing.what_is.body2": "<strong>FreeTetra</strong> is both the project and the first server: what runs here on <code>{{HOST}}</code> you can host yourself the same way and peer with other FreeTetra servers.",
		"landing.what_is.based": "Based on <code>BlueStation</code> (open source TETRA base station, Apache 2.0).",

		// Landing — What's up
		"landing.whats_up.title":     "What's happening on the network",
		"landing.whats_up.intro":     "Watch live who's transmitting and where there is coverage:",
		"landing.whats_up.live.name": "Last Heard",
		"landing.whats_up.live.desc": "Who spoke last — callsign, ISSI, talkgroup, duration. Live update every 2 sec.",
		"landing.whats_up.map.name":  "Coverage Map",
		"landing.whats_up.map.desc":  "Where radios were successfully seen — built from LIP positions, aggregated into H3 hexagons (street/city/region depending on zoom).",
		"landing.whats_up.ui.name":   "Dashboard",
		"landing.whats_up.ui.desc":   "Full overview: repeaters, subscribers, federation peers, SDS console.",

		// Landing — TG-Schema
		"landing.tgs.title":         "Talkgroups (GSSI schema)",
		"landing.tgs.local.name":    "Server-local",
		"landing.tgs.local.desc":    "Stays on this server — NOT federated to other FreeTetra servers. Distributed within the server to all connected cells. By convention: TG 7-9 for service bots (echo, weather, etc.) — each server operator runs their own.",
		"landing.tgs.global.name":   "FreeTetra global",
		"landing.tgs.global.desc":   "All FreeTetra servers worldwide, via Brew federation.",
		"landing.tgs.bm.name":       "BrandMeister bridge",
		"landing.tgs.bm.desc":       "Like 10-90 + DMR bridge to BrandMeister. TG numbers map 1:1 (e.g. TG 262 = Germany, TG 2621 = DL Cluster North).",
		"landing.tgs.services":      "Currently active services: <strong>TG 9</strong> Echo/parrot (server-local — each server operator should run their own echo on TG 9 to avoid federation ping-pong).",

		// Landing — Connect
		"landing.connect.title":     "Connect a server",
		"landing.connect.intro":     "BlueStation config — just point the brew host to this server:",
		"landing.connect.note":      "No registration required! Your <a href=\"https://radioid.net\" style=\"color:var(--blue)\">RadioID</a> is verified automatically. Password: <code style=\"color:var(--accent);font-family:'JetBrains Mono',monospace\">blafablafa</code>",
		"landing.connect.full_doc":  "→ Full guide: Join with your own BlueStation",

		// Landing — Server-Info
		"landing.about.title":  "About this server",
		"landing.about.cluster": "Cluster",
		"landing.about.operator": "Operator",
		"landing.about.contact":  "Contact",

		// Live
		"live.title":         "FreeTetra Live",
		"live.active_calls":  "Active calls",
		"live.last_heard":    "Last Heard",
		"live.silent":        "Quiet on the frequency.",
		"live.no_calls":      "No calls recorded yet.",
		"live.just_now":      "just now",
		"live.ago_s":         "%ds ago",
		"live.ago_min":       "%dmin ago",
		"live.ago_h":         "%dh ago",
		"live.ago_d":         "%dd ago",
		"live.tg_local":      "LOCAL",
		"live.tg_tetra":      "TETRA",
		"live.tg_dmr":        "DMR",

		// Map
		"map.title":          "FreeTetra Map",
		"map.repeater":       "Repeater",
		"map.devices":        "Devices",
		"map.filter_title":   "Time window of shown samples",
		"map.filter_all":     "All",

		// Mitmachen
		"join.title":           "Join",
		"join.tagline":         "FreeTetra is open — every licensed amateur radio operator can join. Here's how.",
		"join.two_ways.title":  "Two paths",
		"join.path_hotspot":    "As a user with your own hotspot",
		"join.path_hotspot.desc": "A small BlueStation at home, for you + your handheld. The common case.",
		"join.path_server":    "As a server operator",
		"join.path_server.desc": "Your own FreeTetra server for a local cluster (club, group, association).",
		"join.path1.title":    "Path 1: Hotspot with BlueStation",
		"join.path2.title":    "Path 2: Your own server",
		"join.contact.title":  "Contact",
	},
}
