#!/bin/sh
set -eu

CADDY_VERSION="v2.10.2"
XCADDY_VERSION="v0.4.4"
FORWARDPROXY_COMMIT="d62c80d3dd2c706b6b87579844d2397bddd18317"

if [ "$#" -lt 3 ] || [ "$#" -gt 4 ]; then
    echo "usage: $0 OUTPUT GOOS GOARCH [GOARM]" >&2
    exit 2
fi

OUTPUT=$1
TARGET_GOOS=$2
TARGET_GOARCH=$3
TARGET_GOARM=${4:-}

find_xcaddy() {
    if command -v xcaddy >/dev/null 2>&1; then
        command -v xcaddy
        return 0
    fi

    gobin=$(go env GOBIN)
    if [ -z "$gobin" ]; then
        gobin=$(go env GOPATH)/bin
    fi
    if command -v cygpath >/dev/null 2>&1; then
        gobin=$(cygpath -u "$gobin")
    fi
    for candidate in "$gobin/xcaddy" "$gobin/xcaddy.exe"; do
        if [ -x "$candidate" ]; then
            printf '%s\n' "$candidate"
            return 0
        fi
    done
    return 1
}

XCADDY=$(find_xcaddy || true)
if [ -z "$XCADDY" ]; then
    unset GOOS GOARCH GOARM
    go install "github.com/caddyserver/xcaddy/cmd/xcaddy@${XCADDY_VERSION}"
    XCADDY=$(find_xcaddy || true)
fi
if [ -z "$XCADDY" ]; then
    echo "could not locate xcaddy after installing ${XCADDY_VERSION}" >&2
    exit 1
fi

workdir=$(mktemp -d)
trap 'rm -rf "$workdir"' EXIT INT TERM
source_dir="$workdir/forwardproxy"
archive="$workdir/forwardproxy.tar.gz"
mkdir -p "$source_dir"
curl -fsSL "https://github.com/klzgrad/forwardproxy/archive/${FORWARDPROXY_COMMIT}.tar.gz" \
    -o "$archive"
tar -xzf "$archive" --strip-components=1 -C "$source_dir"

mkdir -p "$(dirname "$OUTPUT")"
if [ -n "$TARGET_GOARM" ]; then
    GOOS="$TARGET_GOOS" GOARCH="$TARGET_GOARCH" GOARM="$TARGET_GOARM" CGO_ENABLED=0 \
        "$XCADDY" build "$CADDY_VERSION" \
        --with "github.com/caddyserver/forwardproxy=$source_dir" \
        --output "$OUTPUT"
else
    GOOS="$TARGET_GOOS" GOARCH="$TARGET_GOARCH" CGO_ENABLED=0 \
        "$XCADDY" build "$CADDY_VERSION" \
        --with "github.com/caddyserver/forwardproxy=$source_dir" \
        --output "$OUTPUT"
fi

test -s "$OUTPUT"
if ! grep -aFq 'forwardproxy.Handler' "$OUTPUT"; then
    echo "built Caddy does not contain forwardproxy.Handler" >&2
    exit 1
fi
chmod +x "$OUTPUT"
