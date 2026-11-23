#!/bin/sh
# Сборка jbl-assist для колонки (ARMv7, статический): тесты, бинарник, gzip для /data.
# Нужен Go 1.25+. Результат — build/jbl-assist.gz (на колонке распаковывается в /tmp).
set -eu
here=$(cd "$(dirname "$0")" && pwd)
cd "$here"
mkdir -p build
go vet ./...
go test -count=1 ./...
v=$(git describe --tags --always --dirty 2>/dev/null || echo dev)
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 \
    go build -trimpath -ldflags "-s -w -X main.version=$v" -o build/jbl-assist .
gzip -9 -n -c build/jbl-assist > build/jbl-assist.gz
wc -c build/jbl-assist build/jbl-assist.gz | awk '$2 != "total" { printf "%-24s %.1f МБ\n", $2, $1 / 1048576 }'
