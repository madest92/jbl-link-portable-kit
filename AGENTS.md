# AGENTS.md

Памятка для тех, кто дорабатывает набор, — людей и ИИ-агентов. Что набор делает и как его ставить — [README.md](README.md); помощник Алисы — [assist/README.md](assist/README.md). Здесь — как всё устроено, где что лежит и на чём уже обжигались.

Проверено на JBL Link Portable с Алисой, прошивка `1.80.0.228.3676569159.20251219.29.10.0`.

## Сначала прочитать: что ломает колонку

| Нельзя | Что будет | Как быть |
| --- | --- | --- |
| `amixer` — любой, даже `amixer sget` | захват микрофонов падает: в `maind.log` — `SNDRV_PCM_IOCTL_SYNC_PTR failed`, Алиса не слышит «Алиса» | только перезагрузка; громкость своих звуков — в самом файле |
| `aplay -L` | под подозрением: был в той же сессии, что и первый `amixer` | не запускать |
| `pkill -f строка` через `ssh jbl '…'` | убивает и саму ssh-команду (строка — в её командной строке) | убивать по PID |
| `grep -r` по `/tmp` или `/data` | виснет на FIFO | `find … -type f` и `grep` по файлам |
| писать в каталоги с `+i` | `Permission denied` | `chattr -i`, правка, `chattr +i` |
| `sh -x assist/deploy.sh`, `set -x` рядом с ключом | ключ Exa попадёт в вывод | ключ — только через stdin; попал в вывод — перевыпустить |
| менять рабочий SSH без запасного входа | сетевой ADB закрыт — вернуть доступ можно только окном USB при включении | только `sh ssh/ssh.sh upgrade` |
| выводить `/system/vendor/quasar/quasar.cfg` целиком | в нём ключи и секреты | читать нужные ключи (`grep`) |

Проверка после любых действий со звуком (после перезагрузки — 0; растёт — захват сломан):

```sh
ssh jbl 'grep -c SYNC_PTR /data/quasar/daemons_logs/maind.log'
```

Безопасно: `aplay -q -D plug:softvol файл.wav`, чтение порта 9875, `io_control` в 9910, чтение журналов, `strings` по бинарникам.

## Карта репозитория

Скрипт работает либо на компьютере, либо на колонке — это сказано в первой строке комментария («На компьютере» / «На колонке, от root»). Пара `x.sh → device.sh`: скрипт компьютера передаёт скрипт колонки в `sh -s` по ssh или копирует его туда через ADB.

| Файл | Где работает | Что делает |
| --- | --- | --- |
| [setup.sh](setup.sh) | компьютер | всё по шагам: ADB → SSH → блокировка OTA → автозапуск → закрыть сетевой ADB → проверка после перезагрузки |
| [find.sh](find.sh) | компьютер | поиск колонки без аргументов, вызывается из `setup.sh`; не нашёл или нашёл несколько — ошибка: сети — у физических интерфейсов (Linux — `/sys/class/net/*/device`, macOS — `en*`) с маской, шире /16 урезаются; `nc -z` по 1961 и 8080 (оба открыты только у Алисы) через `xargs -P`; адрес — на stdout |
| [lib/kit.sh](lib/kit.sh) | компьютер | `fail`, `jssh`, `jadb`, `on_device`, `fetch`, `ssh_port` — общее для всех скриптов компьютера |
| [adb/adb.sh](adb/adb.sh) → [adb/device.sh](adb/device.sh) | компьютер → колонка | маркеры ADB; `network-on`/`network-off` — сетевой ADB сейчас |
| [ssh/ssh.sh](ssh/ssh.sh) → [ssh/service.sh](ssh/service.sh) | компьютер → колонка | `install IP` — первая установка через ADB, `Host jbl` в `~/.ssh/config`; `upgrade [--rollback]` — замена Dropbear и `service.sh` под запасным сетевым ADB |
| [ssh/service.sh](ssh/service.sh) | колонка | ставится в `/data/jbl-ssh`: `install`, `start`/`stop`/`status` службы `jbl-ssh` (unit в `/run`, порт — строка `PORT=`), `swap`/`rollback`/`restore`/`restart` для `upgrade` |
| [ssh/build.sh](ssh/build.sh) | компьютер, CI | статический Dropbear ARMv7 (zig или Docker) |
| [ota/ota.sh](ota/ota.sh) → [ota/device.sh](ota/device.sh) | компьютер → колонка | `+i` на `/bkupgrade` |
| [autostart/autostart.sh](autostart/autostart.sh) → [autostart/device.sh](autostart/device.sh) | компьютер → колонка | автозапуск по ssh: `install`, `status`, `on`, `off`; `device.sh` ставится в `/data/jbl-boot`, при загрузке — `boot` и `hooks`; хук SSH пишет `install` |
| [autostart/plugin.c](autostart/plugin.c), [autostart/build.sh](autostart/build.sh) | компьютер, CI | плагин Fluent Bit `flb-in_jbl_boot.so` |
| [assist/](assist/) | Go: колонка; `deploy.sh`, `build.sh` — компьютер | помощник Алисы; `service.sh` — его служба и хук на колонке |

