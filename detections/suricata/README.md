# ARTEX detection rules (Suricata / network)

> 한국어: 이 디렉터리는 [방어·탐지 가이드(docs/defense-ko.md)](../../docs/defense-ko.md) 2·4절의
> 네트워크 관측 지문을 실제로 배포 가능한 [Suricata](https://suricata.io) 규칙으로 옮긴 것입니다.
> 로그·호스트 계층은 [`../sigma/`](../sigma/)(Sigma)가 담당합니다. 모든 규칙은 자신이 소유하거나
> 서면 허가를 받은 시스템을 지키는 **방어·탐지 목적에만** 사용하십시오.

The network-layer companion to the [Sigma rules](../sigma/). These [Suricata](https://suricata.io)
signatures cover the one ARTEX artifact that is observable on the wire, and every indicator is grounded in a
string or behaviour verified in this repository's source, not inferred. The host, log, and SIEM layers live
under [`../sigma/`](../sigma/); the defense guide ([Korean](../../docs/defense-ko.md) ·
[English](../../docs/defense-en.md)) explains the full picture.

## Rules — [`artex.rules`](artex.rules)

- **sid 1000001** — `ARTEX enrichment prober User-Agent`. An inbound HTTP `GET` whose User-Agent starts with
  `artex-enrich/` — the asset-enrichment prober (`enrich/enrich.go:233`). The single-request presence
  indicator. `classtype: attempted-recon`.
- **sid 1000002** — `ARTEX enrichment prober high-rate enumeration`. The same User-Agent crossing a
  `detection_filter` rate of **30 requests in 300 s per source** — the machine-speed velocity a single-hit
  rule misses. Mirrors the Sigma correlation `artex_enrich_scan_velocity`. `classtype: attempted-recon`.

## Scope and honesty — read before deploying

- **Only the enrich prober is network-observable.** ARTEX's actual attack traffic carries **no**
  ARTEX-specific fingerprint: the worker routes the tools it runs through a local recording proxy
  (`127.0.0.1:8788`) and those tools keep their own default User-Agents. Detect that traffic with generic
  scanner signatures and the behavioural SIEM rules under [`../sigma/`](../sigma/), not here.
- **The User-Agent is only visible in plaintext.** It appears where traffic is plaintext HTTP or inspected at
  a TLS-terminating proxy / WAF. End-to-end TLS encrypts it, so deploy these where you actually see the HTTP
  request buffer.
- **A static User-Agent can be changed** by the operator, so its absence does **not** mean safety. The durable
  signal is behaviour — rate and breadth — which is why sid 1000002 (and the Sigma correlation layer) key on
  velocity, and why pure web multi-stage detection needs base rules specific to your environment.
- **Deliberately omitted.** The self-update User-Agent `artex-selfupdate` travels over HTTPS to GitHub and is
  not network-observable (TLS SNI alone is too common to alert on). The audit-control marker is an
  operator-side log artifact, not target-facing traffic — detect it with
  [`../sigma/artex_guard_audit_framing.yml`](../sigma/artex_guard_audit_framing.yml). The server port `:8787`
  and recording proxy `127.0.0.1:8788` (`cmd/artex/main.go`) are host-forensic (`ss`/`netstat`), not a
  network signature.

## Validate and test

Validated with Suricata 8. The load test needs no traffic and always runs:

```sh
# syntax + engine load test (expect: "Configuration provided was successfully loaded")
docker run --rm -v "$PWD/detections/suricata":/r -w /r jasonish/suricata:latest \
  suricata -T -S artex.rules -l /tmp
```

To confirm the rules actually fire, run Suricata offline against a packet capture that contains a plaintext
HTTP request carrying the enrich User-Agent (for example, capture a loopback `curl -A 'artex-enrich/1.0'`
against a local server, or synthesize flows with scapy), then read the alerts:

```sh
suricata -r enrich.pcap -S artex.rules -l out && \
  grep -c '"signature_id":1000001' out/eve.json    # presence: one per probe
```

A reference run over 35 enrich probes from one source within ~35 s produced **35** alerts on sid 1000001 and
**5** on sid 1000002 (fired after the 30-in-300 s threshold was crossed); the same capture with a benign
browser User-Agent produced **0** alerts, confirming the signatures are specific.

## Contributing

Detection contributions are welcome. New rules should keep every indicator grounded in an observable fact,
state limitations in a comment, pass `suricata -T` cleanly, and avoid any content that reads as attack
guidance. See [`../../CONTRIBUTING.md`](../../CONTRIBUTING.md) and the Sigma layer in
[`../sigma/`](../sigma/) / [`../README.md`](../README.md).
