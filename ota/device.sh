#!/bin/sh
# На колонке, от root: запрет записи в каталог загрузки обновлений (/bkupgrade, атрибут +i).
#   enable | status | disable
# Блокирует только новые файлы в корне каталога; другие способы обновления не закрывает.
set -eu
DIR=/bkupgrade

fail() { echo "ОШИБКА: $*" >&2; exit 1; }
[ "$(id -u)" = 0 ] || fail 'нужен root'
[ -d "$DIR" ] && [ ! -L "$DIR" ] || fail "нет каталога $DIR"
grep -Eq '"updatesDir"[[:space:]]*:[[:space:]]*"/bkupgrade"' /system/vendor/quasar/quasar.cfg ||
    fail 'обновления качаются в другой каталог'

no_writer() {
    ! pidof a01localupdate updater_switcher >/dev/null 2>&1 || fail 'идёт установка обновления'
}

# в корне — только штатный wiimu, скачанных образов нет
no_downloads() {
    for e in "$DIR"/* "$DIR"/.[!.]* "$DIR"/..?*; do
        [ -e "$e" ] || [ -L "$e" ] || continue
        [ "$e" = "$DIR/wiimu" ] && [ -d "$e" ] && [ ! -L "$e" ] || fail "лишнее в $DIR: $e — проверьте вручную"
    done
    ! find "$DIR" -maxdepth 3 -type f | grep -Eiq '\.(img|zip|swu)$' || fail "в $DIR уже скачан образ обновления"
}

# можно ли создать файл в каталоге (пробный удаляется)
writable() {
    p="$DIR/.jbl-ota-probe-$$"
    [ ! -e "$p" ] && [ ! -L "$p" ] || fail "занято имя $p"
    if (set -C; : > "$p") 2>/dev/null; then
        rm -f "$p"
        return 0
    fi
    return 1
}

case "${1:-status}" in
    enable)
        no_writer
        no_downloads
        chattr +i "$DIR"
        sync
        ! writable || fail "$DIR всё ещё открыт для записи"
        echo "обновления заблокированы: в $DIR нельзя писать"
        ;;
    status)
        if writable; then
            echo "обновления не заблокированы: $DIR открыт для записи"
            exit 2
        fi
        echo "обновления заблокированы: в $DIR нельзя писать"
        ;;
    disable)
        no_writer
        chattr -i "$DIR"
        sync
        writable || fail "$DIR всё ещё закрыт"
        echo "блокировка снята: $DIR открыт для записи"
        ;;
    *) fail 'использование: device.sh enable|status|disable' ;;
esac
