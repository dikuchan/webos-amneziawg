#!/bin/sh
set -eu
GO_BIN=${GO_BIN:-go}
mkdir -p .build
"$GO_BIN" test -tags browser -c -o .build/lan-browser.test .
exec .build/lan-browser.test -test.run '^TestLANBrowserServer$' -test.timeout 4m
