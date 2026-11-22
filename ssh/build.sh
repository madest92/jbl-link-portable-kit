#!/bin/sh
# Сборка Dropbear для колонки (ARMv7, статически, musl): build/dropbear, build/dropbearkey.
# Нужен zig 0.13+ или Docker. Версия — DROPBEAR_VERSION (matt.ucc.asn.au/dropbear/releases).
set -eu
DROPBEAR_VERSION=2026.94
here=$(cd "$(dirname "$0")" && pwd)
cd "$here"
mkdir -p build

# сборка в build/src через zig cc
build() {
    src=build/src/dropbear-$DROPBEAR_VERSION
    if [ ! -d "$src" ]; then
        mkdir -p build/src
        tgz=build/src/dropbear-$DROPBEAR_VERSION.tar.bz2
        curl -fsSL -o "$tgz" "https://matt.ucc.asn.au/dropbear/releases/dropbear-$DROPBEAR_VERSION.tar.bz2"
        tar -xjf "$tgz" -C build/src
    fi
    cd "$src"
    t='-target arm-linux-musleabihf -mcpu=cortex_a7'
    [ -f Makefile ] || CC="zig cc $t" AR='zig ar' RANLIB='zig ranlib' CFLAGS='-Os' LDFLAGS='-s' \
        ./configure --host=arm-linux-musleabihf --enable-static --disable-zlib \
        --disable-lastlog --disable-utmp --disable-utmpx --disable-wtmp --disable-wtmpx \
        --disable-pututline --disable-pututxline >/dev/null
    make -j"$(nproc)" PROGRAMS='dropbear dropbearkey' >/dev/null
    cp dropbear dropbearkey ../../
}

if command -v zig >/dev/null 2>&1; then
    (build)
elif command -v docker >/dev/null 2>&1; then
    docker run --rm -v "$here:/src" -w /src alpine:3.22 sh -c \
        "apk add --no-cache zig make bzip2 curl >/dev/null && sh build.sh && chown -R $(id -u):$(id -g) build"
    exit
else
    echo 'ОШИБКА: нужен zig или Docker' >&2
    exit 1
fi
ls -l build/dropbear build/dropbearkey | awk '{ printf "%-20s %.1f МБ\n", $NF, $5 / 1048576 }'
