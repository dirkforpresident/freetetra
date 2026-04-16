# FreeTetra

Freier, foederierter TETRA-Brew-Server fuer Amateurfunk.

Verbindet [BlueStation](https://github.com/MidnightBlueLabs/tetra-bluestation)-Basisstationen
ueber das Brew-Protokoll und ermoeglicht **Federation** zwischen unabhaengigen Servern.

## Was ist das?

FreeTetra ist ein Brew-Server mit Federation — wie E-Mail, aber fuer TETRA-Funk:

- Jeder Funkamateur kann seinen eigenen Server betreiben
- Server peeren untereinander und tauschen Subscriber/Calls aus
- Kein zentraler Server, kein Machthaber
- BlueStations verbinden sich zu einem beliebigen Server und erreichen alle

```
Server DO0RAM  <--peer-->  Server DO0XYZ  <--peer-->  Server DL0ABC
   ^  ^  ^                    ^  ^                        ^
  BS  BS  BS               BS  BS                       BS
```

## Wie mitmachen?

### Stufe 1: Funkgeraet einbuchen
Einfach bei einer BlueStation in deiner Naehe einbuchen. Fertig.

### Stufe 2: Eigene BlueStation aufstellen
Raspberry Pi + SDR + Antenne. Erweitert die Abdeckung fuer alle in deiner Region.

### Stufe 3: Eigener Server betreiben
FreeTetra-Server aufsetzen, mit anderen peeren. Werde Teil der Federation.

## Schnellstart (Server)

```bash
# Binary herunterladen (oder selbst bauen, siehe unten)
wget https://github.com/freetetra/server/releases/latest/download/freetetra-linux-amd64
chmod +x freetetra-linux-amd64

# Config erstellen
cp .env.example .env
nano .env

# Starten
./freetetra-linux-amd64
```

### Selbst bauen

```bash
git clone https://github.com/freetetra/server.git
cd server
go build -o freetetra ./cmd/tetra-brew
```

### Docker

```bash
docker compose up --build brew-router
```

## BlueStation verbinden

```toml
[brew]
host = "freetetra.1xx.is"
port = 443
tls = true
username = DEINE_DIGITALFUNK_ID
password = "blafablafa"
```

Keine Registrierung noetig — die Digitalfunk-ID (ISSI) wird automatisch
ueber die [RadioID](https://radioid.net) API verifiziert.

## GSSI-Schema

Einheitliches Talkgroup-Schema fuer alle FreeTetra-Server:

```
GSSI  1 -  4    Lokal (Sprache) — nur auf dieser Zelle, keine Services
GSSI  5 - 22    Lokal (Services) — Radio, Echo, Ansagen, Tests
GSSI 23 - 90    Federation — werden zwischen Servern geteilt
GSSI 91+        Reserviert — spaeter evtl. DMR-Bridge
```

| GSSI | Typ | Beschreibung |
|---|---|---|
| 1-4 | Lokal | Freie Sprach-Talkgroups (nur diese Zelle) |
| 5 | Lokal | Frei |
| 6 | Service | Webradio (z.B. Deutschlandfunk) |
| 7-21 | Service | Frei fuer lokale Dienste |
| 22 | Service | Echo / Papagei |
| 23-90 | Federation | Geteilte Talkgroups (alle Server) |
| 91+ | Reserviert | Geplant: DMR-Bridge |

Server-Betreiber koennen GSSI 5-21 frei fuer eigene lokale Services nutzen.
Federation-GSSIs (23-90) sind netzweit einheitlich — gleiche Nummer, gleiche Bedeutung.

---

## Features

### Basis (von tetra-brew-mockup / cheetah)

| Feature | Beschreibung |
|---|---|
| **Brew-Protokoll** | Volles Binary-Protokoll (class/type Header, little-endian) ueber WebSocket |
| **HTTP Digest Auth** | Sichere Authentifizierung fuer BlueStation-Verbindungen (RFC 2831) |
| **Group Call Routing** | Sprache wird an alle Clients geroutet die auf der Talkgroup (GSSI) hoeren |
| **Subscriber Management** | Register/Deregister/Affiliate/Deaffiliate von ISSIs und GSSIs |
| **SDS Routing** | Short Data Service (Textnachrichten) werden zwischen Subscribers geroutet |
| **Echo/Papagei** | Testservice der empfangene Sprache zurueckspielt (konfigurierbarer GSSI) |
| **Webradio Bridge** | Internet-Audiostreams in Talkgroups einspeisen (ffmpeg + ACELP Encoder) |
| **Zello Bridge** | Bidirektionale Bruecke zwischen Brew-Talkgroups und Zello-Kanaelen |
| **Netstack Bridge** | Anbindung an echte TETRA-Infrastruktur via MQTT |
| **Dashboard UI** | Vuetify Web-Dashboard mit Live-Polling (Clients, Calls, SDS) |
| **SDS API** | HTTP API zum Senden/Empfangen von SDS-Nachrichten |
| **Virtual SDS** | Virtuelle SDS-Endpunkte per API |
| **Callout Manager** | Managed Group-Call Steuerung |
| **Module Clients** | Separate Binaries fuer Webradio, Zello, Echo (verbinden sich als Brew-Client) |

### FreeTetra-Erweiterungen (von DO1XX)

| Feature | Beschreibung |
|---|---|
| **Federation / Peering** | Server-zu-Server WebSocket-Verbindungen. Subscriber und Calls werden automatisch zwischen Servern geteilt. Loop-Prevention verhindert Endlosschleifen. |
| **RadioID Auto-Auth** | Automatische Authentifizierung ueber die RadioID API. Jede bei radioid.net registrierte ISSI (= lizenzierter Funkamateur) darf sich verbinden. Kein manuelles Account-Anlegen. |
| **ISSI Blocklist** | Einzelne ISSIs koennen gesperrt werden (defekte/stoerende Geraete). API: `/api/radioid/block?issi=XXXXX&action=block` (nur von localhost). |
| **Auth Rate Limiting** | Nach 5 fehlgeschlagenen Login-Versuchen in 2 Minuten wird die IP fuer 15 Minuten gesperrt. Schutz gegen Brute-Force. |
| **LIP Position Tracking** | Eingehende SDS werden automatisch auf TETRA LIP (Location Information Protocol) geprueft. Positionen werden pro ISSI mit Timestamp gespeichert. |
| **APRS-IS Integration** | Decodierte LIP-Positionen werden automatisch an APRS-IS gesendet. Callsign-Lookup ueber RadioID (ISSI → Rufzeichen). Positionen erscheinen auf aprs.fi. |
| **Oeffentliche Startseite** | Landing Page auf `/` mit Erklaerung, Live-Statistiken, Mitmach-Anleitung. Ohne Login fuer jeden zugaenglich. |
| **Public Status API** | `GET /api/public/status` liefert Live-Zahlen (Clients, Subscribers, Positionen) ohne Auth. |
| **Localhost-geschuetzte Admin-APIs** | Admin-Endpunkte (`/api/radioid/*`) sind nur von localhost erreichbar — auch wenn der Server auf 0.0.0.0 bindet. |

---

## Konfiguration

Alle Einstellungen ueber Umgebungsvariablen (`.env` Datei):

### Server

```env
BREW_MODE=server
HTTP_LISTEN_ADDR=127.0.0.1:8091
BREW_SERVER_USERNAME=standard-user      # statischer Fallback-User
BREW_SERVER_PASSWORD=passwort
BREW_SERVER_REALM=brew
```

### RadioID Auto-Auth

```env
RADIOID_AUTH_ENABLED=true
RADIOID_SHARED_KEY=blafablafa           # gemeinsames Passwort fuer alle RadioID-User
```

Wenn aktiviert, darf sich jede ISSI verbinden die bei radioid.net als
lizenzierter Funkamateur registriert ist. Das Shared Key ist das Passwort
das alle User in ihrer BlueStation-Config eintragen.

### Federation

```env
FEDERATION_ENABLED=true
FEDERATION_NAME=DO0RAM                  # Name dieses Servers
FEDERATION_KEY=blafablafa            # Schluessel fuer Peer-Auth
FEDERATION_PEERS=wss://brew.example.com/peer/,wss://other.de/peer/
```

### APRS-IS

```env
APRS_ENABLED=true
APRS_CALLSIGN=DO0RAM
APRS_PASSCODE=18098                     # wird aus Callsign berechnet
APRS_SERVER=euro.aprs2.net:14580
```

### Echo / Papagei

```env
ECHO_TALKGROUP=10002
ECHO_BREW_ISSI=899002
```

### Webradio

Laeuft als separater Prozess (`tetra-brew-webradio`):

```env
BREW_MODE=webradio
BREW_CLIENT_BASE_URL=http://127.0.0.1:8091
WEBRADIO_ENABLED=true
WEBRADIO_STREAM_URL=https://st01.sslstream.dlf.de/dlf/01/128/mp3/stream.mp3
WEBRADIO_TALKGROUP=887
WEBRADIO_SOURCE_ISSI=900001
WEBRADIO_ENCODER_BIN=/opt/freetetra/tetra-acelp-stdio
```

Benoetigt einen TETRA ACELP Encoder. Bauanleitung:
```bash
git clone https://github.com/ElijahHamilton/tetra-acelp.git
cd tetra-acelp
# stdio-Wrapper bauen:
gcc -Icodec/ -Ofast encoder_stdio.c codec/*.c -o tetra-acelp-stdio
```

---

## API Endpunkte

### Oeffentlich (ohne Auth)

| Endpunkt | Methode | Beschreibung |
|---|---|---|
| `/` | GET | Landing Page |
| `/api/public/status` | GET | Live-Statistiken (Clients, Subscribers, Positionen) |
| `/api/positions` | GET | Letzte Position pro ISSI |
| `/api/positions/history` | GET | Letzte 100 Positionsupdates |
| `/brew/` | GET | Brew Discovery Endpoint (Digest Auth) |
| `/peer/` | WebSocket | Federation Peer-Verbindung (Key Auth) |

### Admin (nur localhost)

| Endpunkt | Methode | Beschreibung |
|---|---|---|
| `/ui` | GET | Admin-Dashboard (Vuetify) |
| `/api/dashboard/snapshot` | GET | Dashboard-Daten |
| `/api/radioid/lookup?issi=XXX` | GET | ISSI bei RadioID nachschlagen |
| `/api/radioid/users` | GET | Alle bekannten User (Cache) |
| `/api/radioid/block?issi=XXX&action=block` | GET | ISSI sperren |
| `/api/radioid/block?issi=XXX&action=unblock` | GET | ISSI entsperren |
| `/api/sds/send?to=ISSI&text=MSG` | GET | SDS senden |
| `/api/sds/virtual/endpoints` | GET/POST | Virtuelle SDS-Endpunkte |

---

## Sicherheit

Vierschichtiger Schutz:

```
Verbindungsversuch
  │
  ├─ 1. RadioID: Ist die ISSI ein lizenzierter Funkamateur? → Nein → ABGELEHNT
  │
  ├─ 2. Shared Key: Stimmt das Passwort? → Nein → ABGELEHNT
  │
  ├─ 3. Rate Limit: 5x falsches PW in 2 Min? → Ja → 15 MIN GESPERRT
  │
  ├─ 4. Blocklist: ISSI manuell gesperrt? → Ja → ABGELEHNT
  │
  └─ ✅ VERBUNDEN
```

Admin-APIs sind zusaetzlich auf localhost beschraenkt.

---

## Architektur

```
                    ┌─────────────────────────────────────────┐
                    │           FreeTetra Server (Go)         │
                    │                                         │
  BlueStation ──────┤  Brew Protocol Handler                  │
  (WebSocket)       │    ├── Subscriber Management            │
                    │    ├── Call Routing (Group/Individual)   │
                    │    ├── SDS Routing                       │
                    │    └── RadioID Auto-Auth                 │
                    │                                         │
  Andere Server ────┤  Federation Hub                         │
  (WebSocket)       │    ├── Peer Management                  │
                    │    ├── Subscriber Sync                   │
                    │    ├── Call Forwarding                   │
                    │    └── Loop Prevention                   │
                    │                                         │
                    │  Services                               │
                    │    ├── LIP Position Tracking → APRS-IS  │
                    │    ├── Echo/Papagei                      │
                    │    ├── SDS Bridge + Virtual Endpoints    │
                    │    └── Dashboard UI                      │
                    │                                         │
  Webradio ─────────┤  Module Clients (separate Prozesse)     │
  Zello Bridge      │    ├── tetra-brew-webradio              │
  Echo              │    ├── tetra-brew-zello                 │
                    │    └── tetra-brew-echo                  │
                    └─────────────────────────────────────────┘
```

---

## Lizenz

GPLv3 — Frei wie in Freiheit. Jeder darf den Code nutzen, aendern und verbreiten,
solange Aenderungen ebenfalls unter GPLv3 veroeffentlicht werden.

## Credits

- Brew-Server Basis: [tetra-brew-mockup](https://github.com/) von cheetah
- ACELP Codec: [tetra-acelp](https://github.com/ElijahHamilton/tetra-acelp) von ElijahHamilton, basierend auf [outerplane/tetra-codec](https://github.com/outerplane/tetra-codec)
- [BlueStation](https://github.com/MidnightBlueLabs/tetra-bluestation) von MidnightBlueLabs
- Federation, RadioID-Auth, APRS-IS, Positions-Tracking: DO1XX
