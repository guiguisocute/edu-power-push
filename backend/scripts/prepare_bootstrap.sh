#!/usr/bin/env bash
set -Eeuo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
if [[ $# -ne 1 ]]; then
  echo "usage: $0 <campus-data-directory>" >&2
  exit 2
fi
source_dir=$1
target_dir="$repo_dir/bootstrap"

for name in room_meters.json meter_balances.json; do
  if [[ ! -f "$source_dir/$name" ]]; then
    echo "missing source file: $source_dir/$name" >&2
    exit 1
  fi
done

mkdir -p "$target_dir"
cp -- "$source_dir/room_meters.json" "$target_dir/room_meters.json"
cp -- "$source_dir/meter_balances.json" "$target_dir/meter_balances.json"
printf 'bootstrap prepared at %s\n' "$target_dir"
