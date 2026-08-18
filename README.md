# Device Monitor

Two pieces:

- **`server/server.py`** — runs on your main device. Serves the control
  panel and remembers when it last heard from each other device.
- **`agent/`** — the mini program for every other device. It's a
  compiled native executable — nothing to install, no Python, no
  runtime. Just copy the file for your OS over and run it.

## 1. Set a shared secret

Open `server/server.py` and change `TOKEN` to a long random string.
You'll put the same string in each device's config file (step 3) — it
stops random devices on your network from reporting themselves in.

## 2. Turn on encryption (do this before step 3)

By default the server would run over plain HTTP, meaning IPs, device
names, and your token travel across the network unencrypted — anyone
sniffing traffic on the same network (or the internet, if you expose
it) could read them. Fix this with a self-signed certificate. You only
do this once, on the main device, in the `server/` folder:

```
openssl req -x509 -newkey rsa:2048 -keyout key.pem -out cert.pem -days 3650 -nodes \
  -subj "/CN=device-monitor" \
  -addext "subjectAltName=IP:192.168.1.20"
```

Replace `192.168.1.20` with whatever address the agents will actually
connect to — your main device's LAN IP (or, if you set up dynamic DNS
per Option A below, `-addext "subjectAltName=DNS:yourname.duckdns.org"`
instead). This step matters: without a matching SAN entry, modern TLS
will refuse the connection.

`cert.pem` and `key.pem` land next to `server.py`. The server picks
them up automatically next time it starts and switches to HTTPS.

Don't have `openssl`? It ships by default on macOS and Linux. On
Windows, install it via `winget install ShiningLight.OpenSSL` or run
this step from WSL/Git Bash.

## 3. Start the server on your main device

```
python3 server/server.py
```

If it found `cert.pem`/`key.pem` it prints an `https://` URL; if not,
it warns you it's running unencrypted and tells you how to fix it.

## 4. Install the agent on every other device

Pick the file for that device from `agent/dist/`:

| OS | File |
|---|---|
| Windows | `agent-windows-amd64.exe` |
| Mac (Apple Silicon) | `agent-macos-arm64` |
| Mac (Intel) | `agent-macos-intel` |
| Linux (most PCs/servers) | `agent-linux-amd64` |
| Linux (Raspberry Pi, ARM) | `agent-linux-arm64` |

There is no single "generic" build — each of these is a separate
native executable compiled for that exact OS and CPU, which is why
none of them need an installer or runtime on the target device.

Copy that one file to the device (Mac/Linux: `chmod +x` it first).
**Also copy `server/cert.pem`** to the same folder on that device and
rename it to `server-cert.pem` — this is what lets the agent trust
your specific server without trusting random certificates from
anyone else ("certificate pinning"). Then run the agent once, e.g. by
double-clicking, or `./agent-linux-amd64` in a terminal.

The first run writes a plain-text `agent-config.json` next to itself
and then starts retrying the server (it'll fail until you edit the
config — that's expected). Open `agent-config.json` in any text
editor and set:

```json
{
  "server_url": "https://192.168.1.20:8765",
  "token": "the-same-secret-you-put-in-server.py",
  "device_name": ""
}
```

Restart the agent. It will keep retrying — every few seconds at
first, backing off up to once a minute — until it reaches the server,
then switches to a steady heartbeat every 45s, plus an immediate
report the moment it notices its IP changed (e.g. you joined a
different wifi network). Once it connects you'll see
`pinned TLS trust to server-cert.pem` in its output, confirming
traffic to your server is encrypted.

No compiler, no Python, no package installs on that device — the
binary, `agent-config.json`, and `server-cert.pem` are the only three
things there.

## 5. Make the agent always-running

It loops forever on its own, it just needs the OS to start it and
keep it alive.

**Windows** — Task Scheduler: create a task that runs the `.exe`,
trigger "At log on", check "Run whether user is logged on or not".

**macOS (launchd)** — create
`~/Library/LaunchAgents/com.you.devicemonitor.plist`:
```xml
<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
  <key>Label</key><string>com.you.devicemonitor</string>
  <key>ProgramArguments</key>
  <array><string>/full/path/to/agent-macos-arm64</string></array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
</dict></plist>
```
Then: `launchctl load ~/Library/LaunchAgents/com.you.devicemonitor.plist`

**Linux (systemd user service)** — create
`~/.config/systemd/user/devicemonitor.service`:
```ini
[Unit]
Description=Device Monitor Agent

[Service]
ExecStart=/full/path/to/agent-linux-amd64
Restart=always

[Install]
WantedBy=default.target
```
Then: `systemctl --user enable --now devicemonitor.service`

**Phones/tablets**: iOS/Android don't allow always-on background
processes like this — this setup is for laptops/desktops/servers/
Raspberry Pis etc.

---

## Does this work across different wifi networks?

Out of the box: **no** — `192.168.1.20` only means something on your
home LAN. A device on a coffee-shop wifi or cellular data can't reach
it. Two ways to fix that:

### Option A — port forward + a free dynamic DNS hostname (recommended)

Your home internet connection's public IP usually changes over time,
so you want a hostname that always points at it rather than a raw IP.

1. Sign up for a free dynamic DNS hostname, e.g. via **DuckDNS**
   (duckdns.org) or **No-IP** — you'll get something like
   `yourname.duckdns.org`.
2. In your router's settings, forward external TCP port `8765` to the
   main device's local IP and port `8765`. (Every router's UI calls
   this something slightly different: "Port Forwarding", "Virtual
   Server", "NAT rules".)
3. Many routers have a built-in DDNS client (look for "DDNS" in
   settings) that keeps your hostname updated automatically. If yours
   doesn't, the DDNS provider gives you a tiny updater you run on the
   main device instead.
4. Set every agent's `server_url` to
   `https://yourname.duckdns.org:8765` instead of the local IP. Now it
   works from anywhere with internet access — home, other wifi,
   cellular.
5. Regenerate `cert.pem` with that hostname in the SAN instead of your
   LAN IP (step 2 above), and re-copy the new `cert.pem` to each
   device as `server-cert.pem`. Alternatively, since you now have a
   real public hostname, you could get a free trusted certificate from
   Let's Encrypt for it instead of a self-signed one — then you don't
   need to distribute `server-cert.pem` at all, since it'll be trusted
   automatically. That's more setup (running `certbot`); self-signed +
   pinning is simpler and just as private for personal use.

With HTTPS in place (step 2), your token and every device's IPs are
encrypted in transit, including over the open internet in this setup.

### Option B — a mesh VPN (Tailscale/ZeroTier), more setup, more private

Instead of exposing anything to the public internet, install
Tailscale (or ZeroTier) on the main device and on each other device.
They form a private virtual network and give each device a stable
address that works from anywhere, with no port-forwarding and no
public exposure. This does mean installing something on each device
(one more download per device, unlike the agent binary above), but
it's the more secure, more "it just works" long-term answer if you
want this permanently. Happy to write up the exact steps if you'd
rather go this route.

---

## Notes

- "Online" in the control panel = a heartbeat within the last 90
  seconds; "stale" = overdue but recently seen; older = offline.
  Adjust `ONLINE_THRESHOLD` in `server.py` if you change the agent's
  `heartbeat_seconds`.
- Public IP is fetched via api.ipify.org (falls back to ifconfig.me),
  so agents need outbound internet access for that field — private IP
  and check-ins work fine even on a LAN-only device.
- If your main device's local IP can change, give it a static/
  reserved IP in your router so `server_url` doesn't go stale (this
  matters whether you're using Option A or staying LAN-only).
