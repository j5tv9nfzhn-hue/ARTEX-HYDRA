# ARTEX detection rule tests

> 한국어: 이 디렉터리는 [`../`](../)의 탐지 규칙이 실제로 발화하는지를 재현 가능하게 증명하는
> 회귀 테스트입니다. 바이너리 캡처를 저장소에 넣지 않고, 패킷 캡처를 매번 결정론적으로 생성한 뒤
> [Suricata](https://suricata.io)로 직접 돌려 경보 수를 확인합니다. 모든 테스트는 자신이 소유하거나
> 서면 허가를 받은 시스템을 지키는 **방어·탐지 목적에만** 쓰십시오.

Reproducible regression tests that prove the rules under [`../`](../) actually fire — and, just as
important, stay silent on benign traffic. A detection rule you cannot run is a claim; these tests turn the
claims in the rule files and the defense guide into something a reviewer can re-run from source.

No binary packet capture is committed. The capture is **synthesized deterministically on every run** and
removed afterwards, so the test ships as readable source, not as an opaque fixture, and never bloats the
repository.

## Suricata — [`suricata/`](suricata/)

[`suricata/run.sh`](suricata/run.sh) exercises the network rules in
[`../suricata/artex.rules`](../suricata/artex.rules) end to end and asserts three properties:

- **Presence** — sid `1000001` fires exactly once per enrichment probe.
- **Velocity** — sid `1000002` fires once the `detection_filter` rate of 30 requests in 300 s per source is
  crossed.
- **Specificity** — an identical capture whose only change is a benign browser User-Agent produces **zero**
  ARTEX alerts.

[`suricata/gen_pcap.py`](suricata/gen_pcap.py) builds the capture with [scapy](https://scapy.net): N
independent plaintext HTTP request/response flows from one fixed source, each carrying a chosen
User-Agent, at a fixed base timestamp spaced one second apart. It only writes a file — it never sends a
packet or touches a network.

### Run it

Needs only Docker; scapy and Suricata both run in containers.

```sh
detections/tests/suricata/run.sh
```

Expected output (abridged):

```
  PASS  sid 1000001 presence: one alert per probe  (got 35, want eq 35)
  PASS  sid 1000002 velocity: fires past 30-in-300s  (got 5, want ge 1)
  PASS  benign browser UA produces no ARTEX alerts  (got 0, want eq 0)
RESULT: PASS
```

The script exits non-zero if any assertion fails, so it drops straight into CI or a pre-commit hook.
Override the images with `SURICATA_IMAGE` / `PYTHON_IMAGE` if you mirror them internally.

### Why the velocity count is a floor, not an exact match

`run.sh` asserts the presence count (`1000001 == 35`) and the benign count (`== 0`) exactly, because those
are engine-version-independent: one alert per matching request, and no match on a different User-Agent. The
velocity rule's count depends on how a given Suricata release resolves the `detection_filter` threshold at
the boundary, so the test asserts `>= 1` and records the reference value separately. On **Suricata 8.0.7**
the reference run produces **5** alerts on sid `1000002` (flows 31–35, after the 30-in-300 s threshold is
crossed).

## Sigma

The Sigma rules under [`../sigma/`](../sigma/) are validated structurally and by compilation with
[sigma-cli](https://github.com/SigmaHQ/sigma-cli) — `sigma check` (0 errors) and `sigma convert` to a target
query language — as documented in [`../README.md`](../README.md). A live event-matching harness for the
generic `webserver` / `proxy` / `application` log sources is intentionally not shipped yet: matching those
authoritatively needs a backend that normalizes the fields, and a weak matcher would undercut the rules
rather than support them. Network rules are different — Suricata reads a pcap offline and emits the alert
record directly — which is why the reproducible matching test exists here first.

## Contributing

A new detection rule is stronger with a test that shows it firing. Tests should synthesize their own input
deterministically, assert engine-version-independent properties exactly (and softer ones as floors with a
recorded reference), and avoid any content that reads as attack guidance. See
[`../../CONTRIBUTING.md`](../../CONTRIBUTING.md) and the rule indexes in [`../README.md`](../README.md).
