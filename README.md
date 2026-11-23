# jbl-link-portable-kit

<!-- JBL Link Portable (Yandex Alice): root, SSH, OTA block, autostart and AI answers via Exa — no firmware flashing. Kit for rooting the JBL Link Portable smart speaker with Yandex Alice (Яндекс Алиса): root ADB over USB, Dropbear SSH with key auth, blocking firmware OTA updates, boot hooks via a Fluent Bit plugin, and an AI voice assistant that answers questions Alice cannot, using Exa web search. Documentation is in Russian. -->

Набор для JBL Link Portable с Алисой: root, блокировка обновлений прошивки, SSH по ключу, автозапуск своих скриптов и помощник Алисы с ответами нейросети.

**Проверено на:** JBL Link Portable с Алисой, прошивка `1.80.0.228.3676569159.20251219` (`version_full` — `…20251219.29.10.0`), ARMv7, Yocto, systemd 234. Скрипты принимают прошивки `1.80.0.228.*`; другие — только с явным `JBL_ALLOW_FIRMWARE=1` (для автозапуска — `--allow-firmware`).

Корень (только для чтения) и разделы не меняются: всё своё — в `/data`, у каждой части есть откат. Скрипты отказываются работать поверх чужих или неожиданных файлов.

## Что понадобится

- Колонка, настроенная в приложении и подключённая к Wi-Fi.
- Кабель USB-C с передачей данных (задний разъём колонки).
- Компьютер с Linux или macOS: `adb`, OpenSSH, `curl` или `wget`.

## Установка

Два шага, команды — из корня набора.

### 1. Колонка с SSH

```sh
sh setup.sh
```

Адрес колонки скрипт находит сам: обходит локальную сеть компьютера и ищет устройство с открытыми портами 1961 и 8080. Не нашёл или нашёл несколько колонок — остановится с ошибкой; тогда укажите адрес из списка клиентов роутера: `sh setup.sh IP`. Только найти адрес — `sh find.sh`.

Дальше скрипт ведёт по шагам и пропускает уже сделанное:

1. **Root ADB.** Попросит выключить колонку кнопкой питания и включить, когда появится «жду USB»: в первые секунды загрузки USB ADB открыт с root. Создаст маркеры ADB, защитит их от удаления (`+i`) и после перезагрузки (попросит нажать кнопку) дождётся ADB по сети.
2. **SSH.** Dropbear 2026.94 только по ключу на порту 22, в `/data/jbl-ssh`, со своим ключом сервера. Ключ клиента — `~/.ssh/jbl_link_portable` (создаётся; свой — `JBL_KEY=путь`). В `~/.ssh/config` добавляется `Host jbl`, если его нет. Парольный вход выключен.
3. **Блокировка обновлений.** Обновление прошивки заменит корень и может закрыть доступ: в каталог загрузки `/bkupgrade` запрещается писать (если там нет скачанных образов и не идёт установка). Это закрывает известный каталог, а не все способы обновления.
4. **Автозапуск.** В `/data/quasar/fluent-bit/device.cfg` добавляется строка `@INCLUDE`: штатный Fluent Bit при загрузке подгружает наш плагин, тот запускает `/data/jbl-boot/hooks/*.sh` — среди них запуск SSH. Копия `device.cfg` до правки сохраняется в `/data/jbl-boot`.
5. **Сетевой ADB закрывается** — это root без пароля для всей сети. USB ADB при включении остаётся: им можно вернуть доступ.

Затем попросит перезагрузить колонку и проверит, что SSH и автозапуск поднялись сами. Дальше — только `ssh jbl`.

На macOS разрешите USB-аксессуар, если появится запрос; если окно USB пропущено — выключите колонку и запустите `setup.sh` снова. При нескольких устройствах ADB отключите остальные.

### 2. Помощник Алисы

```sh
EXA_KEY_FILE=~/exa.txt sh assist/deploy.sh
```

