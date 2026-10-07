# ARTEX 호스트 분류(triage)

한국어 · [English](README.md)

[`artex_host_triage.py`](artex_host_triage.py) 는 ARTEX 가 실행된 정황이 의심되는 **호스트 한 대에서 직접**
돌리는 읽기 전용 분류(triage) 스크립트입니다. 이 디렉터리의 나머지 자료는 SIEM([Sigma](../sigma/))·네트워크
센서([Suricata](../suricata/))·위협 인텔리전스 플랫폼([침해지표](../indicators/))을 운용하는 방어자를 위한
것입니다. 이 스크립트는 그와 다른 대응자, 곧 SIEM 없이 의심 호스트의 셸 앞에 서서 로컬 상태만으로 "여기서
ARTEX 가 돌았는가"를 빠르고 근거 있게 답해야 하는 사람을 위한 것입니다.

이 스크립트는 디렉터리의 다른 자료가 담은 지문을 그대로 점검하고, 여기에 더해 **[침해지표
목록](../indicators/artex_indicators.csv)이 의도적으로 Sigma 규칙 없이 둔 세 가지 호스트·DB 지표**까지
점검합니다. 그 세 지표는 로그나 네트워크로 관측되지 않아 호스트에서 직접 확인할 수밖에 없는 것들입니다
(`server-listen-port`, `recording-proxy-endpoint`, `postgres-exploration-schema`).

## 무엇을 점검하는가

모든 점검 항목은 이 저장소 소스에서 확인한 문자열이나 경로에 근거하며, 각 발견에는 대응하는 Sigma 규칙이나
침해지표 행과 동일한 한계를 함께 적습니다.

- **리슨 포트**: `:8787`(관리 UI)과 `127.0.0.1:8788`(기록 프록시)을 확인합니다. 두 값은
  [`cmd/artex/main.go`](../../cmd/artex/main.go) 의 `--addr`·`--proxy` 플래그 기본값입니다. 실행 중인
  호스트에서는 `ss`·`netstat`·`lsof` 출력을 파싱하고, `--ports-from` 으로 넘긴 파일에서 읽을 수도 있습니다.
- **기록 프록시 아티팩트**: 기록기가 첫 실행 때 만드는 중간자(MITM) CA 파일
  `<데이터 디렉터리>/traffic/_ca/mitmproxy-ca-cert.pem` 과, 그 옆의 `_index/index.sqlite`·`_blobs/` 를
  확인합니다([`traffic/traffic.go`](../../traffic/traffic.go). 데이터 디렉터리 기본값은 실행 파일 옆의
  `data/` 입니다). 이 CA 는 오가는 HTTP(S) 를 복호화해 기록하는 중간자 트래픽 기록기의 신뢰 앵커입니다
  (MITRE ATT&CK T1557).
- **로그 마커**: 로그 파일에서 보강 프로브의 User-Agent `artex-enrich/1.0`
  ([`enrich/enrich.go`](../../enrich/enrich.go)), 자체 업데이트 송신의 User-Agent `artex-selfupdate`
  ([`selfupdate/github.go`](../../selfupdate/github.go)), 플랫폼 가드 감사 마커
  ([`guard/guard.go`](../../guard/guard.go))를 찾습니다. 가드 마커는 비(非)ASCII 프레이밍까지 원문 그대로
  두어 grep 이 실제로 일치하도록 했습니다.
- **PostgreSQL 탐색 스키마**: ARTEX 저장소의 이중 그래프 테이블(`exploration_nodes`·`_edges`·`_anchors` 와
  `assets`·`companies`·`activity`, 그리고 `agent_prompts` 시드)을 확인합니다
  ([`db/schema.sql`](../../db/schema.sql)). DSN 을 주면 `psql` 로 조회하고, `psql` 이 없으면 손으로 돌릴 수
  있는 읽기 전용 쿼리를 그대로 출력합니다.

발견은 **분류를 위한 단서이지 단정이 아닙니다.** 또한 어떤 항목도 걸리지 않았다고 해서 안전하다는 뜻은
아닙니다. 운영자는 바이너리 이름을 바꾸거나, 데이터 디렉터리를 옮기거나, 포트를 바꿀 수 있기 때문입니다.

## 사용법

```sh
# 호스트를 처음부터 끝까지 점검합니다
detections/triage/artex_host_triage.py \
    --data-dir /opt/artex/data \
    --log /var/log/syslog --log-dir /var/log/artex \
    --pg-dsn "$ARTEX_PG_DSN"

# 기계가 읽는 형식으로 출력하고, 하나라도 걸리면 비-0 으로 종료합니다
detections/triage/artex_host_triage.py --data-dir /opt/artex/data --json --exit-code

# 재현 가능한 픽스처 자가 테스트(호스트 상태를 건드리지 않습니다)
detections/triage/artex_host_triage.py --self-test
```

이 스크립트는 순수 Python 3 표준 라이브러리만 씁니다. 설치가 필요 없고, 네트워크를 쓰지 않으며,
`--self-test` 가 쓰는 자체 임시 디렉터리 말고는 아무 데도 쓰지 않습니다. 호스트 상태(열린 포트, 데이터
디렉터리, 로그 파일, 그리고 DSN 을 줄 때만 데이터베이스)를 읽어 발견한 내용을 출력합니다. 종료 코드는
기본적으로 `0` 입니다(게이트가 아니라 분류 용도입니다). `--exit-code` 를 주면 지표가 하나라도 걸렸을 때 `1`
로 종료합니다.

## 어떻게 정직함을 유지하는가

`--self-test` 는 합성 호스트를 만듭니다. 심어 둔 CA·색인·블롭 저장소가 있는 데이터 디렉터리, 각 마커가 든
로그, 포트 목록을 만든 뒤, 모든 점검이 그 위에서 발화하는지 단언하고, 이어서 깨끗한 호스트와 정상 로그에서는
발견이 **0** 건인지(오탐이 없는지) 단언합니다. 이 자가 테스트는 [`detections` CI
워크플로](../../.github/workflows/detections.yml)에 연결되어 있고 [`detections/tests/run-all.sh`](../tests/run-all.sh)
가 다시 돌립니다. 그래서 어떤 점검이 깨지거나, 지표가 grep 하는 소스 문자열에서 어긋나면 머지 게이트에서
실패합니다. 돌려 볼 수 없는 탐지는 주장일 뿐이라는 원칙을 따릅니다.
