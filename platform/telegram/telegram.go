package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/chenhg5/cc-connect/core"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func init() {
	core.RegisterPlatform("telegram", New)
}

type replyContext struct {
	chatID          int64
	messageID       int
	messageThreadID int // forum topic ID; 0 = General / no forum
}

// topicWorkDir maps a chat+topic combination to a working directory.
type topicWorkDir struct {
	ChatID  int64
	TopicID int
	WorkDir string
}

type Platform struct {
	token                 string
	allowFrom             string
	groupReplyAll         bool
	shareSessionInChannel bool
	topicWorkDirs         []topicWorkDir
	bot                   *tgbotapi.BotAPI
	httpClient            *http.Client
	handler               core.MessageHandler
	cancel                context.CancelFunc
}

func New(opts map[string]any) (core.Platform, error) {
	token, _ := opts["token"].(string)
	if token == "" {
		return nil, fmt.Errorf("telegram: token is required")
	}
	allowFrom, _ := opts["allow_from"].(string)
	core.CheckAllowFrom("telegram", allowFrom)

	// Build HTTP client with optional proxy support
	httpClient := &http.Client{Timeout: 60 * time.Second}
	if proxyURL, _ := opts["proxy"].(string); proxyURL != "" {
		u, err := url.Parse(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("telegram: invalid proxy URL %q: %w", proxyURL, err)
		}
		proxyUser, _ := opts["proxy_username"].(string)
		proxyPass, _ := opts["proxy_password"].(string)
		if proxyUser != "" {
			u.User = url.UserPassword(proxyUser, proxyPass)
		}
		httpClient.Transport = &http.Transport{Proxy: http.ProxyURL(u)}
		slog.Info("telegram: using proxy", "proxy", u.Host, "auth", proxyUser != "")
	}

	groupReplyAll, _ := opts["group_reply_all"].(bool)
	shareSessionInChannel, _ := opts["share_session_in_channel"].(bool)

	// Parse topic_workdirs configuration
	var twds []topicWorkDir
	if rawList, ok := opts["topic_workdirs"].([]any); ok {
		for _, item := range rawList {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			var twd topicWorkDir
			switch v := m["chat_id"].(type) {
			case int64:
				twd.ChatID = v
			case float64:
				twd.ChatID = int64(v)
			case string:
				twd.ChatID, _ = strconv.ParseInt(v, 10, 64)
			}
			switch v := m["topic_id"].(type) {
			case int64:
				twd.TopicID = int(v)
			case float64:
				twd.TopicID = int(v)
			}
			if dir, ok := m["work_dir"].(string); ok {
				twd.WorkDir = dir
			}
			if twd.ChatID != 0 && twd.WorkDir != "" {
				twds = append(twds, twd)
				slog.Info("telegram: topic workdir mapping",
					"chat_id", twd.ChatID, "topic_id", twd.TopicID, "work_dir", twd.WorkDir)
			}
		}
	}

	return &Platform{
		token: token, allowFrom: allowFrom,
		groupReplyAll: groupReplyAll, shareSessionInChannel: shareSessionInChannel,
		topicWorkDirs: twds, httpClient: httpClient,
	}, nil
}

func (p *Platform) Name() string { return "telegram" }

// forumFields holds forum-specific fields extracted from raw update JSON.
// The go-telegram-bot-api/v5 library doesn't support these fields natively.
type forumFields struct {
	MessageThreadID int
	IsForum         bool
}

// extractForumFields parses forum-specific fields from raw update JSON.
func extractForumFields(raw json.RawMessage, isCallback bool) forumFields {
	var ff forumFields
	if isCallback {
		var parsed struct {
			CallbackQuery *struct {
				Message *struct {
					MessageThreadID int `json:"message_thread_id"`
					Chat            *struct {
						IsForum bool `json:"is_forum"`
					} `json:"chat"`
				} `json:"message"`
			} `json:"callback_query"`
		}
		if json.Unmarshal(raw, &parsed) == nil && parsed.CallbackQuery != nil && parsed.CallbackQuery.Message != nil {
			ff.MessageThreadID = parsed.CallbackQuery.Message.MessageThreadID
			if parsed.CallbackQuery.Message.Chat != nil {
				ff.IsForum = parsed.CallbackQuery.Message.Chat.IsForum
			}
		}
	} else {
		var parsed struct {
			Message *struct {
				MessageThreadID int `json:"message_thread_id"`
				Chat            *struct {
					IsForum bool `json:"is_forum"`
				} `json:"chat"`
			} `json:"message"`
		}
		if json.Unmarshal(raw, &parsed) == nil && parsed.Message != nil {
			ff.MessageThreadID = parsed.Message.MessageThreadID
			if parsed.Message.Chat != nil {
				ff.IsForum = parsed.Message.Chat.IsForum
			}
		}
	}
	return ff
}

