#!/bin/sh
# Сборка плагина: build/flb-in_jbl_boot.so (без неё autostart.sh скачает его из релиза).
# Нужны clang и ld.lld с целью ARM, иначе — Docker (alpine с clang и lld).
set -eu
here=$(cd "$(dirname "$0")" && pwd)
cd "$here"
mkdir -p build
# без libc и sysroot: неразрешённые символы берутся из процесса Fluent Bit
cmd='clang --target=armv7-linux-gnueabihf -mfpu=vfpv3-d16 -mfloat-abi=hard -fPIC -O2 -Wall -Werror \
    -ffreestanding -fno-stack-protector -c plugin.c -o build/plugin.o &&
ld.lld -shared -m armelf_linux_eabi --hash-style=both build/plugin.o -o build/flb-in_jbl_boot.so'
if command -v clang >/dev/null 2>&1 && command -v ld.lld >/dev/null 2>&1; then
    sh -c "$cmd"
elif command -v docker >/dev/null 2>&1; then
    docker run --rm -v "$here:/src" -w /src alpine:3.20 sh -c \
        "apk add --no-cache clang lld >/dev/null && $cmd && chown $(id -u):$(id -g) build build/*"
else
    echo 'ОШИБКА: нужны clang и ld.lld или Docker' >&2
    exit 1
fi
rm -f build/plugin.o
sha256sum build/flb-in_jbl_boot.so
