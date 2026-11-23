#!/bin/sh
# На компьютере. Найти колонку в локальной сети: у неё открыты порты 1961 и 8080. Сети — физических
# интерфейсов (Wi-Fi, Ethernet), докер и VPN пропускаются. Нужны только nc и xargs.
#   sh find.sh
# Адрес — на stdout. Не нашли или нашли несколько — ошибка: адрес тогда указывают вручную.
set -eu
KIT=$(cd "$(dirname "$0")" && pwd)
. "$KIT/lib/kit.sh"

PORTS='1961 8080' # открыты у колонки
EXTRA='22 5555'   # SSH и сетевой ADB — только для вывода
JOBS=256          # проверок одновременно
MIN_PREFIX=16     # сети шире урезаются до /16 вокруг адреса компьютера

# open АДРЕС ПОРТ — порт открыт; таймаут — JBL_FIND_WAIT секунд
open() { nc -z -w "${JBL_FIND_WAIT:-1}" "$1" "$2" >/dev/null 2>&1; }

# probe АДРЕС — если открыты все PORTS, строка «АДРЕС ПОРТЫ» в $JBL_FIND_TMP/found; вызывает xargs -P
probe() {
    for p in $PORTS; do
        open "$1" "$p" || return 0
    done
    found=$PORTS
    for p in $EXTRA; do
        if open "$1" "$p"; then found="$found $p"; fi
    done
    echo "$1 $found" >> "$JBL_FIND_TMP/found"
}

if [ "${1:-}" = probe ]; then
    probe "$2"
    exit 0
fi

# prefix_len МАСКА — длина префикса по маске вида 0xffffff00 (так её печатает macOS)
prefix_len() {
    m=$(($1))
    n=0
    b=$((0x80000000))
    while [ "$b" -gt 0 ] && [ $((m & b)) -ne 0 ]; do
        n=$((n + 1))
        b=$((b >> 1))
    done
    echo "$n"
}

# nets — «АДРЕС ДЛИНА» физических интерфейсов, без link-local
nets() {
    if [ "$(uname -s)" = Darwin ]; then
        for i in $(ifconfig -l); do
            case $i in en[0-9]*) ;; *) continue ;; esac
            ifconfig "$i" | sed -n 's/^[[:space:]]*inet \([0-9.]*\) netmask \(0x[0-9a-fA-F]*\).*/\1 \2/p' |
                while read -r a m; do echo "$a $(prefix_len "$m")"; done
        done
    else
        command -v ip >/dev/null 2>&1 || fail 'нужна команда ip'
        for d in /sys/class/net/*/device; do
            [ -e "$d" ] || continue
            i=${d#/sys/class/net/}
            i=${i%/device}
            ip -4 -o addr show dev "$i" scope global | sed -n 's/.* inet \([0-9.]*\)\/\([0-9]*\) .*/\1 \2/p'
        done
    fi | grep -v '^169\.254\.' || true
}

# hosts АДРЕС ДЛИНА — адреса сети, кроме своего
hosts() {
    awk -v ip="$1" -v len="$2" 'BEGIN {
        split(ip, o, ".")
        n = ((o[1] * 256 + o[2]) * 256 + o[3]) * 256 + o[4]
        size = 2 ^ (32 - len)
        base = int(n / size) * size
        for (h = base + 1; h < base + size - 1; h++) {
            if (h == n) continue
            printf "%d.%d.%d.%d\n", int(h / 16777216), int(h / 65536) % 256, int(h / 256) % 256, h % 256
        }
    }'
}

command -v nc >/dev/null 2>&1 || fail 'нужен nc'
command -v xargs >/dev/null 2>&1 || fail 'нужен xargs'

list=$(nets)
[ -n "$list" ] || fail 'не найдено ни одной локальной сети: компьютер не подключён к Wi-Fi или Ethernet?'

JBL_FIND_TMP=$(mktemp -d)
export JBL_FIND_TMP
trap 'rm -rf "$JBL_FIND_TMP"' EXIT

# Колонка по Wi-Fi отвечает на ARP не сразу: если никого нет, повторяем обход с большим таймаутом
for wait in 1 2 3; do
    JBL_FIND_WAIT=$wait
    export JBL_FIND_WAIT
    [ "$wait" = 1 ] || echo "никого не нашёл, повторяю (таймаут $wait с)…" >&2
    printf '%s\n' "$list" | while read -r a len; do
        if [ "$len" -lt "$MIN_PREFIX" ]; then
            echo "сеть $a/$len слишком большая: беру $a/$MIN_PREFIX" >&2
            len=$MIN_PREFIX
        fi
        [ "$len" -le 30 ] || continue
        echo "ищу колонку в сети $a/$len (порты $PORTS)…" >&2
        hosts "$a" "$len" | xargs -n 1 -P "$JOBS" sh "$0" probe
    done
    [ ! -s "$JBL_FIND_TMP/found" ] || break
done

[ -s "$JBL_FIND_TMP/found" ] || fail 'колонка не найдена: проверьте, что компьютер и колонка в одной сети'
list=$(sort -t. -k1,1n -k2,2n -k3,3n -k4,4n "$JBL_FIND_TMP/found")
printf '%s\n' "$list" | while read -r a p; do echo "$a — порты: $p" >&2; done
[ "$(printf '%s\n' "$list" | wc -l)" -eq 1 ] || fail 'найдено несколько колонок: укажите адрес нужной'
echo "${list%% *}"
