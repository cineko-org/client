#!/usr/bin/env bash
set -euo pipefail

test -f internal/adapters/storage/local/store.go
test -f desktop_monitor.go
test -f desktop_probe.go
grep -Fq 'probe.LocalScanner' desktop_probe.go
grep -Fq 'localstore.Open' desktop_launch.go
test ! -e internal/adapters/storage/centralhttp/store.go
test ! -e desktop_execution.go
! grep -R -En --include='*.go' 'gen/go/cineko/service|centralhttp' . --exclude-dir=vendor
! grep -Eq 'releasecontract publish|case "publish"' cmd/releasecontract/main.go
! grep -Fq 'replace github.com/cineko-org/contracts/v3' go.mod
! grep -Fq 'replace github.com/cineko-org/probe/v2' go.mod
grep -Fq 'github.com/cineko-org/contracts/v3 v3.7.0' go.mod
grep -Fq 'github.com/cineko-org/probe/v2 v2.8.0' go.mod

printf 'Client-only behavior boundary checks passed\n'
