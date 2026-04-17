# FreeTetra

Federated TETRA backhaul server for amateur radio.

Built for operators who already run a BlueStation repeater or hotspot and want
to connect it to a decentralized network — no central authority, no single
point of failure.

## What it is

A drop-in Brew server that connects to multiple peers and exchanges
subscribers, calls, and SDS via mesh relay. Any server can be a seed.
Any server can go down without taking the network with it.

Built on cheetah's `tetra-brew-mockup` with federation, mesh routing,
RadioID auto-auth, APRS-IS forwarding, and BlueStation telemetry added.

## Requirements

- Working BlueStation repeater or hotspot with recent firmware (telemetry
  endpoint support)
- Linux server with a public IP (VPS, home server with port forwarding,
  or HamNet node)
- Go 1.24+ for building from source, or pre-built binary from releases

## Quick start

```bash
git clone https://github.com/dirkforpresident/freetetra.git
cd freetetra
go build -o freetetra ./cmd/tetra-brew
cp .env.example .env
# edit .env
./freetetra
```

Or via Docker:

```bash
docker compose up -d
```

## BlueStation configuration

Point your BS at the server. Any valid DMR/TETRA ID registered on
radioid.net is accepted automatically.

```toml
[brew]
host = "your-freetetra-server.tld"
port = 443
tls = true
username = YOUR_ISSI
password = "blafablafa"

[telemetry]
host = "your-freetetra-server.tld"
port = 443
use_tls = true
username = "YOUR_ISSI"
password = "blafablafa"
```

## GSSI scheme

```
1 - 4     local (not forwarded between servers)
5 - 90    federation (shared across all peered servers)
91+       reserved (future DMR bridge)
```

Enforced in code. Local GSSIs stay on the originating cell; federated
GSSIs are mesh-relayed to all connected peers.

## Federation

Peer with one seed, get the rest via gossip. Servers advertise their known
peers; new servers auto-connect to everyone.

```env
FEDERATION_ENABLED=true
FEDERATION_NAME=YOUR_CALLSIGN
FEDERATION_KEY=blafablafa
FEDERATION_SELF_URL=wss://your-server.tld/peer/
FEDERATION_PEERS=wss://freetetra.1xx.is/peer/
```

Mesh relay with TTL (max 10 hops), message deduplication, path tracking.
Loops are prevented. Voice frames flood-forward to all connected peers.
Kill any server and the rest keeps working.

## RadioID auto-auth

Connection is accepted if the username (ISSI) is a licensed amateur
on radioid.net. No account creation, no manual allow-list.

```env
RADIOID_AUTH_ENABLED=true
RADIOID_SHARED_KEY=blafablafa
RADIOID_SYNC_ON_START=true
RADIOID_SYNC_EVERY=24h
```

The full users database (~260k entries) is cached locally as `users.txt`
and refreshed periodically. For HamNet or other offline deployments:

```env
RADIOID_OFFLINE_MODE=true
```

Peers without internet automatically download the DB from connected peers
that have it. One internet-connected seed is enough for a whole HamNet
cluster.

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

## Endpoints

Public (no auth):

```
GET  /                       landing page
GET  /ui                     admin dashboard (read-only)
GET  /api/public/status      live counts
GET  /api/positions          last position per ISSI
GET  /api/telemetry/clients  connected BlueStations
GET  /api/peers              connected federation peers
GET  /api/users.txt          local RadioID database (for peer sync)
```

Authenticated (by protocol):

```
GET  /brew/                  BlueStation discovery (HTTP Digest + RadioID)
WS   /                       telemetry (HTTP Basic + RadioID)
WS   /peer/                  federation peer (shared key)
```

Localhost only (admin actions):

```
GET  /api/radioid/block      ban/unban an ISSI
GET  /api/radioid/users      list cached users
```

## Services (optional)

Echo/parrot and webradio run as separate client processes, not part of the
core server.

```bash
./tetra-brew-echo      # echo service
./tetra-brew-webradio  # webradio (ACELP encoder required)
```

## Build

ACELP codec is included under `codec/`. No external downloads.

```bash
go build ./cmd/tetra-brew
go build ./cmd/tetra-brew-webradio
go build ./cmd/tetra-brew-echo

# ACELP encoder for webradio
gcc -Icodec/ -Ofast codec/encoder_stdio.c codec/codec/*.c -o tetra-acelp-stdio
```

## License

GPLv3.

## Credits

- `tetra-brew-mockup` by cheetah — Brew protocol implementation
- `tetra-acelp` by ElijahHamilton (based on outerplane/tetra-codec) — ACELP codec
- `tetra-bluestation` by MidnightBlueLabs — TETRA base station (FOSS)
- Federation, mesh, RadioID, APRS, telemetry, docs: DO1XX
