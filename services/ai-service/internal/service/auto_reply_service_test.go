package service

import (
	"strings"
	"testing"

	"inlinechat/services/ai-service/internal/chatclient"
)

func TestMatchSmallTalkReply(t *testing.T) {
	tests := []struct {
		name   string
		query  string
		wantOK bool
		want   string
	}{
		{name: "greeting", query: "你好", wantOK: true, want: "您好，我是 AI 客服，很高兴为您服务。您可以直接问我产品、价格、发货、售后等问题。"},
		{name: "thanks", query: "谢谢！", wantOK: true, want: "不客气，您有其他问题可以继续问我。"},
		{name: "bye", query: "bye", wantOK: true, want: "好的，您有需要随时再来咨询。"},
		{name: "identity", query: "你能做什么？", wantOK: true, want: "我是网站 AI 客服，可以根据当前站点资料回答产品、价格、发货和售后相关问题。"},
		{name: "normal question", query: "介绍产品", wantOK: false, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := matchSmallTalkReply(tt.query)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if got != tt.want {
				t.Fatalf("reply = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildSearchQueryUsesRecentContextForFollowUp(t *testing.T) {
	messages := []*chatclient.Message{
		{ID: 12, SenderType: "visitor", Content: "它支持7天无理由吗"},
		{ID: 11, SenderType: "ai", Content: "请告诉我您想咨询哪款产品。"},
		{ID: 10, SenderType: "visitor", Content: "我想了解云感记忆棉枕"},
		{ID: 9, SenderType: "visitor", Content: "你好"},
	}

	got := buildSearchQuery("它支持7天无理由吗", messages, 12)
	if !strings.Contains(got, "当前问题：它支持7天无理由吗") {
		t.Fatalf("search query = %q", got)
	}
	if !strings.Contains(got, "访客：我想了解云感记忆棉枕") {
		t.Fatalf("search query = %q", got)
	}
	if strings.Contains(got, "访客：你好") {
		t.Fatalf("search query should skip smalltalk context, got %q", got)
	}
}

func TestBuildSearchQueryKeepsStandaloneQuestion(t *testing.T) {
	got := buildSearchQuery("床垫支持7天无理由退换货吗", nil, 0)
	if got != "床垫支持7天无理由退换货吗" {
		t.Fatalf("search query = %q", got)
	}
}

func TestBuildSearchQueryRewritesBroadIntroQuestion(t *testing.T) {
	messages := []*chatclient.Message{
		{ID: 4, SenderType: "visitor", Content: "？"},
		{ID: 3, SenderType: "visitor", Content: "你好"},
	}
	got := buildSearchQuery("介绍下你们产品", messages, 4)
	if got != introSearchTemplate {
		t.Fatalf("search query = %q", got)
	}
}

func TestBuildSearchQueryRewritesFeaturedProductQuestion(t *testing.T) {
	got := buildSearchQuery("介绍你们最好产品", nil, 0)
	if got != featuredSearchTemplate {
		t.Fatalf("search query = %q", got)
	}
}

func TestBuildSearchQueryKeepsFocusedNaturalQuestion(t *testing.T) {
	got := buildSearchQuery("请介绍下你们的品牌定位是什么", nil, 0)
	if got != "请介绍下你们的品牌定位是什么" {
		t.Fatalf("search query = %q", got)
	}
}

func TestBuildSearchQueryKeepsDetailedIntroQuestion(t *testing.T) {
	got := buildSearchQuery("介绍这款产品尺寸", nil, 0)
	if got != "介绍这款产品尺寸" {
		t.Fatalf("search query = %q", got)
	}
}

func TestIsLowSignalContext(t *testing.T) {
	if !isLowSignalContext("？") {
		t.Fatal("expected punctuation-only content to be low-signal")
	}
	if isLowSignalContext("云感记忆棉枕") {
		t.Fatal("expected product name to be valid context")
	}
}