## Как это работает на колонке

Загрузка:

```text
fluent-bit (штатный, режим logs.cfg) читает /data/quasar/fluent-bit/device.cfg
  → @INCLUDE /data/jbl-boot/fluent.conf: [SERVICE] — копия штатной + Plugins_File, [INPUT] jbl_boot
  → плагин flb-in_jbl_boot.so: fork + execve «device.sh boot», Fluent Bit не ждёт
  → device.sh boot: один раз за загрузку (/run/jbl-boot.once) — systemd-run --unit=jbl-boot … device.sh hooks
  → device.sh hooks: hooks/*.sh по имени, до 2 мин; сбой хука не останавливает следующие
      10-ssh.sh     → /data/jbl-ssh/service.sh start    → служба jbl-ssh (порт 22)
      20-assist.sh  → /data/jbl-assist/service.sh start → служба jbl-assist (хук ставит assist/deploy.sh)
  → итог — /run/jbl-boot.status, журнал — /run/jbl-boot.log
```

| Где | Что |
| --- | --- |
| `/data/jbl-ssh` | `bin/dropbear`, `service.sh` (+ `*.old` после `ssh.sh upgrade`), `host_ed25519`, `auth/authorized_keys` |
| `/data/jbl-boot` | `device.sh`, плагин, `fluent.conf`, `plugins.conf`, `hooks/`, `device.cfg.orig` (до правки), `owner` (метка набора) |
| `/data/jbl-assist` | `jbl-assist.gz`, `service.sh`, `exa.key`, `debug`; бинарник распаковывается в `/tmp/jbl-assist` |
| `/run/systemd/system/` | unit-ы `jbl-ssh`, `jbl-assist` — пересоздаются при каждой загрузке; `jbl-boot` — временная служба `systemd-run` |
| `/run/jbl-boot.log`, `/run/jbl-ssh.log`, `/tmp/jbl-assist.log` | журналы наших частей (journald ничего не хранит) |
| `/data/quasar_activate_adb`, `/data/network_adb`, `/tmp/lock_activated_adb` | маркеры ADB: USB после загрузки, сетевой 5555, не выключать USB до перезагрузки |

- Fluent Bit грузит плагин только в режиме `logs.cfg`. `onboot.sh` при загрузке ставит `idle.cfg`, на `logs.cfg` переключает `fluent-bitd` по конфигу с сервера. Если сервер выключит логи, автозапуск пропадёт (предположение). Тип плагина Fluent Bit берёт из имени файла (`flb-in_jbl_boot.so` → символ `in_jbl_boot_plugin`); выводы в этой сборке — только `http` и `stdout`.
- `device.cfg` подключается в `logs.cfg` раньше штатной `[SERVICE]`, а Fluent Bit берёт первую `[SERVICE]`: поэтому в `fluent.conf` — копия штатной с `Plugins_File`.
- Сброс Алисы (`data_reset.sh`) стирает `/data/quasar/*`, а с ним и `device.cfg` с нашей строкой; `/data/jbl-*` остаются — `sh autostart/autostart.sh on` вернёт строку.
- adbd — служба `android-tools-adbd`; маркер сетевого ADB читает при старте, поэтому `adb/device.sh network-on|off` перезапускает её. adbd не передаёт stdin и код возврата.

## Колонка

