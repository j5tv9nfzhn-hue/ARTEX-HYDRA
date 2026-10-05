#!/usr/bin/env bash
#
# Reproducible regression test for the ARTEX Suricata rules
# (../../suricata/artex.rules). It proves three properties with no committed
# binary capture and no host dependencies beyond Docker:
#
#   1. sid 1000001 fires exactly once per enrich probe   (presence)
#   2. sid 1000002 fires once the 30-in-300s rate is hit (velocity)
#   3. an identical capture with a benign browser UA      (specificity)
#      produces zero alerts
#
# Everything runs in containers: scapy synthesizes a deterministic pcap, then
# `suricata -r` reads it offline. The pcap is generated into a scratch dir that
# is removed on exit and is never committed.
#
# Usage:   detections/tests/suricata/run.sh
# Env:     SURICATA_IMAGE (default jasonish/suricata:latest)
#          PYTHON_IMAGE   (default python:3.12-slim)
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$HERE/../../.." && pwd)"
RULES_DIR="$REPO/detections/suricata"
SURICATA_IMAGE="${SURICATA_IMAGE:-jasonish/suricata:latest}"
PYTHON_IMAGE="${PYTHON_IMAGE:-python:3.12-slim}"

NUM_FLOWS=35
ENRICH_UA="artex-enrich/1.0"
BENIGN_UA="Mozilla/5.0 (X11; Linux x86_64; rv:128.0) Gecko/20100101 Firefox/128.0"

# Scratch must live under the repo tree so Docker Desktop (macOS) can bind-mount
# it; /tmp and $TMPDIR are not shared by default. It is git-ignored and removed
# on exit.
SCRATCH="$(mktemp -d "$HERE/.scratch.XXXXXX")"
cleanup() { rm -rf "$SCRATCH"; }
trap cleanup EXIT

fail=0
note() { printf '  %s\n' "$1"; }

echo "== 1/3  synthesize deterministic captures (scapy) =="
docker run --rm -v "$SCRATCH:/out" -v "$HERE:/src:ro" "$PYTHON_IMAGE" sh -c "
  pip install --quiet --disable-pip-version-check scapy >/dev/null 2>&1 &&
  python /src/gen_pcap.py /out/enrich.pcap '$ENRICH_UA' $NUM_FLOWS &&
  python /src/gen_pcap.py /out/benign.pcap '$BENIGN_UA' $NUM_FLOWS
"

run_suricata() { # $1 = capture basename
  local name="$1"
  mkdir -p "$SCRATCH/$name-out"
  # -k none: crafted packets carry no valid checksums; do not drop on them.
  docker run --rm -v "$SCRATCH:/data" -v "$RULES_DIR:/r:ro" "$SURICATA_IMAGE" \
    suricata -r "/data/$name.pcap" -S /r/artex.rules -k none -l "/data/$name-out" \
    >/dev/null 2>&1
}

alerts() { # $1 = capture basename, $2 = sid (or "any")
  python3 - "$SCRATCH/$1-out/eve.json" "$2" <<'PY'
import json, sys
path, sid = sys.argv[1], sys.argv[2]
n = 0
with open(path) as f:
    for line in f:
        line = line.strip()
        if not line:
            continue
        try:
            e = json.loads(line)
        except ValueError:
            continue
        if e.get("event_type") != "alert":
            continue
        if sid == "any" or e.get("alert", {}).get("signature_id") == int(sid):
            n += 1
print(n)
PY
}

expect() { # $1 label, $2 actual, $3 op (eq|ge), $4 expected
  local label="$1" actual="$2" op="$3" expected="$4" ok
  case "$op" in
    eq) [ "$actual" -eq "$expected" ] && ok=1 || ok=0 ;;
    ge) [ "$actual" -ge "$expected" ] && ok=1 || ok=0 ;;
  esac
  if [ "$ok" -eq 1 ]; then
    note "PASS  $label  (got $actual, want $op $expected)"
  else
    note "FAIL  $label  (got $actual, want $op $expected)"
    fail=1
  fi
}

echo "== 2/3  run Suricata offline over the enrich capture =="
run_suricata enrich
e1="$(alerts enrich 1000001)"
e2="$(alerts enrich 1000002)"
expect "sid 1000001 presence: one alert per probe" "$e1" eq "$NUM_FLOWS"
expect "sid 1000002 velocity: fires past 30-in-300s" "$e2" ge 1
note "reference (Suricata 8.0.7): sid 1000002 = 5 (flows 31-35)"

echo "== 3/3  run Suricata offline over the benign capture =="
run_suricata benign
b="$(alerts benign any)"
expect "benign browser UA produces no ARTEX alerts" "$b" eq 0

echo
if [ "$fail" -eq 0 ]; then
  echo "RESULT: PASS"
else
  echo "RESULT: FAIL"
fi
exit "$fail"
