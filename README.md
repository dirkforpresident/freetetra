# FreeTetra

Federated TETRA backhaul server for amateur radio.

A drop-in Brew server that peers with other FreeTetra servers and exchanges
subscribers, calls, voice frames, SDS, BlueStation telemetry and coverage
samples via mesh relay. Any server can be a seed. Any server can go down
without taking the network with it.

The flagship server runs at **[freetetra.de](https://freetetra.de)** — landing
page, live view, map, and a setup guide for new operators are public.

## What it does

- Brew protocol server for BlueStation TMO-sites and hotspots (binary WebSocket)
- Server-to-server federation with mesh relay, TTL, deduplication, path tracking
- RadioID auto-auth — any licensed amateur on [radioid.net](https://radioid.net)
  is accepted automatically, no manual allow-list
- LIP position decoding → APRS-IS forwarding
- Coverage database with H3 hex aggregation (street/city/region zoom)
- Last-Heard live view, coverage map, operator dashboard
- DE/EN i18n on all public pages
- Optional DMR/BrandMeister bridge (separate `tetra-brew-dmrbridge` process)

## Requirements

- BlueStation TMO-site or hotspot — see
  [MidnightBlueLabs/tetra-bluestation](https://github.com/MidnightBlueLabs/tetra-bluestation)
- Linux server with public IP (VPS, home server with port forwarding, or HamNet node)
- Go 1.24+ for building, or pre-built binary from releases
- TLS reverse proxy in front (nginx + Letsencrypt, recommended)

## Quick start

```bash
git clone https://github.com/dirkforpresident/freetetra.git
cd freetetra
go build -o freetetra ./cmd/tetra-brew
cp .env.example .env
# edit .env — at minimum set FEDERATION_NAME and OPERATOR_NAME
./freetetra
```

For a production setup with nginx + SSL + systemd: see [INSTALL.md](INSTALL.md).

### Docker Compose with multiple env files

`docker-compose.yml` supports a shared env file plus per-service env files.
By default all services still read `.env`, so existing setups keep working.

Use overrides when you want separate configs per service:

```bash
ENV_COMMON_FILE=.env.common \
ENV_FREETETRA_FILE=.env.freetetra \
ENV_WEBRADIO_FILE=.env.webradio \
ENV_ECHO_FILE=.env.echo \
docker compose up -d
```

If you only want one service-specific override, set just that variable.

### Host routing used by this Docker setup

- `http://free.tetra.cat:80` -> this machine `:8091` (main instance)
- `http://free2.tetra.cat:80` -> this machine `:8092` (loopback test instance)

## BlueStation configuration

Point your BS at the server. Any valid DMR/TETRA ID registered on
[radioid.net](https://radioid.net) is accepted automatically.

```toml
[brew]
host = "your-freetetra-server.tld"
port = 443
tls = true
username = YOUR_ISSI            # = DMR-ID + 2-digit SSID, e.g. 262356300
password = "blafablafa"         # shared key for all RadioID users
```

The rest of the BlueStation config (SDR frequencies, `[net_info]`, `[cell_info]`)
is BlueStation-side setup and documented in the BlueStation docs.

## GSSI scheme

```
TG 1-9      Local       — stays on this server, never federated
                          By convention: TG 7-9 for service bots (echo, weather)
                          Each server operator runs their own.
TG 10-90    FreeTetra   — federated to all FreeTetra servers worldwide
TG 91+      BrandMeister — federated PLUS bridged to DMR/BrandMeister on
                          servers that run the dmrbridge. TG numbers map 1:1
                          (e.g. TG 262 = Germany, TG 2621 = DL Cluster Nord).
```

Enforced in code (`internal/federation/hub.go::isFederatedGSSI`). Local GSSIs
stay on the originating server; everything ≥10 is mesh-relayed to all peers.

## Federation

FreeTetra is an **open federation** with a **symmetric shared key**:

```env
FEDERATION_ENABLED=true
FEDERATION_NAME=YOUR_CALLSIGN
FEDERATION_KEY=freetetra-federation-2026
FEDERATION_SELF_URL=free.tetra.cat:8102
FEDERATION_PEERS=free2.tetra.cat:8202
```

Federation transport is gRPC. Peer values are `host:port` targets.
Use one HTTP/WS port and one gRPC port per instance.

### NGINX on port 80

If you run NGINX in front, expose both hostnames on public port `80` and route
HTTP/WS plus federation gRPC to the correct internal backend ports.

Ready-to-use config for your current two-host setup is in
`docs/nginx-two-ports.conf`:

- public `free.tetra.cat:80` -> internal main HTTP/WS `:8101` and gRPC `:8102`
- public `free2.tetra.cat:80` -> internal loopback HTTP/WS `:8201` and gRPC `:8202`

The key `freetetra-federation-2026` is **deliberately public** — anyone can join.
If you want a **private mesh** (closed group, no connection to the public network),
just pick your own `FEDERATION_KEY` and share it only with your own peers.

### What gets federated

| Type | Mechanism |
|---|---|
| Subscribers (ISSI affiliations) | `MsgSubscriberUpdate` event + periodic anti-entropy sync every 30s |
| Voice calls (group TX) | `MsgCallStart`/`MsgCallEnd` + binary `MsgCallFrame` for voice |
| SDS messages | `MsgSDSRelay` |
| LIP position samples | `MsgPositionSample` — every server's coverage map shows the whole network |
| BlueStation heartbeats | `MsgStationUpdate` — station list is consistent across servers |
| Peer gossip | `MsgPeerExchange` — discover new peers via known peers |
| RadioID users.txt | `MsgUsersDBOffer`/`MsgUsersDBRequest` — offline peers can sync the user DB from connected peers |

### Resilience

- Mesh relay with msg-ID dedup (30s window) + TTL (max 10 hops) + path tracking → no loops possible
- Initial full state sync on every new peer connection
- Periodic anti-entropy sync every 30s for subscribers + stations
- Auto-reconnect on TCP drop (10s delay)
- Self-connect protection: gossip filters our own URL/name out

## RadioID auto-auth

Connection is accepted if the username (ISSI) is a licensed amateur on
[radioid.net](https://radioid.net). No account creation, no manual allow-list.

```env
RADIOID_AUTH_ENABLED=true
RADIOID_SHARED_KEY=blafablafa
RADIOID_SYNC_ON_START=true
RADIOID_SYNC_EVERY=24h
```

The full users database (~260k entries) is cached locally as `users.txt` and
refreshed periodically. For HamNet or other offline deployments:

```env
RADIOID_OFFLINE_MODE=true
```

Peers without internet automatically download the DB from connected peers that
have it. One internet-connected seed is enough for a whole HamNet cluster.

Manual bans (localhost only, via SSH):

```
GET /api/radioid/block?issi=XXX&action=block
```

## APRS-IS

LIP position reports (SDS Type4) are decoded and forwarded to APRS-IS.
Callsign is looked up via RadioID (ISSI → callsign).

```env
APRS_ENABLED=true
APRS_CALLSIGN=YOUR_CALLSIGN
APRS_PASSCODE=CALCULATED_PASSCODE
APRS_SERVER=euro.aprs2.net:14580
```

## Operator info

Each server can advertise its operator info on the landing page:

```env
OPERATOR_NAME=YOUR_CALLSIGN
OPERATOR_CONTACT=you@example.com
OPERATOR_DESCRIPTION=Short text about who runs this server and for whom.
```

## Endpoints

Public, all i18n DE/EN via Accept-Language and `/lang/de` `/lang/en` cookie toggle:

```
GET  /              landing page
GET  /live          last-heard live view (2s polling, glow animations)
GET  /map           coverage map (H3 hexes, time-decay filter 7d/30d/90d/all)
GET  /mitmachen     join guide for new operators
GET  /ui            admin dashboard (read-only by default)
GET  /api/public/status     live counts
GET  /api/live/last-heard   last 100 heard calls
GET  /api/positions         last position per ISSI
GET  /api/map?res=N&days=N  coverage hexes (resolution 5/7/9, time-decay)
GET  /api/stations          known BlueStations (federated)
GET  /api/telemetry/clients connected BlueStations
GET  /api/peers             connected federation peers
GET  /api/users.txt         local RadioID database (for peer sync)
GET  /lang/{de|en}          set language cookie + redirect to referer
```

Authenticated (by protocol):

```
GET  /brew/                 BlueStation discovery (HTTP Digest + RadioID)
WS   /                      telemetry (HTTP Basic + RadioID)
WS   /peer/                 federation peer (shared key via X-Brew-Key header)
```

Localhost only (admin actions):

```
GET  /api/radioid/block     ban/unban an ISSI
GET  /api/radioid/users     list cached users
```

## Service bots (optional)

Echo, webradio, and DMR-bridge are separate Brew-client processes, not part of
the core server. Run them alongside the server if needed.

```bash
./tetra-brew-echo         # echo/parrot service (set ECHO_TALKGROUP)
./tetra-brew-webradio     # webradio (ACELP encoder required)
./tetra-brew-dmrbridge    # BrandMeister DMR bridge (TG 91+)
```

By convention service bots should run on local TGs (1-9) so they don't
create federation ping-pong with other servers running similar bots.

## Build

ACELP codec is included under `codec/`. No external downloads.

```bash
go build ./cmd/tetra-brew
go build ./cmd/tetra-brew-webradio
go build ./cmd/tetra-brew-echo
go build ./cmd/tetra-brew-dmrbridge

# ACELP encoder for webradio
gcc -Icodec/ -Ofast codec/encoder_stdio.c codec/codec/*.c -o tetra-acelp-stdio
```

CGO is required (h3-go uber/h3-go uses libh3). On macOS:

```bash
brew install gcc
```

### Regenerate protobuf code

```bash
./scripts/gen-proto.sh
```

This regenerates both federation protobuf APIs:

- `internal/federation/proto/federation.proto`
- `internal/federation/proto/v2/federation_v2_draft.proto`

### Federation loopback Docker test

Run a 3-node federation test (`ft-a -> ft-b -> ft-c`) plus an echo loopback
module behind `ft-c`. The test verifies that `ft-a` discovers `ft-c` via
gossip through `ft-b`.

```bash
./tests/federation-loopback/run.sh
```

To keep the stack running for manual inspection:

```bash
KEEP_RUNNING=1 ./tests/federation-loopback/run.sh
```

### External free2.tetra.cat TG25 loopback test

Run a local loopback federation node and attach an echo module on talkgroup 25.
The loopback node is the `free2.tetra.cat` instance and attempts to federate
to the main `free.tetra.cat` instance via federation RPC
(`FEDERATION_PEERS=free.tetra.cat:8102`).

```bash
./tests/federation-freecat/run.sh
```

Optional overrides:

```bash
FREECAT_HOST=free.tetra.cat FREECAT_RPC_PORT=8102 KEEP_RUNNING=1 ./tests/federation-freecat/run.sh
```

## Configuration reference

All env-vars are documented in [.env.example](.env.example).

## Companion tools

Run alongside FreeTetra (typically on the Pi next to a BlueStation):

| Repo | Purpose |
|---|---|
| [`freetetra-agent`](https://github.com/dirkforpresident/freetetra-agent) | Station registration daemon. Small PIN-protected web UI to declare callsign, position, frequencies. Pushes to `/api/stations/push`. |
| [`freetetra-lip-aprs`](https://github.com/dirkforpresident/freetetra-lip-aprs) | LIP → APRS bridge. Reads BlueStation journal, extracts individual LIP position SDS (e.g. on TG 262999), pushes to the server which forwards to APRS-IS. |

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

GPLv3.

## Credits

- `tetra-brew-mockup` by cheetah — initial Brew protocol implementation
- `tetra-acelp` by ElijahHamilton (based on outerplane/tetra-codec) — ACELP codec
- `tetra-bluestation` by MidnightBlueLabs — TETRA base station (Apache 2.0)
- Federation, mesh, RadioID, APRS, coverage, telemetry, web UI, i18n, docs: DO1XX
