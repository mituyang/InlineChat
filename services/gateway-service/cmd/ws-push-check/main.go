package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/websocket"
)

const widgetSessionHeader = "X-InlineChat-Widget-Session"

type createConversationResponse struct {
	ID uint64 `json:"id"`
}

type createMessageResponse struct {
	ID          uint64 `json:"id"`
	ClientMsgID string `json:"client_msg_id"`
}

type listMessagesResponse struct {
	Items []messageItem `json:"items"`
}

type messageItem struct {
	ID          uint64 `json:"id"`
	Content     string `json:"content"`
	ClientMsgID string `json:"client_msg_id"`
}

type envelope struct {
	Type    string          `json:"type"`
	Error   string          `json:"error"`
	Payload json.RawMessage `json:"payload"`
}

type messageAckPayload struct {
	ClientMsgID string `json:"client_msg_id"`
	MessageID   uint64 `json:"message_id"`
}

type messageNewPayload struct {
	ConversationID uint64      `json:"conversation_id"`
	Message        messageItem `json:"message"`
	ClientMsgID    string      `json:"client_msg_id"`
}

func main() {
	gatewayURL := strings.TrimRight(getenv("GATEWAY_URL", "http://127.0.0.1:8200"), "/")
	siteID := strings.TrimSpace(getenv("WS_CHECK_SITE_ID", "site_demo"))
	siteDomain := strings.TrimSpace(getenv("WS_CHECK_SITE_DOMAIN", ""))
	visitorToken := fmt.Sprintf("ws_check_%d", time.Now().UnixNano())
	clientMsgIDFromWS := fmt.Sprintf("ws_msg_%d", time.Now().UnixNano())
	clientMsgIDFromHTTP := fmt.Sprintf("http_msg_%d", time.Now().UnixNano())
	if siteDomain == "" {
		fatalf("缺少 WS_CHECK_SITE_DOMAIN，无法获取 widget session")
	}
	widgetSession, err := fetchWidgetSession(gatewayURL, siteID, siteDomain)
	if err != nil {
		fatalf("获取 widget session 失败: %v", err)
	}

	conversationResp := createConversationResponse{}
	if err := requestJSONWithHeaders(http.MethodPost, gatewayURL+"/api/chat/v1/conversations", map[string]any{
		"site_id":       siteID,
		"visitor_token": visitorToken,
	}, map[string]string{
		widgetSessionHeader: widgetSession,
	}, &conversationResp); err != nil {
		fatalf("创建会话失败: %v", err)
	}
	if conversationResp.ID == 0 {
		fatalf("创建会话返回无效 conversation_id")
	}

	wsURL := toWSURL(gatewayURL) + "/ws/" + strconv.FormatUint(conversationResp.ID, 10) + "?visitor_token=" + url.QueryEscape(visitorToken)
	config, err := websocket.NewConfig(wsURL, gatewayURL)
	if err != nil {
		fatalf("构建 websocket 配置失败: %v", err)
	}

	conn, err := websocket.DialConfig(config)
	if err != nil {
		fatalf("连接 websocket 失败: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(12 * time.Second))

	recvCh := make(chan envelope, 8)
	errCh := make(chan error, 1)
	go readLoop(conn, recvCh, errCh)

	wsSendPayload := map[string]any{
		"type": "message.send",
		"payload": map[string]any{
			"content":       "message from websocket",
			"client_msg_id": clientMsgIDFromWS,
			"visitor_token": visitorToken,
		},
	}
	if err := websocket.Message.Send(conn, mustJSON(wsSendPayload)); err != nil {
		fatalf("发送 websocket 消息失败: %v", err)
	}

	if err := waitForAck(recvCh, errCh, clientMsgIDFromWS, 8*time.Second); err != nil {
		fatalf("websocket 发消息链路校验失败: %v", err)
	}

	listResp := listMessagesResponse{}
	if err := requestJSON(http.MethodGet, fmt.Sprintf("%s/api/chat/v1/conversations/%d/messages?limit=50&visitor_token=%s", gatewayURL, conversationResp.ID, url.QueryEscape(visitorToken)), nil, &listResp); err != nil {
		fatalf("拉取消息失败: %v", err)
	}
	if !hasClientMsgID(listResp.Items, clientMsgIDFromWS) {
		fatalf("未在消息列表中找到 websocket 写入的消息 client_msg_id=%s", clientMsgIDFromWS)
	}

	httpMessageResp := createMessageResponse{}
	if err := requestJSON(http.MethodPost, fmt.Sprintf("%s/api/chat/v1/conversations/%d/messages", gatewayURL, conversationResp.ID), map[string]any{
		"sender_type":   "visitor",
		"content":       "message from http",
		"client_msg_id": clientMsgIDFromHTTP,
		"visitor_token": visitorToken,
	}, &httpMessageResp); err != nil {
		fatalf("HTTP 发消息失败: %v", err)
	}
	if httpMessageResp.ID == 0 {
		fatalf("HTTP 发消息返回无效 message_id")
	}

	if err := waitForMessageNew(recvCh, errCh, clientMsgIDFromHTTP, 8*time.Second); err != nil {
		fatalf("HTTP->Redis->WebSocket 推送链路校验失败: %v", err)
	}

	fmt.Printf("ws_push_check_ok conversation_id=%d ws_client_msg_id=%s http_client_msg_id=%s\n", conversationResp.ID, clientMsgIDFromWS, clientMsgIDFromHTTP)
}

func waitForAck(recvCh <-chan envelope, errCh <-chan error, targetClientMsgID string, timeout time.Duration) error {
	deadline := time.After(timeout)

	for {
		select {
		case err := <-errCh:
			if err != nil {
				return err
			}
		case <-deadline:
			return fmt.Errorf("等待 message.ack 超时")
		case env := <-recvCh:
			switch env.Type {
			case "error":
				if strings.TrimSpace(env.Error) != "" {
					return errors.New(env.Error)
				}
				return fmt.Errorf("收到 websocket error 事件")
			case "message.ack":
				payload := messageAckPayload{}
				if err := json.Unmarshal(env.Payload, &payload); err != nil {
					return fmt.Errorf("解析 ack payload 失败: %w", err)
				}
				if payload.ClientMsgID == targetClientMsgID {
					return nil
				}
			}
		}
	}
}

func waitForMessageNew(recvCh <-chan envelope, errCh <-chan error, targetClientMsgID string, timeout time.Duration) error {
	deadline := time.After(timeout)
	for {
		select {
		case err := <-errCh:
			if err != nil {
				return err
			}
		case <-deadline:
			return fmt.Errorf("等待 message.new 超时")
		case env := <-recvCh:
			switch env.Type {
			case "error":
				if strings.TrimSpace(env.Error) != "" {
					return errors.New(env.Error)
				}
				return fmt.Errorf("收到 websocket error 事件")
			case "message.new":
				clientMsgID, err := parseMessageNewClientMsgID(env.Payload)
				if err != nil {
					return fmt.Errorf("解析 message.new payload 失败: %w", err)
				}
				if clientMsgID == targetClientMsgID {
					return nil
				}
			}
		}
	}
}

func parseMessageNewClientMsgID(raw json.RawMessage) (string, error) {
	payload := messageNewPayload{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", err
	}
	if payload.Message.ClientMsgID != "" {
		return payload.Message.ClientMsgID, nil
	}
	return payload.ClientMsgID, nil
}

func readLoop(conn *websocket.Conn, recvCh chan<- envelope, errCh chan<- error) {
	for {
		var raw string
		if err := websocket.Message.Receive(conn, &raw); err != nil {
			errCh <- fmt.Errorf("读取 websocket 消息失败: %w", err)
			return
		}
		env := envelope{}
		if err := json.Unmarshal([]byte(raw), &env); err != nil {
			errCh <- fmt.Errorf("解析 websocket 消息失败: %w", err)
			return
		}
		recvCh <- env
	}
}

func hasClientMsgID(items []messageItem, clientMsgID string) bool {
	for _, item := range items {
		if item.ClientMsgID == clientMsgID {
			return true
		}
	}
	return false
}

func requestJSON(method string, url string, body any, out any) error {
	return requestJSONWithHeaders(method, url, body, nil, out)
}

func requestJSONWithHeaders(method string, url string, body any, headers map[string]string, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		req.Header.Set(key, value)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	rawResp, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s %s failed: status=%d body=%s", method, url, resp.StatusCode, strings.TrimSpace(string(rawResp)))
	}

	if out == nil {
		return nil
	}
	if len(rawResp) == 0 {
		return fmt.Errorf("%s %s empty response", method, url)
	}
	if err := json.Unmarshal(rawResp, out); err != nil {
		return err
	}
	return nil
}

func fetchWidgetSession(gatewayURL string, siteID string, siteDomain string) (string, error) {
	parentOrigin := "https://" + siteDomain
	widgetURL := fmt.Sprintf("%s/app/widget/?site_id=%s&parent_origin=%s", gatewayURL, url.QueryEscape(siteID), url.QueryEscape(parentOrigin))
	req, err := http.NewRequest(http.MethodGet, widgetURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Referer", parentOrigin+"/ws-push-check")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	body := string(raw)
	start := strings.Index(body, `__INLINECHAT_WIDGET_SESSION__="`)
	if start < 0 {
		return "", fmt.Errorf("widget session not found")
	}
	start += len(`__INLINECHAT_WIDGET_SESSION__="`)
	end := strings.Index(body[start:], `"`)
	if end < 0 {
		return "", fmt.Errorf("widget session not found")
	}
	token := strings.TrimSpace(body[start : start+end])
	if token == "" {
		return "", fmt.Errorf("widget session not found")
	}
	return token, nil
}

func toWSURL(httpURL string) string {
	if strings.HasPrefix(httpURL, "https://") {
		return "wss://" + strings.TrimPrefix(httpURL, "https://")
	}
	return "ws://" + strings.TrimPrefix(httpURL, "http://")
}

func mustJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func getenv(key string, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v
}
