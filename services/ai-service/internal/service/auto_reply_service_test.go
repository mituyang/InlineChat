package service

import "testing"

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
			wantPrefix: "您好，这里是青禾家居客服，",
		},
		{
			name:       "thanks",
			query:      "谢谢！",
			wantMatch:  true,
			wantPrefix: "不客气，这是我应该做的。",
		},
		{
			name:       "identity",
			query:      "你是谁",
			wantMatch:  true,
			wantPrefix: "您好，这里是青禾家居客服。",
		},
		{
			name:       "capability",
			query:      "你能做什么",
			wantMatch:  true,
			wantPrefix: "您好，这里是青禾家居客服。",
		},
		{
			name:       "goodbye",
			query:      "拜拜",
			wantMatch:  true,
			wantPrefix: "好的，感谢您咨询青禾家居。",
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
