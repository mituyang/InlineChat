package knowledgebase

import (
	"strings"
	"testing"
)

func TestExtractProductPrices(t *testing.T) {
	raw := `
## 11. 价格体系与定价逻辑

### 11.1 当前主推产品价格

| 产品名称 | 建议零售价 |
| --- | --- |
| 云感记忆棉枕 | ¥159 |
| 暮岚针织四件套 | ¥399 |

### 25.1 产品 SKU 速查表

| SKU | 产品名称 | 品类 | 建议零售价 | 质保/售后口径 |
| --- | --- | --- | --- | --- |
| QH-SL-101 | 云感记忆棉枕 | 睡眠家居 | ¥159 | 30 天质量问题支持处理 |
| QH-ST-301 | 可折叠收纳柜 | 收纳整理 | ¥199 起 | 24 个月结构质保 |
`

	got := extractProductPrices(raw)
	if len(got) != 3 {
		t.Fatalf("extractProductPrices() len = %d, want 3", len(got))
	}

	if got[0].Name != "云感记忆棉枕" || got[0].PriceText != "¥159" || got[0].PriceValue != 159 {
		t.Fatalf("extractProductPrices()[0] = %#v", got[0])
	}
	if got[1].Name != "暮岚针织四件套" || got[1].PriceValue != 399 {
		t.Fatalf("extractProductPrices()[1] = %#v", got[1])
	}
	if got[2].Name != "可折叠收纳柜" || got[2].PriceText != "¥199 起" || got[2].PriceValue != 199 {
		t.Fatalf("extractProductPrices()[2] = %#v", got[2])
	}
}

func TestParsePriceValue(t *testing.T) {
	tests := []struct {
		input  string
		want   float64
		wantOK bool
	}{
		{input: "¥399", want: 399, wantOK: true},
		{input: "¥199 起", want: 199, wantOK: true},
		{input: "无", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, ok := parsePriceValue(tt.input)
			if ok != tt.wantOK {
				t.Fatalf("parsePriceValue() ok = %v, want %v", ok, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if got != tt.want {
				t.Fatalf("parsePriceValue() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExtractRetrievalTerms(t *testing.T) {
	raw := `
## 2. 公司简介

青禾家居是一家围绕日常居住场景展开产品研发与品牌运营的家居生活品牌。

## 3. 品牌定位与核心理念

### 3.1 品牌定位

强调高性价比与长期使用体验。

### 19.1 标准版品牌介绍

适用于官网介绍与销售答疑，重点展示和推荐的核心单品更适合首轮沟通。
主推单品会用于零售介绍、活动展示与客服答疑。
代表单品通常兼顾价格效率、体验改善与空间适配。

## 11. 价格体系与定价逻辑

| 产品名称 | 建议零售价 |
| --- | --- |
| 暮岚针织四件套 | ¥399 |
`

	chunks := splitMarkdownIntoChunks(raw, defaultChunkChars, defaultChunkOverlap)
	prices := extractProductPrices(raw)
	got := extractRetrievalTerms(chunks, prices)

	for _, want := range []string{"品牌", "品牌定位", "品牌介绍", "暮岚针织四件套", "单品"} {
		found := false
		for _, item := range got {
			if item == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("extractRetrievalTerms() = %#v, want contains %q", got, want)
		}
	}
}

func TestExtractYearMilestones(t *testing.T) {
	raw := `
## 4. 品牌发展历程

### 2023 年：多仓履约网络建立

完成杭州、佛山、武汉、成都四地仓配布局，提升不同区域订单的发货效率，减少跨区域调拨带来的时效波动。

### 2025 年：企业采购体系升级

设立企业采购顾问机制，完善报价、合同、对公付款、发票开具与分批交付流程。
`

	chunks := splitMarkdownIntoChunks(raw, defaultChunkChars, defaultChunkOverlap)
	got := extractYearMilestones(chunks)
	if len(got) != 2 {
		t.Fatalf("extractYearMilestones() len = %d, want 2", len(got))
	}
	if got[0].Year != 2023 || got[0].Title != "多仓履约网络建立" {
		t.Fatalf("extractYearMilestones()[0] = %#v", got[0])
	}
	if got[1].Year != 2025 || got[1].Title != "企业采购体系升级" {
		t.Fatalf("extractYearMilestones()[1] = %#v", got[1])
	}
	for _, part := range []string{"杭州", "佛山", "武汉", "成都"} {
		if !strings.Contains(got[0].Summary, part) {
			t.Fatalf("extractYearMilestones()[0].Summary = %q, want contains %q", got[0].Summary, part)
		}
	}
}
