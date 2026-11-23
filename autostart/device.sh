#!/bin/sh
# На колонке, от root: автозапуск через штатный Fluent Bit. Ставится в /data/jbl-boot/device.sh.
#   install КАТАЛОГ  поставить из КАТАЛОГА (device.sh, flb-in_jbl_boot.so) и включить; есть SSH набора —
#                    хук hooks/10-ssh.sh
#   status           установлен ли, включён ли, что было в этой загрузке
#   on | off         добавить или убрать строку @INCLUDE в device.cfg; файлы остаются
#   boot             вызывает плагин: один раз за загрузку — хуки отдельной службой jbl-boot, сразу выход
#   hooks            выполнить hooks/*.sh по имени; сбой хука не останавливает следующие
# Fluent Bit (режим logs.cfg) читает device.cfg раньше своей секции [SERVICE], поэтому наша
# [SERVICE] — копия штатной с Plugins_File: Fluent Bit берёт первую. Fluent Bit перезапускается при
# смене режима логов — хуки только при первом старте за загрузку (/run чистится перезагрузкой).
set -eu
D=/data/jbl-boot
NEW=$D.new
CFG=/data/quasar/fluent-bit/device.cfg
LOGS=/system/vendor/quasar/fluent-bit/logs.cfg
LINK=/data/quasar/fluent-bit/fb.cfg
LINE="@INCLUDE /data/jbl-boot/fluent.conf"
OWNER=jbl-link-portable-kit
ONCE=/run/jbl-boot.once
STATUS=/run/jbl-boot.status
LOG=/run/jbl-boot.log

fail() { echo "ОШИБКА: $*" >&2; exit 1; }
[ "$(id -u)" = 0 ] || fail 'нужен root'

ours() { [ -d "$D" ] && [ ! -L "$D" ] && [ "$(cat "$D/owner" 2>/dev/null)" = "$OWNER" ]; }
enabled() { grep -Fxq "$LINE" "$CFG"; }

# device.cfg: обычный файл root, без чужих @INCLUDE
check_cfg() {
    { [ -f "$CFG" ] && [ ! -L "$CFG" ]; } || fail "нет $CFG"
    [ "$(stat -c %u "$CFG")" = 0 ] || fail "$CFG не root"
    other=$(grep '^@INCLUDE' "$CFG" | grep -Fvx "$LINE" || true)
    [ -z "$other" ] || fail "в device.cfg чужое подключение: $other — уберите его откатом того, кто добавил"
}

# set_line on|off — переписать device.cfg с нашей строкой или без неё (+i — от перезаписи).
# Проверки явные: вызов из «a || set_line» выключает set -e внутри функции.
set_line() {
    check_cfg
    t=$CFG.jbl-boot
    if [ "$1" = on ]; then
        { cat "$CFG" && { [ -z "$(tail -c 1 "$CFG")" ] || echo; } && echo "$LINE"; } > "$t" ||
            { rm -f "$t"; fail "не прочитан $CFG"; }
    else
        grep -Fvx "$LINE" "$CFG" > "$t"
        [ -s "$t" ] || { rm -f "$t"; fail "device.cfg без нашей строки пуст — не трогаю"; }
    fi
    chattr -i "$CFG" || { rm -f "$t"; fail "не снять +i с $CFG"; }
    { cat "$t" > "$CFG" && rm -f "$t"; } || { rm -f "$t"; fail "не записан $CFG"; }
    if [ "$1" = on ]; then
        chattr +i "$CFG" || fail "не поставить +i на $CFG"
    fi
    sync
    if [ "$1" = on ]; then enabled || fail 'строка не записалась'; else ! enabled || fail 'строка осталась'; fi
}

