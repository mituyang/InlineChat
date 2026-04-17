package service

import (
	"strings"
	"testing"

	"inlinechat/services/ai-service/internal/chatclient"
	"inlinechat/services/ai-service/internal/knowledgebase"
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

	state := buildConversationState("它支持7天无理由吗", messages, 12)
	if state.ActiveTopic != "云感记忆棉枕" {
		t.Fatalf("state.ActiveTopic = %q", state.ActiveTopic)
	}
	if !strings.Contains(state.Summary, "关注点：售后") {
		t.Fatalf("state.Summary = %q", state.Summary)
	}

	got := buildSearchQuery("它支持7天无理由吗", state)
	if !strings.Contains(got, "当前问题：它支持7天无理由吗") {
		t.Fatalf("search query = %q", got)
	}
	if !strings.Contains(got, "当前主题：云感记忆棉枕") {
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
	got := buildSearchQuery("床垫支持7天无理由退换货吗", buildConversationState("床垫支持7天无理由退换货吗", nil, 0))
	if got != "床垫支持7天无理由退换货吗" {
		t.Fatalf("search query = %q", got)
	}
}

func TestBuildSearchQueryRewritesBroadIntroQuestion(t *testing.T) {
	messages := []*chatclient.Message{
		{ID: 4, SenderType: "visitor", Content: "？"},
		{ID: 3, SenderType: "visitor", Content: "你好"},
	}
	got := buildSearchQuery("介绍下你们产品", buildConversationState("介绍下你们产品", messages, 4))
	if got != introSearchTemplate {
		t.Fatalf("search query = %q", got)
	}
}

func TestBuildSearchQueryRewritesFeaturedProductQuestion(t *testing.T) {
	got := buildSearchQuery("介绍你们最好产品", buildConversationState("介绍你们最好产品", nil, 0))
	if got != featuredSearchTemplate {
		t.Fatalf("search query = %q", got)
	}
}

func TestBuildSearchQueryRewritesFeaturedProductListQuestion(t *testing.T) {
	got := buildSearchQuery("有哪些明星单品", buildConversationState("有哪些明星单品", nil, 0))
	if got != featuredSearchTemplate {
		t.Fatalf("search query = %q", got)
	}
}

func TestBuildSearchQueryRewritesTopicIntroQuestion(t *testing.T) {
	got := buildSearchQuery("介绍会员", buildConversationState("介绍会员", nil, 0))
	if got != "会员 介绍 说明 核心内容 核心权益 规则" {
		t.Fatalf("search query = %q", got)
	}
}

func TestBuildSearchQueryRewritesShortTopicLookupQuestion(t *testing.T) {
	got := buildSearchQuery("品牌故事", buildConversationState("品牌故事", nil, 0))
	if got != "品牌故事 介绍 说明 核心内容 背景 历程" {
		t.Fatalf("search query = %q", got)
	}
}

func TestBuildSearchQueryKeepsFocusedNaturalQuestion(t *testing.T) {
	got := buildSearchQuery("请介绍下你们的品牌定位是什么", buildConversationState("请介绍下你们的品牌定位是什么", nil, 0))
	if got != "请介绍下你们的品牌定位是什么" {
		t.Fatalf("search query = %q", got)
	}
}

func TestBuildSearchQueryKeepsDetailedIntroQuestion(t *testing.T) {
	got := buildSearchQuery("介绍这款产品尺寸", buildConversationState("介绍这款产品尺寸", nil, 0))
	if got != "介绍这款产品尺寸" {
		t.Fatalf("search query = %q", got)
	}
}

func TestBuildSearchQueryKeepsPriceAliasQuestion(t *testing.T) {
	got := buildSearchQuery("溪木砧板套组价钱", buildConversationState("溪木砧板套组价钱", nil, 0))
	if got != "溪木砧板套组价钱" {
		t.Fatalf("search query = %q", got)
	}
}

func TestBuildSearchQueryRewritesStatefulIntroQuestion(t *testing.T) {
	messages := []*chatclient.Message{
		{ID: 7, SenderType: "visitor", Content: "介绍一下"},
		{ID: 6, SenderType: "ai", Content: "可以，您想了解哪款产品？"},
		{ID: 5, SenderType: "visitor", Content: "我想了解云感记忆棉枕"},
	}

	got := buildSearchQuery("介绍一下", buildConversationState("介绍一下", messages, 7))
	if got != "云感记忆棉枕 产品介绍 规格 参数 卖点 适用场景" {
		t.Fatalf("search query = %q", got)
	}
}

func TestBuildSearchQueryDoesNotLeakPreviousTopicIntoStandaloneShortQuestion(t *testing.T) {
	messages := []*chatclient.Message{
		{ID: 9, SenderType: "visitor", Content: "有哪些明星单品"},
		{ID: 8, SenderType: "ai", Content: "当前重点展示产品包括云感记忆棉枕、暮岚针织四件套和极简落地灯。"},
	}

	got := buildSearchQuery("可以开专票吗", buildConversationState("可以开专票吗", messages, 10))
	if got != "可以开专票吗" {
		t.Fatalf("search query = %q", got)
	}
}

func TestBuildPromptBodyIncludesConversationState(t *testing.T) {
	body := buildPromptBody("当前主题：云感记忆棉枕\n关注点：售后", "访客：我想了解云感记忆棉枕", "知识片段", "它支持退换吗")
	if !strings.Contains(body, "当前会话状态：") {
		t.Fatalf("prompt body = %q", body)
	}
	if !strings.Contains(body, "关注点：售后") {
		t.Fatalf("prompt body = %q", body)
	}
}

func TestBuildKnowledgePromptBodyUsesPrimaryDocument(t *testing.T) {
	body := buildKnowledgePromptBody(
		"当前主题：品牌故事",
		"访客：品牌故事",
		"# 品牌故事\n青禾家居成立于 2019 年。",
		[]knowledgebase.SearchResult{{Text: "不会被用到的片段"}},
		"品牌故事",
	)
	if !strings.Contains(body, "站点知识全文（knowledge.md，请直接在全文中查找答案）：") {
		t.Fatalf("prompt body = %q", body)
	}
	if !strings.Contains(body, "青禾家居成立于 2019 年") {
		t.Fatalf("prompt body = %q", body)
	}
	if strings.Contains(body, "不会被用到的片段") {
		t.Fatalf("prompt body should prefer primary document, got %q", body)
	}
}

func TestBuildDirectKnowledgeReplyUsesFAQAnswer(t *testing.T) {
	reply, ok := buildDirectKnowledgeReply("有哪些明星单品", []knowledgebase.SearchResult{
		{
			Kind: knowledgebase.ChunkKindFAQ,
			Text: "问题：青禾家居的主推产品有哪些？\n答案：当前重点展示产品包括云感记忆棉枕、暮岚针织四件套和极简落地灯。",
		},
	}, "当前资料未提及，我暂时无法确认，请联系人工客服。")
	if !ok {
		t.Fatal("expected faq direct reply")
	}
	if reply != "当前重点展示产品包括云感记忆棉枕、暮岚针织四件套和极简落地灯。" {
		t.Fatalf("reply = %q", reply)
	}
}

func TestBuildDirectKnowledgeReplyUsesFactAnswer(t *testing.T) {
	reply, ok := buildDirectKnowledgeReply("云感记忆棉枕尺寸多大", []knowledgebase.SearchResult{
		{
			Kind: knowledgebase.ChunkKindFact,
			Text: "尺寸：60 x 35 x 10/12 cm",
		},
	}, "当前资料未提及，我暂时无法确认，请联系人工客服。")
	if !ok {
		t.Fatal("expected fact direct reply")
	}
	if reply != "尺寸：60 x 35 x 10/12 cm" {
		t.Fatalf("reply = %q", reply)
	}
}

func TestBuildDirectKnowledgeReplyUsesPriceAnswerForHowMuchQuestion(t *testing.T) {
	reply, ok := buildDirectKnowledgeReply("溪木砧板套组多少钱", []knowledgebase.SearchResult{
		{
			Kind: knowledgebase.ChunkKindFact,
			Text: "产品名称：溪木砧板套组\n建议零售价：¥129\n所属产品线：厨房家居",
		},
	}, "当前资料未提及，我暂时无法确认，请联系人工客服。")
	if !ok {
		t.Fatal("expected fact direct reply")
	}
	if reply != "建议零售价：¥129" {
		t.Fatalf("reply = %q", reply)
	}
}

func TestBuildDirectKnowledgeReplyUsesFactAnswerEvenWhenTopResultIsNarrative(t *testing.T) {
	reply, ok := buildDirectKnowledgeReply("暖域床头台适用场景", []knowledgebase.SearchResult{
		{
			Kind: knowledgebase.ChunkKindNarrative,
			Text: "暖域床头台灯更适合作为卧室和桌面场景中的辅助光源。",
		},
		{
			Kind: knowledgebase.ChunkKindFact,
			Text: "适用场景：床头、书桌、客房边几、休闲角",
		},
	}, "当前资料未提及，我暂时无法确认，请联系人工客服。")
	if !ok {
		t.Fatal("expected fact direct reply")
	}
	if reply != "适用场景：床头、书桌、客房边几、休闲角" {
		t.Fatalf("reply = %q", reply)
	}
}

func TestBuildDirectKnowledgeReplyUsesFactAnswerForInvoiceAliasQuestion(t *testing.T) {
	reply, ok := buildDirectKnowledgeReply("可以开专票吗", []knowledgebase.SearchResult{
		{
			Kind: knowledgebase.ChunkKindFact,
			Text: "发票支持：支持企业采购客户申请增值税专用发票",
		},
	}, "当前资料未提及，我暂时无法确认，请联系人工客服。")
	if !ok {
		t.Fatal("expected fact direct reply")
	}
	if reply != "发票支持：支持企业采购客户申请增值税专用发票" {
		t.Fatalf("reply = %q", reply)
	}
}

func TestBuildDirectKnowledgeReplyUsesFactListAnswer(t *testing.T) {
	reply, ok := buildDirectKnowledgeReply("有哪些明星单品", []knowledgebase.SearchResult{
		{
			Kind: knowledgebase.ChunkKindFact,
			Text: "SKU：QH-SL-101\n产品名称：云感记忆棉枕\n品类：睡眠家居",
		},
		{
			Kind: knowledgebase.ChunkKindFact,
			Text: "SKU：QH-SL-204\n产品名称：暮岚针织四件套\n品类：睡眠家居",
		},
		{
			Kind: knowledgebase.ChunkKindFact,
			Text: "SKU：QH-LT-112\n产品名称：极简落地灯\n品类：居家照明",
		},
	}, "当前资料未提及，我暂时无法确认，请联系人工客服。")
	if !ok {
		t.Fatal("expected list direct reply")
	}
	if reply != "当前资料中可确认的明星单品包括：云感记忆棉枕、暮岚针织四件套、极简落地灯。" {
		t.Fatalf("reply = %q", reply)
	}
}

func TestBuildDirectKnowledgeReplyUsesNarrativeOverviewAnswer(t *testing.T) {
	reply, ok := buildDirectKnowledgeReply("介绍会员", []knowledgebase.SearchResult{
		{
			Kind:    knowledgebase.ChunkKindNarrative,
			Section: "青禾家居品牌手册与产品资料 / 15. 会员体系与新客权益 / 15.1 禾苗会员",
			Text:    "注册即成为禾苗会员，可领取 88 元券包。\n券包内容：\n- 满 299 减 30\n- 满 599 减 20\n- 满 999 减 38",
		},
		{
			Kind:    knowledgebase.ChunkKindNarrative,
			Section: "青禾家居品牌手册与产品资料 / 15. 会员体系与新客权益 / 15.2 青禾会员",
			Text:    "年度消费满 999 元自动升级。\n核心权益：\n- 消费积分累计\n- 每月会员专场日\n- 部分常销款会员专享价格\n- 生日月定向券包",
		},
		{
			Kind:    knowledgebase.ChunkKindNarrative,
			Section: "青禾家居品牌手册与产品资料 / 15. 会员体系与新客权益 / 15.3 禾选会员",
			Text:    "年度消费满 3999 元自动升级。\n核心权益：\n- 专属客服通道\n- 新品优先预约\n- 季度家居搭配推荐\n- 重点售后优先处理",
		},
	}, "当前资料未提及，我暂时无法确认，请联系人工客服。")
	if !ok {
		t.Fatal("expected narrative overview direct reply")
	}
	if !strings.Contains(reply, "禾苗会员：注册即成为禾苗会员，可领取 88 元券包") {
		t.Fatalf("reply = %q", reply)
	}
	if !strings.Contains(reply, "青禾会员：年度消费满 999 元自动升级") {
		t.Fatalf("reply = %q", reply)
	}
	if !strings.Contains(reply, "禾选会员：年度消费满 3999 元自动升级") {
		t.Fatalf("reply = %q", reply)
	}
}

func TestBuildDirectKnowledgeReplyUsesShortTopicOverviewAnswer(t *testing.T) {
	reply, ok := buildDirectKnowledgeReply("品牌故事", []knowledgebase.SearchResult{
		{
			Kind:    knowledgebase.ChunkKindNarrative,
			Section: "青禾家居品牌手册与产品资料 / 2. 公司简介",
			Text:    "青禾家居是一家围绕日常居住场景展开产品研发与品牌运营的家居生活品牌。",
		},
		{
			Kind:    knowledgebase.ChunkKindNarrative,
			Section: "青禾家居品牌手册与产品资料 / 4. 品牌发展历程",
			Text:    "品牌于 2019 年成立，并逐步完善产品矩阵与供应链能力。",
		},
	}, "当前资料未提及，我暂时无法确认，请联系人工客服。")
	if !ok {
		t.Fatal("expected short-topic overview direct reply")
	}
	if !strings.Contains(reply, "公司简介：青禾家居是一家围绕日常居住场景展开产品研发与品牌运营的家居生活品牌") {
		t.Fatalf("reply = %q", reply)
	}
	if !strings.Contains(reply, "品牌发展历程：品牌于 2019 年成立") {
		t.Fatalf("reply = %q", reply)
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
