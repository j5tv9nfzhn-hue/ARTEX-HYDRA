# ARTEX detection rules

> 한국어: 이 디렉터리는 [방어·탐지 가이드(docs/defense-ko.md)](../docs/defense-ko.md)의 4절 "탐지 규칙"을
> 실제로 배포 가능한 [Sigma](https://sigmahq.io) 규칙으로 옮긴 것입니다. 모든 규칙은 자신이 소유하거나 서면
> 허가를 받은 시스템을 지키는 **방어·탐지 목적에만** 사용하십시오.

Deployable [Sigma](https://sigmahq.io) rules that formalize the pseudo-rules in the defense guide
([Korean](../docs/defense-ko.md) · [English](../docs/defense-en.md), section 4) into a vendor-neutral
format you can convert to your own SIEM or EDR query language. Every indicator here is grounded in a
string or behaviour verified in this repository's source, not inferred.

## Atomic rules

- **`sigma/artex_enrich_user_agent.yml`** — inbound `artex-enrich/1.0` User-Agent from ARTEX asset
  enrichment (`enrich/enrich.go`). Target-side, supporting indicator. `level: high`.
- **`sigma/artex_selfupdate_egress.yml`** — outbound `artex-selfupdate` User-Agent from the self-update
  routine (`selfupdate/github.go`). Host/forensic egress indicator. `level: medium`.
- **`sigma/artex_guard_audit_framing.yml`** — the platform-guard control marker written to the audit log
  on a blocked tool call (`guard/guard.go`). Host/forensic indicator. `level: high`.
- **`sigma/destructive_command_hunting.yml`** — destructive shell/DB commands mirroring the ARTEX guard's
  built-in deny list (`db/db.go` seed). Generic hunting lead, not an ARTEX signature. `level: medium`.

## Correlation rules (behaviour)

Static strings can be changed; behaviour is harder to hide. These Sigma **correlation** rules in
[`sigma/correlation/`](sigma/correlation/) encode the behaviour-based layer of the defense guide
(sections 4.1–4.2 and 4.4). Each references an atomic rule above by its `id`, so convert the whole
`sigma/` tree — not a single correlation file — to resolve the reference (see below).

- **`sigma/correlation/artex_enrich_scan_velocity.yml`** — a burst of `artex-enrich/1.0` probes from one
  source in a short window (enrichment runs at concurrency 4 with no rate limit). The velocity the
  single-request rule misses. `event_count`, `level: high`.
- **`sigma/correlation/artex_enrich_fanout.yml`** — one source carrying the enrichment User-Agent to many
  *distinct* hosts: machine-speed fan-out across an asset list, where breadth (not just volume) is the
  tell. `value_count`, `level: high`.
- **`sigma/correlation/artex_guard_block_burst.yml`** — repeated platform-guard control markers on one
  host, i.e. an active ARTEX run tripping its guard rather than a document that merely quotes the marker.
  `event_count`, `level: high`.
- **`sigma/correlation/artex_guard_marker_then_destructive.yml`** — the guard marker and a destructive
  command co-occurring on one host within a window (defense guide §4.2, multi-stage). Combining an
  ARTEX-specific marker with the otherwise-generic destructive-command signal raises specificity.
  `temporal`, `level: high`.

Thresholds and windows are conservative defaults — tune them to your baseline. The pure web multi-stage
case in §4.2 (enumerate → probe → authenticate) still needs base rules specific to your environment,
because the attack traffic itself carries no ARTEX-unique User-Agent.

## Network rules (Suricata)

Sigma covers host and log telemetry. The one ARTEX artifact observable on the wire — the enrichment prober's
`artex-enrich/1.0` HTTP User-Agent (`enrich/enrich.go`) — ships as [Suricata](https://suricata.io) rules in
[`suricata/`](suricata/): a presence signature plus a high-rate enumeration variant. ARTEX's actual attack
traffic carries no ARTEX-unique User-Agent, so the network layer is intentionally narrow; see
[`suricata/README.md`](suricata/README.md) for the scope, the TLS caveat, and how to validate with
`suricata -T` and a reference pcap.

## Tests

The Suricata network rules ship with a reproducible regression test in [`tests/`](tests/): it synthesizes a
deterministic capture with scapy, runs `suricata -r` over it, and asserts that the presence rule fires once
per probe, the velocity rule trips past its rate threshold, and a benign-User-Agent capture produces zero
alerts. No binary capture is committed — the test regenerates it on every run. See
[`tests/README.md`](tests/README.md). The Sigma rules are validated by `sigma check` and `sigma convert`
(below).

## How to read these honestly

- **Static indicators can be changed.** An operator can set a different User-Agent, so the absence of
  `artex-enrich/1.0` or `artex-selfupdate` does **not** mean safety. The durable signal is *behaviour* —
  a single source chaining recon → enumeration → probing → auth/injection attempts, adapting to responses,
  running without pause. That layer is described in the defense guide (sections 1, 2, and 4.1–4.2); the
  `sigma/correlation/` rules above ship it as deployable correlations (velocity, fan-out, guard-block
  burst, and a guard-marker-with-destructive-command multi-stage), and the pure web multi-stage case
  still needs base rules specific to your environment.
- **The destructive-command rule is generic hunting.** It mirrors ARTEX's guard deny list, but the same
  commands are run by legitimate administrators. Treat a hit as a lead, allow-list your environment, and
  do not attribute it to ARTEX on its own.
- **Port indicators are host-forensic, not Sigma.** The ARTEX server default `:8787` and the recording
  proxy `127.0.0.1:8788` (`cmd/artex/main.go`) are best checked on a suspected host with `ss`/`netstat`,
  so they are documented in the defense guide rather than shipped as a noisy network rule.

## Validate and convert

These rules are validated with [sigma-cli](https://github.com/SigmaHQ/sigma-cli) (pySigma). To reproduce:

```sh
python3 -m venv .venv && . .venv/bin/activate
pip install sigma-cli

# structural + best-practice validation (expect: 0 errors, 0 issues)
sigma check detections/sigma/

# compile to a target query language, e.g. Splunk
sigma plugin install splunk
sigma convert -t splunk --without-pipeline detections/sigma/artex_enrich_user_agent.yml

# convert the whole tree so the correlation rules can resolve the atomic rules they reference by id
sigma convert -t splunk --without-pipeline detections/sigma/
```

Supported targets include Splunk, Elasticsearch, Microsoft Sentinel, QRadar, and others — see
`sigma plugin list`. Apply a processing pipeline for your product to map field names correctly.

## Contributing

Detection and hardening contributions are welcome. New rules should keep every indicator grounded in an
observable fact, state limitations in the `description`, pass `sigma check` cleanly, and avoid any content
that reads as attack guidance. See [`../CONTRIBUTING.md`](../CONTRIBUTING.md).
