package ws

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"inlinechat/services/realtime-service/internal/authclient"
	"inlinechat/services/realtime-service/internal/chatclient"
)

const testAllowedOrigin = "http://example.com"

type chatCall struct {
	conversationID uint64
	req            chatclient.CreateMessageRequest
}

type fakeChatClient struct {
	mu              sync.Mutex
	calls           []chatCall
	resp            *chatclient.Message
	err             error
	conversation    *chatclient.Conversation
	conversationErr error
	listMessages    []*chatclient.Message
	listMessagesErr error
}

type fakeAuthClient struct {
	mu             sync.Mutex
	meResp         *authclient.MeResult
	meErr          error
	authorizations []string
}

func (f *fakeAuthClient) Me(_ context.Context, authorization string) (*authclient.MeResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.authorizations = append(f.authorizations, authorization)
	if f.meErr != nil {
		return nil, f.meErr
	}
	if f.meResp == nil {
		return &authclient.MeResult{
			AgentID: 7,
			Role:    "agent",
		}, nil
	}
	cp := *f.meResp
	return &cp, nil
}

func (f *fakeChatClient) CreateMessage(_ context.Context, conversationID uint64, req chatclient.CreateMessageRequest) (*chatclient.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, chatCall{conversationID: conversationID, req: req})
	if f.err != nil {
		return nil, f.err
	}
	if f.resp != nil {
		cp := *f.resp
		return &cp, nil
	}
	return &chatclient.Message{ID: 1, ConversationID: conversationID, Status: "sent"}, nil
}

func (f *fakeChatClient) GetConversation(_ context.Context, conversationID uint64) (*chatclient.Conversation, error) {
	if f.conversationErr != nil {
		return nil, f.conversationErr
	}
	if f.conversation != nil {
		cp := *f.conversation
		return &cp, nil
	}
	return &chatclient.Conversation{
		ID:           conversationID,
		VisitorToken: "vt_1",
		Status:       "open",
	}, nil
}

func (f *fakeChatClient) ListMessages(_ context.Context, conversationID uint64, in chatclient.ListMessagesInput) ([]*chatclient.Message, error) {
	if f.listMessagesErr != nil {
		return nil, f.listMessagesErr
	}
	out := make([]*chatclient.Message, 0, len(f.listMessages))
	for i := range f.listMessages {
		item := f.listMessages[i]
		if item == nil {
			continue
		}
		cp := *item
		if cp.ConversationID == 0 {
			cp.ConversationID = conversationID
		}
		if in.BeforeID > 0 && cp.ID >= in.BeforeID {
			continue
		}
		out = append(out, &cp)
		if in.Limit > 0 && len(out) >= in.Limit {
			break
		}
	}
	return out, nil
}

func (f *fakeChatClient) lastCall() (chatCall, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return chatCall{}, false
	}
	return f.calls[len(f.calls)-1], true
}

func TestHandlerMessageSendDefaultVisitor(t *testing.T) {
	gin.SetMode(gin.TestMode)

	fakeClient := &fakeChatClient{resp: &chatclient.Message{ID: 11, ConversationID: 1, Status: "sent"}}
	handler := NewHandler(NewHub(), fakeClient, nil, []string{testAllowedOrigin}, time.Second, "secret", "", "inlinechat-auth", zap.NewNop())

	ts := newWSTestServer(t, handler)

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/1?visitor_token=vt_1"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, wsDialHeader())
	if err != nil {
		t.Fatalf("dial websocket failed: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(map[string]any{
		"type": "message.send",
		"payload": map[string]any{
			"content":       "hello",
			"client_msg_id": "c1",
			"visitor_token": "vt_1",
		},
	}); err != nil {
		t.Fatalf("write ws message failed: %v", err)
	}

	_, raw, err := readWSMessage(t, conn)
	if err != nil {
		t.Fatalf("read ws message failed: %v", err)
	}

	var ack map[string]any
	if err := json.Unmarshal(raw, &ack); err != nil {
		t.Fatalf("unmarshal ack failed: %v", err)
	}
	if ack["type"] != "message.ack" {
		t.Fatalf("expected message.ack, got %v", ack)
	}

	call, ok := fakeClient.lastCall()
	if !ok {
		t.Fatal("expected CreateMessage call")
	}
	if call.req.SenderType != "visitor" {
		t.Fatalf("expected sender_type visitor, got %s", call.req.SenderType)
	}
}

