#!/bin/sh
# На колонке, от root: SSH по ключу — Dropbear из /data/jbl-ssh на порту PORT.
#   install КАТАЛОГ   первая установка из КАТАЛОГА (dropbear, dropbearkey, service.sh, authorized_keys)
#   start | stop | status
#   swap КАТАЛОГ      замена: рабочие bin/dropbear и service.sh → *.old, из КАТАЛОГА — рабочие
#   rollback          рабочие ⇄ *.old
#   restore           *.old → рабочие (после неудачной замены)
#   restart           перезапуск службы через 2 с — сеанс ssh, из которого вызван, внутри jbl-ssh
# Unit — в /run (корень только для чтения), его пересоздаёт start. Каталог — root, go-rwx, +i:
# при загрузке droproot_setup.sh открывает /data группе users.
set -eu
umask 077
STATE=/data/jbl-ssh
PORT=22 # ssh/ssh.sh читает отсюда
UNIT=/run/systemd/system/jbl-ssh.service
AUTH=/run/jbl-ssh-auth
MARK='# Managed by jbl-link-portable-kit'
FILES='bin/dropbear service.sh' # то, что меняет swap

fail() { echo "ОШИБКА: $*" >&2; exit 1; }
[ "$(id -u)" = 0 ] || fail 'нужен root'

# путь — не ссылка, root:root и права $2
secure() {
    [ ! -L "$1" ] || fail "символическая ссылка: $1"
    [ "$(stat -c '%u:%g:%a' "$1")" = "0:0:$2" ] || fail "не root:root $2: $1"
}

install() {
    stage=${1:?использование: service.sh install КАТАЛОГ}
    tmp=$STATE.new
    [ "$(uname -m)" = armv7l ] || fail 'нужен ARMv7'
    fw=$(cat /system/vendor/quasar/version_full)
    case "$fw" in
        1.80.0.228.*) ;;
        *) [ "${JBL_ALLOW_FIRMWARE:-}" = 1 ] || fail "непроверенная прошивка $fw (JBL_ALLOW_FIRMWARE=1 — на свой риск)" ;;
    esac
    if [ -e "$STATE" ] || [ -L "$STATE" ]; then
        echo "SSH уже установлен в $STATE — ничего не меняю"
        echo JBL_SSH_ALREADY
        return
    fi
    for f in dropbear dropbearkey service.sh authorized_keys; do
        { [ -f "$stage/$f" ] && [ ! -L "$stage/$f" ]; } || fail "нет файла $f"
    done
    grep -q '^ssh-ed25519 ' "$stage/authorized_keys" || fail 'нужен открытый ключ ed25519'
    sh -n "$stage/service.sh"
    # остаток прерванной установки
    if [ -e "$tmp" ] || [ -L "$tmp" ]; then
        { [ -d "$tmp" ] && [ ! -L "$tmp" ]; } || fail "не каталог: $tmp"
        { chattr -R -i "$tmp" && rm -rf "$tmp"; } || fail "не удалось убрать $tmp"
    fi
    # сборка в $tmp: рабочий каталог появляется только целиком
    mkdir "$tmp" "$tmp/bin" "$tmp/auth"
    cp "$stage/dropbear" "$tmp/bin/dropbear"
    cp "$stage/authorized_keys" "$tmp/auth/authorized_keys"
    cp "$stage/service.sh" "$tmp/service.sh"
    chmod 755 "$tmp/bin/dropbear" "$tmp/service.sh" "$stage/dropbearkey"
    # ключ сервера — свой у каждой колонки
    out=$("$stage/dropbearkey" -t ed25519 -f "$tmp/host_ed25519" 2>&1) || fail "dropbearkey: $out"
    [ -s "$tmp/host_ed25519" ] || fail 'dropbearkey не создал ключ сервера'
    chmod 600 "$tmp/host_ed25519" "$tmp/auth/authorized_keys"
    chown -R 0:0 "$tmp"
    sync
    # каталог с +i не переименовать: сначала на место, сразу затем +i
    mv "$tmp" "$STATE"
    chattr -R +i "$STATE"
    sync
    start
    echo JBL_SSH_INSTALLED
}

start() {
    secure "$STATE" 700
    secure "$STATE/auth" 700
    secure "$STATE/auth/authorized_keys" 600
    secure "$STATE/host_ed25519" 600
    secure "$STATE/bin" 700
    secure "$STATE/bin/dropbear" 755
    [ -s "$STATE/auth/authorized_keys" ] || fail 'нет ключей клиентов'
    [ -s "$STATE/host_ed25519" ] || fail 'нет ключа сервера'
    # Dropbear не принимает ключи под /data (system:users 775 у родителя) — копия в /run
    [ ! -L "$AUTH" ] || fail "символическая ссылка: $AUTH"
    mkdir -p "$AUTH"
    secure "$AUTH" 700
    [ ! -L "$AUTH/authorized_keys" ] || fail "символическая ссылка: $AUTH/authorized_keys"
    cp "$STATE/auth/authorized_keys" "$AUTH/authorized_keys"
    chmod 600 "$AUTH/authorized_keys"
    if [ -e "$UNIT" ] || [ -L "$UNIT" ]; then
        { [ ! -L "$UNIT" ] && grep -Fxq "$MARK" "$UNIT"; } || fail "$UNIT создан не этим скриптом"
    fi
    mkdir -p "${UNIT%/*}"
    cat > "$UNIT" <<UNIT
$MARK
[Unit]
Description=JBL Link Portable key-only SSH
After=network.target droproot_setup.service

[Service]
Type=simple
ExecStart=/bin/sh -c 'exec $STATE/bin/dropbear -F -s -D $AUTH -r $STATE/host_ed25519 -p $PORT -P /run/jbl-ssh.pid -K 60 2>>/run/jbl-ssh.log'
WorkingDirectory=$STATE
UMask=0077
Restart=on-failure
RestartSec=2
UNIT
    systemctl daemon-reload
    systemctl start jbl-ssh.service
    systemctl is-active --quiet jbl-ssh.service || fail 'служба не запустилась (/run/jbl-ssh.log)'
    echo "SSH запущен на порту $PORT"
}

