#!/usr/bin/env bash
#
# Runs every detection test suite under this directory in one command — the
# local-developer and pre-commit counterpart to the per-suite CI steps in
# ../../.github/workflows/detections.yml. CONTRIBUTING.md and this directory's
# README.md promise that each suite "drops straight into CI or a pre-commit
# hook"; this is the single entry point that honours that promise for all of
# them at once, so a contributor does not have to invoke the seven run.sh scripts
# by hand (and reviewers do not have to improvise a loop).
#
# It runs the suites in the same order as CI, lets each suite's own output flow
# through, prints a one-line PASS/FAIL summary per suite at the end, and exits
# non-zero if any suite failed — so it is safe to drop into a CI step or a
# pre-commit hook. Every suite runs to completion even if an earlier one fails,
# so one invocation surfaces every regression rather than only the first.
#
# No host dependency beyond Docker: each suite runs its checks in a container and
# writes nothing to the repo tree (see the per-suite run.sh headers). The image
# and version overrides the child scripts honour (PYTHON_IMAGE, SIGMA_CLI_VERSION,
# SIGMAHQ_VALIDATORS_VERSION, SURICATA_IMAGE) are inherited from this process's
# environment, so exporting any of them here applies to every suite at once.
#
# Usage:   detections/tests/run-all.sh
set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"

# Same order as the steps in .github/workflows/detections.yml.
SUITES="sigma sigma_lint sigma_backends suricata attack indicators misp"

fail=0
results=""
for suite in $SUITES; do
  printf '\n===== %s =====\n' "$suite"
  if "$HERE/$suite/run.sh"; then
    results="${results}  PASS  ${suite}"$'\n'
  else
    rc=$?
    results="${results}  FAIL  ${suite} (exit ${rc})"$'\n'
    fail=1
  fi
done

printf '\n===== detection suites summary =====\n'
printf '%s' "$results"
if [ "$fail" -ne 0 ]; then
  printf 'RESULT: FAIL\n'
  exit 1
fi
printf 'RESULT: PASS\n'
