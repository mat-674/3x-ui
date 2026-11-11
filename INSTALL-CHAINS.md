# Installing the client-side proxy chains build

This branch carries an installer for the `feat/sub-client-proxy-chains` build —
the one with **Settings → Subscription → Chain Proxies**. It is the upstream
`install.sh` with one step added, so everything else (SSL, PostgreSQL, panel
port, unattended mode) behaves exactly as documented.

The official release still supplies `bin/` (xray, mtg-multi, geoip/geosite),
the systemd units and the `/usr/bin/x-ui` CLI. Only `/usr/local/x-ui/x-ui` is
replaced by the chain build.

## Quick start

```bash
bash <(curl -Ls https://raw.githubusercontent.com/mat-674/3x-ui/installer/chains/install.sh)
```

The script looks for the panel binary in this order:

1. `XUI_CHAIN_BINARY` — a file already on the host.
2. `XUI_CHAIN_BIN_URL` — a URL to download it from.
3. The release asset `x-ui-linux-<arch>` under the `chain-build` tag.
4. A Docker build from `feat/sub-client-proxy-chains`.

If none of those work, it stops **before** touching the existing install rather
than leaving a half-installed panel.

## Sources you can pick from

**Docker build** (no extra steps, needs Docker on the host; takes a few minutes):

```bash
bash install.sh
```

**Your own binary** (scp it over first):

```bash
XUI_CHAIN_BINARY=/root/x-ui-linux-amd64 bash install.sh
```

**A published build:**

```bash
XUI_CHAIN_BIN_URL=https://example.com/x-ui-linux-amd64 bash install.sh
```

## Publishing a build so the plain command works

Upload the binary as a release asset on this fork, tagged `chain-build`:

```bash
# amd64, built from the chain branch
cp x-ui-linux-amd64 /tmp/
# then: GitHub → Releases → Draft a new release → tag "chain-build" →
#       attach x-ui-linux-amd64 (and x-ui-linux-arm64, x-ui-linux-386)
```

After that `bash install.sh` finds it at source 3 and needs no Docker.

Expected asset names, one per architecture:

```
x-ui-linux-amd64
x-ui-linux-arm64
x-ui-linux-armv7
x-ui-linux-armv6
x-ui-linux-386
```

A matching `x-ui-linux-<arch>.sha256` is verified when present; a missing
sidecar is tolerated with a warning.

## Knobs

| Variable | Default | Purpose |
|---|---|---|
| `XUI_CHAIN_BINARY` | – | Path to a local panel binary |
| `XUI_CHAIN_BIN_URL` | – | URL to download the panel binary from |
| `XUI_CHAIN_REPO` | `mat-674/3x-ui` | Repository the release/Docker sources come from |
| `XUI_CHAIN_REF` | `feat/sub-client-proxy-chains` | Branch or tag the Docker build uses |
| `XUI_CHAIN_TAG` | `chain-build` | Release tag holding the prebuilt assets |

## After installing

`/usr/local/x-ui/x-ui -v` should report a version whose commit is the chain
build. Then:

```bash
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:2053/   # 200
```

Log in, open **Settings → Subscription → Chain Proxies**, and the two tabs
(Proxies / Selectors) are there. The per-inbound control is **Client chain** on
the inbound's **Basic** tab.

Note that an inbound assigned a chain is intentionally left out of raw/Base64
links and the legacy Clash-for-Windows output — neither format can carry an
upstream hop, and exporting a direct connection would silently bypass the chain.
JSON and Mihomo subscriptions emit it properly.