# unlock / lock — снять и вернуть +i для замены; lock убирает *.new, но не *.swap (рабочий файл,
# застрявший посреди rollback). Ошибка lock — сообщение и код 1.
unlock() {
    chattr -i "$STATE" "$STATE/bin" || return 1
    for f in $FILES; do
        for g in "$STATE/$f" "$STATE/$f.old" "$STATE/$f.new" "$STATE/$f.swap"; do
            [ ! -e "$g" ] || chattr -i "$g" || return 1
        done
    done
}
lock() {
    rc=0
    for f in $FILES; do
        rm -f "$STATE/$f.new"
        for g in "$STATE/$f" "$STATE/$f.old" "$STATE/$f.swap"; do
            [ ! -e "$g" ] || { chown 0:0 "$g" && chattr +i "$g"; } || rc=1
        done
    done
    chattr +i "$STATE/bin" "$STATE" || rc=1
    sync
    [ "$rc" = 0 ] || echo "ОШИБКА: не удалось вернуть +i в $STATE — до перезагрузки: chattr -R +i $STATE" >&2
    return "$rc"
}
# done_swap КОД СООБЩЕНИЕ — закрыть каталог; КОД не 0 — ошибка
done_swap() {
    lock || exit 1
    [ "$1" = 0 ] || fail "$2"
    echo JBL_SSH_SWAPPED
}

swap() {
    new=${1:?использование: service.sh swap КАТАЛОГ}
    for f in dropbear service.sh; do
        { [ -f "$new/$f" ] && [ ! -L "$new/$f" ]; } || fail "нет файла $new/$f"
    done
    sh -n "$new/service.sh" || fail 'service.sh с ошибкой синтаксиса'
    [ -x "$STATE/bin/dropbear" ] || fail 'нет рабочего bin/dropbear'
    rc=0
    # сначала копии (место на /data кончается здесь), потом переименования
    unlock &&
        cp "$new/dropbear" "$STATE/bin/dropbear.new" && cp "$new/service.sh" "$STATE/service.sh.new" &&
        chmod 755 "$STATE/bin/dropbear.new" "$STATE/service.sh.new" &&
        mv -f "$STATE/bin/dropbear" "$STATE/bin/dropbear.old" && mv -f "$STATE/bin/dropbear.new" "$STATE/bin/dropbear" &&
        mv -f "$STATE/service.sh" "$STATE/service.sh.old" && mv -f "$STATE/service.sh.new" "$STATE/service.sh" || rc=1
    done_swap "$rc" 'замена не выполнена'
}

# back rollback|restore
back() {
    [ -f "$STATE/bin/dropbear.old" ] || fail 'прежней версии нет'
    rc=0
    unlock || rc=1
    for f in $FILES; do
        { [ "$rc" = 0 ] && [ -f "$STATE/$f.old" ]; } || continue
        if [ "$1" = rollback ]; then
            mv -f "$STATE/$f" "$STATE/$f.swap" && mv -f "$STATE/$f.old" "$STATE/$f" &&
                mv -f "$STATE/$f.swap" "$STATE/$f.old" || rc=1
        else
            mv -f "$STATE/$f.old" "$STATE/$f" || rc=1
        fi
    done
    done_swap "$rc" 'прежняя версия не возвращена'
}

case "${1:-status}" in
    install) shift; install "$@" ;;
    start) start ;;
    status) systemctl is-active jbl-ssh.service ;;
    stop)
        { [ -f "$UNIT" ] && grep -Fxq "$MARK" "$UNIT"; } || fail 'служба установлена не этим скриптом'
        systemctl stop jbl-ssh.service
        echo 'SSH остановлен'
        ;;
    swap) shift; swap "$@" ;;
    rollback | restore) back "$1" ;;
    restart)
        # unit пересоздаёт start рабочего service.sh: в нём порт и путь
        systemd-run /bin/sh -c "sleep 2; systemctl stop jbl-ssh.service; exec /bin/sh $STATE/service.sh start" >/dev/null 2>&1 ||
            fail 'systemd-run не запустил перезапуск'
        ;;
    *) fail 'использование: service.sh install КАТАЛОГ | start | stop | status | swap КАТАЛОГ | rollback | restore | restart' ;;
esac
