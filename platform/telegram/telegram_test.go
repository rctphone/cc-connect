package telegram

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
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

// --- sendBatcher tests ---

type sentMsg struct {
	chatID   int64
	text     string
	replyTo  int
	threadID int
}

func collectingSender(mu *sync.Mutex, sent *[]sentMsg) func(int64, string, int, int) error {
	return func(chatID int64, text string, replyTo, threadID int) error {
		mu.Lock()
		*sent = append(*sent, sentMsg{chatID, text, replyTo, threadID})
		mu.Unlock()
		return nil
	}
}

func TestBatcher_SingleMessage(t *testing.T) {
	var mu sync.Mutex
	var sent []sentMsg
	b := newSendBatcher(50*time.Millisecond, collectingSender(&mu, &sent))

	b.enqueue(100, 0, 0, "hello")
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 1 {
		t.Fatalf("expected 1 send, got %d", len(sent))
	}
	if sent[0].text != "hello" {
		t.Errorf("text = %q, want %q", sent[0].text, "hello")
	}
	if sent[0].chatID != 100 {
		t.Errorf("chatID = %d, want 100", sent[0].chatID)
	}
}

func TestBatcher_CoalesceTwo(t *testing.T) {
	var mu sync.Mutex
	var sent []sentMsg
	b := newSendBatcher(100*time.Millisecond, collectingSender(&mu, &sent))

	b.enqueue(100, 0, 0, "first")
	b.enqueue(100, 0, 0, "second")
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 1 {
		t.Fatalf("expected 1 coalesced send, got %d", len(sent))
	}
	if sent[0].text != "first\n\nsecond" {
		t.Errorf("text = %q, want %q", sent[0].text, "first\n\nsecond")
	}
}

func TestBatcher_MaxLenSplit(t *testing.T) {
	var mu sync.Mutex
	var sent []sentMsg
	b := newSendBatcher(100*time.Millisecond, collectingSender(&mu, &sent))
	b.maxLen = 20 // force small max

	b.enqueue(100, 0, 0, "aaaaaaaaaa") // 10 chars
	b.enqueue(100, 0, 0, "bbbbbbbbbb") // 10 chars — total 10+2+10=22 > 20
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 2 {
		t.Fatalf("expected 2 sends (split by maxLen), got %d", len(sent))
	}
	if sent[0].text != "aaaaaaaaaa" {
		t.Errorf("first text = %q, want %q", sent[0].text, "aaaaaaaaaa")
	}
	if sent[1].text != "bbbbbbbbbb" {
		t.Errorf("second text = %q, want %q", sent[1].text, "bbbbbbbbbb")
	}
}

func TestBatcher_FlushChat(t *testing.T) {
	var mu sync.Mutex
	var sent []sentMsg
	b := newSendBatcher(5*time.Second, collectingSender(&mu, &sent)) // long delay

	b.enqueue(100, 0, 0, "buffered")
	b.flushChat(100, 0)

	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 1 {
		t.Fatalf("expected 1 send after flushChat, got %d", len(sent))
	}
	if sent[0].text != "buffered" {
		t.Errorf("text = %q, want %q", sent[0].text, "buffered")
	}
}

func TestBatcher_DifferentChats(t *testing.T) {
	var mu sync.Mutex
	var sent []sentMsg
	b := newSendBatcher(50*time.Millisecond, collectingSender(&mu, &sent))

	b.enqueue(100, 0, 0, "chat100")
	b.enqueue(200, 0, 0, "chat200")
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 2 {
		t.Fatalf("expected 2 sends (different chats), got %d", len(sent))
	}
	// Order not guaranteed, check both present
	texts := map[string]bool{sent[0].text: true, sent[1].text: true}
	if !texts["chat100"] || !texts["chat200"] {
		t.Errorf("expected chat100 and chat200, got %q and %q", sent[0].text, sent[1].text)
	}
}

func TestBatcher_DifferentReplyTo(t *testing.T) {
	var mu sync.Mutex
	var sent []sentMsg
	b := newSendBatcher(100*time.Millisecond, collectingSender(&mu, &sent))

	b.enqueue(100, 0, 0, "send-msg")
	b.enqueue(100, 0, 42, "reply-msg") // different replyTo → flush first
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 2 {
		t.Fatalf("expected 2 sends (different replyTo), got %d", len(sent))
	}
	if sent[0].text != "send-msg" || sent[0].replyTo != 0 {
		t.Errorf("first: text=%q replyTo=%d, want send-msg/0", sent[0].text, sent[0].replyTo)
	}
	if sent[1].text != "reply-msg" || sent[1].replyTo != 42 {
		t.Errorf("second: text=%q replyTo=%d, want reply-msg/42", sent[1].text, sent[1].replyTo)
	}
}

func TestBatcher_FlushAll(t *testing.T) {
	var mu sync.Mutex
	var sent []sentMsg
	b := newSendBatcher(5*time.Second, collectingSender(&mu, &sent)) // long delay

	b.enqueue(100, 0, 0, "a")
	b.enqueue(200, 5, 0, "b")
	b.flushAll()

	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 2 {
		t.Fatalf("expected 2 sends after flushAll, got %d", len(sent))
	}
}

func TestBatcher_DifferentThreads(t *testing.T) {
	var mu sync.Mutex
	var sent []sentMsg
	b := newSendBatcher(50*time.Millisecond, collectingSender(&mu, &sent))

	b.enqueue(100, 0, 0, "general")
	b.enqueue(100, 42, 0, "topic42")
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 2 {
		t.Fatalf("expected 2 sends (different threads), got %d", len(sent))
	}
}
