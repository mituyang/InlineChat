package service

import (
	"strings"
	"testing"

	"inlinechat/services/ai-service/internal/knowledgebase"
)

func TestMatchPresetReply(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		wantMatch  bool
		wantPrefix string
	}{
		{
			name:       "greeting",
			query:      "你好",
			wantMatch:  true,
			wantPrefix: "您好呀，这里是青禾家居客服",
		},
		{
			name:       "thanks",
			query:      "谢谢！",
			wantMatch:  true,
			wantPrefix: "不客气呀，这是我应该做的",
		},
		{
			name:       "identity",
			query:      "你是谁",
			wantMatch:  true,
			wantPrefix: "您好呀，我是青禾家居客服",
		},
		{
			name:       "capability",
			query:      "你能做什么",
			wantMatch:  true,
			wantPrefix: "您好呀，我是青禾家居客服",
		},
		{
			name:       "goodbye",
			query:      "拜拜",
			wantMatch:  true,
			wantPrefix: "好的，感谢您咨询青禾家居呀",
		},
		{
			name:      "factual-question",
			query:     "青禾家居是做什么的",
			wantMatch: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := matchPresetReply(tt.query)
			if ok != tt.wantMatch {
				t.Fatalf("matchPresetReply() match = %v, want %v", ok, tt.wantMatch)
			}
			if !tt.wantMatch {
				return
			}
			if got != tt.wantPrefix && len(got) < len(tt.wantPrefix) {
				t.Fatalf("matchPresetReply() reply = %q, want prefix %q", got, tt.wantPrefix)
			}
			if got[:len(tt.wantPrefix)] != tt.wantPrefix {
				t.Fatalf("matchPresetReply() reply = %q, want prefix %q", got, tt.wantPrefix)
			}
		})
	}
}

func TestCompactText(t *testing.T) {
	got := compactText("  你 好！  ")
	if got != "你好" {
		t.Fatalf("compactText() = %q, want %q", got, "你好")
	}
}

func TestBuildSearchQueries(t *testing.T) {
	got := buildSearchQueries("你们是做什么的")
	if len(got) < 2 {
		t.Fatalf("buildSearchQueries() len = %d, want >= 2", len(got))
	}
	if got[0] != "你们是做什么的" {
		t.Fatalf("buildSearchQueries()[0] = %q, want %q", got[0], "你们是做什么的")
	}

	foundRewrite := false
	for _, item := range got {
		if item == "青禾家居是做什么的" {
			foundRewrite = true
			break
		}
	}
	if !foundRewrite {
		t.Fatalf("buildSearchQueries() = %#v, want rewritten query", got)
	}
}

func TestMatchClarifyReply(t *testing.T) {
	reply, ok := matchClarifyReply("有推荐吗")
	if !ok {
		t.Fatalf("matchClarifyReply() match = false, want true")
	}
	if reply == "" {
		t.Fatalf("matchClarifyReply() reply is empty")
	}
}

func TestBuildStructuredFacts(t *testing.T) {
	prices := []knowledgebase.ProductPrice{
		{Name: "云感记忆棉枕", PriceText: "¥159", PriceValue: 159},
		{Name: "暮岚针织四件套", PriceText: "¥399", PriceValue: 399},
		{Name: "柔雾珐琅锅", PriceText: "¥329", PriceValue: 329},
		{Name: "可折叠收纳柜", PriceText: "¥199 起", PriceValue: 199},
		{Name: "溪木砧板套组", PriceText: "¥129", PriceValue: 129},
	}

	tests := []struct {
		name      string
		query     string
		wantMatch bool
		wantParts []string
	}{
		{
			name:      "highest-price",
			query:     "你们最贵产品是哪个",
			wantMatch: true,
			wantParts: []string{"主推产品价格表", "暮岚针织四件套：399元", "云感记忆棉枕：159元", "必须先依据上表完成比较"},
		},
		{
			name:      "single-product-price",
			query:     "柔雾珐琅锅多少钱",
			wantMatch: true,
			wantParts: []string{"柔雾珐琅锅：329元", "原文：¥329"},
		},
		{
			name:      "threshold-above",
			query:     "价格高于300元的有哪些",
			wantMatch: true,
			wantParts: []string{"价格高于/低于/以内/以上", "暮岚针织四件套：399元"},
		},
		{
			name:      "product-count",
			query:     "一共多少款主推产品",
			wantMatch: true,
			wantParts: []string{"主推产品数量", "溪木砧板套组：129元"},
		},
		{
			name:      "non-structured",
			query:     "青禾家居是做什么的",
			wantMatch: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildStructuredFacts(tt.query, prices)
			ok := got != ""
			if ok != tt.wantMatch {
				t.Fatalf("buildStructuredFacts() match = %v, want %v", ok, tt.wantMatch)
			}
			if !tt.wantMatch {
				return
			}
			for _, part := range tt.wantParts {
				if !strings.Contains(got, part) {
					t.Fatalf("buildStructuredFacts() = %q, want contains %q", got, part)
				}
			}
		})
	}
}

func TestParsePriceThresholdQuery(t *testing.T) {
	tests := []struct {
		query    string
		wantMode string
		wantVal  float64
		wantOK   bool
	}{
		{query: "价格高于300元的有哪些", wantMode: "gt", wantVal: 300, wantOK: true},
		{query: "价格不低于300元的有哪些", wantMode: "gte", wantVal: 300, wantOK: true},
		{query: "200元以下的产品", wantMode: "lte", wantVal: 200, wantOK: true},
		{query: "低于150元的产品", wantMode: "lt", wantVal: 150, wantOK: true},
		{query: "青禾家居是做什么的", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			gotMode, gotVal, gotOK := parsePriceThresholdQuery(tt.query)
			if gotOK != tt.wantOK {
				t.Fatalf("parsePriceThresholdQuery() ok = %v, want %v", gotOK, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if gotMode != tt.wantMode || gotVal != tt.wantVal {
				t.Fatalf("parsePriceThresholdQuery() = (%q, %v), want (%q, %v)", gotMode, gotVal, tt.wantMode, tt.wantVal)
			}
		})
	}
}

func TestBuildPromptBody(t *testing.T) {
	got := buildPromptBody("[1] 价格体系\n暮岚针织四件套 ¥399", "补充结构化事实如下：\n主推产品价格表：\n- 暮岚针织四件套：399元", "最昂贵产品是啥")
	for _, part := range []string{"/no_think", "知识片段如下", "补充结构化事实如下", "用户问题：最昂贵产品是啥"} {
		if !strings.Contains(got, part) {
			t.Fatalf("buildPromptBody() = %q, want contains %q", got, part)
		}
	}
}

func TestNormalizeReplyLanguage(t *testing.T) {
	got := normalizeReplyLanguage("如果您需要更详细的 pricing 信息，建议咨询客服 service 哦 😊")
	if strings.Contains(strings.ToLower(got), "pricing") || strings.Contains(strings.ToLower(got), "service") {
		t.Fatalf("normalizeReplyLanguage() = %q, still contains english word", got)
	}
	for _, part := range []string{"价格信息", "客服", "😊"} {
		if !strings.Contains(got, part) {
			t.Fatalf("normalizeReplyLanguage() = %q, want contains %q", got, part)
		}
	}
}