func TestHandlerMessageSendAgentWithToken(t *testing.T) {
	gin.SetMode(gin.TestMode)

	secret := "secret"
	issuer := "inlinechat-auth"
	token := mustIssueAgentToken(t, secret, issuer, 7, "agent")

	fakeClient := &fakeChatClient{resp: &chatclient.Message{ID: 22, ConversationID: 2, Status: "sent"}}
	fakeAuth := &fakeAuthClient{meResp: &authclient.MeResult{AgentID: 7, Role: "agent"}}
	handler := NewHandler(NewHub(), fakeClient, fakeAuth, []string{testAllowedOrigin}, time.Second, secret, "", issuer, zap.NewNop())

	ts := newWSTestServer(t, handler)

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/2?access_token=" + url.QueryEscape(token)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, wsDialHeader())
	if err != nil {
		t.Fatalf("dial websocket failed: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(map[string]any{
		"type": "message.send",
		"payload": map[string]any{
			"sender_type":   "agent",
			"sender_id":     "999",
			"content":       "from agent",
			"client_msg_id": "a1",
		},
	}); err != nil {
		t.Fatalf("write ws message failed: %v", err)
	}

	if _, _, err := readWSMessage(t, conn); err != nil {
		t.Fatalf("read ws message failed: %v", err)
	}

	call, ok := fakeClient.lastCall()
	if !ok {
		t.Fatal("expected CreateMessage call")
	}
	if call.req.SenderType != "agent" {
		t.Fatalf("expected sender_type agent, got %s", call.req.SenderType)
	}
	if call.req.SenderID != "7" {
		t.Fatalf("expected sender_id from token=7, got %s", call.req.SenderID)
	}
}

func TestHandlerMessageSendAgentWithoutToken(t *testing.T) {
	gin.SetMode(gin.TestMode)

	fakeClient := &fakeChatClient{}
	handler := NewHandler(NewHub(), fakeClient, nil, []string{testAllowedOrigin}, time.Second, "secret", "", "inlinechat-auth", zap.NewNop())

	ts := newWSTestServer(t, handler)

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/3?visitor_token=vt_1"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, wsDialHeader())
	if err != nil {
		t.Fatalf("dial websocket failed: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(map[string]any{
		"type": "message.send",
		"payload": map[string]any{
			"sender_type":   "agent",
			"content":       "x",
			"client_msg_id": "a2",
		},
	}); err != nil {
		t.Fatalf("write ws message failed: %v", err)
	}

	_, raw, err := readWSMessage(t, conn)
	if err != nil {
		t.Fatalf("read ws message failed: %v", err)
	}

	var env map[string]any
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal error envelope failed: %v", err)
	}
	if env["type"] != "message.nack" {
		t.Fatalf("expected message.nack, got %v", env)
	}
	payload, _ := env["payload"].(map[string]any)
	if payload == nil {
		t.Fatalf("missing nack payload: %v", env)
	}
	if payload["client_msg_id"] != "a2" {
		t.Fatalf("expected client_msg_id a2, got %v", payload["client_msg_id"])
	}
}

func TestHandlerMessageSendCreateMessageFailedReturnsNack(t *testing.T) {
	gin.SetMode(gin.TestMode)

	fakeClient := &fakeChatClient{err: context.DeadlineExceeded}
	handler := NewHandler(NewHub(), fakeClient, nil, []string{testAllowedOrigin}, time.Second, "secret", "", "inlinechat-auth", zap.NewNop())

	ts := newWSTestServer(t, handler)

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/5?visitor_token=vt_1"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, wsDialHeader())
	if err != nil {
		t.Fatalf("dial websocket failed: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(map[string]any{
		"type": "message.send",
		"payload": map[string]any{
			"content":       "x",
			"client_msg_id": "c-timeout",
			"visitor_token": "vt_1",
		},
	}); err != nil {
		t.Fatalf("write ws message failed: %v", err)
	}

	_, raw, err := readWSMessage(t, conn)
	if err != nil {
		t.Fatalf("read ws message failed: %v", err)
	}

	var env map[string]any
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal nack envelope failed: %v", err)
	}
	if env["type"] != "message.nack" {
		t.Fatalf("expected message.nack, got %v", env)
	}
}

