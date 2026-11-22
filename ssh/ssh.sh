#!/bin/sh
# На компьютере: SSH по ключу на колонке.
#   sh ssh/ssh.sh install IP            через ADB: JBL_ADB=IP:5555 (по умолчанию) или usb
#   sh ssh/ssh.sh upgrade [--rollback]  другая версия поверх работающего SSH: Dropbear из ssh/build
#                                       (sh ssh/build.sh), иначе из релиза, и service.sh набора;
#                                       --rollback — прежние (*.old)
# install: JBL_KEY — ключ клиента (по умолчанию ~/.ssh/jbl_link_portable, создаётся); Host jbl — в
# ~/.ssh/config, если его нет; уже установлен — на колонке ничего не меняется.
# upgrade: на время замены открыт сетевой ADB — запасной вход; не поднялся SSH — прежняя версия
# возвращается через ADB.
set -eu
KIT=$(cd "$(dirname "$0")/.." && pwd)
. "$KIT/lib/kit.sh"
B=$KIT/ssh/build
SVC=$KIT/ssh/service.sh
DEV_ADB=$KIT/adb/device.sh
S=/data/jbl-ssh
CFG=$HOME/.ssh/config

# ssh_up [ПОПЫТОК] — SSH отвечает (по 3 с между попытками)
ssh_up() {
    i=0
    until [ "$(jssh id -u 2>/dev/null)" = 0 ]; do
        i=$((i + 1))
        [ $i -lt "${1:-10}" ] || return 1
        sleep 3
    done
}

# есть ли строка Host с этим именем — сравнение слов, не регулярное выражение
has_host() {
    [ -f "$CFG" ] || return 1
    awk -v h="$JBL_HOST" 'tolower($1) == "host" { for (i = 2; i <= NF; i++) if (tolower($i) == tolower(h)) f = 1 }
        END { exit !f }' "$CFG"
}

install() {
    ip=${1:?использование: ssh.sh install IP_КОЛОНКИ}
    JBL_ADB=${JBL_ADB:-$ip:5555}
    key=${JBL_KEY:-$HOME/.ssh/jbl_link_portable}
    stage=/tmp/jbl-ssh-install
    for c in adb ssh ssh-keygen; do
        command -v "$c" >/dev/null 2>&1 || fail "нужен $c"
    done
    mkdir -p "$HOME/.ssh"
    chmod 700 "$HOME/.ssh"
    if [ ! -f "$key" ]; then
        ssh-keygen -q -t ed25519 -N '' -C jbl-link-portable -f "$key"
        echo "создан ключ $key"
    fi
    [ -s "$key.pub" ] || fail "нет $key.pub"
    for f in dropbear dropbearkey; do
        [ -s "$B/$f" ] || { mkdir -p "$B"; fetch "$f" "$B/$f"; }
    done

    [ "$JBL_ADB" = usb ] || adb connect "$JBL_ADB" >/dev/null 2>&1 || true
    [ "$(jadb shell id -u 2>/dev/null | tr -d '\r')" = 0 ] || fail "нет root ADB ($JBL_ADB): см. adb/adb.sh"

    echo 'копирую файлы на колонку…'
    jadb shell "rm -rf $stage && mkdir -m 700 $stage" >/dev/null
    for f in "$B/dropbear" "$B/dropbearkey" "$SVC"; do
        jadb push "$f" "$stage/" >/dev/null
    done
    jadb push "$key.pub" "$stage/authorized_keys" >/dev/null
    out=$(jadb shell "sh $stage/service.sh install $stage; rm -rf $stage" | tr -d '\r')
    printf '%s\n' "$out" | grep -v '^JBL_' || true
    case "$out" in *JBL_SSH_INSTALLED* | *JBL_SSH_ALREADY*) ;; *) fail 'установка не прошла' ;; esac

    port=$(ssh_port < "$SVC")
    if has_host; then
        echo "в ~/.ssh/config уже есть Host $JBL_HOST — не меняю"
        cfg=$(ssh -G "$JBL_HOST" | awk '/^port / { print $2; exit }')
        [ "$cfg" = "$port" ] ||
            echo "ВНИМАНИЕ: у Host $JBL_HOST — Port $cfg, SSH колонки — на порту $port: исправьте ~/.ssh/config"
    else
        printf '\nHost %s\n    HostName %s\n    Port %s\n    User root\n    IdentityFile "%s"\n    IdentitiesOnly yes\n    StrictHostKeyChecking no\n    UserKnownHostsFile /dev/null\n    LogLevel ERROR\n' \
            "$JBL_HOST" "$ip" "$port" "$key" >> "$CFG"
        chmod 600 "$CFG"
        echo "в ~/.ssh/config добавлен Host $JBL_HOST"
    fi
    ssh_up 5 || fail "SSH не отвечает: ssh $JBL_HOST (ключ $key есть на колонке? Host $JBL_HOST в ~/.ssh/config)"
    echo "SSH работает: ssh $JBL_HOST"
}

