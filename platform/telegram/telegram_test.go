package telegram

import (
	"encoding/json"
	"testing"
)

func TestExtractEntityText(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		offset int
		length int
		want   string
	}{
		{
			name:   "ASCII only",
			text:   "hello @bot world",
			offset: 6,
			length: 4,
			want:   "@bot",
		},
		{
			name:   "Chinese before mention",
			text:   "你好 @mybot 你好",
			offset: 3,
			length: 6,
			want:   "@mybot",
		},
		{
			// 👍 is U+1F44D = surrogate pair (2 UTF-16 code units)
			// "Hi " = 3, "👍" = 2, " " = 1 → @mybot starts at UTF-16 offset 6
			name:   "emoji before mention (surrogate pair)",
			text:   "Hi 👍 @mybot test",
			offset: 6,
			length: 6,
			want:   "@mybot",
		},
		{
			name:   "multiple emoji before mention",
			text:   "🎉🎊 @testbot",
			offset: 5,
			length: 8,
			want:   "@testbot",
		},
		{
			name:   "out of range returns empty",
			text:   "short",
			offset: 10,
			length: 5,
			want:   "",
		},
		{
			name:   "negative offset returns empty",
			text:   "hello",
			offset: -1,
			length: 3,
			want:   "",
		},
		{
			name:   "negative length returns empty",
			text:   "hello",
			offset: 0,
			length: -1,
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractEntityText(tt.text, tt.offset, tt.length)
			if got != tt.want {
				t.Errorf("extractEntityText(%q, %d, %d) = %q, want %q",
					tt.text, tt.offset, tt.length, got, tt.want)
			}
		})
	}
}

func TestSessionKeyForChat(t *testing.T) {
	tests := []struct {
		name      string
		shared    bool
		chatID    int64
		userID    int64
		isForum   bool
		topicID   int
		wantKey   string
	}{
		{
			name:    "non-forum, per-user",
			chatID:  -100123, userID: 456,
			wantKey: "telegram:-100123:456",
		},
		{
			name:    "non-forum, shared",
			shared:  true,
			chatID:  -100123, userID: 456,
			wantKey: "telegram:-100123",
		},
		{
			name:    "forum topic, per-user",
			chatID:  -100123, userID: 456,
			isForum: true, topicID: 42,
			wantKey: "telegram:-100123:topic:42:456",
		},
		{
			name:    "forum topic, shared",
			shared:  true,
			chatID:  -100123, userID: 456,
			isForum: true, topicID: 42,
			wantKey: "telegram:-100123:topic:42",
		},
		{
			name:    "forum but topicID=0 (General), per-user",
			chatID:  -100123, userID: 456,
			isForum: true, topicID: 0,
			wantKey: "telegram:-100123:456",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Platform{shareSessionInChannel: tt.shared}
			got := p.sessionKeyForChat(tt.chatID, tt.userID, tt.isForum, tt.topicID)
			if got != tt.wantKey {
				t.Errorf("sessionKeyForChat() = %q, want %q", got, tt.wantKey)
			}
		})
	}
}

func TestParseSessionKeyForTopic(t *testing.T) {
	tests := []struct {
		key       string
		wantChat  int64
		wantTopic int
	}{
		{"telegram:-100123:456", -100123, 0},
		{"telegram:-100123", -100123, 0},
		{"telegram:-100123:topic:42:456", -100123, 42},
		{"telegram:-100123:topic:42", -100123, 42},
		{"invalid", 0, 0},
		{"feishu:abc", 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			chatID, topicID := parseSessionKeyForTopic(tt.key)
			if chatID != tt.wantChat || topicID != tt.wantTopic {
				t.Errorf("parseSessionKeyForTopic(%q) = (%d, %d), want (%d, %d)",
					tt.key, chatID, topicID, tt.wantChat, tt.wantTopic)
			}
		})
	}
}