func TestHandlerMessageSendTooLongReturnsNack(t *testing.T) {
	gin.SetMode(gin.TestMode)

	fakeClient := &fakeChatClient{}
	handler := NewHandler(NewHub(), fakeClient, nil, []string{testAllowedOrigin}, time.Second, "secret", "", "inlinechat-auth", zap.NewNop())

	ts := newWSTestServer(t, handler)

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/8?visitor_token=vt_1"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, wsDialHeader())
	if err != nil {
		t.Fatalf("dial websocket failed: %v", err)
	}
	defer conn.Close()

	longContent := strings.Repeat("a", maxMessageContentChars+1)
	if err := conn.WriteJSON(map[string]any{
		"type": "message.send",
		"payload": map[string]any{
			"content":       longContent,
			"client_msg_id": "c-too-long",
			"visitor_token": "vt_1",
		},
	}); err != nil {
		t.Fatalf("write ws message failed: %v", err)
	}

	_, raw, err := readWSMessage(t, conn)
	if err != nil {
		t.Fatalf("read ws message failed: %v", err)
	}

	var env map[string]any
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal nack envelope failed: %v", err)
	}
	if env["type"] != "message.nack" {
		t.Fatalf("expected message.nack, got %v", env)
	}
	if _, ok := fakeClient.lastCall(); ok {
		t.Fatal("CreateMessage should not be called when content is too long")
	}
}

func TestHandlerRejectInvalidAccessToken(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := NewHandler(NewHub(), &fakeChatClient{}, nil, []string{testAllowedOrigin}, time.Second, "secret", "", "inlinechat-auth", zap.NewNop())
	ts := newWSTestServer(t, handler)

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/4?access_token=bad"
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, wsDialHeader())
	if err == nil {
		t.Fatal("expected websocket handshake error")
	}
	if resp == nil || resp.StatusCode != 401 {
		t.Fatalf("expected 401 handshake response, got %+v err=%v", resp, err)
	}
}

func TestHandlerRejectAccessTokenInvalidatedByAuthService(t *testing.T) {
	gin.SetMode(gin.TestMode)

	secret := "secret"
	issuer := "inlinechat-auth"
	token := mustIssueAgentToken(t, secret, issuer, 7, "agent")
	fakeAuth := &fakeAuthClient{
		meErr: status.Error(codes.Unauthenticated, "invalid token"),
	}
	handler := NewHandler(NewHub(), &fakeChatClient{}, fakeAuth, []string{testAllowedOrigin}, time.Second, secret, "", issuer, zap.NewNop())
	ts := newWSTestServer(t, handler)

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/4?access_token=" + url.QueryEscape(token)
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, wsDialHeader())
	if err == nil {
		t.Fatal("expected websocket handshake error")
	}
	if resp == nil || resp.StatusCode != 401 {
		t.Fatalf("expected 401 handshake response, got %+v err=%v", resp, err)
	}

	fakeAuth.mu.Lock()
	defer fakeAuth.mu.Unlock()
	if len(fakeAuth.authorizations) != 1 {
		t.Fatalf("expected auth me called once, got %d", len(fakeAuth.authorizations))
	}
	if fakeAuth.authorizations[0] != "Bearer "+token {
		t.Fatalf("unexpected authorization header: %s", fakeAuth.authorizations[0])
	}
}

func TestHandlerRejectMissingVisitorToken(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := NewHandler(NewHub(), &fakeChatClient{}, nil, []string{testAllowedOrigin}, time.Second, "secret", "", "inlinechat-auth", zap.NewNop())
	ts := newWSTestServer(t, handler)

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/6"
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, wsDialHeader())
	if err == nil {
		t.Fatal("expected websocket handshake error")
	}
	if resp == nil || resp.StatusCode != 401 {
		t.Fatalf("expected 401 handshake response, got %+v err=%v", resp, err)
	}
}

