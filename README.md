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

### Stufe 2: Eigene BlueStation
Pi + SDR + Antenne aufstellen und bei einem bestehenden Server anmelden.

### Stufe 3: Eigener Server
FreeTetra-Server aufsetzen, mit anderen peeren. Erweitert das Netz.

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

## Features

- **Brew-Protokoll**: Volles Binary-Protokoll mit HTTP Digest Auth
- **Federation**: Server-zu-Server Peering (WebSocket)
- **Subscriber-Sync**: ISSIs und Talkgroup-Affiliations werden zwischen Peers geteilt
- **Call-Routing**: Sprache wird automatisch ueber Server-Grenzen geroutet
- **Loop-Prevention**: Nachrichten kreisen nicht im Netz
- **Echo/Papagei**: Testservice zum Pruefen der eigenen Verbindung
- **Netstack-Bridge**: Anbindung an echte TETRA-Infrastruktur via MQTT
- **Zello-Bridge**: Bidirektionale Bruecke zu Zello-Kanaelen
- **Web-Radio**: Internet-Streams in Talkgroups einspeisen
- **Dashboard**: Web-UI mit Live-Status
- **SDS-API**: Textnachrichten senden/empfangen per HTTP

## Federation konfigurieren

```env
# In .env oder config:
FEDERATION_PEERS=wss://brew.example.com/peer/,wss://brew.other.de/peer/
FEDERATION_KEY=shared-secret-zwischen-den-servern
FEDERATION_NAME=DO0RAM
```

Peers verbinden sich automatisch und tauschen Subscriber-Tabellen aus.
Calls werden an Peers weitergeleitet wenn dort Subscriber auf der Talkgroup sind.

## Lizenz

GPLv3 — Frei wie in Freiheit. Jeder darf den Code nutzen, aendern und verbreiten,
solange Aenderungen ebenfalls unter GPLv3 veroeffentlicht werden.

## Credits

- Basierend auf dem tetra-brew-mockup von cheetah
- [BlueStation](https://github.com/MidnightBlueLabs/tetra-bluestation) von MidnightBlueLabs
- Federation-Konzept und Implementierung: DO1XX + Community
