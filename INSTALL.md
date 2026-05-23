# FreeTetra — Installation

Production setup for a Linux server (Ubuntu/Debian), with nginx + Letsencrypt
+ systemd. For a quick dev setup see [README.md](README.md).

## Prerequisites

- Linux server (Ubuntu 22.04+ / Debian 12+) with public IP
- A domain (e.g. `freetetra-example.de`) pointing to your server
- `nginx`, `certbot`, `go` 1.24+, `git`, `gcc` installed

```bash
sudo apt update
sudo apt install -y nginx certbot python3-certbot-nginx golang-go git build-essential jq
```

## 1. Build

```bash
git clone https://github.com/dirkforpresident/freetetra.git /opt/freetetra/src
cd /opt/freetetra/src
go build -o /opt/freetetra/freetetra ./cmd/tetra-brew

# optional service bots
go build -o /opt/freetetra/tetra-brew-echo ./cmd/tetra-brew-echo
go build -o /opt/freetetra/tetra-brew-webradio ./cmd/tetra-brew-webradio
go build -o /opt/freetetra/tetra-brew-dmrbridge ./cmd/tetra-brew-dmrbridge
```

## 2. Configuration

```bash
cp /opt/freetetra/src/.env.example /opt/freetetra/.env
# edit /opt/freetetra/.env — at minimum set:
#   FEDERATION_NAME=YOUR_CALLSIGN
#   FEDERATION_SELF_URL=wss://your-server.tld/peer/
#   OPERATOR_NAME=YOUR_CALLSIGN
#   OPERATOR_CONTACT=you@example.com
```

The default `FEDERATION_KEY=freetetra-federation-2026` peers with the public
network. Use your own key if you want a private mesh.

```bash
mkdir -p /opt/freetetra/data
```

## 3. nginx reverse proxy

Create `/etc/nginx/sites-available/freetetra-example.de`:

```nginx
server {
    listen 80;
    server_name freetetra-example.de;
    location /.well-known/acme-challenge/ { root /var/www/letsencrypt; }
    location / { return 301 https://freetetra-example.de$request_uri; }
}

server {
    listen 443 ssl http2;
    server_name freetetra-example.de;
    ssl_certificate /etc/letsencrypt/live/freetetra-example.de/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/freetetra-example.de/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:8091;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_read_timeout 86400s;
        proxy_send_timeout 86400s;
    }
}
```

```bash
sudo mkdir -p /var/www/letsencrypt
sudo ln -s /etc/nginx/sites-available/freetetra-example.de /etc/nginx/sites-enabled/
sudo nginx -t && sudo systemctl reload nginx
```

## 4. SSL certificate

```bash
sudo certbot certonly --webroot -w /var/www/letsencrypt \
    -d freetetra-example.de \
    --key-type ecdsa --agree-tos -m you@example.com
sudo systemctl reload nginx
```

`certbot.timer` handles auto-renewal.

## 5. systemd service

Create `/etc/systemd/system/freetetra.service`:

```ini
[Unit]
Description=FreeTetra Brew Server
After=network.target

[Service]
Type=simple
WorkingDirectory=/opt/freetetra
EnvironmentFile=/opt/freetetra/.env
ExecStart=/opt/freetetra/freetetra
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable freetetra
sudo systemctl start freetetra
sudo systemctl status freetetra
```

## 6. Verify

```bash
curl https://freetetra-example.de/api/public/status
# → {"server":"FreeTetra YOUR_CALLSIGN","tmo_sites":0,...}

curl https://freetetra-example.de/api/peers
# → {"count":2,"peers":[{"name":"DO0RAM","direction":"outgoing",...}]}
```

Open `https://freetetra-example.de/` in a browser. You should see the landing
page with your operator info card. `/live`, `/map`, `/mitmachen` all work.

## 7. Optional: service bots

Echo on TG 9 (local), as a separate Brew-client process:

Create `/opt/freetetra/.env.echo`:

```env
BREW_MODE=echo
BREW_CLIENT_BASE_URL=http://127.0.0.1:8091
BREW_CLIENT_USERNAME=YOUR_BREW_USERNAME
BREW_CLIENT_PASSWORD=blafablafa
ECHO_TALKGROUP=9
ECHO_SOURCE_ISSI=900002
ECHO_BREW_ISSI=900002
ECHO_PLAYBACK_DELAY=100ms
ECHO_FRAME_INTERVAL=60ms
```

systemd unit `/etc/systemd/system/freetetra-echo.service`:

```ini
[Unit]
Description=FreeTetra Echo
After=freetetra.service

[Service]
Type=simple
WorkingDirectory=/opt/freetetra
EnvironmentFile=/opt/freetetra/.env.echo
ExecStart=/opt/freetetra/tetra-brew-echo
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now freetetra-echo
```

Same pattern for `tetra-brew-webradio` and `tetra-brew-dmrbridge`.

## 8. Federation peering

Once your server is up, peer with the public FreeTetra network by setting
in `/opt/freetetra/.env`:

```env
FEDERATION_PEERS=wss://freetetra.de/peer/
FEDERATION_KEY=freetetra-federation-2026
```

Restart `freetetra.service`. Check peer status:

```bash
curl https://freetetra-example.de/api/peers | jq
```

A connection in both `incoming` and `outgoing` direction means peering is
fully established.

## Updates

```bash
cd /opt/freetetra/src
git pull
go build -o /tmp/ft ./cmd/tetra-brew
sudo systemctl stop freetetra
sudo cp /tmp/ft /opt/freetetra/freetetra
sudo systemctl start freetetra
```

## Multiple servers on one host

Running both `freetetra.de` and `hh.freetetra.de` on the same physical
host (as the reference setup does):

- Run a second binary on a different port (e.g. 8093)
- Use a separate working directory (`/opt/freetetra-hh/`) and `.env`
- nginx vhost for the second domain, proxying to 8093
- Separate systemd unit `freetetra-hh.service`

The two instances can peer with each other via `wss://` over their
public DNS names — same as if they were on different machines.
