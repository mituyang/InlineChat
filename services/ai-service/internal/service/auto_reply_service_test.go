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
	got := buildSearchQueries("你们是做什么的", nil, nil)
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

func TestBuildSearchQueriesFuzzyRewrite(t *testing.T) {
	got := buildSearchQueries("你们的品拍故事", []string{
		"品牌",
		"品牌定位",
		"品牌介绍",
		"售后服务",
	}, nil)

	for _, want := range []string{
		"你们的品牌故事",
		"青禾家居的品拍故事",
		"青禾家居的品牌故事",
	} {
		found := false
		for _, item := range got {
			if item == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("buildSearchQueries() = %#v, want contains %q", got, want)
		}
	}
}

func TestBuildSearchQueriesFuzzyFeaturedProductIntent(t *testing.T) {
	got := buildSearchQueries("讲讲你们最好的蛋品", []string{
		"核心单品",
		"主推产品",
		"明星产品资料",
		"单品",
	}, nil)

	for _, want := range []string{
		"讲讲你们最好的单品",
		"青禾家居主推产品有哪些",
		"介绍一下青禾家居重点推荐的核心单品",
	} {
		found := false
		for _, item := range got {
			if item == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("buildSearchQueries() = %#v, want contains %q", got, want)
		}
	}
}