«Алиса, спроси у нейросети …», «загугли …» и её ответы «не знаю» → ответ [Exa](https://exa.ai) её голосом, с уточнениями. Подробно — [assist/README.md](assist/README.md).

## Состав

| Папка | Что | Где на колонке |
| --- | --- | --- |
| [adb/](adb/) | маркеры ADB: `adb.sh enable IP`, `status`, `network-on`, `network-off` | `/data/quasar_activate_adb`, `/data/network_adb` |
| [ssh/](ssh/) | Dropbear и служба: `ssh.sh install IP` (через ADB), `ssh.sh upgrade` | `/data/jbl-ssh`, служба `jbl-ssh` |
| [ota/](ota/) | блокировка обновлений: `ota.sh enable`, `status`, `disable` | `+i` на `/bkupgrade` |
| [autostart/](autostart/) | автозапуск: `autostart.sh install`, `status`, `on`, `off`; свои скрипты — в `hooks` | `/data/jbl-boot` |
| [assist/](assist/) | помощник Алисы: `deploy.sh` | `/data/jbl-assist`, служба `jbl-assist` |
| [lib/](lib/) | общее для скриптов на компьютере | — |

Части можно запускать по отдельности — `setup.sh` вызывает их по очереди. Скрипты на колонке (`device*.sh`, `service.sh`) копируют и запускают скрипты на компьютере. Доступ к колонке — `ssh jbl` (другой алиас — `JBL_HOST=…`); `ota.sh` и `adb.sh status` до установки SSH — через ADB: `JBL_ADB=IP:5555`.

Свой скрипт при загрузке — `/data/jbl-boot/hooks/NN-имя.sh` (root, права 700): короткий, долгое — отдельной службой. Хуки выполняются по имени один раз за загрузку, на все — 2 минуты; сбой одного не останавливает следующие, итог — `sh autostart/autostart.sh status`, журнал — `/run/jbl-boot.log`. Каталог защищён `+i`: `chattr -i` перед правкой, `chattr +i` после.

> [!NOTE]
> Автозапуск работает, пока Fluent Bit загружается со штатным `logs.cfg`. В режиме `idle.cfg` плагин не грузится; не отключайте `fluent-bitd`, если нужен автозапуск.

## Готовые файлы и сборка

Бинарники в репозитории не хранятся: Dropbear, плагин автозапуска и помощника собирает CI ([ci.yml](.github/workflows/ci.yml)), а `setup.sh` и `assist/deploy.sh` скачивают их из [последнего релиза](https://github.com/madest92/jbl-link-portable-kit/releases/latest). Собрать самому — файлы окажутся в `*/build/` и будут взяты вместо скачанных:

| Что | Команда | Нужно |
| --- | --- | --- |
| Dropbear | `sh ssh/build.sh` | zig или Docker |
| Помощник | `sh assist/build.sh` | Go 1.25+ |
| Плагин автозапуска | `sh autostart/build.sh` | clang и ld.lld или Docker |

Свежий Dropbear — новая `DROPBEAR_VERSION` в `ssh/build.sh` ([список версий](https://matt.ucc.asn.au/dropbear/releases/)). Обновить на колонке, где SSH уже стоит, — `sh ssh/ssh.sh upgrade`: на время замены включается сетевой ADB (запасной вход), заменяются Dropbear и `service.sh` (прежние — в `*.old`), служба перезапускается; не поднялся SSH или файлы не совпали по sha256 — прежние возвращаются через ADB. Вернуть прежнюю версию вручную — `sh ssh/ssh.sh upgrade --rollback`.

CI на каждый push и pull request проверяет скрипты (shellcheck), гоняет тесты установщика автозапуска и помощника, и только если всё зелёное — собирает. Релиз — тег `v*`: `git tag v1.0 && git push --tags`.

## Состояние

```sh
sh adb/adb.sh status                   # маркеры ADB, порт 5555
sh ota/ota.sh status                   # заблокирован ли /bkupgrade
ssh jbl 'sh /data/jbl-ssh/service.sh status'
sh autostart/autostart.sh status
ssh jbl 'sh /data/jbl-assist/service.sh status'
```

## Откат

| Что | Команда |
| --- | --- |
| Помощник | `ssh jbl 'sh /data/jbl-assist/service.sh disable'` |
| Автозапуск | `sh autostart/autostart.sh off` — уберёт нашу строку из `device.cfg`, файлы останутся (`on` — вернуть); совсем — затем `ssh jbl 'chattr -R -i /data/jbl-boot; rm -rf /data/jbl-boot'` |
| SSH | `ssh jbl 'sh /data/jbl-ssh/service.sh stop'` — до следующей загрузки; совсем — откат автозапуска и `chattr -R -i /data/jbl-ssh; rm -rf /data/jbl-ssh` |
| Блокировка обновлений | `sh ota/ota.sh disable` |
| ADB | маркер USB — `chattr -i /data/quasar_activate_adb; rm /data/quasar_activate_adb` |

## Если не получилось

| Симптом | Что проверить |
| --- | --- |
| Компьютер не видит колонку по USB | кабель с данными, запрос macOS, включение после «жду USB» |
| Нет ADB по сети после перезагрузки | та же сеть, `adb connect IP:5555`; маркеры — `JBL_ADB=usb sh adb/adb.sh status` |
| SSH просит пароль или отвергает ключ | `Host jbl` в `~/.ssh/config`: `HostName`, порт 22, `IdentityFile` — ключ, что на колонке |
| SSH после закрытия сетевого ADB пропал | USB ADB в окне при включении: `JBL_ADB=usb sh ssh/ssh.sh install IP` |
| SSH работает вручную, но не после перезагрузки | `sh autostart/autostart.sh status`, `/run/jbl-boot.log`, режим `logs.cfg` |
| «уже установлен» | ничего не заменяется намеренно: проверьте имеющуюся установку |

Не запускать на колонке `amixer` (любой): ломает захват микрофонов до перезагрузки.

## Лицензия

MIT, см. [LICENSE](LICENSE). Dropbear — своя лицензия (MIT-подобная), исходники — [matt.ucc.asn.au/dropbear](https://matt.ucc.asn.au/dropbear/dropbear.html).