| Что | Значение |
| --- | --- |
| Прошивка | проверять префикс `1.80.0.228.` в `/system/vendor/quasar/version_full`, не строку целиком |
| CPU, память | 4 × Cortex-A35 в режиме ARMv7, ~490 МиБ; свои бинарники — только статические ARMv7 (musl); aarch64 не запускается |
| ОС | Yocto, systemd 234, ядро 4.4, busybox 1.24.1 `sh` |
| Корень | UBIFS, только чтение — не трогаем |
| `/data` | 17 МБ, переживает перезагрузку. При загрузке `droproot_setup.sh` делает `chown -R system:users` и `chmod -R 775` — свои каталоги: root, `go-rwx`, `chattr -R +i` |
| `/tmp`, `/run` | RAM; tmpfs не поддерживает `chattr` |
| Часы | UTC, базы поясов нет. Пояс и координаты Алисы — только в её `yiod.log` (ротация ~25 мин; сразу после загрузки там сначала `Europe/Moscow`, потом настоящий) |
| Порты наружу | 22 — наш SSH; 1961 — `glagold` (TLS); 8080 — `maind --yiod`; 5555 — adbd, если включён |
| Журналы Алисы | `/data/quasar/daemons_logs/*.log` (+ `.1`): диалог — `yiod.log`, захват и громкость — `maind.log`, плееры — `mediad.log` |
| Есть | busybox `nc` `dd` `od` `awk` `sed` `mkfifo` `flock` `wget`, `sha256sum`, `chattr`, `systemd-run`, `aplay`, `openssl` 1.0.2 |
| Нет | python, curl, socat, `timeout`, `setsid`, `lsattr`, `inotifyd`, `scp` (файлы — через stdin ssh) |

Подводные камни busybox и systemd:

- `nc` выходит, как только кончился stdin; `od` в конвейере копит вывод до конца потока.
- awk работает с байтами: `[а-я]` не работает, `tolower` кириллицу не понижает.
- `X=1 функция` оставляет `X` после вызова.
- `systemd-run` не знает `RestartSec` — службы файлом в `/run/systemd/system`. Таймер `systemd-run --on-active` точен до минуты; задержка — `systemd-run /bin/sh -c 'sleep N; …'`.
- Перезапуск `jbl-ssh` из ssh-сеанса убивает сам сеанс — только отложенно, как в `ssh/service.sh restart`.

Звук: свои звуки — `aplay -q -D plug:softvol файл.wav` (WAV, громкость не зависит от громкости Алисы). Усилитель засыпает на цифровой тишине и съедает начало — в начало файла ~1 с тихого шума.

## Канал Алисы

TCP на 127.0.0.1, кадр — 4 байта длины (LE) + protobuf `QuasarMessage` старого формата: сообщение — прямо в своём поле верхнего уровня, без конверта 252/253. 9875 (aliced) — события: состояние, распознанный текст, ответ Алисы ещё до речи. 9910 (iohub_clients) — `io_control`: `stop_conversation`, `start_conversation`, `vins_server_action` (текст её голосом). Соединение с 9910 держать ~0,3 с и дочитывать, иначе RST и команда теряется. Номера полей и форматы — комментарии в [assist/alice.go](assist/alice.go); схемы `.proto` встроены в `/system/vendor/quasar/maind`.

## Как пишем код

- Тексты, сообщения и комментарии — на русском; комментарии — что и неочевидное почему, без истории правок. Префиксы ошибок не трогать: в скриптах набора — `ОШИБКА:`, в помощнике (`assist/main.go`, `assist/service.sh`) — `ERROR:`.
- Скрипты компьютера — POSIX `sh` с `set -eu`, колонки — busybox `sh`. Доступ к колонке — `jssh` (`ssh jbl`, алиас — `JBL_HOST`); до SSH — `jadb` (`JBL_ADB=IP:5555` или `usb`).
- Скрипт колонки запускается через `on_device` (ssh: `sh -s` со stdin; ADB: текст целиком в командной строке, код возврата — строкой `JBL_RC=`). Итог, который надо разобрать на компьютере, — строкой-маркером (`JBL_SSH_INSTALLED`, `JBL_SSH_SWAPPED`); код возврата после конвейера (`| tee`, `| tr`) теряется.
- Сначала все проверки, потом изменения. Уже установлено — ничего не менять и сказать об этом; прерванная установка должна повторяться (готовый каталог появляется последним шагом).
- Своё на колонке — в `/data/jbl-*`; unit-ы — в `/run`. Чужие файлы не трогать: наши помечены `# Managed by …`, без метки — отказ. Ссылки вместо файлов — отказ.
- После снятия `+i` вернуть его при любом исходе (см. `lock` в `ssh/service.sh`), иначе после перезагрузки `droproot_setup.sh` откроет каталог и служба откажется стартовать.
- Секреты (ключ Exa) — только через stdin, не в командной строке и не в выводе.
- Бинарники в репозитории не храним: их собирает CI, релиз — тег `v*` (zig 0.14 для Dropbear, clang для плагина, Go для помощника). Скрипты берут `*/build/`, иначе качают релиз.