func (p *Platform) Start(handler core.MessageHandler) error {
	p.handler = handler

	bot, err := tgbotapi.NewBotAPIWithClient(p.token, tgbotapi.APIEndpoint, p.httpClient)
	if err != nil {
		return fmt.Errorf("telegram: auth failed: %w", err)
	}
	p.bot = bot

	slog.Info("telegram: connected", "bot", bot.Self.UserName)

	// Drain pending updates from previous session to avoid re-processing old messages.
	// offset -1 tells Telegram to mark all pending updates as confirmed, returning only the latest one.
	drain := tgbotapi.NewUpdate(-1)
	drain.Timeout = 0
	if _, err := bot.GetUpdates(drain); err != nil {
		slog.Warn("telegram: failed to drain old updates", "error", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel

	// Custom update polling to extract forum topic fields (message_thread_id, is_forum)
	// that the go-telegram-bot-api/v5 library doesn't support.
	go p.pollUpdates(ctx)

	return nil
}

// pollUpdates fetches updates using raw API calls to extract forum topic fields
// alongside the standard tgbotapi.Update parsing.
func (p *Platform) pollUpdates(ctx context.Context) {
	offset := 0
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		params := make(tgbotapi.Params)
		params.AddNonZero("offset", offset)
		params["timeout"] = "30"

		resp, err := p.bot.MakeRequest("getUpdates", params)
		if err != nil {
			slog.Debug("telegram: getUpdates error", "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}

		var rawUpdates []json.RawMessage
		if err := json.Unmarshal(resp.Result, &rawUpdates); err != nil {
			slog.Warn("telegram: failed to parse raw updates", "error", err)
			continue
		}

		for _, raw := range rawUpdates {
			// Parse into standard tgbotapi.Update for all regular fields
			var update tgbotapi.Update
			if err := json.Unmarshal(raw, &update); err != nil {
				continue
			}
			if update.UpdateID >= offset {
				offset = update.UpdateID + 1
			}

			// Handle inline keyboard button clicks
			if update.CallbackQuery != nil {
				ff := extractForumFields(raw, true)
				p.handleCallbackQueryWithForum(update.CallbackQuery, ff.MessageThreadID)
				continue
			}

			if update.Message == nil {
				continue
			}

			// Extract forum fields from raw JSON
			ff := extractForumFields(raw, false)
			p.handleMessageUpdate(update.Message, ff)
		}
	}
}

// sessionKeyForChat builds a session key incorporating forum topic information.
func (p *Platform) sessionKeyForChat(chatID int64, userID int64, isForum bool, topicID int) string {
	if isForum && topicID != 0 {
		if p.shareSessionInChannel {
			return fmt.Sprintf("telegram:%d:topic:%d", chatID, topicID)
		}
		return fmt.Sprintf("telegram:%d:topic:%d:%d", chatID, topicID, userID)
	}
	if p.shareSessionInChannel {
		return fmt.Sprintf("telegram:%d", chatID)
	}
	return fmt.Sprintf("telegram:%d:%d", chatID, userID)
}

func (p *Platform) handleMessageUpdate(msg *tgbotapi.Message, ff forumFields) {
	msgTime := time.Unix(int64(msg.Date), 0)
	if core.IsOldMessage(msgTime) {
		slog.Debug("telegram: ignoring old message after restart", "date", msgTime)
		return
	}
	userName := msg.From.UserName
	if userName == "" {
		userName = strings.TrimSpace(msg.From.FirstName + " " + msg.From.LastName)
	}
	sessionKey := p.sessionKeyForChat(msg.Chat.ID, msg.From.ID, ff.IsForum, ff.MessageThreadID)
	userID := strconv.FormatInt(msg.From.ID, 10)
	if !core.AllowList(p.allowFrom, userID) {
		slog.Debug("telegram: message from unauthorized user", "user", userID)
		return
	}

	isGroup := msg.Chat.Type == "group" || msg.Chat.Type == "supergroup"

	// In group chats, filter messages not directed at this bot (unless group_reply_all)
	if isGroup && !p.groupReplyAll {
		slog.Debug("telegram: checking group message", "bot", p.bot.Self.UserName, "text", msg.Text, "is_command", msg.IsCommand())
		if !p.isDirectedAtBot(msg) {
			return
		}
	}

	rctx := replyContext{chatID: msg.Chat.ID, messageID: msg.MessageID, messageThreadID: ff.MessageThreadID}

	// Handle photo messages
	if msg.Photo != nil && len(msg.Photo) > 0 {
		best := msg.Photo[len(msg.Photo)-1]
		imgData, err := p.downloadFile(best.FileID)
		if err != nil {
			slog.Error("telegram: download photo failed", "error", err)
			return
		}
		caption := msg.Caption
		if p.bot.Self.UserName != "" {
			caption = strings.ReplaceAll(caption, "@"+p.bot.Self.UserName, "")
			caption = strings.TrimSpace(caption)
		}
		coreMsg := &core.Message{
			SessionKey: sessionKey, Platform: "telegram",
			UserID: userID, UserName: userName,
			Content:   caption,
			MessageID: strconv.Itoa(msg.MessageID),
			Images:    []core.ImageAttachment{{MimeType: "image/jpeg", Data: imgData}},
			ReplyCtx:  rctx,
		}
		p.handler(p, coreMsg)
		return
	}

	// Handle voice messages
	if msg.Voice != nil {
		slog.Debug("telegram: voice received", "user", userName, "duration", msg.Voice.Duration)
		audioData, err := p.downloadFile(msg.Voice.FileID)
		if err != nil {
			slog.Error("telegram: download voice failed", "error", err)
			return
		}
		coreMsg := &core.Message{
			SessionKey: sessionKey, Platform: "telegram",
			UserID: userID, UserName: userName,
			MessageID: strconv.Itoa(msg.MessageID),
			Audio: &core.AudioAttachment{
				MimeType: msg.Voice.MimeType,
				Data:     audioData,
				Format:   "ogg",
				Duration: msg.Voice.Duration,
			},
			ReplyCtx: rctx,
		}
		p.handler(p, coreMsg)
		return
	}

	// Handle audio file messages
	if msg.Audio != nil {
		slog.Debug("telegram: audio file received", "user", userName)
		audioData, err := p.downloadFile(msg.Audio.FileID)
		if err != nil {
			slog.Error("telegram: download audio failed", "error", err)
			return
		}
		format := "mp3"
		if msg.Audio.MimeType != "" {
			parts := strings.SplitN(msg.Audio.MimeType, "/", 2)
			if len(parts) == 2 {
				format = parts[1]
			}
		}
		coreMsg := &core.Message{
			SessionKey: sessionKey, Platform: "telegram",
			UserID: userID, UserName: userName,
			MessageID: strconv.Itoa(msg.MessageID),
			Audio: &core.AudioAttachment{
				MimeType: msg.Audio.MimeType,
				Data:     audioData,
				Format:   format,
				Duration: msg.Audio.Duration,
			},
			ReplyCtx: rctx,
		}
		p.handler(p, coreMsg)
		return
	}

	// Handle document (file) messages
	if msg.Document != nil {
		slog.Info("telegram: document received", "user", userName, "file_name", msg.Document.FileName, "mime", msg.Document.MimeType, "file_id", msg.Document.FileID)
		fileData, err := p.downloadFile(msg.Document.FileID)
		if err != nil {
			slog.Error("telegram: download document failed", "error", err)
			return
		}
		caption := msg.Caption
		if p.bot.Self.UserName != "" {
			caption = strings.ReplaceAll(caption, "@"+p.bot.Self.UserName, "")
			caption = strings.TrimSpace(caption)
		}
		coreMsg := &core.Message{
			SessionKey: sessionKey, Platform: "telegram",
			UserID: userID, UserName: userName,
			Content:   caption,
			MessageID: strconv.Itoa(msg.MessageID),
			Files:     []core.FileAttachment{{MimeType: msg.Document.MimeType, Data: fileData, FileName: msg.Document.FileName}},
			ReplyCtx:  rctx,
		}
		p.handler(p, coreMsg)
		return
	}

	if msg.Text == "" {
		return
	}

	text := msg.Text
	if p.bot.Self.UserName != "" {
		text = strings.ReplaceAll(text, "@"+p.bot.Self.UserName, "")
		text = strings.TrimSpace(text)
	}

	coreMsg := &core.Message{
		SessionKey: sessionKey, Platform: "telegram",
		UserID: userID, UserName: userName,
		Content:   text,
		MessageID: strconv.Itoa(msg.MessageID),
		ReplyCtx:  rctx,
	}

	slog.Debug("telegram: message received", "user", userName, "chat", msg.Chat.ID, "topic", ff.MessageThreadID)
	p.handler(p, coreMsg)
}

func (p *Platform) handleCallbackQueryWithForum(cb *tgbotapi.CallbackQuery, threadID int) {
	if cb.Message == nil || cb.From == nil {
		return
	}

	data := cb.Data
	chatID := cb.Message.Chat.ID
	msgID := cb.Message.MessageID
	userID := strconv.FormatInt(cb.From.ID, 10)

	if !core.AllowList(p.allowFrom, userID) {
		slog.Debug("telegram: callback from unauthorized user", "user", userID)
		return
	}

	// Answer the callback to clear the loading indicator
	answer := tgbotapi.NewCallback(cb.ID, "")
	p.bot.Request(answer)

	userName := cb.From.UserName
	if userName == "" {
		userName = strings.TrimSpace(cb.From.FirstName + " " + cb.From.LastName)
	}
	// For callbacks, we detect forum from threadID > 0 (if the message was in a topic)
	isForum := threadID > 0
	sessionKey := p.sessionKeyForChat(chatID, cb.From.ID, isForum, threadID)
	rctx := replyContext{chatID: chatID, messageID: msgID, messageThreadID: threadID}

	// Command callbacks (cmd:/lang en, cmd:/mode yolo, etc.)
	if strings.HasPrefix(data, "cmd:") {
		command := strings.TrimPrefix(data, "cmd:")

		// Edit original message: append the chosen option and remove buttons
		origText := cb.Message.Text
		if origText == "" {
			origText = ""
		}
		p.apiEditMessageText(chatID, msgID, origText+"\n\n> "+command, "", &tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}})

		p.handler(p, &core.Message{
			SessionKey: sessionKey,
			Platform:   "telegram",
			UserID:     userID,
			UserName:   userName,
			Content:    command,
			MessageID:  strconv.Itoa(msgID),
			ReplyCtx:   rctx,
		})
		return
	}

	// AskUserQuestion callbacks (askq:qIdx:optIdx)
	if strings.HasPrefix(data, "askq:") {
		// Extract label from after the last colon for display
		parts := strings.SplitN(data, ":", 3)
		choiceLabel := data
		if len(parts) == 3 {
			// Try to find the option label from the original message buttons
			for _, row := range cb.Message.ReplyMarkup.InlineKeyboard {
				for _, btn := range row {
					if btn.CallbackData != nil && *btn.CallbackData == data {
						choiceLabel = "✅ " + btn.Text
					}
				}
			}
		}

		origText := cb.Message.Text
		if origText == "" {
			origText = "(question)"
		}
		p.apiEditMessageText(chatID, msgID, origText+"\n\n"+choiceLabel, "", &tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}})

		p.handler(p, &core.Message{
			SessionKey: sessionKey,
			Platform:   "telegram",
			UserID:     userID,
			UserName:   userName,
			Content:    data,
			MessageID:  strconv.Itoa(msgID),
			ReplyCtx:   rctx,
		})
		return
	}

	// Queue callbacks (queue:yes, queue:skip, queue:clear)
	if strings.HasPrefix(data, "queue:") {
		// Show user's choice on the original message
		var choiceLabel string
		switch data {
		case "queue:yes":
			choiceLabel = "▶️ Yes"
		case "queue:skip":
			choiceLabel = "⏭ Skip"
		case "queue:clear":
			choiceLabel = "🗑 Clear"
		default:
			choiceLabel = data
		}
		origText := cb.Message.Text
		if origText == "" {
			origText = "(queue)"
		}
		p.apiEditMessageText(chatID, msgID, origText+"\n\n"+choiceLabel, "", &tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}})

		p.handler(p, &core.Message{
			SessionKey: sessionKey,
			Platform:   "telegram",
			UserID:     userID,
			UserName:   userName,
			Content:    data,
			MessageID:  strconv.Itoa(msgID),
			ReplyCtx:   rctx,
		})
		return
	}

	// Permission callbacks (perm:allow, perm:deny, perm:allow_all)
	var responseText string
	switch data {
	case "perm:allow":
		responseText = "allow"
	case "perm:deny":
		responseText = "deny"
	case "perm:allow_all":
		responseText = "allow all"
	default:
		slog.Debug("telegram: unknown callback data", "data", data)
		return
	}

	choiceLabel := responseText
	switch data {
	case "perm:allow":
		choiceLabel = "✅ Allowed"
	case "perm:deny":
		choiceLabel = "❌ Denied"
	case "perm:allow_all":
		choiceLabel = "✅ Allow All"
	}

	origText := cb.Message.Text
	if origText == "" {
		origText = "(permission request)"
	}
	p.apiEditMessageText(chatID, msgID, origText+"\n\n"+choiceLabel, "", &tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}})

	p.handler(p, &core.Message{
		SessionKey: sessionKey,
		Platform:   "telegram",
		UserID:     userID,
		UserName:   userName,
		Content:    responseText,
		MessageID:  strconv.Itoa(msgID),
		ReplyCtx:   rctx,
	})
}

