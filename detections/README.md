# ARTEX detection rules (Sigma)

> 한국어: 이 디렉터리는 [방어·탐지 가이드(docs/defense-ko.md)](../docs/defense-ko.md)의 4절 "탐지 규칙"을
> 실제로 배포 가능한 [Sigma](https://sigmahq.io) 규칙으로 옮긴 것입니다. 모든 규칙은 자신이 소유하거나 서면
> 허가를 받은 시스템을 지키는 **방어·탐지 목적에만** 사용하십시오.

Deployable [Sigma](https://sigmahq.io) rules that formalize the pseudo-rules in the defense guide
([Korean](../docs/defense-ko.md) · [English](../docs/defense-en.md), section 4) into a vendor-neutral
format you can convert to your own SIEM or EDR query language. Every indicator here is grounded in a
string or behaviour verified in this repository's source, not inferred.

## Rules

- **`sigma/artex_enrich_user_agent.yml`** — inbound `artex-enrich/1.0` User-Agent from ARTEX asset
  enrichment (`enrich/enrich.go`). Target-side, supporting indicator. `level: high`.
- **`sigma/artex_selfupdate_egress.yml`** — outbound `artex-selfupdate` User-Agent from the self-update
  routine (`selfupdate/github.go`). Host/forensic egress indicator. `level: medium`.
- **`sigma/artex_guard_audit_framing.yml`** — the platform-guard control marker written to the audit log
  on a blocked tool call (`guard/guard.go`). Host/forensic indicator. `level: high`.
- **`sigma/destructive_command_hunting.yml`** — destructive shell/DB commands mirroring the ARTEX guard's
  built-in deny list (`db/db.go` seed). Generic hunting lead, not an ARTEX signature. `level: medium`.

## How to read these honestly

- **Static indicators can be changed.** An operator can set a different User-Agent, so the absence of
  `artex-enrich/1.0` or `artex-selfupdate` does **not** mean safety. The durable signal is *behaviour* —
  a single source chaining recon → enumeration → probing → auth/injection attempts, adapting to responses,
  running without pause. That layer is described in the defense guide (sections 1, 2, and 4.1–4.2) and does
  not reduce to a single atomic rule; build it as a correlation rule in your SIEM.
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
```

Supported targets include Splunk, Elasticsearch, Microsoft Sentinel, QRadar, and others — see
`sigma plugin list`. Apply a processing pipeline for your product to map field names correctly.

## Contributing

Detection and hardening contributions are welcome. New rules should keep every indicator grounded in an
observable fact, state limitations in the `description`, pass `sigma check` cleanly, and avoid any content
that reads as attack guidance. See [`../CONTRIBUTING.md`](../CONTRIBUTING.md).
