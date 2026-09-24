#!/bin/sh
set -e
TARGET_ARCH=${1:-amd64}
TARGET_VARIANT=${2:-}
case "$TARGET_ARCH" in
    amd64)
        ARCH="64"
        FNAME="amd64"
        CADDY_GOARCH="amd64"
        CADDY_GOARM=""
        ;;
    386 | i386)
        ARCH="32"
        FNAME="i386"
        CADDY_GOARCH="386"
        CADDY_GOARM=""
        ;;
    arm64 | aarch64)
        ARCH="arm64-v8a"
        FNAME="arm64"
        CADDY_GOARCH="arm64"
        CADDY_GOARM=""
        ;;
    arm)
        case "$TARGET_VARIANT" in
            v6)
                ARCH="arm32-v6"
                FNAME="armv6"
                CADDY_GOARM="6"
                ;;
            v7 | "")
                ARCH="arm32-v7a"
                FNAME="arm32"
                CADDY_GOARM="7"
                ;;
            *)
                echo "DockerInit: unsupported ARM variant: $TARGET_VARIANT" >&2
                exit 1
                ;;
        esac
        CADDY_GOARCH="arm"
        ;;
    armv8 | armv7 | armv6)
        case "$TARGET_ARCH" in
            armv8)
                ARCH="arm64-v8a"
                FNAME="arm64"
                CADDY_GOARCH="arm64"
                CADDY_GOARM=""
                ;;
            armv7)
                ARCH="arm32-v7a"
                FNAME="arm32"
                CADDY_GOARCH="arm"
                CADDY_GOARM="7"
                ;;
            armv6)
                ARCH="arm32-v6"
                FNAME="armv6"
                CADDY_GOARCH="arm"
                CADDY_GOARM="6"
                ;;
        esac
        ;;
    *)
        echo "DockerInit: unsupported architecture: $TARGET_ARCH" >&2
        exit 1
        ;;
esac
ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
BIN_DIR="$ROOT_DIR/build/bin"
mkdir -p "$BIN_DIR"
"$ROOT_DIR/tools/build-naive-caddy.sh" "$BIN_DIR/caddy-linux-$CADDY_GOARCH" linux "$CADDY_GOARCH" "$CADDY_GOARM"
cd "$BIN_DIR"
MTG_MULTI_VER=$(curl -sfL "https://api.github.com/repos/mhsanaei/mtg-multi/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1)
if [ -z "$MTG_MULTI_VER" ]; then
    echo "DockerInit: could not resolve the latest mtg-multi release tag" >&2
    exit 1
fi
curl -sfLRO "https://github.com/XTLS/Xray-core/releases/download/v26.9.9/Xray-linux-${ARCH}.zip"
unzip "Xray-linux-${ARCH}.zip"
rm -f "Xray-linux-${ARCH}.zip" geoip.dat geosite.dat
mv xray "xray-linux-${FNAME}"
# mtg-multi (MTProto sidecar) ships prebuilt release binaries for every target
# we package, so download and unpack the matching one instead of compiling.
case $FNAME in
    i386) MTGARCH="386" ;;
    arm32) MTGARCH="armv7" ;;
    *) MTGARCH="$FNAME" ;;
esac
MTG_PKG="mtg-multi-${MTG_MULTI_VER#v}-linux-${MTGARCH}"
curl -sfLRO "https://github.com/mhsanaei/mtg-multi/releases/download/${MTG_MULTI_VER}/${MTG_PKG}.tar.gz"
tar -xzf "${MTG_PKG}.tar.gz"
mv "${MTG_PKG}/mtg-multi" "mtg-linux-${FNAME}"
rm -rf "${MTG_PKG}" "${MTG_PKG}.tar.gz"
chmod +x "mtg-linux-${FNAME}"
case $FNAME in
    amd64)
        curl -sfLRo "tuic-server" "https://github.com/EAimTY/tuic/releases/download/tuic-server-1.0.0/tuic-server-1.0.0-x86_64-unknown-linux-musl"
        ;;
    arm64)
        curl -sfLRo "tuic-server" "https://github.com/EAimTY/tuic/releases/download/tuic-server-1.0.0/tuic-server-1.0.0-aarch64-unknown-linux-musl"
        ;;
    arm32)
        curl -sfLRo "tuic-server" "https://github.com/EAimTY/tuic/releases/download/tuic-server-1.0.0/tuic-server-1.0.0-armv7-unknown-linux-musleabihf"
        ;;
    i386)
        curl -sfLRo "tuic-server" "https://github.com/EAimTY/tuic/releases/download/tuic-server-1.0.0/tuic-server-1.0.0-i686-unknown-linux-musl"
        ;;
esac
if [ -f "tuic-server" ]; then
    if [ ! -s "tuic-server" ]; then
        echo "DockerInit: tuic-server download was empty" >&2
        exit 1
    fi
    chmod +x "tuic-server"
fi
curl -sfLRO https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geoip.dat
curl -sfLRO https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geosite.dat
curl -sfLRo geoip_IR.dat https://github.com/chocolate4u/Iran-v2ray-rules/releases/latest/download/geoip.dat
curl -sfLRo geosite_IR.dat https://github.com/chocolate4u/Iran-v2ray-rules/releases/latest/download/geosite.dat
curl -sfLRo geoip_RU.dat https://github.com/runetfreedom/russia-v2ray-rules-dat/releases/latest/download/geoip.dat
curl -sfLRo geosite_RU.dat https://github.com/runetfreedom/russia-v2ray-rules-dat/releases/latest/download/geosite.dat
cd ../../