// isDirectedAtBot checks whether a group message is directed at this bot:
//   - Command with @thisbot suffix (e.g. /help@thisbot)
//   - Command without @suffix (broadcast to all bots — accept it)
//   - Command with @otherbot suffix → reject
//   - Non-command: accept if bot is @mentioned or message is a reply to bot
func (p *Platform) isDirectedAtBot(msg *tgbotapi.Message) bool {
	botName := p.bot.Self.UserName

	// Commands: /cmd or /cmd@botname
	if msg.IsCommand() {
		atIdx := strings.Index(msg.Text, "@")
		spaceIdx := strings.Index(msg.Text, " ")
		cmdEnd := len(msg.Text)
		if spaceIdx > 0 {
			cmdEnd = spaceIdx
		}
		if atIdx > 0 && atIdx < cmdEnd {
			target := msg.Text[atIdx+1 : cmdEnd]
			slog.Debug("telegram: command with @suffix", "bot", botName, "target", target, "match", strings.EqualFold(target, botName))
			return strings.EqualFold(target, botName)
		}
		slog.Debug("telegram: command without @suffix, accepting", "bot", botName, "text", msg.Text)
		return true // /cmd without @suffix — accept
	}

	// Non-command: check @mention
	if msg.Entities != nil {
		for _, e := range msg.Entities {
			if e.Type == "mention" {
				mention := extractEntityText(msg.Text, e.Offset, e.Length)
				slog.Debug("telegram: checking mention", "bot", botName, "mention", mention, "match", strings.EqualFold(mention, "@"+botName))
				if strings.EqualFold(mention, "@"+botName) {
					return true
				}
			}
		}
	}

	// Check if replying to a message from this bot
	if msg.ReplyToMessage != nil && msg.ReplyToMessage.From != nil {
		slog.Debug("telegram: checking reply", "bot_id", p.bot.Self.ID, "reply_from_id", msg.ReplyToMessage.From.ID)
		if msg.ReplyToMessage.From.ID == p.bot.Self.ID {
			return true
		}
	}

	// Also check caption entities (for photos with captions)
	if msg.CaptionEntities != nil {
		for _, e := range msg.CaptionEntities {
			if e.Type == "mention" {
				mention := extractEntityText(msg.Caption, e.Offset, e.Length)
				if strings.EqualFold(mention, "@"+botName) {
					return true
				}
			}
		}
	}

	slog.Debug("telegram: ignoring group message not directed at bot", "chat", msg.Chat.ID, "bot", botName, "text", msg.Text, "entities", msg.Entities)
	return false
}

