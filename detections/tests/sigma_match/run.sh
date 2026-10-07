#!/usr/bin/env bash
#
# Reproducible live event-matching test for the ARTEX atomic Sigma rules
# (../../sigma/*.yml). The sibling sigma/ suite proves those rules are valid and
# COMPILE to a backend query; this suite proves they actually FIRE on a matching
# event and stay quiet on a benign one — the same "a detection you cannot run is
# only a claim" guarantee the suricata/ suite already gives the network rule with
# a pcap replay. For each atomic rule it asserts a representative malicious event
# matches and a benign event does not.
#
# It proves three properties with no host dependency beyond Docker (pySigma runs
# in a container, nothing is installed on the host and nothing is written to the
# repo tree):
#
#   1. rule/sample pairing   every atomic rule has an events/<name>.json and
#                            every events file maps to a rule (no orphans)
#   2. true positives        each rule matches all of its malicious sample events
#   3. true negatives        each rule matches none of its benign sample events
#
# pySigma parses each rule and compiles its modifiers and condition into a tree;
# check.py only walks that tree, so the authoritative Sigma logic stays in
# pySigma (see check.py's header). Correlation rules under sigma/correlation/ are
# time-windowed aggregations and are out of scope for single-event matching — the
# sigma/ and sigma_backends/ suites cover those. Matching is case-insensitive;
# see check.py for the full scope and honesty notes.
#
# Usage:   detections/tests/sigma_match/run.sh
# Env:     PYTHON_IMAGE    (default python:3.12-slim)
#          PYSIGMA_VERSION (default 2.0.0 — the pinned reference version)
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$HERE/../../.." && pwd)"
SIGMA_DIR="$REPO/detections/sigma"
PYTHON_IMAGE="${PYTHON_IMAGE:-python:3.12-slim}"
PYSIGMA_VERSION="${PYSIGMA_VERSION:-2.0.0}"

# Everything runs inside the container: check.sh installs the pinned pySigma and
# runs check.py, which asserts the three properties and exits non-zero on any
# failure. The rule tree and this directory are mounted read-only.
docker run --rm \
  -v "$SIGMA_DIR:/sigma:ro" \
  -v "$HERE:/src:ro" \
  -e PYSIGMA_VERSION="$PYSIGMA_VERSION" \
  "$PYTHON_IMAGE" sh /src/check.sh
