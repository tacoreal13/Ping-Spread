# Device Monitor

Two pieces:

- **`server/server.py`** — runs on your main device. Serves the control
  panel and remembers when it last heard from each other device.
- **`agent/`** — the mini program for every other device. Now installed
  with one command — no manual binary picking, no hand-editing config.
  Works on Windows, macOS, Linux, and Chromebooks (via ChromeOS's
  built-in Linux container).

## 0. One-time GitHub setup

1. Push this repo to GitHub (public or private both work).
2. In `install.sh` and `install.ps1`, change `OWNER/REPO` at the top to
   your actual `github-username/repo-name`.
3. Tag a release so the binaries get built and attached automatically:
   ```
   git tag v1.0.0
   git push origin v1.0.0
   ```
   This triggers `.github/workflows/release.yml`, which cross-compiles
   all 5 platform binaries and attaches them to a GitHub Release. Check
   the "Actions" and "Releases" tabs on GitHub to confirm it worked.
   (You can also trigger it manually from the Actions tab without a tag.)

From then on, every device just runs a curl/irm one-liner — no
downloading the right file, no manually writing JSON.

## 1. Set a shared secret

Open `server/server.py` and change `TOKEN` to a long random string.
You'll pass this same string to the installer on each device — it
stops random devices on your network from reporting themselves in.

## 2. Turn on encryption (do this before step 3)

By default the server runs over plain HTTP. Fix that with a
self-signed certificate, generated once on the main device in the
`server/` folder:

```
openssl req -x509 -newkey rsa:2048 -keyout key.pem -out cert.pem -days 3650 -nodes \
  -subj "/CN=device-monitor" \
  -addext "subjectAltName=IP:192.168.1.20"
```

Replace `192.168.1.20` with your main device's actual address (LAN IP,
or your dynamic DNS hostname — see "Does this work across networks?"
below). `cert.pem`/`key.pem` land next to `server.py` and the server
picks them up automatically.

## 3. Start the server

```
python3 server/server.py
```

It prints the URL to use (`https://...` once the cert is in place).

## 4. Install the agent on every other device

**Mac / Linux:**
```
DM_SERVER_URL="https://192.168.1.20:8765" \
DM_TOKEN="the-same-secret-from-step-1" \
DM_CERT_URL="https://192.168.1.20:8765/cert" \
DM_AUTOSTART=1 \
bash <(curl -fsSL https://raw.githubusercontent.com/OWNER/REPO/main/install.sh)
```

**Windows (PowerShell):**
```
$env:DM_SERVER_URL = "https://192.168.1.20:8765"
$env:DM_TOKEN      = "the-same-secret-from-step-1"
$env:DM_CERT_URL   = "https://192.168.1.20:8765/cert"
$env:DM_AUTOSTART  = "1"
irm https://raw.githubusercontent.com/OWNER/REPO/main/install.ps1 | iex
```

That's it — the script detects the OS/CPU, downloads the matching
binary from your latest GitHub Release, writes `agent-config.json`,
fetches and pins the server's certificate, and (with `DM_AUTOSTART=1`)
installs itself as a systemd/launchd/Scheduled Task service so it
survives reboots. Run `bash install.sh --help` to see every option.

**Chromebooks (ChromeOS):** ChromeOS itself can't run native binaries,
but its built-in **Linux (Crostini)** container is just Debian under
the hood — turn it on in Settings > Advanced > Developer, open the
Linux terminal once, then run the exact same `install.sh` one-liner
from above inside it. The script auto-detects Crostini and picks
`agent-linux-amd64` (most Chromebooks) or `agent-linux-arm64`
automatically.

One real limitation worth knowing: the Linux container only starts
when you open a Linux app, not automatically when the Chromebook
boots — ChromeOS has no hook for that. With `DM_AUTOSTART=1` the
installer sets things up so the agent starts the moment the container
*does* come up (via systemd if your Crostini image has it, otherwise a
cron `@reboot` entry), and if you flip on **Settings > Linux > "Linux
apps run in the background"**, it'll then keep running even after you
close the terminal window. So it's "starts reliably once Linux has
been opened" rather than "always on from cold boot" — good enough for
a laptop that's usually awake, less so if you need it the instant the
Chromebook powers on.

Note: `DM_CERT_URL` works because `server.py` now serves the *public*
half of the certificate at `/cert` with no auth required — that's fine
to expose, since a certificate's public half isn't secret (the private
key in `key.pem` never leaves the server).

### If you'd rather not pipe curl into bash

Reasonable instinct. Download and read the script first:
```
curl -fsSL -o install.sh https://raw.githubusercontent.com/OWNER/REPO/main/install.sh
less install.sh          # read it
DM_SERVER_URL=... DM_TOKEN=... bash install.sh
```

## 5. Rolling out an update later

Bump the version, tag, push:
```
git tag v1.0.1 && git push origin v1.0.1
```
Then re-run the same one-liner on each device — it always grabs
`releases/latest`, so it'll pick up the new build.

---

## Does this work across different wifi networks?

Out of the box: **no** — `192.168.1.20` only means something on your
home LAN. Two ways to fix that:

### Option A — port forward + a free dynamic DNS hostname (recommended)

1. Sign up for a free dynamic DNS hostname, e.g. **DuckDNS**
   (duckdns.org) or **No-IP** — you'll get something like
   `yourname.duckdns.org`.
2. In your router's settings, forward external TCP port `8765` to the
   main device's local IP and port `8765`.
3. Many routers have a built-in DDNS client that keeps the hostname
   updated automatically.
4. Use `https://yourname.duckdns.org:8765` as `DM_SERVER_URL` on every
   device from now on.
5. Regenerate `cert.pem` with that hostname in the SAN instead of your
   LAN IP (step 2 above). Or, since you now have a real public
   hostname, get a free trusted cert from Let's Encrypt instead — then
   you can skip `DM_CERT_URL`/`server-cert.pem` entirely.

### Option B — a mesh VPN (Tailscale/ZeroTier), more setup, more private