// --- Raw API helpers for forum topic support ---
// The go-telegram-bot-api/v5 library doesn't expose message_thread_id in its
// Chattable types (the params() method is unexported). These helpers use
// BotAPI.MakeRequest directly to include the field.

// apiSendMessage sends a message via the Telegram API, optionally into a forum topic.
func (p *Platform) apiSendMessage(chatID int64, text, parseMode string, replyToMsgID, threadID int, markup *tgbotapi.InlineKeyboardMarkup) (int, error) {
	params := make(tgbotapi.Params)
	params.AddNonZero64("chat_id", chatID)
	params.AddNonEmpty("text", text)
	params.AddNonEmpty("parse_mode", parseMode)
	params.AddNonZero("reply_to_message_id", replyToMsgID)
	params.AddNonZero("message_thread_id", threadID)
	if markup != nil {
		data, _ := json.Marshal(markup)
		params["reply_markup"] = string(data)
	}
	resp, err := p.bot.MakeRequest("sendMessage", params)
	if err != nil {
		return 0, err
	}
	var sent tgbotapi.Message
	if err := json.Unmarshal(resp.Result, &sent); err != nil {
		return 0, fmt.Errorf("telegram: parse sendMessage response: %w", err)
	}
	return sent.MessageID, nil
}

// apiEditMessageText edits a message's text.
func (p *Platform) apiEditMessageText(chatID int64, msgID int, text, parseMode string, markup *tgbotapi.InlineKeyboardMarkup) error {
	params := make(tgbotapi.Params)
	params.AddNonZero64("chat_id", chatID)
	params.AddNonZero("message_id", msgID)
	params.AddNonEmpty("text", text)
	params.AddNonEmpty("parse_mode", parseMode)
	if markup != nil {
		data, _ := json.Marshal(markup)
		params["reply_markup"] = string(data)
	}
	_, err := p.bot.MakeRequest("editMessageText", params)
	return err
}