func TestBuildSearchQueriesYearRewrite(t *testing.T) {
	got := buildSearchQueries("23年你们做了啥", nil, []knowledgebase.YearMilestone{
		{Year: 2023, Title: "多仓履约网络建立"},
	})

	for _, want := range []string{
		"2023年你们做了啥",
		"2023 年 多仓履约网络建立",
	} {
		found := false
		for _, item := range got {
			if item == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("buildSearchQueries() = %#v, want contains %q", got, want)
		}
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

func TestBuildTermContextReply(t *testing.T) {
	results := []knowledgebase.SearchResult{
		{
			ID:      1,
			Section: "25.1 产品 SKU 速查表",
			Text:    "SKU 速查表用于查看不同产品对应的 SKU 编号。",
		},
		{
			ID:      2,
			Section: "4. 品牌策略",
			Text:    "品牌强调少而稳定的 SKU 策略，避免过度复杂。",
		},
	}

	reply, ok := buildTermContextReply("sku是啥", results)
	if !ok {
		t.Fatalf("buildTermContextReply() match = false, want true")
	}
	for _, part := range []string{"SKU", "产品编号", "采购报价"} {
		if !strings.Contains(reply, part) {
			t.Fatalf("buildTermContextReply() = %q, want contains %q", reply, part)
		}
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

func TestBuildDeterministicReply(t *testing.T) {
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
		wantParts []string
	}{
		{
			name:      "highest-price",
			query:     "最昂贵产品是啥",
			wantParts: []string{"暮岚针织四件套", "¥399"},
		},
		{
			name:      "lowest-price",
			query:     "最便宜的是哪个",
			wantParts: []string{"溪木砧板套组", "¥129"},
		},
		{
			name:      "single-product-price",
			query:     "柔雾珐琅锅多少钱",
			wantParts: []string{"柔雾珐琅锅", "¥329"},
		},
		{
			name:      "threshold-price",
			query:     "价格高于300元的有哪些",
			wantParts: []string{"暮岚针织四件套", "柔雾珐琅锅", "2款"},
		},
		{
			name:      "product-count",
			query:     "一共多少款主推产品",
			wantParts: []string{"共5款", "云感记忆棉枕", "溪木砧板套组"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := buildDeterministicReply(tt.query, prices, nil)
			if !ok {
				t.Fatalf("buildDeterministicReply() match = false")
			}
			for _, part := range tt.wantParts {
				if !strings.Contains(got, part) {
					t.Fatalf("buildDeterministicReply() = %q, want contains %q", got, part)
				}
			}
		})
	}
}

func TestBuildDeterministicReplyFeaturedProducts(t *testing.T) {
	prices := []knowledgebase.ProductPrice{
		{Name: "云感记忆棉枕", PriceText: "¥159", PriceValue: 159},
		{Name: "暮岚针织四件套", PriceText: "¥399", PriceValue: 399},
		{Name: "极简落地灯", PriceText: "¥239", PriceValue: 239},
	}

	got, ok := buildDeterministicReply("讲讲你们最好的蛋品", prices, []string{
		"核心单品",
		"主推产品",
		"单品",
	})
	if !ok {
		t.Fatalf("buildDeterministicReply() match = false")
	}
	for _, part := range []string{"没有唯一能被定义为“最好”", "云感记忆棉枕", "暮岚针织四件套", "极简落地灯"} {
		if !strings.Contains(got, part) {
			t.Fatalf("buildDeterministicReply() = %q, want contains %q", got, part)
		}
	}
}

func TestBuildYearMilestoneReply(t *testing.T) {
	reply, ok := buildYearMilestoneReply("23年你们做了啥", []knowledgebase.YearMilestone{
		{
			Year:    2023,
			Title:   "多仓履约网络建立",
			Summary: "完成杭州、佛山、武汉、成都四地仓配布局，提升不同区域订单的发货效率，减少跨区域调拨带来的时效波动。",
		},
	})
	if !ok {
		t.Fatalf("buildYearMilestoneReply() match = false")
	}
	for _, part := range []string{"2023 年", "多仓履约网络建立", "杭州", "佛山", "武汉", "成都"} {
		if !strings.Contains(reply, part) {
			t.Fatalf("buildYearMilestoneReply() = %q, want contains %q", reply, part)
		}
	}
}

func TestBuildYearMilestoneReplyMissingYear(t *testing.T) {
	reply, ok := buildYearMilestoneReply("2024年你们做了啥", []knowledgebase.YearMilestone{
		{Year: 2019, Title: "品牌成立"},
		{Year: 2021, Title: "照明与厨房类目扩展"},
		{Year: 2023, Title: "多仓履约网络建立"},
	})
	if !ok {
		t.Fatalf("buildYearMilestoneReply() match = false")
	}
	for _, part := range []string{"未单独记录 2024 年", "2019年", "2021年", "2023年"} {
		if !strings.Contains(reply, part) {
			t.Fatalf("buildYearMilestoneReply() = %q, want contains %q", reply, part)
		}
	}
}

func TestNormalizeReplyLanguage(t *testing.T) {
	got := normalizeReplyLanguage("最贵产品是啥", "如果您需要更详细的 pricing 信息，建议咨询客服 service 哦 😊")
	if strings.Contains(strings.ToLower(got), "pricing") || strings.Contains(strings.ToLower(got), "service") {
		t.Fatalf("normalizeReplyLanguage() = %q, still contains english word", got)
	}
	for _, part := range []string{"价格信息", "客服", "😊"} {
		if !strings.Contains(got, part) {
			t.Fatalf("normalizeReplyLanguage() = %q, want contains %q", got, part)
		}
	}
}

func TestNormalizeReplyLanguageKeepsEnglishForTermQuestion(t *testing.T) {
	got := normalizeReplyLanguage("sku是啥", "SKU 是 Stock Keeping Unit 的缩写，常用于产品编号。")
	for _, part := range []string{"SKU", "Stock Keeping Unit"} {
		if !strings.Contains(got, part) {
			t.Fatalf("normalizeReplyLanguage() = %q, want contains %q", got, part)
		}
	}
}

func TestNormalizeReplyLanguageKeepsEmailAndDomain(t *testing.T) {
	got := normalizeReplyLanguage("怎么开发票", "如需开具发票，请联系企业采购邮箱 b2b@qinghehome.cn 或访问 qinghehome.cn。")
	for _, part := range []string{"b2b@qinghehome.cn", "qinghehome.cn"} {
		if !strings.Contains(got, part) {
			t.Fatalf("normalizeReplyLanguage() = %q, want contains %q", got, part)
		}
	}
}

func TestExtractQuestionTerms(t *testing.T) {
	got := extractQuestionTerms("b2b 是什么意思，SKU 又是什么")
	for _, part := range []string{"b2b", "SKU"} {
		found := false
		for _, item := range got {
			if item == part {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("extractQuestionTerms() = %#v, want contains %q", got, part)
		}
	}
}
