# 상류 동기화와 번역 드리프트 방지 (메인테이너 안내)

이 문서는 **메인테이너**가 원본 저장소 [Autumn-27/ARTEX](https://github.com/Autumn-27/ARTEX)의
변경을 따라잡으면서 한국어 현지화를 유지하는 절차를 정리한 것입니다. 기여 범위·법적 책임·
현지화 방침은 [CONTRIBUTING.md](CONTRIBUTING.md)에, 사용자용 안내는 [README.md](README.md)에
있으므로, 이 문서는 그 방침을 **실제로 어떻게 집행하는지**에만 집중합니다.

현지화의 핵심 목표는 한 문장으로 요약됩니다. 원본의 **판단 성능을 그대로 보존하면서 사용자에게
보이는 산출물만 한국어로 바꾸는 것**입니다. 상류가 갱신될 때마다 이 경계가 흐트러지기 쉬우므로,
아래 절차와 검사로 번역 드리프트를 막습니다.

---

## 1. 현지화 구조 한눈에 보기

이 저장소는 상류 ARTEX 를 **포크**해서 그 이력 위에 한국어 현지화 커밋을 쌓은 구조입니다.
상류 `main` 의 모든 커밋이 이 저장소의 이력에 포함되어 있고, 그 위에 현지화 커밋이 더해져
있습니다. 따라서 상류 변경을 가져오는 일은 "상류 `main` 과의 차이를 확인하고, 보존할 것과
번역할 것을 가려서 반영하는 일"이 됩니다.

산출물은 세 갈래로 나뉩니다.

- **원문을 그대로 두는 자산**(아래 2절). 번역하면 성능이나 상류 대조가 깨집니다.
- **코드에 고정된 출력 언어 강제**. `agent/prompt.go` 의 `langDirective()` 가 각 역할의 system
  프롬프트 끝에 "사용자 노출 출력은 한국어로 작성하라"는 지시를 덧붙입니다.
- **한국어로 번역하는 사용자 노출 문자열**. UI 는 `web/messages/ko.json` 에, 서버의
  사용자 응답 문구는 각 Go 파일의 명명 상수에 둡니다.

---

## 2. 원문을 보존하는 자산 (번역 금지)

다음 자산은 번역하지 않고 원문(중국어 또는 영어)을 유지합니다. 상류 변경이 이 자산에 닿으면
**번역 없이 그대로 반영**합니다.

- **에이전트 내부 추론 프롬프트(두뇌 본문).** `agent/promptcatalog.go` 와 DB 시드
  `agent_prompts` 에 있는 행동 지침 본문입니다. 원문(중국어)으로 벤치마크된 동작을 유지해야
  하므로 번역하면 판단에 드리프트가 생깁니다.
- **표시와 에이전트 입력을 겸하는 문자열.** 활동 타임라인에 보이면서 동시에 플래너·리포터의
  입력 컨텍스트로 되먹여지는 일부 문구(작업 중단 사유, 가로채기 차단 메시지, 트래픽 증거 헬퍼
  등)는 하나의 레코드가 두 용도를 겸하므로 원문을 보존합니다. 판정 근거는
  `work/DECISIONS-FOR-JIWOO.md` 의 "두뇌 경계 기록"에 사안별로 적혀 있습니다.
- **원본 중국어 문서·문자열.** 문서는 `README.zh.md`, UI 문자열은 `web/messages/zh.json` 에
  원문을 그대로 남겨 상류 변경과 대조하기 쉽게 합니다. 한국어 번역은 `web/messages/ko.json`
  에만 채웁니다.
- **명령·페이로드·코드·URL·식별자·로그 원문.** 분석에 필요한 원본이므로 번역하지 않습니다.
  Go 코드 주석도 우선순위가 가장 낮아 상류 대조가 끝나는 시점까지 원문을 둡니다.

---

## 3. 상류 추적 베이스

상류 리모트가 다음과 같이 설정되어 있어야 합니다. 없다면 추가합니다.

```bash
git remote add upstream https://github.com/Autumn-27/ARTEX
git remote -v   # upstream 이 보이는지 확인
```

현재 현지화가 반영을 마친 상류 베이스 커밋은 다음과 같습니다.

- **베이스 = `d003372`** (상류 `main`, 2026-10-03, PR #189 `fix/sse-same-origin` 병합).

이 값은 "이 커밋까지의 상류 변경은 전부 이 저장소에 녹아 있다"는 뜻입니다. 상류 변경을 새로
반영할 때마다 이 베이스를 7절의 방법으로 갱신합니다.

---

## 4. 상류 변경을 가져오는 절차

### 4.1 상류를 내려받고 차이를 확인합니다

```bash
git fetch upstream
git rev-list --count d003372..upstream/main        # 미반영 상류 커밋 수
git log --oneline d003372..upstream/main           # 미반영 커밋 목록
```

`git fetch` 는 상류의 원격 추적 브랜치만 갱신하므로 작업 트리와 `HEAD` 에는 영향을 주지
않습니다. 미반영 커밋이 0 이면 상류와 동기화된 상태이므로 더 할 일이 없습니다.

### 4.2 변경 파일을 분류합니다

미반영 커밋이 어떤 파일을 건드렸는지 보고, 2절의 보존 자산과 번역 대상으로 나눕니다.

```bash
git log --name-status --oneline d003372..upstream/main
```

분류 기준은 다음과 같습니다.

- `agent/promptcatalog.go`·`agent_prompts` 시드, 그리고 2절의 표시 겸 입력 문자열이 바뀌었다면
  → **번역 없이 그대로 반영**합니다.
- Go 백엔드 로직(`db/`·`llmrec/`·`server/` 등)이 바뀌었다면 → 로직은 그대로 반영하되,
  **새로 생긴 사용자 응답 문구**(`writeErr` 등)가 있는지 확인해서 한국어 상수로 번역합니다.
- UI(`web/src/**`)가 바뀌어 **새 화면 문자열**이 생겼다면 → 하드코딩하지 말고
  `web/messages/zh.json`(원문)과 `web/messages/ko.json`(번역)에 같은 키로 추가합니다.

### 4.3 반영합니다

기능 단위로 병합하거나 선별 반영한 뒤, 4.2 에서 가려낸 새 문자열을 한국어로 번역합니다.
병합 과정에서 `ko.json`·`zh.json` 의 키가 어긋나거나 사용자 노출 자리에 원문이 새어 들어오기
쉬우므로, 반영 직후 반드시 5절의 검사를 돌립니다.

> **예시(2026-10-05 기준 미반영 커밋).** `git fetch upstream` 결과 상류 `main` 이
> `b55ceb1` 로 앞서 있고, 베이스 `d003372` 대비 커밋 2개(`86729b6` 모델 폴백 승인 토큰 계량
> 기능 + 병합 커밋 `b55ceb1`)가 미반영입니다. 이 커밋은 `db/llm_usage.go`·`llmrec/llmrec.go`·
> `server/intercept.go`·`server/server.go` 같은 Go 로직과 `web/src/app/(main)/system/intercept/page.tsx`·
> `web/src/lib/api.ts`·`web/src/lib/mock/handler.ts`·`web/src/lib/types.ts` 를 건드립니다.
> 따라서 메인테이너는 Go 로직은 그대로 반영하고, intercept 설정 페이지에 새로 생긴 화면
> 문자열만 `ko.json`·`zh.json` 키로 추출·번역하면 됩니다. (이 두 커밋은 이 문서를 쓴 시점에는
> 아직 반영하지 않았으므로 베이스는 `d003372` 로 둡니다.)

---

## 5. 번역 대칭과 드리프트 검사

상류 반영이나 번역 작업 뒤에 아래 세 가지를 확인합니다.

### 5.1 ko ↔ zh 메시지 대칭과 사용자 노출 CJK

`ko.json` 과 `zh.json` 의 키가 정확히 같고, `ko.json` 값에 중국어 한자가 남아 있지 않아야
합니다. 아래 스크립트가 세 수치를 출력합니다.

```bash
python3 - <<'PY'
import json, re
ko = json.load(open('web/messages/ko.json'))
zh = json.load(open('web/messages/zh.json'))
def flatten(d, p=''):
    out = {}
    if isinstance(d, dict):
        for k, v in d.items(): out.update(flatten(v, p + '/' + k))
    elif isinstance(d, list):
        for i, v in enumerate(d): out.update(flatten(v, p + '/' + str(i)))
    else: out[p] = d
    return out
fk, fz = flatten(ko), flatten(zh)
han = re.compile(r'[㐀-鿿]')
print('ko leaf keys :', len(fk))
print('zh leaf keys :', len(fz))
print('key symdiff  :', len(set(fk) ^ set(fz)))        # 0 이어야 함
print('ko vals w/CJK:', sum(1 for v in fk.values() if isinstance(v, str) and han.search(v)))  # 0 이어야 함
PY
```

기준값(2026-10-05): `ko leaf keys = 2950`, `zh leaf keys = 2950`, `key symdiff = 0`,
`ko vals w/CJK = 0`. 키 수는 상류 반영으로 늘 수 있지만, ko 와 zh 는 항상 같아야 하고
`key symdiff` 와 `ko vals w/CJK` 는 항상 0 이어야 합니다.

### 5.2 두뇌 자산의 원문 보존 확인

두뇌 본문은 중국어 원문을 유지하므로, 아래 검사에서 **한자 라인 수가 0 으로 떨어지면** 오히려
두뇌가 실수로 번역돼 오염됐다는 신호입니다.

```bash
python3 -c "import re; han=re.compile(r'[㐀-鿿]'); t=open('agent/promptcatalog.go').read(); print('promptcatalog.go CJK lines =', sum(1 for l in t.splitlines() if han.search(l)))"
```

기준값(2026-10-05): `promptcatalog.go CJK lines = 70`. 이 수가 크게 줄면 두뇌 본문이 번역됐는지
확인합니다.

### 5.3 빌드 산출물에 원문이 새지 않는지

UI 를 정적으로 내보낸 뒤 프리렌더 HTML 에 중국어가 보이면 번역 누락입니다.

```bash
cd web && npm ci && NEXT_EXPORT=1 npm run build   # out/ 생성
# out/**/*.html 에서 가시 텍스트의 중국어 한자가 0 인지 확인
```

---

## 6. 빌드와 테스트로 마무리 검증

반영·번역 뒤에는 [CONTRIBUTING.md 의 개발 환경](CONTRIBUTING.md#개발-환경) 절차대로 백엔드와
프런트엔드를 검증합니다. 로컬에 Go 가 없으면 Docker 로 동일하게 돌릴 수 있습니다.

```bash
docker run --rm -v "$PWD":/src -w /src \
  -v artexko-gomod:/go/pkg/mod -v artexko-gocache:/root/.cache/go-build \
  golang:1.26 sh -c 'go build ./... && go vet ./... && go test ./... -count=1'
```

사용자 노출 문구를 번역할 때는 그 문구를 단언하는 회귀 테스트(`*_localized_test.go`)를 함께
두어, 나중에 상류 변경이 다시 중국어를 끌어와도 테스트가 잡게 합니다. 번역 검증은 반드시
역량 있는(프런티어급) 모델로 합니다. 저가·소형 모델은 출력이 원문으로 되돌아갈 수 있어
번역 적용 여부를 그 출력만으로 판단하면 안 됩니다.

---

## 7. 베이스 갱신 기록

상류 변경을 반영하고 검증까지 마쳤다면, 이 문서 3절의 **베이스 커밋 값을 새 상류 커밋으로
갱신**하고 그 변경을 같은 커밋 또는 뒤따르는 커밋에 포함합니다. 이렇게 해두면 다음 메인테이너가
"어디까지 반영됐는지"를 이 문서 한 곳에서 확인할 수 있습니다.

커밋 메시지는 [CONTRIBUTING.md 의 커밋 메시지 규칙](CONTRIBUTING.md#커밋-메시지)을 따릅니다.
예를 들어 상류 동기화 커밋은 다음과 같이 적습니다.

```
chore(upstream): 상류 d003372..b55ceb1 반영 (intercept 토큰 계량) + 신규 UI 문자열 번역
```