// apiSendChatAction sends a chat action (e.g. "typing"), optionally in a forum topic.
func (p *Platform) apiSendChatAction(chatID int64, threadID int, action string) {
	params := make(tgbotapi.Params)
	params.AddNonZero64("chat_id", chatID)
	params.AddNonEmpty("action", action)
	params.AddNonZero("message_thread_id", threadID)
	p.bot.MakeRequest("sendChatAction", params)
}

// sendWithFallback sends a message with HTML mode, falling back to plain text on parse error.
func (p *Platform) sendWithFallback(chatID int64, content string, replyToMsgID, threadID int, markup *tgbotapi.InlineKeyboardMarkup) (int, error) {
	html := core.MarkdownToSimpleHTML(content)
	msgID, err := p.apiSendMessage(chatID, html, tgbotapi.ModeHTML, replyToMsgID, threadID, markup)
	if err != nil {
		if strings.Contains(err.Error(), "can't parse") {
			msgID, err = p.apiSendMessage(chatID, content, "", replyToMsgID, threadID, markup)
		}
		if err != nil {
			return 0, err
		}
	}
	return msgID, nil
}

func (p *Platform) Reply(ctx context.Context, rctx any, content string) error {
	rc, ok := rctx.(replyContext)
	if !ok {
		return fmt.Errorf("telegram: invalid reply context type %T", rctx)
	}
	_, err := p.sendWithFallback(rc.chatID, content, rc.messageID, rc.messageThreadID, nil)
	if err != nil {
		return fmt.Errorf("telegram: reply: %w", err)
	}
	return nil
}

// Send sends a new message (not a reply)
func (p *Platform) Send(ctx context.Context, rctx any, content string) error {
	rc, ok := rctx.(replyContext)
	if !ok {
		return fmt.Errorf("telegram: invalid reply context type %T", rctx)
	}
	_, err := p.sendWithFallback(rc.chatID, content, 0, rc.messageThreadID, nil)
	if err != nil {
		return fmt.Errorf("telegram: send: %w", err)
	}
	return nil
}