func TestHandlerRejectDisallowedOrigin(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := NewHandler(NewHub(), &fakeChatClient{}, nil, []string{testAllowedOrigin}, time.Second, "secret", "", "inlinechat-auth", zap.NewNop())
	ts := newWSTestServer(t, handler)

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/6?visitor_token=vt_1"
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, wsDialHeaderWithOrigin("http://evil.example.com"))
	if err == nil {
		t.Fatal("expected websocket handshake error")
	}
	if resp == nil || resp.StatusCode != 403 {
		t.Fatalf("expected 403 handshake response, got %+v err=%v", resp, err)
	}
}

func TestHandlerReplayMessagesAfterLastMessageID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	fakeClient := &fakeChatClient{
		listMessages: []*chatclient.Message{
			{ID: 15, ConversationID: 1, SenderType: "agent", Content: "m15", ClientMsgID: "c15", Status: "sent"},
			{ID: 14, ConversationID: 1, SenderType: "visitor", Content: "m14", ClientMsgID: "c14", Status: "sent"},
			{ID: 13, ConversationID: 1, SenderType: "agent", Content: "m13", ClientMsgID: "c13", Status: "sent"},
		},
	}
	handler := NewHandler(NewHub(), fakeClient, nil, []string{testAllowedOrigin}, time.Second, "secret", "", "inlinechat-auth", zap.NewNop())

	ts := newWSTestServer(t, handler)

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/1?visitor_token=vt_1&last_message_id=13"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, wsDialHeader())
	if err != nil {
		t.Fatalf("dial websocket failed: %v", err)
	}
	defer conn.Close()

	_, firstRaw, err := readWSMessage(t, conn)
	if err != nil {
		t.Fatalf("read first replay message failed: %v", err)
	}
	_, secondRaw, err := readWSMessage(t, conn)
	if err != nil {
		t.Fatalf("read second replay message failed: %v", err)
	}
	_, endRaw, err := readWSMessage(t, conn)
	if err != nil {
		t.Fatalf("read replay end message failed: %v", err)
	}

	firstID := mustReadMessageIDFromEvent(t, firstRaw)
	secondID := mustReadMessageIDFromEvent(t, secondRaw)
	if firstID != 14 || secondID != 15 {
		t.Fatalf("expected replay order 14 -> 15, got %d -> %d", firstID, secondID)
	}
	replayEnd := mustReadReplayEndPayload(t, endRaw)
	if replayEnd.LastMessageID != 15 {
		t.Fatalf("expected replay end last_message_id=15, got %d", replayEnd.LastMessageID)
	}
	if replayEnd.ReplayedCount != 2 {
		t.Fatalf("expected replayed_count=2, got %d", replayEnd.ReplayedCount)
	}
	if replayEnd.Truncated {
		t.Fatal("expected truncated=false")
	}
	if replayEnd.NextBeforeID != 0 {
		t.Fatalf("expected next_before_id=0, got %d", replayEnd.NextBeforeID)
	}
}

func TestHandlerReplayMessagesTruncatedSendsNextBeforeID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	previousLimit := maxReplayMessages
	maxReplayMessages = 3
	t.Cleanup(func() {
		maxReplayMessages = previousLimit
	})

	fakeClient := &fakeChatClient{
		listMessages: []*chatclient.Message{
			{ID: 16, ConversationID: 1, SenderType: "agent", Content: "m16", ClientMsgID: "c16", Status: "sent"},
			{ID: 15, ConversationID: 1, SenderType: "agent", Content: "m15", ClientMsgID: "c15", Status: "sent"},
			{ID: 14, ConversationID: 1, SenderType: "agent", Content: "m14", ClientMsgID: "c14", Status: "sent"},
			{ID: 13, ConversationID: 1, SenderType: "agent", Content: "m13", ClientMsgID: "c13", Status: "sent"},
			{ID: 12, ConversationID: 1, SenderType: "agent", Content: "m12", ClientMsgID: "c12", Status: "sent"},
		},
	}
	handler := NewHandler(NewHub(), fakeClient, nil, []string{testAllowedOrigin}, time.Second, "secret", "", "inlinechat-auth", zap.NewNop())

	ts := newWSTestServer(t, handler)
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/1?visitor_token=vt_1&last_message_id=1"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, wsDialHeader())
	if err != nil {
		t.Fatalf("dial websocket failed: %v", err)
	}
	defer conn.Close()

	gotIDs := make([]uint64, 0, 3)
	for i := 0; i < 3; i++ {
		_, raw, readErr := readWSMessage(t, conn)
		if readErr != nil {
			t.Fatalf("read replay message failed: %v", readErr)
		}
		gotIDs = append(gotIDs, mustReadMessageIDFromEvent(t, raw))
	}
	if gotIDs[0] != 14 || gotIDs[1] != 15 || gotIDs[2] != 16 {
		t.Fatalf("expected replay order 14 -> 15 -> 16, got %v", gotIDs)
	}

	_, endRaw, err := readWSMessage(t, conn)
	if err != nil {
		t.Fatalf("read replay end message failed: %v", err)
	}
	replayEnd := mustReadReplayEndPayload(t, endRaw)
	if replayEnd.LastMessageID != 16 {
		t.Fatalf("expected replay end last_message_id=16, got %d", replayEnd.LastMessageID)
	}
	if replayEnd.ReplayedCount != 3 {
		t.Fatalf("expected replayed_count=3, got %d", replayEnd.ReplayedCount)
	}
	if !replayEnd.Truncated {
		t.Fatal("expected truncated=true")
	}
	if replayEnd.NextBeforeID != 14 {
		t.Fatalf("expected next_before_id=14, got %d", replayEnd.NextBeforeID)
	}
}