## Правки и коммиты

- Изменились поведение, команды, пути или состав файлов — в той же правке обновить [README.md](README.md) (для пользователя), [assist/README.md](assist/README.md) (для помощника) и этот файл, если это их касается.
- Markdown — без переноса строк по ширине: абзац и пункт списка — одной строкой. Разделитель таблицы — с пробелами, `| --- | --- |`, не `|---|---|`: иначе ругается линтер.
- Коммиты — на русском и лаконично: заголовок — что сделано; тело — только неочевидное «почему», если оно есть; каждая строка — не длиннее 80 символов. Без упоминаний ИИ и строк `Co-Authored-By`.

## Проверка

Как в CI ([ci.yml](.github/workflows/ci.yml)); настройки shellcheck — в [.shellcheckrc](.shellcheckrc):

```sh
git ls-files '*.sh' | xargs shellcheck
go -C assist test -race ./...
sh assist/build.sh && sh ssh/build.sh && sh autostart/build.sh
```

Скрипты колонки без колонки — `busybox sh` с заглушками `id`, `chattr`, `chown`, `systemd-run` в `PATH` и каталогом вместо `/data/…`.

На колонке — не трогая рабочее:

| Что | Как |
| --- | --- |
| Dropbear | копия `ssh/service.sh` с `STATE=/data/jbl-ssh-kittest`, `install` без запуска службы; запуск вручную на свободном порту, например 2222 (рабочий занимает 22; `-D` — копия ключей в `/run`); вход, отказ паролю, передача файла; затем `chattr -R -i` и удалить |
| Рабочий Dropbear | только `ssh/ssh.sh upgrade`: запасной сетевой ADB, замена, перезапуск, возврат через ADB при сбое |
| Плагин | `rm /run/jbl-boot.once /run/jbl-boot.status`, затем второй Fluent Bit на пару секунд: `/system/vendor/quasar/fluent-bit/fluent-bit -e /data/jbl-boot/flb-in_jbl_boot.so -i jbl_boot -o stdout` (остановить по PID); плагин сработал — в `/run/jbl-boot.log` новые строки, появился `/run/jbl-boot.status`. Хуки при этом выполнятся ещё раз — они должны это переносить |
| Помощник | `ask` на колонке; сквозной тест — служба остановлена, `(подстановка & jbl-assist text) \| jbl-assist consume` ([assist/README.md](assist/README.md)) |
| Установщики | ветка «уже установлено» — на рабочей колонке; без сетевого ADB — `adb`, подменённый пересылкой `shell`/`push` через `ssh jbl`; запись в `~/.ssh` — с временным `HOME` |

Ключ Exa для проверок — не основной ключ пользователя; ключи не печатать и не коммитить.

## Типовые задачи

| Задача | Где |
| --- | --- |
| Свой скрипт при загрузке | `/data/jbl-boot/hooks/NN-имя.sh` (root, 700, короткий, повторный запуск не вредит; долгое — своей службой); в набор — запись хука при установке своей части, как `assist/service.sh enable` или хук SSH в `autostart/device.sh install` |
| Фразы-триггеры, тексты колонки, промпты | [assist/config.go](assist/config.go) |
| Новая версия Dropbear | `DROPBEAR_VERSION` в `ssh/build.sh`, затем `sh ssh/ssh.sh upgrade` |
| Порт SSH | `PORT=` в `ssh/service.sh`; на колонку — `ssh/ssh.sh upgrade`, он же напомнит про `~/.ssh/config` |
| Другая прошивка | префикс `1.80.0.228.*` в `ssh/service.sh` и `autostart/device.sh`; сначала проверить всё по таблице выше |