// SendWithButtons sends a message with an inline keyboard.
func (p *Platform) SendWithButtons(ctx context.Context, rctx any, content string, buttons [][]core.ButtonOption) error {
	rc, ok := rctx.(replyContext)
	if !ok {
		return fmt.Errorf("telegram: invalid reply context type %T", rctx)
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	for _, row := range buttons {
		var btns []tgbotapi.InlineKeyboardButton
		for _, b := range row {
			btns = append(btns, tgbotapi.NewInlineKeyboardButtonData(b.Text, b.Data))
		}
		rows = append(rows, btns)
	}
	markup := &tgbotapi.InlineKeyboardMarkup{InlineKeyboard: rows}

	_, err := p.sendWithFallback(rc.chatID, content, 0, rc.messageThreadID, markup)
	if err != nil {
		return fmt.Errorf("telegram: sendWithButtons: %w", err)
	}
	return nil
}

// DeletePreviewMessage deletes a stale preview message so the caller can send a fresh one.
func (p *Platform) DeletePreviewMessage(ctx context.Context, previewHandle any) error {
	h, ok := previewHandle.(*telegramPreviewHandle)
	if !ok {
		return fmt.Errorf("telegram: invalid preview handle type %T", previewHandle)
	}
	del := tgbotapi.NewDeleteMessage(h.chatID, h.messageID)
	_, err := p.bot.Request(del)
	if err != nil {
		slog.Debug("telegram: delete preview message failed", "error", err)
	}
	return err
}

// trackableMsgHandle stores info needed to delete a sent message later.
type trackableMsgHandle struct {
	chatID    int64
	messageID int
}

// SendTrackable implements core.CompactToolTracker.
// Sends a message and returns a handle that can be used to delete it later.
func (p *Platform) SendTrackable(ctx context.Context, rctx any, content string) (any, error) {
	rc, ok := rctx.(replyContext)
	if !ok {
		return nil, fmt.Errorf("telegram: invalid reply context type %T", rctx)
	}
	msgID, err := p.sendWithFallback(rc.chatID, content, 0, rc.messageThreadID, nil)
	if err != nil {
		return nil, fmt.Errorf("telegram: sendTrackable: %w", err)
	}
	return &trackableMsgHandle{chatID: rc.chatID, messageID: msgID}, nil
}

// DeleteMessages implements core.CompactToolTracker.
// Deletes previously sent messages by their handles.
func (p *Platform) DeleteMessages(ctx context.Context, rctx any, handles []any) {
	for _, h := range handles {
		tm, ok := h.(*trackableMsgHandle)
		if !ok {
			continue
		}
		del := tgbotapi.NewDeleteMessage(tm.chatID, tm.messageID)
		if _, err := p.bot.Request(del); err != nil {
			slog.Debug("telegram: delete tracked message failed", "error", err, "msg_id", tm.messageID)
		}
	}
}

// pinnedMsgHandle stores info needed to edit/unpin a pinned message.
type pinnedMsgHandle struct {
	chatID    int64
	messageID int
	threadID  int
}

// SendAndPin implements core.PinnableMessage.
func (p *Platform) SendAndPin(ctx context.Context, rctx any, content string) (any, error) {
	rc, ok := rctx.(replyContext)
	if !ok {
		return nil, fmt.Errorf("telegram: invalid reply context type %T", rctx)
	}
	msgID, err := p.sendWithFallback(rc.chatID, content, 0, rc.messageThreadID, nil)
	if err != nil {
		return nil, fmt.Errorf("telegram: sendAndPin: %w", err)
	}

	// Pin the message
	params := make(tgbotapi.Params)
	params.AddNonZero64("chat_id", rc.chatID)
	params.AddNonZero("message_id", msgID)
	params["disable_notification"] = "true"
	if _, err := p.bot.MakeRequest("pinChatMessage", params); err != nil {
		slog.Debug("telegram: pin message failed", "error", err, "msg_id", msgID)
		// Don't fail — the message was sent, just couldn't be pinned
	}

	return &pinnedMsgHandle{chatID: rc.chatID, messageID: msgID, threadID: rc.messageThreadID}, nil
}

// EditPinned implements core.PinnableMessage.
func (p *Platform) EditPinned(ctx context.Context, handle any, content string) error {
	h, ok := handle.(*pinnedMsgHandle)
	if !ok {
		return fmt.Errorf("telegram: invalid pinned handle type %T", handle)
	}
	html := core.MarkdownToSimpleHTML(content)
	err := p.apiEditMessageText(h.chatID, h.messageID, html, tgbotapi.ModeHTML, nil)
	if err != nil {
		if strings.Contains(err.Error(), "not modified") {
			return nil
		}
		// Fall back to plain text
		err2 := p.apiEditMessageText(h.chatID, h.messageID, content, "", nil)
		if err2 != nil && strings.Contains(err2.Error(), "not modified") {
			return nil
		}
		return err2
	}
	return nil
}

// FindQueuePin implements core.QueuePinReader.
// It uses getChat to find a pinned message with the queue marker prefix.
func (p *Platform) FindQueuePin(ctx context.Context, rctx any) (string, any, error) {
	rc, ok := rctx.(replyContext)
	if !ok {
		return "", nil, fmt.Errorf("telegram: invalid reply context type %T", rctx)
	}

	params := make(tgbotapi.Params)
	params.AddNonZero64("chat_id", rc.chatID)
	resp, err := p.bot.MakeRequest("getChat", params)
	if err != nil {
		return "", nil, fmt.Errorf("telegram: getChat: %w", err)
	}

	var chat struct {
		PinnedMessage *struct {
			MessageID int    `json:"message_id"`
			Text      string `json:"text"`
		} `json:"pinned_message"`
	}
	if err := json.Unmarshal(resp.Result, &chat); err != nil {
		return "", nil, fmt.Errorf("telegram: parse getChat: %w", err)
	}

	if chat.PinnedMessage == nil || !strings.HasPrefix(chat.PinnedMessage.Text, core.QueuePinPrefix) {
		return "", nil, nil
	}

	handle := &pinnedMsgHandle{
		chatID:    rc.chatID,
		messageID: chat.PinnedMessage.MessageID,
		threadID:  rc.messageThreadID,
	}
	return chat.PinnedMessage.Text, handle, nil
}

// Unpin implements core.PinnableMessage.
func (p *Platform) Unpin(ctx context.Context, handle any) error {
	h, ok := handle.(*pinnedMsgHandle)
	if !ok {
		return fmt.Errorf("telegram: invalid pinned handle type %T", handle)
	}
	params := make(tgbotapi.Params)
	params.AddNonZero64("chat_id", h.chatID)
	params.AddNonZero("message_id", h.messageID)
	if _, err := p.bot.MakeRequest("unpinChatMessage", params); err != nil {
		slog.Debug("telegram: unpin message failed", "error", err, "msg_id", h.messageID)
	}
	// Also delete the pinned message to clean up
	del := tgbotapi.NewDeleteMessage(h.chatID, h.messageID)
	if _, err := p.bot.Request(del); err != nil {
		slog.Debug("telegram: delete pinned message failed", "error", err, "msg_id", h.messageID)
	}
	return nil
}

func (p *Platform) downloadFile(fileID string) ([]byte, error) {
	fileConfig := tgbotapi.FileConfig{FileID: fileID}
	file, err := p.bot.GetFile(fileConfig)
	if err != nil {
		return nil, fmt.Errorf("get file: %w", err)
	}
	link := file.Link(p.bot.Token)

	resp, err := p.httpClient.Get(link)
	if err != nil {
		errMsg := core.RedactToken(err.Error(), p.bot.Token)
		return nil, fmt.Errorf("download file %s: %s", fileID, errMsg)
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func (p *Platform) ReconstructReplyCtx(sessionKey string) (any, error) {
	// Formats:
	//   telegram:{chatID}:{userID}           (non-forum)
	//   telegram:{chatID}                    (shared, non-forum)
	//   telegram:{chatID}:topic:{topicID}:{userID}  (forum)
	//   telegram:{chatID}:topic:{topicID}           (shared, forum)
	parts := strings.SplitN(sessionKey, ":", 5)
	if len(parts) < 2 || parts[0] != "telegram" {
		return nil, fmt.Errorf("telegram: invalid session key %q", sessionKey)
	}
	chatID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("telegram: invalid chat ID in %q", sessionKey)
	}
	rc := replyContext{chatID: chatID}
	// Check for topic format: telegram:{chatID}:topic:{topicID}...
	if len(parts) >= 4 && parts[2] == "topic" {
		topicID, err := strconv.Atoi(parts[3])
		if err == nil {
			rc.messageThreadID = topicID
		}
	}
	return rc, nil
}

// telegramPreviewHandle stores the chat, message, and thread IDs for an editable preview message.
type telegramPreviewHandle struct {
	chatID          int64
	messageID       int
	messageThreadID int
}

// SendPreviewStart sends a new message and returns a handle for subsequent edits.
// Uses HTML mode to match UpdateMessage formatting, falling back to plain text
// if Telegram rejects the HTML (reduces visible format "jump" during streaming).
func (p *Platform) SendPreviewStart(ctx context.Context, rctx any, content string) (any, error) {
	rc, ok := rctx.(replyContext)
	if !ok {
		return nil, fmt.Errorf("telegram: invalid reply context type %T", rctx)
	}

	msgID, err := p.sendWithFallback(rc.chatID, content, 0, rc.messageThreadID, nil)
	if err != nil {
		return nil, fmt.Errorf("telegram: send preview: %w", err)
	}
	return &telegramPreviewHandle{chatID: rc.chatID, messageID: msgID, messageThreadID: rc.messageThreadID}, nil
}

// UpdateMessage edits an existing message identified by previewHandle.
func (p *Platform) UpdateMessage(ctx context.Context, previewHandle any, content string) error {
	h, ok := previewHandle.(*telegramPreviewHandle)
	if !ok {
		return fmt.Errorf("telegram: invalid preview handle type %T", previewHandle)
	}

	html := core.MarkdownToSimpleHTML(content)
	slog.Debug("telegram: UpdateMessage",
		"content_len", len(content), "html_len", len(html),
		"content_prefix", truncateForLog(content, 80),
		"html_prefix", truncateForLog(html, 80))

	err := p.apiEditMessageText(h.chatID, h.messageID, html, tgbotapi.ModeHTML, nil)
	if err != nil {
		errMsg := err.Error()
		slog.Debug("telegram: UpdateMessage HTML failed", "error", errMsg)
		if strings.Contains(errMsg, "not modified") {
			return nil
		}
		if strings.Contains(errMsg, "can't parse") {
			slog.Debug("telegram: UpdateMessage falling back to plain text", "full_html", html)
			err2 := p.apiEditMessageText(h.chatID, h.messageID, content, "", nil)
			if err2 != nil {
				if strings.Contains(err2.Error(), "not modified") {
					return nil
				}
				return fmt.Errorf("telegram: edit message: %w", err2)
			}
			return nil
		}
		return fmt.Errorf("telegram: edit message: %w", err)
	}
	slog.Debug("telegram: UpdateMessage HTML success")
	return nil
}

func truncateForLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// StartTyping sends a "typing…" chat action and repeats every 5 seconds
// until the returned stop function is called.
func (p *Platform) StartTyping(ctx context.Context, rctx any) (stop func()) {
	rc, ok := rctx.(replyContext)
	if !ok {
		return func() {}
	}

	p.apiSendChatAction(rc.chatID, rc.messageThreadID, tgbotapi.ChatTyping)

	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				p.apiSendChatAction(rc.chatID, rc.messageThreadID, tgbotapi.ChatTyping)
			}
		}
	}()

	return func() { close(done) }
}

