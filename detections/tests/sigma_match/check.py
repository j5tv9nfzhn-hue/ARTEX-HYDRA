#!/usr/bin/env python3
#
# Live event-matching test for the ARTEX atomic Sigma rules (../../sigma/*.yml).
# check.sh installs a pinned pySigma inside a container and runs this script with
# the rule tree mounted read-only at /sigma and this directory at /src.
#
# WHAT THIS PROVES, AND WHY IT IS DIFFERENT FROM THE sigma/ SUITE
# --------------------------------------------------------------
# The sigma/ suite proves each rule is structurally valid and COMPILES to a
# backend query, and that its indicator strings survive into that query. It does
# NOT prove the rule actually fires on a matching event, or stays quiet on a
# benign one: a field renamed to something the log never carries, a wildcard that
# silently dropped, or an over-broad token would all still compile cleanly. The
# README's own principle is that "a detection you cannot run is only a claim," and
# the Suricata suite already backs its network rule with a real pcap replay
# (fires on the probe UA, silent on a benign browser). This suite closes the same
# gap for the host/log-layer Sigma rules: for every atomic rule it asserts a
# representative malicious event MATCHES and a benign event DOES NOT.
#
# HOW IT MATCHES (trust model)
# ----------------------------
# It does not hand-parse the YAML or re-implement Sigma's modifier logic. pySigma
# parses each rule and compiles its modifiers and condition into a tree:
# `|contains` becomes a wildcard-wrapped value, `|all` becomes an AND over values,
# `1 of selection_*` becomes an OR over the selection groups. This script only
# walks that compiled tree (AND / OR / NOT / field-equals / keyword) and tests
# each leaf against the event, so the authoritative parsing stays in pySigma. A
# leaf value or condition node this script does not explicitly support raises
# rather than passing silently (fail-closed), so a future rule using an
# unsupported construct surfaces loudly here instead of being waved through.
#
# SCOPE AND HONESTY (read before trusting a green run)
# ----------------------------------------------------
#  - ATOMIC RULES ONLY. The four correlation rules under sigma/correlation/ are
#    time-windowed aggregations (event_count / value_count over many events); a
#    single-event matcher cannot model them and they are deliberately excluded.
#    The sigma/ and sigma_backends/ suites already prove those compile.
#  - Matching is CASE-INSENSITIVE. This mirrors the default of the splunk backend
#    the sigma/ suite targets, and the destructive rule's own false-positive note
#    assumes it (it warns that lowercase coreutils `truncate` shares the uppercase
#    `TRUNCATE ` token and must be allow-listed). Your SIEM's case handling and
#    field normalisation may differ; this is a regression test for the rules'
#    field/value/condition logic, not a substitute for validating in your stack.
#  - Keyword matching (the audit-framing rule) is modelled as a full-text
#    substring search across all event field values, the common interpretation of
#    an unbound Sigma keyword.
#
# Exits non-zero on any failure. Standard library only beyond pySigma.

import glob
import json
import os
import re
import sys

from sigma.collection import SigmaCollection
from sigma.conditions import (
    ConditionAND,
    ConditionFieldEqualsValueExpression,
    ConditionNOT,
    ConditionOR,
    ConditionValueExpression,
)
from sigma.types import (
    SigmaNull,
    SigmaNumber,
    SigmaRegularExpression,
    SigmaString,
    SpecialChars,
)

SIGMA_DIR = os.environ.get("SIGMA_DIR", "/sigma")
EVENTS_DIR = os.environ.get("EVENTS_DIR", "/src/events")

fail = 0


def note(msg):
    print(f"  {msg}")


def passed(msg):
    note(f"PASS  {msg}")


def bad(msg):
    global fail
    note(f"FAIL  {msg}")
    fail = 1


# --- matcher -------------------------------------------------------------------


def sigmastring_to_regex(value):
    """Compile a pySigma SigmaString (literal text plus wildcards) to an anchored,
    case-insensitive regex. `|contains` already wrapped the value in multi
    wildcards upstream, so a plain string compiles to an exact match and a
    contains-value compiles to a substring match — exactly the Sigma semantics."""
    parts = []
    for part in value.s:
        if part == SpecialChars.WILDCARD_MULTI:
            parts.append(".*")
        elif part == SpecialChars.WILDCARD_SINGLE:
            parts.append(".")
        elif isinstance(part, str):
            parts.append(re.escape(part))
        else:
            raise ValueError(f"unsupported SigmaString part: {part!r}")
    return re.compile("^" + "".join(parts) + "$", re.DOTALL | re.IGNORECASE)