upgrade() {
    new=/tmp/jbl-ssh-new
    JBL_ADB=$(ssh -G "$JBL_HOST" | awk '/^hostname / { print $2; exit }'):5555
    command -v adb >/dev/null 2>&1 || fail 'нужен adb'
    [ "$(jssh id -u 2>/dev/null)" = 0 ] || fail "нет ssh $JBL_HOST"
    jssh "[ -x $S/bin/dropbear ]" || fail 'SSH на колонке не установлен — sh ssh/ssh.sh install IP'
    version() { jssh "$1 -V 2>&1 | head -n 1"; }
    # sums ФАЙЛ… — sha256 файлов колонки подряд
    sums() { jssh "cat $* | sha256sum" | cut -d' ' -f1; }
    net_off() { jssh sh -s network-off < "$DEV_ADB"; }

    old_port=$(jssh "cat $S/service.sh" | ssh_port)
    if [ "${1:-}" = --rollback ]; then
        jssh "[ -f $S/bin/dropbear.old ]" || fail 'прежней версии нет'
        svc=$S/service.sh
        if jssh "[ -f $S/service.sh.old ]"; then svc=$S/service.sh.old; fi
        want=$(sums $S/bin/dropbear.old $svc)
        new_port=$(jssh "cat $svc" | ssh_port)
        echo "сейчас: $(version $S/bin/dropbear); будет: $(version $S/bin/dropbear.old)"
        set -- rollback
    else
        [ $# = 0 ] || fail 'использование: ssh.sh upgrade [--rollback]'
        [ -s "$B/dropbear" ] || { mkdir -p "$B"; fetch dropbear "$B/dropbear"; }
        jssh "rm -rf $new && mkdir -m 700 $new"
        jssh "cat > $new/dropbear && chmod 755 $new/dropbear" < "$B/dropbear"
        jssh "cat > $new/service.sh" < "$SVC"
        want=$(sums $new/dropbear $new/service.sh)
        new_port=$(ssh_port < "$SVC")
        echo "сейчас: $(version $S/bin/dropbear); будет: $(version $new/dropbear)"
        set -- swap "$new"
    fi
    [ "$new_port" = "$old_port" ] || echo "порт SSH: $old_port → $new_port"

    echo 'запасной вход: сетевой ADB…'
    jssh sh -s network-on < "$DEV_ADB"
    adb disconnect "$JBL_ADB" >/dev/null 2>&1 || true # прежнее подключение могло остаться мёртвым
    adb connect "$JBL_ADB" >/dev/null 2>&1 || true
    if [ "$(jadb shell id -u 2>/dev/null | tr -d '\r')" != 0 ]; then
        jssh "rm -rf $new"
        net_off
        fail "нет root по ADB ($JBL_ADB) — замена отменена"
    fi
    echo "ADB: root через $JBL_ADB"

    echo 'замена и перезапуск службы…'
    out=$(jssh sh -s "$@" < "$SVC" 2>&1) || true
    jssh "rm -rf $new"
    printf '%s\n' "$out" | grep -v '^JBL_' || true
    case "$out" in
        *JBL_SSH_SWAPPED*) ;;
        *) net_off; fail 'замена не выполнена — работает прежняя версия' ;;
    esac
    jssh sh -s restart < "$SVC"
    sleep 5
    JBL_SSH_PORT=$new_port
    if ssh_up && [ "$(sums $S/bin/dropbear $S/service.sh)" = "$want" ]; then
        net_off
        adb disconnect "$JBL_ADB" >/dev/null 2>&1 || true
        echo "готово: $(version $S/bin/dropbear) на порту $new_port; прежняя — bin/dropbear.old"
        cfg=$(ssh -G "$JBL_HOST" | awk '/^port / { print $2; exit }')
        [ "$cfg" = "$new_port" ] ||
            echo "ВНИМАНИЕ: в ~/.ssh/config у Host $JBL_HOST — Port $cfg; замените на Port $new_port"
        return
    fi

    echo 'SSH не поднялся или файлы не те — возвращаю прежнюю версию через ADB'
    if [ "$1" = rollback ]; then back=rollback; else back=restore; fi
    # service.sh набора — файлом: целиком в командной строке adb он не поместится
    jadb push "$SVC" /tmp/jbl-ssh-service.sh >/dev/null &&
        jadb shell "sh /tmp/jbl-ssh-service.sh $back && sh /tmp/jbl-ssh-service.sh restart; rm -f /tmp/jbl-ssh-service.sh" || true
    sleep 5
    # shellcheck disable=SC2034 # читает jssh из lib/kit.sh
    JBL_SSH_PORT=$old_port
    ssh_up || fail "SSH нет; вход — adb -s $JBL_ADB shell"
    net_off
    fail 'новая версия не заработала — прежняя возвращена'
}

case "${1:-}" in
    install | upgrade) cmd=$1; shift; "$cmd" "$@" ;;
    *) fail 'использование: ssh.sh install IP | upgrade [--rollback]' ;;
esac