func TestResolveTopicWorkDir(t *testing.T) {
	p := &Platform{
		topicWorkDirs: []topicWorkDir{
			{ChatID: -100123, TopicID: 42, WorkDir: "/home/user/backend"},
			{ChatID: -100123, TopicID: 0, WorkDir: "/home/user/main"},
			{ChatID: -100999, TopicID: 10, WorkDir: "/home/user/other"},
		},
	}

	tests := []struct {
		sessionKey string
		wantDir    string
	}{
		{"telegram:-100123:topic:42:456", "/home/user/backend"},
		{"telegram:-100123:456", "/home/user/main"},
		{"telegram:-100123:topic:0:456", "/home/user/main"},
		{"telegram:-100999:topic:10:789", "/home/user/other"},
		{"telegram:-100999:789", ""},     // no mapping for topic 0 in chat -100999
		{"telegram:-100888:456", ""},      // unknown chat
		{"feishu:abc", ""},                // wrong platform
	}

	for _, tt := range tests {
		t.Run(tt.sessionKey, func(t *testing.T) {
			got := p.ResolveTopicWorkDir(tt.sessionKey)
			if got != tt.wantDir {
				t.Errorf("ResolveTopicWorkDir(%q) = %q, want %q",
					tt.sessionKey, got, tt.wantDir)
			}
		})
	}
}

func TestExtractForumFields(t *testing.T) {
	t.Run("message with forum fields", func(t *testing.T) {
		raw := json.RawMessage(`{
			"update_id": 1,
			"message": {
				"message_id": 10,
				"message_thread_id": 42,
				"chat": {"id": -100123, "type": "supergroup", "is_forum": true}
			}
		}`)
		ff := extractForumFields(raw, false)
		if ff.MessageThreadID != 42 {
			t.Errorf("MessageThreadID = %d, want 42", ff.MessageThreadID)
		}
		if !ff.IsForum {
			t.Error("IsForum = false, want true")
		}
	})

	t.Run("message without forum fields", func(t *testing.T) {
		raw := json.RawMessage(`{
			"update_id": 1,
			"message": {
				"message_id": 10,
				"chat": {"id": -100123, "type": "group"}
			}
		}`)
		ff := extractForumFields(raw, false)
		if ff.MessageThreadID != 0 {
			t.Errorf("MessageThreadID = %d, want 0", ff.MessageThreadID)
		}
		if ff.IsForum {
			t.Error("IsForum = true, want false")
		}
	})

	t.Run("callback with forum fields", func(t *testing.T) {
		raw := json.RawMessage(`{
			"update_id": 1,
			"callback_query": {
				"id": "123",
				"message": {
					"message_id": 10,
					"message_thread_id": 99,
					"chat": {"id": -100123, "type": "supergroup", "is_forum": true}
				}
			}
		}`)
		ff := extractForumFields(raw, true)
		if ff.MessageThreadID != 99 {
			t.Errorf("MessageThreadID = %d, want 99", ff.MessageThreadID)
		}
		if !ff.IsForum {
			t.Error("IsForum = false, want true")
		}
	})
}

func TestReconstructReplyCtx_WithTopic(t *testing.T) {
	p := &Platform{}

	tests := []struct {
		key       string
		wantChat  int64
		wantTopic int
		wantErr   bool
	}{
		{"telegram:-100123:456", -100123, 0, false},
		{"telegram:-100123:topic:42:456", -100123, 42, false},
		{"telegram:-100123:topic:42", -100123, 42, false},
		{"telegram:-100123", -100123, 0, false},
		{"invalid", 0, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			rctx, err := p.ReconstructReplyCtx(tt.key)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ReconstructReplyCtx(%q) error = %v, wantErr %v", tt.key, err, tt.wantErr)
			}
			if err != nil {
				return
			}
			rc := rctx.(replyContext)
			if rc.chatID != tt.wantChat {
				t.Errorf("chatID = %d, want %d", rc.chatID, tt.wantChat)
			}
			if rc.messageThreadID != tt.wantTopic {
				t.Errorf("messageThreadID = %d, want %d", rc.messageThreadID, tt.wantTopic)
			}
		})
	}
}