install() {
    S=${1:?использование: device.sh install КАТАЛОГ}
    [ "$(uname -m)" = armv7l ] || fail 'нужен ARMv7'
    fw=$(cat "/system/vendor/quasar/version_full")
    case "$fw" in
        1.80.0.228.*) ;;
        *) [ "${JBL_ALLOW_FIRMWARE:-}" = 1 ] || fail "непроверенная прошивка $fw (JBL_ALLOW_FIRMWARE=1 — на свой риск)" ;;
    esac
    check_cfg
    if ours; then
        if enabled; then echo 'автозапуск уже установлен — ничего не меняю'; echo JBL_BOOT_ALREADY; return; fi
        echo 'автозапуск был выключен — включаю'
        set_line on
        echo JBL_BOOT_ON
        return
    fi
    { [ ! -e "$D" ] && [ ! -L "$D" ]; } || fail "$D есть, но не наш"

    # штатный logs.cfg: device.cfg подключён до [SERVICE], своих плагинов нет
    inc=$(grep -n '^@INCLUDE /data/quasar/fluent-bit/device.cfg' "$LOGS" | cut -d: -f1 | head -n 1)
    svc=$(grep -n '^\[SERVICE\]' "$LOGS" | cut -d: -f1 | head -n 1)
    { [ -n "$inc" ] && [ -n "$svc" ] && [ "$inc" -lt "$svc" ]; } || fail 'logs.cfg не такой, как ожидалось'
    ! grep -q 'Plugins_File' "$LOGS" || fail 'в logs.cfg уже есть Plugins_File'
    for f in device.sh flb-in_jbl_boot.so; do
        { [ -f "$S/$f" ] && [ ! -L "$S/$f" ]; } || fail "нет файла $f"
    done
    sh -n "$S/device.sh"

    # прерванная установка
    if [ -e "$NEW" ] || [ -L "$NEW" ]; then
        { [ -d "$NEW" ] && [ ! -L "$NEW" ]; } || fail "не каталог: $NEW"
        chattr -R -i "$NEW" 2>/dev/null || true
        rm -rf "$NEW"
    fi
    umask 077
    mkdir "$NEW" "$NEW/hooks"
    cp "$S/device.sh" "$S/flb-in_jbl_boot.so" "$NEW/"
    if [ -f "/data/jbl-ssh/service.sh" ]; then
        printf '#!/bin/sh\n# Managed by %s\nexec /bin/sh /data/jbl-ssh/service.sh start\n' "$OWNER" > "$NEW/hooks/10-ssh.sh"
    fi
    cp "$CFG" "$NEW/device.cfg.orig"
    echo "$OWNER" > "$NEW/owner"
    printf '[PLUGINS]\n    Path /data/jbl-boot/flb-in_jbl_boot.so\n' > "$NEW/plugins.conf"
    {
        echo '[SERVICE]'
        awk '/^\[/ { in_svc = ($0 ~ /^\[SERVICE\][ \t]*$/); next } in_svc && NF' "$LOGS"
        echo '    Plugins_File /data/jbl-boot/plugins.conf'
        echo
        echo '[INPUT]'
        echo '    Name jbl_boot'
    } > "$NEW/fluent.conf"
    [ "$(grep -c . "$NEW/fluent.conf")" -gt 4 ] || fail 'не нашлась секция [SERVICE] в logs.cfg'
    chmod 700 "$NEW" "$NEW/hooks" "$NEW/device.sh"
    [ ! -f "$NEW/hooks/10-ssh.sh" ] || chmod 700 "$NEW/hooks/10-ssh.sh"
    chmod 600 "$NEW/flb-in_jbl_boot.so" "$NEW/device.cfg.orig" "$NEW/owner" "$NEW/plugins.conf" "$NEW/fluent.conf"
    chown -R 0:0 "$NEW"
    sync
    # каталог встаёт на место целиком; +i — сразу затем (с +i не переименовать)
    mv "$NEW" "$D"
    chattr -R +i "$D"
    sync
    set_line on
    echo "установлен: $D; хуки — $D/hooks"
    echo JBL_BOOT_INSTALLED
}

status() {
    if ! ours; then
        if [ -e "$D" ]; then echo "$D есть, но не наш"; else echo 'не установлен'; fi
        return
    fi
    if enabled; then echo "установлен и включён: $D"; else echo "установлен, выключен: $D (включить — on)"; fi
    echo "хуки: $(for h in "$D"/hooks/*.sh; do if [ -e "$h" ]; then printf '%s ' "${h##*/}"; fi; done)"
    case "$(readlink "$LINK" 2>/dev/null)" in
        *logs.cfg) echo 'режим Fluent Bit: logs.cfg — плагин грузится' ;;
        *) echo "режим Fluent Bit: $(readlink "$LINK" 2>/dev/null || echo '?') — плагин не грузится" ;;
    esac
    if [ -f "$STATUS" ]; then
        echo "в этой загрузке: $(cat "$STATUS")"
    elif [ -e "$ONCE" ]; then
        echo 'в этой загрузке: хуки ещё выполняются'
    else
        echo 'в этой загрузке: не запускался'
    fi
}

say() { echo "$(date '+%F %T') $*"; }

# unsafe ФАЙЛ — ссылка, не root:root или запись группе/остальным
unsafe() {
    [ -L "$1" ] || [ "$(stat -c %u:%g "$1")" != 0:0 ] && return 0
    case "$(stat -c %A "$1")" in
        ?????w* | ????????w*) return 0 ;;
        *) return 1 ;;
    esac
}

boot() {
    ours || exit 1
    (set -C; : > "$ONCE") 2>/dev/null || exit 0
    say 'Fluent Bit загрузил плагин'
    systemd-run --unit=jbl-boot --description='jbl-link-portable-kit boot hooks' \
        -p RuntimeMaxSec=120 --no-block /bin/sh "$D/device.sh" hooks && exit 0
    say 'systemd-run не сработал — хуки здесь'
    exec /bin/sh "$D/device.sh" hooks
}

hooks() {
    exec >> "$LOG" 2>&1
    if unsafe "$D/hooks"; then
        say "небезопасные права: $D/hooks — хуки не запущены"
        echo 'хуки не запущены: небезопасные права каталога' > "$STATUS"
        exit 1
    fi
    done_n=0 failed=
    for h in "$D"/hooks/*.sh; do
        [ -e "$h" ] || [ -L "$h" ] || continue
        n=${h##*/}
        if unsafe "$h" || [ ! -f "$h" ]; then
            say "$n: небезопасные права — пропущен"
            failed="$failed $n"
            continue
        fi
        say "$n: старт"
        if /bin/sh "$h" < /dev/null; then
            say "$n: готово"
            done_n=$((done_n + 1))
        else
            say "$n: ошибка $?"
            failed="$failed $n"
        fi
    done
    echo "хуки выполнены: $done_n${failed:+, с ошибкой:$failed}" > "$STATUS"
    [ -z "$failed" ]
}

case "${1:-status}" in
    install) shift; install "$@" ;;
    status) status ;;
    on) ours || fail 'автозапуск не установлен'; enabled || set_line on; echo 'автозапуск включён' ;;
    off) ! enabled || set_line off; echo 'автозапуск выключен: device.cfg без нашей строки' ;;
    boot) boot ;;
    hooks) hooks ;;
    *) fail 'использование: device.sh install КАТАЛОГ | status | on | off | boot | hooks' ;;
esac
