#!/bin/sh
# jbl-assist на колонке: служба jbl-assist и хук автозапуска.
#   enable | disable       хук автозапуска + служба
#   start | stop | status  служба
#   unpack                 бинарник из /data (gzip) → /tmp, перед запуском службы
#   debug [on|off]         подробный журнал
# После enable каталог immutable: правка — disable, правка, enable.
DIR=/data/jbl-assist
RUN=/tmp/jbl-assist
BIN=$RUN/jbl-assist
UNIT=jbl-assist
UNIT_FILE=/run/systemd/system/$UNIT.service
HOOKS=/data/jbl-boot/hooks
HOOK=$HOOKS/20-assist.sh
MARK='# Managed by jbl-assist'

fail() { echo "ERROR: $*" >&2; exit 1; }
root() { [ "$(id -u)" = 0 ] || fail 'нужен root'; }

# файл $1 — не наш (нет метки) — не трогать
ours() { [ ! -e "$1" ] && [ ! -L "$1" ] || { [ ! -L "$1" ] && grep -Fxq "$MARK" "$1"; }; }

case "${1:-}" in
    unpack)
        # /tmp чистится при перезагрузке; каталог — только root
        mkdir -p -m 700 "$RUN" || exit 1
        [ "$(stat -c %u:%a "$RUN")" = 0:700 ] || fail "$RUN: не root 700"
        gunzip -c "$DIR/jbl-assist.gz" > "$BIN.new" && chmod 700 "$BIN.new" && mv -f "$BIN.new" "$BIN" ||
            { rm -f "$BIN.new"; fail 'не удалось распаковать'; } ;;
    start)
        systemctl is-active --quiet "$UNIT" && { echo "$UNIT уже запущена"; exit 0; }
        ours "$UNIT_FILE" || fail "$UNIT_FILE создан не этим скриптом"
        mkdir -p "${UNIT_FILE%/*}"
        # unit в /run: systemd-run в systemd 234 не знает RestartSec
        cat > "$UNIT_FILE" <<UNIT
$MARK
[Unit]
Description=JBL Link Portable assistant
After=network.target droproot_setup.service

[Service]
Type=simple
ExecStartPre=/bin/sh $DIR/service.sh unpack
ExecStart=$BIN run
Restart=always
RestartSec=3
UNIT
        systemctl daemon-reload && systemctl start "$UNIT" ;;
    stop) systemctl stop "$UNIT" ;;
    status) systemctl is-active "$UNIT" ;;
    enable)
        root
        [ -d "$HOOKS" ] || fail "нет $HOOKS: сначала установите автозапуск: sh autostart/autostart.sh install"
        ours "$HOOK" || fail "$HOOK создан не этим скриптом"
        # droproot_setup.sh при загрузке открывает /data группе users: каталог — root, go-rwx, +i
        chattr -R -i "$DIR" && chown -R 0:0 "$DIR" && chmod -R go-rwx "$DIR" &&
            chattr -R +i "$DIR" || fail "не удалось закрыть $DIR"
        chattr -i "$HOOKS" || fail "не удалось снять +i с $HOOKS"
        [ ! -e "$HOOK" ] || chattr -i "$HOOK"
        # сбой хука автозапуск (device.sh hooks) запишет в журнал и итог загрузки, остальные хуки выполнит всё равно
        printf '#!/bin/sh\n%s\nexec /bin/sh %s start\n' "$MARK" "$DIR/service.sh" > "$HOOK" &&
            chmod 700 "$HOOK"
        chattr +i "$HOOK" "$HOOKS" || fail "не удалось вернуть +i в $HOOKS"
        /bin/sh "$DIR/service.sh" start ;;
    disable)
        root
        if [ -f "$HOOK" ] && grep -Fxq "$MARK" "$HOOK"; then
            chattr -i "$HOOKS" "$HOOK" && rm -f "$HOOK" && chattr +i "$HOOKS" || fail "не удалось убрать $HOOK"
        fi
        systemctl stop "$UNIT" 2>/dev/null
        if [ -f "$UNIT_FILE" ] && grep -Fxq "$MARK" "$UNIT_FILE"; then
            rm -f "$UNIT_FILE" && systemctl daemon-reload
        fi
        # каталог — для правки; ключ остаётся +i, иначе droproot_setup.sh откроет его группе
        chattr -R -i "$DIR"
        for k in "$DIR"/*.key; do [ ! -f "$k" ] || chattr +i "$k"; done
        echo "jbl-assist выключен" ;;
    debug)
        case "${2:-}" in
            '') [ -e "$DIR/debug" ] && echo on || echo off; exit 0 ;;
            on|off) ;;
            *) fail 'Использование: service.sh debug [on|off]' ;;
        esac
        root
        chattr -i "$DIR" || fail "не удалось снять +i с $DIR"
        if [ "$2" = on ]; then
            : > "$DIR/debug" && chmod 600 "$DIR/debug"
        else
            [ ! -e "$DIR/debug" ] || chattr -i "$DIR/debug"
            rm -f "$DIR/debug"
        fi
        if [ -f "$HOOK" ]; then
            [ ! -e "$DIR/debug" ] || chattr +i "$DIR/debug"
            chattr +i "$DIR" || fail "не удалось вернуть +i на $DIR"
        fi
        [ -e "$DIR/debug" ] && echo on || echo off ;;
    *) fail 'Использование: service.sh enable|disable|start|stop|status|unpack|debug [on|off]' ;;
esac
