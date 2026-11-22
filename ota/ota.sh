#!/bin/sh
# На компьютере: блокировка обновлений прошивки. sh ota/ota.sh enable|status|disable
# Через ssh; до установки SSH — JBL_ADB=IP:5555 (или usb).
set -eu
KIT=$(cd "$(dirname "$0")/.." && pwd)
. "$KIT/lib/kit.sh"
case "${1:-status}" in
    enable | status | disable) on_device "$KIT/ota/device.sh" "${1:-status}" ;;
    *) fail 'использование: ota.sh enable|status|disable' ;;
esac
