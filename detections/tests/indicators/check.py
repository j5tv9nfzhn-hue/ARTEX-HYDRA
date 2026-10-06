#!/usr/bin/env python3
#
# Source-of-truth consistency test for the ARTEX detection indicators. run.sh
# launches this inside a Python container with the detection rules and the
# upstream source packages they pin mounted read-only under /repo. It proves one
# property the other three detection tests do not: that each rule's pinned
# indicator is still the string ARTEX's own source actually emits.
#
# The Sigma test proves an indicator survives rule->query *compilation*; the
# ATT&CK test proves the layer matches the rules' tags; the Suricata test proves
# the network rule *fires*. None of them look back at the source the indicator
# claims to come from. So the realistic rot they miss is an upstream re-sync that
# bumps the prober User-Agent to "artex-enrich/2.0" or rewrites the guard marker:
# every rule still compiles, the layer still matches, the pcap test still fires on
# the synthesized capture — and the deployed rule silently stops matching real
# ARTEX traffic. This test turns detections/README's claim ("every indicator is
# grounded in a string verified in this repository's source, not inferred") and
# CONTRIBUTING's first contribution contract into a guard a reviewer can re-run.
#
# For every indicator it asserts, bidirectionally:
#   - source drift: the value is still present in the upstream source file(s)
#     that emit it (fails if an upstream re-sync changed the source but not the
#     rule -> the rule is now stale);
#   - rule drift: the value is still pinned in the rule(s) built on it (fails if a
#     rule edit moved the indicator away from the source).
# The enrichment UA also carries a Suricata prefix check, because that rule
# matches the User-Agent by `startswith` and so pins a prefix of the full value.
#
# The destructive-command tokens are handled separately and honestly: they are
# generic hunting leads, not unique ARTEX fingerprints, so the test only asserts
# the correspondence the rule actually claims — each token appears both in ARTEX's
# guard deny-list (db/db.go) and in the hunting rule that mirrors it.
#
# Pure standard library (the slim image already ships python3); nothing is
# installed and nothing is written to the repo. Exits non-zero on any failure.

import os
import sys

ROOT = os.environ.get("ARTEX_REPO_ROOT", "/repo")

# --- exact ARTEX fingerprints ------------------------------------------------
# Each value is an operational string ARTEX emits; a rule is built on it. If an
# upstream re-sync changes the source string, the rule must change with it.
INDICATORS = [
    {
        "label": "enrichment prober User-Agent",
        "value": "artex-enrich/1.0",
        "sources": ["enrich/enrich.go"],
        "rules": ["detections/sigma/artex_enrich_user_agent.yml"],
        # Suricata matches the UA by `startswith`, so it pins a prefix of the
        # full value rather than the whole string. (file, prefix)
        "prefix_rules": [("detections/suricata/artex.rules", "artex-enrich/")],
    },
    {
        "label": "self-update egress User-Agent",
        "value": "artex-selfupdate",
        "sources": ["selfupdate/github.go", "selfupdate/stage.go"],
        "rules": ["detections/sigma/artex_selfupdate_egress.yml"],
    },
    {
        "label": "platform-guard audit framing marker",
        "value": "【ARTEX 平台管控·非目标防御】",
        "sources": ["guard/guard.go"],
        "rules": ["detections/sigma/artex_guard_audit_framing.yml"],
    },
]

# --- generic destructive-command hunting leads -------------------------------
# NOT unique ARTEX fingerprints. These tokens are shared with ARTEX's own guard
# deny-list (db/db.go); the hunting rule mirrors that list. The test asserts only
# the correspondence the rule claims, so it catches an upstream re-sync that drops
# or renames a deny-list entry the rule says it mirrors.
DENYLIST = {
    "source": "db/db.go",
    "rule": "detections/sigma/destructive_command_hunting.yml",
    "tokens": ["rm -rf", "mkfs", "DROP DATABASE", "FLUSHALL"],
}

fail = 0


def note(s):
    print("  " + s)


def ok(s):
    note("PASS  " + s)


def bad(s):
    global fail
    note("FAIL  " + s)
    fail = 1


def read(rel):
    """Return the text of a repo-relative file, or None if it is missing."""
    try:
        with open(os.path.join(ROOT, rel), encoding="utf-8") as fh:
            return fh.read()
    except OSError:
        return None


def contains(rel, needle):
    text = read(rel)
    if text is None:
        return None  # file missing -> distinct from "present but absent"
    return needle in text


print("== 1/3  exact fingerprints are still emitted by the upstream source ==")
for ind in INDICATORS:
    value, label = ind["value"], ind["label"]
    present = [s for s in ind["sources"] if contains(s, value) is True]
    missing_files = [s for s in ind["sources"] if contains(s, value) is None]
    if present:
        ok("%s: %r emitted by %s" % (label, value, ", ".join(present)))
    elif missing_files:
        bad("%s: source file(s) missing: %s (upstream moved the emitter?)"
            % (label, ", ".join(missing_files)))
    else:
        bad("%s: %r NOT found in any source file %s "
            "(upstream drift — update the rule to match)"
            % (label, value, ind["sources"]))

print("== 2/3  each rule still pins the indicator it is built on ==")
for ind in INDICATORS:
    value, label = ind["value"], ind["label"]
    for rule in ind["rules"]:
        hit = contains(rule, value)
        if hit is True:
            ok("%s pins %r" % (rule, value))
        elif hit is None:
            bad("rule file missing: %s" % rule)
        else:
            bad("%s no longer pins %r (rule drift from source)" % (rule, value))
    for rfile, prefix in ind.get("prefix_rules", []):
        if not value.startswith(prefix):
            bad("%s: prefix %r is not a prefix of %r (internal inconsistency)"
                % (rfile, prefix, value))
            continue
        hit = contains(rfile, prefix)
        if hit is True:
            ok("%s pins prefix %r of %r" % (rfile, prefix, value))
        elif hit is None:
            bad("rule file missing: %s" % rfile)
        else:
            bad("%s no longer pins prefix %r" % (rfile, prefix))

print("== 3/3  destructive hunting tokens match ARTEX's guard deny-list ==")
src, rule, tokens = DENYLIST["source"], DENYLIST["rule"], DENYLIST["tokens"]
for tok in tokens:
    in_src = contains(src, tok)
    in_rule = contains(rule, tok)
    if in_src is None:
        bad("deny-list source missing: %s" % src)
    elif in_rule is None:
        bad("hunting rule missing: %s" % rule)
    elif in_src and in_rule:
        ok("%r present in both %s and %s" % (tok, src, rule))
    elif not in_src:
        bad("%r pinned by the rule but absent from %s "
            "(upstream dropped/renamed the deny-list entry)" % (tok, src))
    else:
        bad("%r in the deny-list but not pinned by %s" % (tok, rule))

print()
print("reference: %d exact fingerprints, %d deny-list tokens checked"
      % (len(INDICATORS), len(tokens)))
print("RESULT: %s" % ("PASS" if fail == 0 else "FAIL"))
sys.exit(fail)
