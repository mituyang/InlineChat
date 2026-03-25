package knowledgebase

import "testing"

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
