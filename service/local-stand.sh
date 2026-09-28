#!/usr/bin/env bash
set -euo pipefail
umask 077

if [[ $# != 2 || ! $1 =~ ^(run|ticket)$ || ! $2 =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "usage: local-stand.sh run|ticket <PC Wi-Fi IPv4>" >&2
  exit 2
fi

mode=$1
address=$2
port_a=${PORT_A:-18443}
port_b=${PORT_B:-18444}
root=$(cd "$(dirname "$0")" && pwd)
state="${XDG_STATE_HOME:-$HOME/.local/state}/rassvet-chat-stand/$address"
mkdir -p "$state"
cd "$root"
go build -o "$state/rassvetd" ./cmd/rassvetd

if [[ ! -f "$state/a/cert.pem" ]]; then
  "$state/rassvetd" init-node node-a "$state/a" "$address"
  "$state/rassvetd" init-node node-b "$state/b" "$address"
fi

pin_a=$("$state/rassvetd" node-pin "$state/a/cert.pem")
pin_b=$("$state/rassvetd" node-pin "$state/b/cert.pem")
printf '[{"url":"https://%s:%s","tls_spki_sha256":"%s"},{"url":"https://%s:%s","tls_spki_sha256":"%s"}]\n' \
  "$address" "$port_a" "$pin_a" "$address" "$port_b" "$pin_b" > "$state/nodes.json"

if [[ ! -f "$state/a/chat.db" ]]; then
  "$state/rassvetd" init-db "$state/a/chat.db"
  "$state/rassvetd" init-admin "$state/a/chat.db" 'Администратор' "$state/nodes.json" \
    "$state/a/cert.pem" "$state/b/cert.pem" > "$state/admin-ticket.json"
elif [[ $mode == ticket ]]; then
  "$state/rassvetd" reissue-admin "$state/a/chat.db" "$state/nodes.json" \
    "$state/a/cert.pem" "$state/b/cert.pem" > "$state/admin-ticket.json"
fi

if [[ $mode == ticket ]]; then
  cat "$state/admin-ticket.json"
  exit
fi

echo "Admin ticket: $state/admin-ticket.json (valid for 10 minutes from issue)"
echo "Node A: https://$address:$port_a; node B: https://$address:$port_b"
echo "Logs: $state/a.log and $state/b.log; stop with Ctrl+C"
"$state/rassvetd" serve "$state/a/chat.db" :"$port_a" "$state/a/cert.pem" "$state/a/key.pem" \
  "$state/nodes.json" "$state/a/files" "$state/b/cert.pem" node-b > "$state/a.log" 2>&1 &
pid_a=$!
"$state/rassvetd" serve "$state/b/chat.db" :"$port_b" "$state/b/cert.pem" "$state/b/key.pem" \
  "$state/nodes.json" "$state/b/files" "$state/a/cert.pem" node-a > "$state/b.log" 2>&1 &
pid_b=$!
trap 'kill "$pid_a" "$pid_b" 2>/dev/null || true; wait "$pid_a" "$pid_b" 2>/dev/null || true' EXIT
wait -n "$pid_a" "$pid_b"
