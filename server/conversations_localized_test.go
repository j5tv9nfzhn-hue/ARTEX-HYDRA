package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// conversations.go 의 대화(채팅) API 에러 응답을 한국어로 유지하는 회귀 방어 테스트다.
// 한국어 판정은 F3a 가 만든 assertKoreanError(한글 포함·중국어 한자 0)를 재사용한다.

// TestConversationErrorConstantsLocalized 는 응답 상수 12종이 전부 한국어임을 단언한다.
// 핸들러는 s.pg(w)(DB)를 먼저 거쳐 DB 없는 이 호스트에서 끝까지 못 도므로, 상수 자체를
// 단언한다(auth.go 선례). %d 가 든 형식 문자열은 실제 인자로 채워 최종 문구를 검사한다.
func TestConversationErrorConstantsLocalized(t *testing.T) {
	cases := []struct {
		name string
		msg  string
	}{
		{"request_too_large", convErrRequestTooLarge},
		{"agent_key_empty", convErrAgentKeyEmpty},
		{"agent_key_too_long", fmt.Sprintf(convErrAgentKeyTooLong, maxConversationAgentKeyRunes)},
		{"agent_not_found", convErrAgentNotFound},
		{"llm_profile", convErrLLMProfile},
		{"title_too_long", fmt.Sprintf(convErrTitleTooLong, maxConversationTitleRunes)},
		{"title_or_pinned", convErrTitleOrPinned},
		{"title_empty", convErrTitleEmpty},
		{"bad_conv_id", convErrBadConvID},
		{"ids_count", fmt.Sprintf(convErrIDsCount, maxConversationDeleteBatch)},
		{"message_empty", convErrMessageEmpty},
		{"busy", convErrBusy},
	}
	for _, c := range cases {
		assertKoreanError(t, c.name, c.msg)
	}
}

// TestDecodeConversationRequestTooLargeLocalized 는 요청 본문 초과 경로를 실제 HTTP 응답
// 본문까지 검사한다. decodeConversationRequest 는 Server(DB)를 거치지 않는 패키지 함수라
// DB 없이 돌 수 있고, 상수가 응답에 실제로 실리는 연결까지 확인한다(요청 본문 초과 →
// 413 + 한국어 문구).
func TestDecodeConversationRequestTooLargeLocalized(t *testing.T) {
	// 64KB 한도를 넘기는 유효 JSON 본문. MaxBytesReader 가 읽기 도중 한도 초과를 돌려준다.
	body := `{"title":"` + strings.Repeat("a", maxConversationRequestBytes+1024) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/conversations", strings.NewReader(body))
	rec := httptest.NewRecorder()
	var dst struct {
		Title string `json:"title"`
	}
	if decodeConversationRequest(rec, req, &dst) {
		t.Fatal("본문이 한도를 넘었는데 decodeConversationRequest 가 true 를 돌려줬다")
	}
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("상태 코드 = %d, 기대 = %d", rec.Code, http.StatusRequestEntityTooLarge)
	}
	var resp struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("응답 JSON 파싱 실패: %v (본문=%q)", err, rec.Body.String())
	}
	if resp.Error != convErrRequestTooLarge {
		t.Fatalf("응답 error = %q, 기대 = %q", resp.Error, convErrRequestTooLarge)
	}
	assertKoreanError(t, "decode.too_large.response", resp.Error)
}

// TestConversationDefaultTitlesLocalized 는 대화 기본 제목 두 상수(F8)가 한국어이고 서로
// 구별됨을 단언한다. convDefaultTitle 은 생성 기본값이자 자동 제목 분기의 센티넬이므로,
// 중국어 "新对话" 로 되돌아가면 사용자가 대화 목록·삭제 다이얼로그에서 중국어를 보게 된다.
func TestConversationDefaultTitlesLocalized(t *testing.T) {
	assertKoreanError(t, "default_title", convDefaultTitle)
	assertKoreanError(t, "attachment_title", convAttachmentTitle)
	if convDefaultTitle == convAttachmentTitle {
		t.Fatal("기본 제목과 첨부 기본 제목이 같으면 안 된다")
	}
}

// TestIsDefaultConversationTitle 는 자동 제목 분기의 판정을 핀 고정한다. 생성 기본값
// (convDefaultTitle)과 빈 제목은 자동 제목 대상이고, 사용자가 지은 제목은 아니다. 생성
// 기본값과 센티넬이 같은 상수라 둘이 어긋나 자동 제목이 안 붙는 회귀를 막는다.
func TestIsDefaultConversationTitle(t *testing.T) {
	if !isDefaultConversationTitle("") {
		t.Fatal("빈 제목은 자동 제목 대상이어야 한다")
	}
	if !isDefaultConversationTitle(convDefaultTitle) {
		t.Fatalf("생성 기본값 %q 는 자동 제목 대상이어야 한다", convDefaultTitle)
	}
	if isDefaultConversationTitle("사용자가 지은 제목") {
		t.Fatal("사용자가 지은 제목은 자동 제목 대상이 아니어야 한다")
	}
}