type wsTestServer struct {
	URL      string
	server   *http.Server
	listener net.Listener
}

func newWSTestServer(t *testing.T, handler *Handler) *wsTestServer {
	t.Helper()
	r := gin.New()
	r.GET("/ws/:conversation_id", handler.Serve)

	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen test server failed: %v", err)
	}
	srv := &http.Server{
		Handler: r,
	}
	go func() {
		_ = srv.Serve(listener)
	}()

	ts := &wsTestServer{
		URL:      "http://" + listener.Addr().String(),
		server:   srv,
		listener: listener,
	}
	t.Cleanup(ts.Close)
	return ts
}

func (s *wsTestServer) Close() {
	if s == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.server.Shutdown(ctx)
	_ = s.listener.Close()
}

func mustIssueAgentToken(t *testing.T, secret string, issuer string, agentID uint64, role string) string {
	t.Helper()
	now := time.Now()
	claims := jwt.MapClaims{
		"agent_id": agentID,
		"role":     role,
		"iss":      issuer,
		"sub":      "agent",
		"iat":      now.Unix(),
		"nbf":      now.Unix(),
		"exp":      now.Add(time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("issue token failed: %v", err)
	}
	return signed
}

func readWSMessage(t *testing.T, conn *websocket.Conn) (int, []byte, error) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return 0, nil, err
	}
	return conn.ReadMessage()
}

func wsDialHeader() http.Header {
	return http.Header{
		"Origin": []string{testAllowedOrigin},
	}
}

func wsDialHeaderWithOrigin(origin string) http.Header {
	return http.Header{
		"Origin": []string{origin},
	}
}

func mustReadMessageIDFromEvent(t *testing.T, raw []byte) uint64 {
	t.Helper()
	var env struct {
		Type    string `json:"type"`
		Payload struct {
			Message struct {
				ID uint64 `json:"id"`
			} `json:"message"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal replay event failed: %v", err)
	}
	if env.Type != "message.new" {
		t.Fatalf("expected message.new event, got %s", env.Type)
	}
	return env.Payload.Message.ID
}

type replayEndPayload struct {
	LastMessageID uint64
	ReplayedCount int
	Truncated     bool
	NextBeforeID  uint64
}

func mustReadReplayEndPayload(t *testing.T, raw []byte) replayEndPayload {
	t.Helper()
	var env struct {
		Type    string `json:"type"`
		Payload struct {
			LastMessageID uint64 `json:"last_message_id"`
			ReplayedCount int    `json:"replayed_count"`
			Truncated     bool   `json:"truncated"`
			NextBeforeID  uint64 `json:"next_before_id"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal replay end event failed: %v", err)
	}
	if env.Type != "replay.end" {
		t.Fatalf("expected replay.end event, got %s", env.Type)
	}
	return replayEndPayload{
		LastMessageID: env.Payload.LastMessageID,
		ReplayedCount: env.Payload.ReplayedCount,
		Truncated:     env.Payload.Truncated,
		NextBeforeID:  env.Payload.NextBeforeID,
	}
}
