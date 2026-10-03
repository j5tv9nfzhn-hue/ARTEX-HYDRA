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
