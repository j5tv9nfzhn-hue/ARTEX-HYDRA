#!/bin/sh
#
# In-container half of the ARTEX Sigma live event-matching test. run.sh launches
# this inside a Python container with the atomic Sigma rules mounted read-only at
# /sigma and this directory at /src. It installs a pinned pySigma, then hands off
# to check.py, which asserts that every atomic rule matches its malicious sample
# events and stays quiet on its benign ones (see check.py's header for the trust
# model and scope). pySigma does the parsing; check.py walks the compiled
# condition tree and tests each sample event against it.
#
# POSIX sh (the slim image ships dash). Exits non-zero if any assertion fails.
set -eu

VERSION="${PYSIGMA_VERSION:-2.0.0}"

pip install --quiet --disable-pip-version-check "pysigma==${VERSION}" >/dev/null 2>&1

exec python3 /src/check.py
