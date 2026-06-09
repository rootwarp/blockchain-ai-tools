#!/usr/bin/env bash
#
# Scaffold a new Go module in the monorepo and wire it into the workspace.
#
# Usage: scripts/new-module.sh <app|lib> <name> <module-base>
#   app -> apps/<name>  with a runnable main.go
#   lib -> libs/<name>  with a package stub
#
# Normally invoked via `make new-app name=foo` / `make new-lib name=foo`.

set -euo pipefail

kind="${1:-}"
name="${2:-}"
base="${3:-}"

if [[ -z "$kind" || -z "$name" || -z "$base" ]]; then
	echo "usage: new-module.sh <app|lib> <name> <module-base>" >&2
	echo "  e.g. make new-app name=wallet-analyzer" >&2
	exit 1
fi

if [[ ! -f go.work ]]; then
	echo "error: must be run from the repo root (no go.work here)" >&2
	exit 1
fi

case "$kind" in
	app) dir="apps/$name" ;;
	lib) dir="libs/$name" ;;
	*) echo "error: kind must be 'app' or 'lib', got '$kind'" >&2; exit 1 ;;
esac

# Name must be a path-safe, lowercase identifier starting with a letter.
if [[ ! "$name" =~ ^[a-z][a-z0-9_-]*$ ]]; then
	echo "error: invalid name '$name' — use lowercase letters/digits/'-'/'_', starting with a letter" >&2
	exit 1
fi

if [[ -e "$dir" ]]; then
	echo "error: $dir already exists" >&2
	exit 1
fi

modpath="$base/$dir"

mkdir -p "$dir"
( cd "$dir" && go mod init "$modpath" )

if [[ "$kind" == "app" ]]; then
	cat > "$dir/main.go" <<EOF
package main

import "fmt"

func main() {
	fmt.Println("$name: hello from $modpath")
}
EOF
else
	# Go package names are short and lowercase with no separators.
	pkg=$(printf '%s' "$name" | sed 's/[-_]//g')
	cat > "$dir/$pkg.go" <<EOF
// Package $pkg provides ...
package $pkg
EOF
fi

go work use "./$dir"

echo "created $kind module: $dir"
echo "  module path: $modpath"
echo "  wired into go.work"
