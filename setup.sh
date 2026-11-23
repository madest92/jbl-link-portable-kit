#!/bin/sh
# Готовая колонка одной командой: root ADB → SSH → блокировка обновлений → автозапуск →
# сетевой ADB закрыт. Дальше — только ssh (алиас jbl в ~/.ssh/config). Сделанное пропускается.
#   sh setup.sh [IP]
# Адрес не указан — ищется в сети (find.sh): не нашлась или нашлось несколько колонок — ошибка.
set -eu
KIT=$(cd "$(dirname "$0")" && pwd)
. "$KIT/lib/kit.sh"
IP=${1:-}

for c in adb ssh ssh-keygen; do
    command -v "$c" >/dev/null 2>&1 || fail "нужен $c"
done

[ -n "$IP" ] || { echo 'адрес колонки не указан, ищу в сети…'; IP=$(sh "$KIT/find.sh") || exit 1; echo "колонка: $IP"; }
ADB=$IP:5555

step() { printf '\n== %s\n' "$*"; }
ssh_ok() { [ "$(jssh id -u 2>/dev/null)" = 0 ]; }
adb_ok() {
    adb connect "$ADB" >/dev/null 2>&1 || return 1
    [ "$(adb -s "$ADB" shell id -u 2>/dev/null | tr -d '\r')" = 0 ]
}

step '1/5 Root ADB'
if ssh_ok; then
    echo "ssh $JBL_HOST уже работает — ADB не нужен"
elif adb_ok; then
    echo "сетевой ADB уже работает: $ADB"
else
    sh "$KIT/adb/adb.sh" enable "$IP"
fi

step '2/5 SSH'
if ssh_ok; then
    echo "ssh $JBL_HOST уже работает"
else
    JBL_ADB=$ADB sh "$KIT/ssh/ssh.sh" install "$IP"
fi

step '3/5 Блокировка обновлений'
sh "$KIT/ota/ota.sh" enable

step '4/5 Автозапуск'
log=$(mktemp)
trap 'rm -f "$log"' EXIT
# не через tee: код возврата — от установщика, не от tee
sh "$KIT/autostart/autostart.sh" install > "$log" ||
    { cat "$log"; fail 'автозапуск не установлен; сетевой ADB не закрывался (шаг 5)'; }
cat "$log"
fresh=
grep -q '^установлен: ' "$log" && fresh=1

step '5/5 Закрыть сетевой ADB'
sh "$KIT/adb/adb.sh" network-off

if [ -n "$fresh" ]; then
    step 'Перезагрузка: автозапуск и SSH после загрузки'
    boot=$(jssh cat /proc/sys/kernel/random/boot_id)
    echo 'Перезагрузите колонку кнопкой питания. Жду SSH после загрузки (до 4 мин)…'
    i=0
    until [ "$(jssh cat /proc/sys/kernel/random/boot_id 2>/dev/null || true)" != "$boot" ] && ssh_ok; do
        i=$((i + 1))
        [ "$i" -lt 48 ] || fail "после перезагрузки нет ssh $JBL_HOST"
        sleep 5
    done
fi

step 'Проверка'
sh "$KIT/autostart/autostart.sh" status > "$log" ||
    { cat "$log"; fail 'состояние автозапуска не получено'; }
cat "$log"
grep -q '^в этой загрузке: хуки выполнены' "$log" || fail 'автозапуск не сработал в этой загрузке'
sh "$KIT/ota/ota.sh" status
sh "$KIT/adb/adb.sh" status
echo
echo "Готово: ssh $JBL_HOST. Помощник Алисы: EXA_KEY_FILE=… sh assist/deploy.sh"