def field_match(field, value, event):
    if field not in event:
        return False
    observed = str(event[field])
    if isinstance(value, SigmaString):
        return sigmastring_to_regex(value).search(observed) is not None
    if isinstance(value, SigmaNumber):
        return observed == str(value.number)
    if isinstance(value, SigmaNull):
        return event.get(field) is None
    if isinstance(value, SigmaRegularExpression):
        return re.search(value.regexp, observed) is not None
    raise ValueError(f"unsupported field value type: {type(value).__name__}")


def keyword_match(value, event):
    """Unbound keyword: full-text substring search across all field values."""
    if not isinstance(value, SigmaString):
        raise ValueError("unsupported keyword value type")
    if any(not isinstance(p, str) for p in value.s):
        raise ValueError("wildcard in keyword is not supported by this matcher")
    token = "".join(value.s)
    haystack = " ".join(str(v) for v in event.values())
    return token.lower() in haystack.lower()


def evaluate(node, event):
    if isinstance(node, ConditionAND):
        return all(evaluate(a, event) for a in node.args)
    if isinstance(node, ConditionOR):
        return any(evaluate(a, event) for a in node.args)
    if isinstance(node, ConditionNOT):
        return not evaluate(node.args[0], event)
    if isinstance(node, ConditionFieldEqualsValueExpression):
        return field_match(node.field, node.value, event)
    if isinstance(node, ConditionValueExpression):
        return keyword_match(node.value, event)
    raise ValueError(f"unsupported condition node: {type(node).__name__}")


def rule_matches(rule, event):
    return any(evaluate(c.parsed, event) for c in rule.detection.parsed_condition)


# --- load atomic rules and their paired sample events --------------------------


def load_atomic_rules():
    rules = {}
    for path in sorted(glob.glob(os.path.join(SIGMA_DIR, "*.yml"))):
        stem = os.path.splitext(os.path.basename(path))[0]
        collection = SigmaCollection.from_yaml(open(path, encoding="utf-8").read())
        for rule in collection.rules:
            # Only plain atomic rules; correlation rules (if ever placed here)
            # carry a `.type` and are out of scope for single-event matching.
            if type(rule).__name__ != "SigmaRule":
                continue
            rules[stem] = rule
    return rules


def load_events():
    events = {}
    for path in sorted(glob.glob(os.path.join(EVENTS_DIR, "*.json"))):
        stem = os.path.splitext(os.path.basename(path))[0]
        events[stem] = json.load(open(path, encoding="utf-8"))
    return events


def main():
    rules = load_atomic_rules()
    events = load_events()

    print("== 1/3  every atomic rule is paired with a sample-event file ==")
    rule_stems = set(rules)
    event_stems = set(events)
    orphan_rules = sorted(rule_stems - event_stems)
    orphan_events = sorted(event_stems - rule_stems)
    if orphan_rules:
        bad(f"atomic rules with no events/<name>.json: {orphan_rules}")
    if orphan_events:
        bad(f"event files with no matching atomic rule: {orphan_events}")
    if not orphan_rules and not orphan_events:
        passed(
            f"rule/sample pairing: {len(rules)} atomic rules, "
            f"{len(events)} event files, no orphans"
        )

    print("== 2/3  each rule matches its malicious sample events (true positives) ==")
    for stem in sorted(rule_stems & event_stems):
        rule = rules[stem]
        positives = events[stem].get("positive", [])
        if not positives:
            bad(f"{stem}: no positive sample events")
            continue
        missed = [e for e in positives if not rule_matches(rule, e)]
        if missed:
            bad(f"{stem}: {len(missed)}/{len(positives)} positive events did NOT match")
            for e in missed:
                note(f"        unmatched: {json.dumps(e, ensure_ascii=False)}")
        else:
            passed(f"{stem}: {len(positives)}/{len(positives)} positive events matched")

    print("== 3/3  each rule rejects its benign sample events (true negatives) ==")
    for stem in sorted(rule_stems & event_stems):
        rule = rules[stem]
        negatives = events[stem].get("negative", [])
        if not negatives:
            bad(f"{stem}: no negative sample events")
            continue
        fired = [e for e in negatives if rule_matches(rule, e)]
        if fired:
            bad(f"{stem}: {len(fired)}/{len(negatives)} benign events WRONGLY matched")
            for e in fired:
                note(f"        wrongly matched: {json.dumps(e, ensure_ascii=False)}")
        else:
            passed(
                f"{stem}: {len(negatives)}/{len(negatives)} benign events correctly "
                "not matched"
            )

    print()
    try:
        import importlib.metadata as md

        print(f"reference: pySigma {md.version('pysigma')}, atomic rules only")
    except Exception:
        pass
    print("RESULT: PASS" if fail == 0 else "RESULT: FAIL")
    sys.exit(fail)


if __name__ == "__main__":
    main()
