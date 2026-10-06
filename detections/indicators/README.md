# ARTEX indicators (machine-readable)

> 한국어: [`artex_indicators.csv`](artex_indicators.csv) 는 ARTEX 가 실제로 내보내는 고유 지문(침해지표,
> IoC)을 한 파일로 모은 것입니다. 위협 인텔리전스 플랫폼·SIEM 조회 테이블·호스트 분류 작업에 바로
> 넣을 수 있게 기계가 읽는 CSV 로 둡니다. 모든 값은 이 저장소 소스에서 확인한 문자열이며, 각 행의
> 출처 파일과 탐지 규칙을 함께 적습니다. 배경 설명은 [방어·탐지 가이드(docs/defense-ko.md)](../../docs/defense-ko.md)
> 2절 "방어자가 관측할 수 있는 지문"에 있습니다. 자신이 소유하거나 서면 허가를 받은 시스템을 지키는
> **방어·탐지 목적에만** 사용하십시오.

A single, machine-readable list of the unique fingerprints ARTEX itself emits, for defenders who want the
atomic indicators rather than the detection logic: drop [`artex_indicators.csv`](artex_indicators.csv)
into a threat-intelligence platform, a SIEM lookup table, or a host-triage checklist. Every value is a
string verified in this repository's source — the same grounding the
[detection rules](../README.md) and the defense guide
([Korean](../../docs/defense-ko.md) · [English](../../docs/defense-en.md), section 2) rely on — and each
row records where it comes from and which rule (if any) is built on it.

## Columns

- **`id`** — a stable slug for the indicator.
- **`type`** — the kind of indicator: `http.user-agent`, `string` (a literal to hunt for in logs/files),
  `port`, or `ip-dst|port`. These map onto the equivalent MISP/STIX attribute types.
- **`value`** — the exact indicator. Preserved verbatim, including the non-ASCII guard marker.
- **`perspective`** — `target` (observable in traffic *toward* a system ARTEX probes) or `forensic`
  (observable *on* a host where ARTEX ran or was relayed through). The defense guide keeps these apart on
  purpose; mixing them produces false conclusions.
- **`source`** — the repository-relative source file(s) that emit the value, `;`-separated. This is the
  grounding: if an upstream re-sync changes the emitter, the indicator here must change with it.
- **`rule`** — the detection rule(s) built on the exact value, `;`-separated, or empty for host-forensic
  indicators that are triaged directly rather than shipped as a (noisy) rule.
- **`description`** — a one-line note, including the honest caveat where one applies.

## How to read this honestly

- **These are changeable fingerprints, not proof of safety.** An operator can set a different User-Agent
  or change a default port, so the *absence* of any value here does **not** mean ARTEX is absent. The
  durable signal is behaviour — see the correlation rules and sections 1, 2, and 4.1–4.2 of the defense
  guide.
- **Generic hunting leads are deliberately excluded.** Destructive shell/DB commands (`rm -rf`, `DROP
  DATABASE`, …) are *not* ARTEX fingerprints — legitimate administrators run them too. They are a hunting
  lead, not an import-ready indicator, so they live in
  [`destructive_command_hunting.yml`](../sigma/destructive_command_hunting.yml) and the defense guide, not
  in this list. Importing them as blocking indicators would cause false positives.
- **Host-forensic ports are for triage, not blocking.** `:8787` and `127.0.0.1:8788` describe a host that
  may be running ARTEX; check them with `ss`/`netstat`, do not firewall them blindly.

## Verification

The list is covered by the [indicator source-of-truth test](../tests/indicators/run.sh): it re-reads this
CSV and asserts, for every row, that the value is still present in the cited source file(s) and pinned in
the cited rule(s), and that every indicator the test grounds appears in the list. A row that drifts from
the source, or a known fingerprint dropped from the list, fails the test. Run it with:

```sh
detections/tests/indicators/run.sh
```
