# Общее для скриптов на компьютере. Перед подключением задать KIT — корень набора.

fail() { echo "ОШИБКА: $*" >&2; exit 1; }

JBL_HOST=${JBL_HOST:-jbl} # алиас колонки в ~/.ssh/config

# JBL_SSH_PORT — порт вместо того, что в ~/.ssh/config (ssh/ssh.sh upgrade при смене порта)
jssh() { ssh -o BatchMode=yes -o ConnectTimeout=5 ${JBL_SSH_PORT:+-p "$JBL_SSH_PORT"} "$JBL_HOST" "$@"; }

# ssh_port — порт из строки PORT= в service.sh на stdin; нет строки — ошибка
ssh_port() {
    p=$(sed -n 's/^PORT=\([0-9][0-9]*\).*/\1/p' | head -n 1)
    [ -n "$p" ] || fail 'в service.sh нет строки PORT='
    echo "$p"
}

# jadb — adb к колонке: JBL_ADB=IP:5555 (сетевой) или usb
jadb() {
    case "${JBL_ADB:-}" in
        '') fail 'не задан JBL_ADB (IP:5555 или usb)' ;;
        usb) adb -d "$@" ;;
        *) adb -s "$JBL_ADB" "$@" ;;
    esac
}

# on_device СКРИПТ [АРГУМЕНТЫ] — выполнить скрипт на колонке: через adb, если задан JBL_ADB, иначе ssh
on_device() {
    f=$1
    shift
    if [ -n "${JBL_ADB:-}" ]; then
        # adbd на колонке не передаёт stdin и код возврата: скрипт целиком в командной строке,
        # код — последней строкой JBL_RC=
        out=$(jadb shell "set -- $*; ( $(cat "$f")
); echo JBL_RC=\$?" | tr -d '\r') || true
        printf '%s\n' "$out" | grep -v '^JBL_RC=' || true
        rc=$(printf '%s\n' "$out" | sed -n 's/^JBL_RC=\([0-9][0-9]*\)$/\1/p' | tail -n 1)
        return "${rc:-1}"
    else
        jssh sh -s "$@" < "$f"
    fi
}

# Готовые бинарники — из последнего релиза на GitHub
JBL_RELEASES=${JBL_RELEASES:-https://github.com/madest92/jbl-link-portable-kit/releases/latest/download}

# fetch ИМЯ ФАЙЛ — скачать файл релиза
fetch() {
    echo "скачиваю $1…"
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL -o "$2.part" "$JBL_RELEASES/$1"
    else
        wget -q -O "$2.part" "$JBL_RELEASES/$1"
    fi || { rm -f "$2.part"; fail "не скачан $1 ($JBL_RELEASES/$1)"; }
    mv "$2.part" "$2"
}