func (p *Platform) Stop() error {
	if p.cancel != nil {
		p.cancel()
	}
	if p.bot != nil {
		p.bot.StopReceivingUpdates()
	}
	return nil
}

// ResolveTopicWorkDir implements core.TopicWorkDirResolver.
// It looks up the configured work_dir for the given session key's chat+topic combination.
func (p *Platform) ResolveTopicWorkDir(sessionKey string) string {
	if len(p.topicWorkDirs) == 0 {
		return ""
	}
	chatID, topicID := parseSessionKeyForTopic(sessionKey)
	if chatID == 0 {
		return ""
	}
	for _, twd := range p.topicWorkDirs {
		if twd.ChatID == chatID && twd.TopicID == topicID {
			return twd.WorkDir
		}
	}
	return ""
}

// parseSessionKeyForTopic extracts chatID and topicID from a session key.
func parseSessionKeyForTopic(sessionKey string) (chatID int64, topicID int) {
	parts := strings.SplitN(sessionKey, ":", 5)
	if len(parts) < 2 || parts[0] != "telegram" {
		return 0, 0
	}
	chatID, _ = strconv.ParseInt(parts[1], 10, 64)
	if len(parts) >= 4 && parts[2] == "topic" {
		topicID, _ = strconv.Atoi(parts[3])
	}
	return chatID, topicID
}

