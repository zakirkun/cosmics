#!/bin/sh
# Verifies that every tool the registry references is executable.
#
# The image is the contract: if a name in internal/tools/registry/*.yaml has no
# binary here, a run fails at execute time instead of at build time. Running this
# during the build turns that into an immediate failure.

set -eu

missing=0

require() {
    name="$1"
    if command -v "$name" >/dev/null 2>&1; then
        printf 'ok      %s\n' "$name"
    else
        printf 'MISSING %s\n' "$name"
        missing=$((missing + 1))
    fi
}

require nmap
require httpx
require subfinder
require nuclei
require ffuf
require nikto
require sqlmap
require hydra
require whatweb
require gobuster
require dig
require jq

if [ "$missing" -ne 0 ]; then
    printf '\n%d required tool(s) missing\n' "$missing" >&2
    exit 1
fi

printf '\nall required tools present\n'
