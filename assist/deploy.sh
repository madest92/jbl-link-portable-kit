#!/bin/sh
# Установка на колонку: sh assist/deploy.sh [--build | --download]
# Бинарник: есть Go — сборка, иначе — из последнего релиза; --build / --download — явно.
# Ключ Exa: EXA_API_KEY=… или EXA_KEY_FILE=файл; не задан — ключ на колонке не трогается.
# Файлы — через stdin ssh: scp на колонке нет, ключ не попадает в командную строку.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
KIT=${here%/*}
. "$KIT/lib/kit.sh"
D=/data/jbl-assist

key=${EXA_API_KEY:-}
if [ -z "$key" ] && [ -n "${EXA_KEY_FILE:-}" ]; then
    key=$(head -n 1 "$EXA_KEY_FILE" | tr -d '\r\n ')
fi

case "${1:-}" in
    --build) build=1 ;;
    --download) build= ;;
    '') build=; command -v go >/dev/null 2>&1 && build=1 ;;
    *) fail 'использование: deploy.sh [--build | --download]' ;;
esac
if [ -n "$build" ]; then
    sh "$here/build.sh"
else
    mkdir -p "$here/build"
    fetch jbl-assist.gz "$here/build/jbl-assist.gz"
fi

jssh "[ ! -f $D/service.sh ] || sh $D/service.sh disable"
jssh "mkdir -p $D && cat > $D/jbl-assist.gz.new && mv $D/jbl-assist.gz.new $D/jbl-assist.gz" \
    < "$here/build/jbl-assist.gz"
jssh "cat > $D/service.sh.new && mv $D/service.sh.new $D/service.sh" < "$here/service.sh"
if [ -n "$key" ]; then
    printf '%s\n' "$key" | jssh "umask 077; f=$D/exa.key;
        [ ! -e \$f ] || chattr -i \$f; cat > \$f.new && mv \$f.new \$f"
    echo "ключ exa записан"
fi
jssh "sh $D/service.sh enable"