// RegisterCommands registers bot commands with Telegram for the command menu.
func (p *Platform) RegisterCommands(commands []core.BotCommandInfo) error {
	if p.bot == nil {
		return fmt.Errorf("telegram: bot not initialized")
	}

	// Telegram limits: max 100 commands, description max 256 chars
	var tgCommands []tgbotapi.BotCommand
	seen := make(map[string]bool)
	for _, c := range commands {
		cmd := sanitizeTelegramCommand(c.Command)
		if cmd == "" || seen[cmd] {
			continue
		}
		seen[cmd] = true
		desc := c.Description
		if len(desc) > 256 {
			desc = desc[:253] + "..."
		}
		tgCommands = append(tgCommands, tgbotapi.BotCommand{
			Command:     cmd,
			Description: desc,
		})
	}

	// Telegram nominally allows 100, but BOT_COMMANDS_TOO_MUCH can occur
	// below that threshold (e.g. when commands are set in multiple scopes).
	// Keep a safe margin.
	const maxTelegramCommands = 30
	if len(tgCommands) > maxTelegramCommands {
		slog.Info("telegram: trimming commands", "total", len(tgCommands), "limit", maxTelegramCommands)
		tgCommands = tgCommands[:maxTelegramCommands]
	}

	if len(tgCommands) == 0 {
		slog.Debug("telegram: no commands to register")
		return nil
	}

	slog.Debug("telegram: registering commands", "count", len(tgCommands))
	cfg := tgbotapi.NewSetMyCommands(tgCommands...)
	_, err := p.bot.Request(cfg)
	if err != nil {
		return fmt.Errorf("telegram: setMyCommands failed: %w", err)
	}

	slog.Info("telegram: registered bot commands", "count", len(tgCommands))
	return nil
}

// extractEntityText extracts a substring from text using Telegram's UTF-16 code unit
// offset and length. Telegram Bot API entity offsets are measured in UTF-16 code units,
// not bytes or Unicode code points, so direct byte slicing produces wrong results
// when the text contains non-ASCII characters (e.g. Chinese, emoji).
func extractEntityText(text string, offsetUTF16, lengthUTF16 int) string {
	encoded := utf16.Encode([]rune(text))
	endUTF16 := offsetUTF16 + lengthUTF16
	if offsetUTF16 < 0 || lengthUTF16 < 0 || endUTF16 > len(encoded) {
		return ""
	}
	return string(utf16.Decode(encoded[offsetUTF16:endUTF16]))
}

// isValidTelegramCommand validates if a command string meets Telegram's requirements.
// Telegram command rules:
//   - 1-32 characters long
//   - Only lowercase letters, digits, and underscores
//   - Must start with a letter
func isValidTelegramCommand(cmd string) bool {
	if len(cmd) == 0 || len(cmd) > 32 {
		return false
	}
	if cmd[0] < 'a' || cmd[0] > 'z' {
		return false
	}
	for i := 1; i < len(cmd); i++ {
		c := cmd[i]
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_') {
			return false
		}
	}
	return true
}

// sanitizeTelegramCommand converts a command name to Telegram-compatible format.
// Telegram rules: 1-32 chars, lowercase letters/digits/underscores, must start with a letter.
// Returns "" if the command cannot be sanitized (e.g. empty or no letter to start with).
func sanitizeTelegramCommand(cmd string) string {
	cmd = strings.ToLower(cmd)
	var b strings.Builder
	for _, c := range cmd {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			b.WriteRune(c)
		default:
			b.WriteByte('_')
		}
	}
	result := b.String()
	// Collapse consecutive underscores
	for strings.Contains(result, "__") {
		result = strings.ReplaceAll(result, "__", "_")
	}
	result = strings.Trim(result, "_")
	// Must start with a letter
	if len(result) == 0 || result[0] < 'a' || result[0] > 'z' {
		return ""
	}
	if len(result) > 32 {
		result = result[:32]
	}
	return result
}
