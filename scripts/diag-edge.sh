#!/bin/sh
# Collects everything needed to diagnose the nginx perimeter, the REALITY
# fallback and the firewall into one tarball. Secrets are redacted; the log is
# copied whole because it is the point of the exercise.
#
# Usage: sh scripts/diag-edge.sh [output-dir]
set -u

OUT=${1:-"./xui-diag-$(date -u +%Y%m%dT%H%M%SZ)"}
mkdir -p "$OUT" || exit 1

DB_FOLDER=${XUI_DB_FOLDER:-/etc/x-ui}
LOG_FOLDER=${XUI_LOG_FOLDER:-/var/log/x-ui}
EDGE_DIR="$DB_FOLDER/edge"

grab() {
	name=$1
	shift
	{
		echo "### $name"
		echo "### $*"
		echo
		"$@" 2>&1
		echo
	} >"$OUT/$name.txt"
}

# Redaction: anything that could grant access or impersonate the panel.
redact() {
	sed -E \
		-e 's/("?(secret|apiToken|tgBotToken|twoFactorToken|ldapPassword|smtpPassword|discordBotToken|nodeMtlsCaKeyPem|nodeMtlsClientKeyPem)"?[[:space:]]*[:=][[:space:]]*")[^"]*/\1<redacted>/gi' \
		-e 's/(PRIVATE KEY-----)[A-Za-z0-9+\/=]*/\1<redacted>/g' \
		-e 's/(password|passwd|token)=[^[:space:]&]*/\1=<redacted>/gi'
}

{
	echo "xui perimeter diagnostics"
	echo "collected: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
	echo "host: $(uname -a 2>/dev/null)"
	echo
} >"$OUT/00-summary.txt"

grab 01-version sh -c 'x-ui -v 2>/dev/null || ./x-ui -v 2>/dev/null || echo "x-ui binary not on PATH"'
grab 02-env env | grep -E '^XUI_' | redact
grab 03-nginx-version nginx -V
grab 04-nginx-test sh -c 'nginx -t -c "'"$EDGE_DIR"'/nginx.conf" 2>&1; echo "exit=$?"'
grab 05-nginx-processes ps aux | grep -E '[n]ginx'
grab 06-edge-state ls -la "$EDGE_DIR"
grab 07-edge-config cat "$EDGE_DIR/nginx.conf"
grab 08-ss sh -c 'ss -ltnp 2>/dev/null || netstat -ltnp 2>/dev/null'
grab 09-nft sh -c 'nft list ruleset 2>/dev/null | head -200'
grab 10-nft-xui sh -c 'nft list table inet xui 2>&1'
grab 11-xray-config sh -c 'ls -la '"$DB_FOLDER"'/bin 2>/dev/null; echo; x-ui setting -show true 2>/dev/null | redact || echo "x-ui setting unavailable"'
grab 12-log-tail tail -n 4000 "$LOG_FOLDER/3xui.log"

# Cross-checks the operator cannot easily do by hand: is the REALITY dest the
# same port the perimeter listens on, and does anything answer there.
EDGE_PORT=$(grep -E '^[[:space:]]*listen ' "$EDGE_DIR/nginx.conf" 2>/dev/null | head -1 | sed -E 's/.*listen ([^ ;]+).*/\1/')
if [ -n "${EDGE_PORT:-}" ]; then
	grab 13-perimeter-dial sh -c "curl -sS -o /dev/null -w 'http=%{http_code} connect=%{time_connect}\n' -k --max-time 5 https://$EDGE_PORT/ 2>&1; echo exit=\$?"
fi

DOMAIN=$(grep -E '^[[:space:]]*server_name ' "$EDGE_DIR/nginx.conf" 2>/dev/null | head -1 | awk '{print $2}')
if [ -n "${DOMAIN:-}" ]; then
	grab 14-public-tls sh -c "curl -sS -o /dev/null -w 'root http=%{http_code} tls=%{ssl_verify_result}\n' -k --max-time 8 https://$DOMAIN/ 2>&1; echo exit=\$?"
	grab 15-public-404 sh -c "curl -sS -o /dev/null -w 'unknown http=%{http_code}\n' -k --max-time 8 https://$DOMAIN/wp-login.php 2>&1; echo exit=\$?"
	grab 16-public-panel sh -c "curl -sS -o /dev/null -w 'panel http=%{http_code}\n' -k --max-time 8 https://$DOMAIN/\$(x-ui setting -getWebBasePath 2>/dev/null || echo panel/) 2>&1; echo exit=\$?"
	grab 17-openssl sh -c "echo | openssl s_client -connect $DOMAIN:443 -servername $DOMAIN -alpn h2 2>&1 | grep -E 'subject=|issuer=|ALPN|Protocol|Verify' | head -20"
fi

tar -czf "$OUT.tar.gz" -C "$(dirname "$OUT")" "$(basename "$OUT")" 2>/dev/null
echo "wrote $OUT.tar.gz"
echo "review it before sharing: log lines may contain client emails and IPs."
