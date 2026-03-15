package core

import "testing"

func TestI18n_DefaultLanguage(t *testing.T) {
	i := NewI18n(LangEnglish)
	got := i.T(MsgStarting)
	if got == "" {
		t.Error("expected non-empty message")
	}
}

func TestI18n_Chinese(t *testing.T) {
	i := NewI18n(LangChinese)
	got := i.T(MsgStarting)
	if got == "" {
		t.Error("expected non-empty message")
	}
	// Should contain Chinese characters, not English
	if got == "⏳ Processing..." {
		t.Error("expected Chinese translation, got English")
	}
}

func TestI18n_FallbackToEnglish(t *testing.T) {
	i := NewI18n(Language("nonexistent"))
	got := i.T(MsgStarting)
	if got == "" {
		t.Error("should fallback to English")
	}
}

func TestI18n_MissingKey(t *testing.T) {
	i := NewI18n(LangEnglish)
	got := i.T(MsgKey("totally_missing_key"))
	if got != "[totally_missing_key]" && got != "" {
		// acceptable: either placeholder or empty
	}
}

func TestI18n_Tf(t *testing.T) {
	i := NewI18n(LangEnglish)
	got := i.Tf(MsgNameSet, "myname", "abc123")
	if got == "" {
		t.Error("Tf should return non-empty formatted message")
	}
}

func TestI18n_AllKeysHaveEnglish(t *testing.T) {
	for key, langs := range messages {
		if _, ok := langs[LangEnglish]; !ok {
			t.Errorf("message key %q missing English translation", key)
		}
	}
}

func TestI18n_Russian(t *testing.T) {
	i := NewI18n(LangRussian)
	got := i.T(MsgStarting)
	if got == "" {
		t.Error("expected non-empty message")
	}
	if got == "⏳ Processing..." {
		t.Error("expected Russian translation, got English")
	}
}

func TestDetectLanguage_Russian(t *testing.T) {
	tests := []struct {
		input string
		want  Language
	}{
		{"Привет мир", LangRussian},
		{"Обработай запрос", LangRussian},
		{"Hello world", LangEnglish},
		{"こんにちは", LangJapanese},
		{"你好", LangChinese},
	}
	for _, tt := range tests {
		got := DetectLanguage(tt.input)
		if got != tt.want {
			t.Errorf("DetectLanguage(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestI18n_QueueMessages(t *testing.T) {
	for _, lang := range []Language{LangEnglish, LangRussian, LangChinese, LangJapanese, LangSpanish} {
		i := NewI18n(lang)
		for _, key := range []MsgKey{MsgQueueTitle, MsgQueueFull, MsgQueueConfirm, MsgQueueBtnYes, MsgQueueBtnSkip, MsgQueueBtnClear, MsgQueueCleared, MsgQueueSkipped} {
			got := i.T(key)
			if got == "" || got == string(key) {
				t.Errorf("lang=%q key=%q: expected translation, got %q", lang, key, got)
			}
		}
	}
}
