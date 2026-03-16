package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- stubs for Engine tests ---

type stubAgent struct{}

func (a *stubAgent) Name() string { return "stub" }
func (a *stubAgent) StartSession(_ context.Context, _ string) (AgentSession, error) {
	return &stubAgentSession{}, nil
}
func (a *stubAgent) ListSessions(_ context.Context) ([]AgentSessionInfo, error) { return nil, nil }
func (a *stubAgent) Stop() error                                                { return nil }

type stubAgentSession struct{}

func (s *stubAgentSession) Send(_ string, _ []ImageAttachment, _ []FileAttachment) error { return nil }
func (s *stubAgentSession) RespondPermission(_ string, _ PermissionResult) error         { return nil }
func (s *stubAgentSession) Events() <-chan Event                                         { return make(chan Event) }
func (s *stubAgentSession) CurrentSessionID() string                                     { return "stub-session" }
func (s *stubAgentSession) Alive() bool                                                  { return true }
func (s *stubAgentSession) Close() error                                                 { return nil }

type recordingAgentSession struct {
	stubAgentSession
	lastID     string
	lastResult PermissionResult
	calls      int
}

func (s *recordingAgentSession) RespondPermission(id string, res PermissionResult) error {
	s.lastID = id
	s.lastResult = res
	s.calls++
	return nil
}

type stubPlatformEngine struct {
	n    string
	sent []string
}

func (p *stubPlatformEngine) Name() string               { return p.n }
func (p *stubPlatformEngine) Start(MessageHandler) error { return nil }
func (p *stubPlatformEngine) Reply(_ context.Context, _ any, content string) error {
	p.sent = append(p.sent, content)
	return nil
}
func (p *stubPlatformEngine) Send(_ context.Context, _ any, content string) error {
	p.sent = append(p.sent, content)
	return nil
}
func (p *stubPlatformEngine) Stop() error { return nil }

type stubInlineButtonPlatform struct {
	stubPlatformEngine
	buttonContent string
	buttonRows    [][]ButtonOption
}

func (p *stubInlineButtonPlatform) SendWithButtons(_ context.Context, _ any, content string, buttons [][]ButtonOption) error {
	p.buttonContent = content
	p.buttonRows = buttons
	return nil
}

type stubCardPlatform struct {
	stubPlatformEngine
	repliedCards []*Card
	sentCards    []*Card
	cardErr      error
}

func (p *stubCardPlatform) ReplyCard(_ context.Context, _ any, card *Card) error {
	if p.cardErr != nil {
		return p.cardErr
	}
	p.repliedCards = append(p.repliedCards, card)
	return nil
}

func (p *stubCardPlatform) SendCard(_ context.Context, _ any, card *Card) error {
	if p.cardErr != nil {
		return p.cardErr
	}
	p.sentCards = append(p.sentCards, card)
	return nil
}

type stubModelModeAgent struct {
	stubAgent
	model           string
	mode            string
	reasoningEffort string
}

func (a *stubModelModeAgent) SetModel(model string) {
	a.model = model
}

func (a *stubModelModeAgent) GetModel() string {
	return a.model
}

func (a *stubModelModeAgent) AvailableModels(_ context.Context) []ModelOption {
	return []ModelOption{
		{Name: "gpt-4.1", Desc: "Balanced"},
		{Name: "gpt-4.1-mini", Desc: "Fast"},
	}
}

func (a *stubModelModeAgent) SetMode(mode string) {
	a.mode = mode
}

func (a *stubModelModeAgent) GetMode() string {
	if a.mode == "" {
		return "default"
	}
	return a.mode
}

func (a *stubModelModeAgent) PermissionModes() []PermissionModeInfo {
	return []PermissionModeInfo{
		{Key: "default", Name: "Default", NameZh: "默认", Desc: "Ask before risky actions", DescZh: "危险操作前询问"},
		{Key: "yolo", Name: "YOLO", NameZh: "放手做", Desc: "Skip confirmations", DescZh: "跳过确认"},
	}
}

func (a *stubModelModeAgent) SetReasoningEffort(effort string) {
	a.reasoningEffort = effort
}

func (a *stubModelModeAgent) GetReasoningEffort() string {
	return a.reasoningEffort
}

func (a *stubModelModeAgent) AvailableReasoningEfforts() []string {
	return []string{"low", "medium", "high", "xhigh"}
}

type stubListAgent struct {
	stubAgent
	sessions []AgentSessionInfo
}

func (a *stubListAgent) ListSessions(_ context.Context) ([]AgentSessionInfo, error) {
	return a.sessions, nil
}

type stubDeleteAgent struct {
	stubListAgent
	deleted []string
	errByID map[string]error
}

func (a *stubDeleteAgent) DeleteSession(_ context.Context, sessionID string) error {
	if err := a.errByID[sessionID]; err != nil {
		return err
	}
	a.deleted = append(a.deleted, sessionID)
	return nil
}

type stubProviderAgent struct {
	stubAgent
	providers []ProviderConfig
	active    string
}

func (a *stubProviderAgent) ListProviders() []ProviderConfig {
	return a.providers
}

func (a *stubProviderAgent) SetProviders(providers []ProviderConfig) {
	a.providers = providers
}

func (a *stubProviderAgent) GetActiveProvider() *ProviderConfig {
	for i := range a.providers {
		if a.providers[i].Name == a.active {
			return &a.providers[i]
		}
	}
	return nil
}

func (a *stubProviderAgent) SetActiveProvider(name string) bool {
	if name == "" {
		a.active = ""
		return true
	}
	for _, prov := range a.providers {
		if prov.Name == name {
			a.active = name
			return true
		}
	}
	return false
}

type stubUsageAgent struct {
	stubAgent
	report *UsageReport
	err    error
}

func (a *stubUsageAgent) GetUsage(_ context.Context) (*UsageReport, error) {
	return a.report, a.err
}

func newTestEngine() *Engine {
	return NewEngine("test", &stubAgent{}, []Platform{&stubPlatformEngine{n: "test"}}, "", LangEnglish)
}

func countCardActionValues(card *Card, prefix string) int {
	count := 0
	for _, elem := range card.Elements {
		switch e := elem.(type) {
		case CardActions:
			for _, btn := range e.Buttons {
				if strings.HasPrefix(btn.Value, prefix) {
					count++
				}
			}
		case CardListItem:
			if strings.HasPrefix(e.BtnValue, prefix) {
				count++
			}
		}
	}
	return count
}

func findCardAction(card *Card, value string) (CardButton, bool) {
	for _, elem := range card.Elements {
		switch e := elem.(type) {
		case CardActions:
			for _, btn := range e.Buttons {
				if btn.Value == value {
					return btn, true
				}
			}
		case CardListItem:
			if e.BtnValue == value {
				return CardButton{Text: e.BtnText, Type: e.BtnType, Value: e.BtnValue}, true
			}
		}
	}
	return CardButton{}, false
}

func collectCardActionRows(card *Card) []CardActions {
	rows := make([]CardActions, 0)
	for _, elem := range card.Elements {
		if row, ok := elem.(CardActions); ok {
			rows = append(rows, row)
		}
	}
	return rows
}

// --- alias tests ---

func TestEngine_Alias(t *testing.T) {
	e := newTestEngine()
	e.AddAlias("帮助", "/help")
	e.AddAlias("新建", "/new")

	got := e.resolveAlias("帮助")
	if got != "/help" {
		t.Errorf("resolveAlias('帮助') = %q, want /help", got)
	}

	got = e.resolveAlias("新建 my-session")
	if got != "/new my-session" {
		t.Errorf("resolveAlias('新建 my-session') = %q, want '/new my-session'", got)
	}

	got = e.resolveAlias("random text")
	if got != "random text" {
		t.Errorf("resolveAlias should not modify unmatched content, got %q", got)
	}
}

func TestEngine_ClearAliases(t *testing.T) {
	e := newTestEngine()
	e.AddAlias("帮助", "/help")
	e.ClearAliases()

	got := e.resolveAlias("帮助")
	if got != "帮助" {
		t.Errorf("after ClearAliases, should not resolve, got %q", got)
	}
}

// --- banned words tests ---

func TestEngine_BannedWords(t *testing.T) {
	e := newTestEngine()
	e.SetBannedWords([]string{"spam", "BadWord"})

	if w := e.matchBannedWord("this is spam content"); w != "spam" {
		t.Errorf("expected 'spam', got %q", w)
	}
	if w := e.matchBannedWord("CONTAINS BADWORD HERE"); w != "badword" {
		t.Errorf("expected case-insensitive match 'badword', got %q", w)
	}
	if w := e.matchBannedWord("clean message"); w != "" {
		t.Errorf("expected empty, got %q", w)
	}
}

func TestEngine_BannedWordsEmpty(t *testing.T) {
	e := newTestEngine()
	if w := e.matchBannedWord("anything"); w != "" {
		t.Errorf("no banned words set, should return empty, got %q", w)
	}
}

// --- disabled commands tests ---

func TestEngine_DisabledCommands(t *testing.T) {
	e := newTestEngine()
	e.SetDisabledCommands([]string{"upgrade", "restart"})

	if !e.disabledCmds["upgrade"] {
		t.Error("upgrade should be disabled")
	}
	if !e.disabledCmds["restart"] {
		t.Error("restart should be disabled")
	}
	if e.disabledCmds["help"] {
		t.Error("help should not be disabled")
	}
}

func TestEngine_DisabledCommandsWithSlash(t *testing.T) {
	e := newTestEngine()
	e.SetDisabledCommands([]string{"/upgrade"})

	if !e.disabledCmds["upgrade"] {
		t.Error("upgrade should be disabled even when prefixed with /")
	}
}

// --- admin_from tests ---

func TestEngine_AdminFrom_DenyByDefault(t *testing.T) {
	e := newTestEngine()
	p := &stubPlatformEngine{n: "test"}

	msg := &Message{SessionKey: "test:u1", UserID: "user1", ReplyCtx: "ctx"}
	e.handleCommand(p, msg, "/shell echo hi")

	if len(p.sent) != 1 {
		t.Fatalf("expected 1 reply, got %d", len(p.sent))
	}
	if !strings.Contains(p.sent[0], "admin") {
		t.Errorf("expected admin required message, got: %s", p.sent[0])
	}
}

func TestEngine_AdminFrom_ExplicitUser(t *testing.T) {
	e := newTestEngine()
	e.SetAdminFrom("admin1,admin2")
	p := &stubPlatformEngine{n: "test"}

	if !e.isAdmin("admin1") {
		t.Error("admin1 should be admin")
	}
	if !e.isAdmin("admin2") {
		t.Error("admin2 should be admin")
	}
	if e.isAdmin("user3") {
		t.Error("user3 should not be admin")
	}

	// non-admin user tries /shell
	msg := &Message{SessionKey: "test:u3", UserID: "user3", ReplyCtx: "ctx"}
	e.handleCommand(p, msg, "/shell echo hi")
	if len(p.sent) != 1 || !strings.Contains(p.sent[0], "admin") {
		t.Errorf("non-admin should be blocked from /shell, got: %v", p.sent)
	}
}

func TestEngine_AdminFrom_Wildcard(t *testing.T) {
	e := newTestEngine()
	e.SetAdminFrom("*")

	if !e.isAdmin("anyone") {
		t.Error("wildcard admin_from should allow any user")
	}
	if !e.isAdmin("12345") {
		t.Error("wildcard admin_from should allow any user ID")
	}
}

func TestEngine_AdminFrom_GatesRestart(t *testing.T) {
	e := newTestEngine()
	p := &stubPlatformEngine{n: "test"}

	msg := &Message{SessionKey: "test:u1", UserID: "user1", ReplyCtx: "ctx"}
	e.handleCommand(p, msg, "/restart")

	if len(p.sent) != 1 || !strings.Contains(p.sent[0], "admin") {
		t.Errorf("non-admin should be blocked from /restart, got: %v", p.sent)
	}
}

func TestEngine_AdminFrom_GatesUpgrade(t *testing.T) {
	e := newTestEngine()
	p := &stubPlatformEngine{n: "test"}

	msg := &Message{SessionKey: "test:u1", UserID: "user1", ReplyCtx: "ctx"}
	e.handleCommand(p, msg, "/upgrade")

	if len(p.sent) != 1 || !strings.Contains(p.sent[0], "admin") {
		t.Errorf("non-admin should be blocked from /upgrade, got: %v", p.sent)
	}
}

func TestEngine_AdminFrom_AllowsNonPrivileged(t *testing.T) {
	e := newTestEngine()
	p := &stubPlatformEngine{n: "test"}

	msg := &Message{SessionKey: "test:u1", UserID: "user1", ReplyCtx: "ctx"}
	e.handleCommand(p, msg, "/help")

	if len(p.sent) == 0 {
		t.Fatal("expected /help to produce a reply")
	}
	if strings.Contains(p.sent[0], "admin") {
		t.Errorf("/help should not require admin, got: %s", p.sent[0])
	}
}

func TestEngine_AdminFrom_GatesCommandsAddExec(t *testing.T) {
	e := newTestEngine()
	p := &stubPlatformEngine{n: "test"}

	msg := &Message{SessionKey: "test:u1", UserID: "user1", ReplyCtx: "ctx"}
	e.handleCommand(p, msg, "/commands addexec mysh echo hello")

	if len(p.sent) != 1 || !strings.Contains(p.sent[0], "admin") {
		t.Errorf("non-admin should be blocked from /commands addexec, got: %v", p.sent)
	}
}

func TestEngine_AdminFrom_GatesCustomExecCommand(t *testing.T) {
	e := newTestEngine()
	e.commands.Add("deploy", "", "", "echo deploying", "", "config")
	p := &stubPlatformEngine{n: "test"}

	msg := &Message{SessionKey: "test:u1", UserID: "user1", ReplyCtx: "ctx"}
	e.handleCommand(p, msg, "/deploy")

	if len(p.sent) != 1 || !strings.Contains(p.sent[0], "admin") {
		t.Errorf("non-admin should be blocked from custom exec command, got: %v", p.sent)
	}
}

func TestEngine_AdminFrom_AdminCanRunShell(t *testing.T) {
	e := newTestEngine()
	e.SetAdminFrom("admin1")
	p := &stubPlatformEngine{n: "test"}

	msg := &Message{SessionKey: "test:a1", UserID: "admin1", ReplyCtx: "ctx"}
	e.handleCommand(p, msg, "/shell echo hello")

	// Shell runs async in a goroutine, so the command should be accepted (not blocked).
	// No "admin" error should be in replies.
	for _, s := range p.sent {
		if strings.Contains(s, "admin") {
			t.Errorf("admin user should not be blocked, got: %s", s)
		}
	}
}

// --- permission prompt card tests ---

func TestSendPermissionPrompt_CardPlatform(t *testing.T) {
	e := newTestEngine()
	p := &stubCardPlatform{stubPlatformEngine: stubPlatformEngine{n: "feishu"}}

	e.sendPermissionPrompt(p, "ctx", "full prompt text", "write_file", "/tmp/test.txt")

	if len(p.sentCards) != 1 {
		t.Fatalf("expected 1 sent card, got %d", len(p.sentCards))
	}
	card := p.sentCards[0]
	if card.Header == nil || card.Header.Color != "orange" {
		t.Errorf("expected orange header, got %+v", card.Header)
	}
	if !card.HasButtons() {
		t.Error("expected card to have buttons")
	}
	buttons := card.CollectButtons()
	if len(buttons) < 2 {
		t.Fatalf("expected at least 2 button rows, got %d", len(buttons))
	}
	if buttons[0][0].Data != "perm:allow" {
		t.Errorf("expected first button data=perm:allow, got %s", buttons[0][0].Data)
	}
	if buttons[0][1].Data != "perm:deny" {
		t.Errorf("expected second button data=perm:deny, got %s", buttons[0][1].Data)
	}
	if buttons[1][0].Data != "perm:allow_all" {
		t.Errorf("expected third button data=perm:allow_all, got %s", buttons[1][0].Data)
	}
	if len(p.sent) != 0 {
		t.Errorf("plain text should not be sent when card is used, got %v", p.sent)
	}

	// Verify Extra fields carry i18n labels and body for card callback updates
	var allowBtn, denyBtn CardButton
	for _, elem := range card.Elements {
		if actions, ok := elem.(CardActions); ok {
			for _, btn := range actions.Buttons {
				switch btn.Value {
				case "perm:allow":
					allowBtn = btn
				case "perm:deny":
					denyBtn = btn
				}
			}
		}
	}
	if allowBtn.Extra == nil {
		t.Fatal("allow button should have Extra map")
	}
	if allowBtn.Extra["perm_color"] != "green" {
		t.Errorf("allow button perm_color should be green, got %s", allowBtn.Extra["perm_color"])
	}
	if allowBtn.Extra["perm_body"] == "" {
		t.Error("allow button perm_body should not be empty")
	}
	if !strings.Contains(allowBtn.Extra["perm_label"], "Allow") {
		t.Errorf("allow button perm_label should contain 'Allow', got %s", allowBtn.Extra["perm_label"])
	}
	if denyBtn.Extra["perm_color"] != "red" {
		t.Errorf("deny button perm_color should be red, got %s", denyBtn.Extra["perm_color"])
	}
}

func TestSendPermissionPrompt_InlineButtonPlatform(t *testing.T) {
	e := newTestEngine()
	p := &stubInlineButtonPlatform{stubPlatformEngine: stubPlatformEngine{n: "telegram"}}

	e.sendPermissionPrompt(p, "ctx", "full prompt text", "write_file", "/tmp/test.txt")

	if p.buttonContent != "full prompt text" {
		t.Errorf("expected button content to be prompt, got %s", p.buttonContent)
	}
	if len(p.buttonRows) < 2 {
		t.Fatalf("expected at least 2 button rows, got %d", len(p.buttonRows))
	}
	if p.buttonRows[0][0].Data != "perm:allow" {
		t.Errorf("expected perm:allow, got %s", p.buttonRows[0][0].Data)
	}
}

func TestSendPermissionPrompt_PlainPlatform(t *testing.T) {
	e := newTestEngine()
	p := &stubPlatformEngine{n: "plain"}

	e.sendPermissionPrompt(p, "ctx", "full prompt text", "write_file", "/tmp/test.txt")

	if len(p.sent) != 1 || p.sent[0] != "full prompt text" {
		t.Errorf("expected plain text fallback, got %v", p.sent)
	}
}

func TestCmdList_MultiWorkspaceUsesWorkspaceSessions(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	globalAgent := &stubListAgent{
		sessions: []AgentSessionInfo{
			{ID: "g1", Summary: "Global One", MessageCount: 1},
		},
	}
	e := NewEngine("test", globalAgent, []Platform{p}, "", LangEnglish)

	baseDir := t.TempDir()
	bindingPath := filepath.Join(t.TempDir(), "bindings.json")
	e.SetMultiWorkspace(baseDir, bindingPath)

	wsDir := filepath.Join(baseDir, "ws1")
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	channelID := "C123"
	e.workspaceBindings.Bind("project:test", channelID, "chan", wsDir)

	ws := e.workspacePool.GetOrCreate(wsDir)
	ws.agent = &stubListAgent{
		sessions: []AgentSessionInfo{
			{ID: "w1", Summary: "Workspace One", MessageCount: 2},
		},
	}
	ws.sessions = NewSessionManager("")

	msg := &Message{SessionKey: "slack:" + channelID + ":U1", ReplyCtx: "ctx"}
	e.cmdList(p, msg, nil)

	if len(p.sent) == 0 {
		t.Fatal("expected /list to send a response")
	}
	if strings.Contains(p.sent[0], "Global One") {
		t.Fatalf("expected workspace sessions, got global list: %q", p.sent[0])
	}
	if !strings.Contains(p.sent[0], "Workspace One") {
		t.Fatalf("expected workspace list to contain session summary, got %q", p.sent[0])
	}
}

func TestHandlePendingPermission_MultiWorkspaceLookup(t *testing.T) {
	e := newTestEngine()
	e.multiWorkspace = true

	sessionKey := "slack:C123:U1"
	interactiveKey := "/tmp/ws:" + sessionKey

	pending := &pendingPermission{
		RequestID: "req-1",
		ToolInput: map[string]any{"path": "/tmp/x"},
		Resolved:  make(chan struct{}),
	}
	session := &recordingAgentSession{}

	e.interactiveMu.Lock()
	e.interactiveStates[interactiveKey] = &interactiveState{
		agentSession: session,
		pending:      pending,
	}
	e.interactiveMu.Unlock()

	p := &stubPlatformEngine{n: "test"}
	msg := &Message{SessionKey: sessionKey, ReplyCtx: "ctx"}

	if !e.handlePendingPermission(p, msg, "allow") {
		t.Fatal("expected pending permission to be handled")
	}

	e.interactiveMu.Lock()
	state := e.interactiveStates[interactiveKey]
	e.interactiveMu.Unlock()
	if state == nil {
		t.Fatal("expected interactive state to remain")
	}
	state.mu.Lock()
	hasPending := state.pending != nil
	state.mu.Unlock()
	if hasPending {
		t.Fatal("expected pending permission to be cleared")
	}

	select {
	case <-pending.Resolved:
	default:
		t.Fatal("expected pending permission to be resolved")
	}

	if session.calls != 1 {
		t.Fatalf("RespondPermission calls = %d, want 1", session.calls)
	}
	if session.lastID != "req-1" {
		t.Fatalf("RespondPermission id = %q, want %q", session.lastID, "req-1")
	}
	if session.lastResult.Behavior != "allow" {
		t.Fatalf("RespondPermission behavior = %q, want %q", session.lastResult.Behavior, "allow")
	}
}

func TestHandlePendingPermission_ConfigWorkspaceLookup(t *testing.T) {
	dir := t.TempDir()
	p := &stubPlatformEngine{n: "telegram"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)

	e.SetConfigWorkspaces([]ConfigWorkspace{
		{ChannelKey: "-100123:topic:42", WorkDir: dir},
	})

	sessionKey := "telegram:-100123:topic:42:456"
	interactiveKey := dir + ":" + sessionKey

	pending := &pendingPermission{
		RequestID: "req-cfg-1",
		ToolInput: map[string]any{"command": "ls"},
		Resolved:  make(chan struct{}),
	}
	session := &recordingAgentSession{}

	e.interactiveMu.Lock()
	e.interactiveStates[interactiveKey] = &interactiveState{
		agentSession: session,
		pending:      pending,
	}
	e.interactiveMu.Unlock()

	msg := &Message{SessionKey: sessionKey, ReplyCtx: "ctx"}

	if !e.handlePendingPermission(p, msg, "allow") {
		t.Fatal("expected pending permission to be handled via config workspace suffix match")
	}

	select {
	case <-pending.Resolved:
	default:
		t.Fatal("expected pending permission to be resolved")
	}

	if session.calls != 1 {
		t.Fatalf("RespondPermission calls = %d, want 1", session.calls)
	}
	if session.lastResult.Behavior != "allow" {
		t.Fatalf("RespondPermission behavior = %q, want %q", session.lastResult.Behavior, "allow")
	}
}

// --- quiet tests ---

func TestQuietSessionToggle(t *testing.T) {
	e := newTestEngine()
	p := &stubPlatformEngine{n: "test"}
	msg := &Message{SessionKey: "test:user1", ReplyCtx: "ctx"}

	// /quiet — per-session toggle on
	e.cmdQuiet(p, msg, nil)

	e.interactiveMu.Lock()
	state := e.interactiveStates["test:user1"]
	e.interactiveMu.Unlock()

	if state == nil {
		t.Fatal("expected interactiveState to be created")
	}
	state.mu.Lock()
	q := state.quiet
	state.mu.Unlock()
	if !q {
		t.Fatal("expected session quiet to be true")
	}

	// /quiet — per-session toggle off
	e.cmdQuiet(p, msg, nil)
	state.mu.Lock()
	q = state.quiet
	state.mu.Unlock()
	if q {
		t.Fatal("expected session quiet to be false after second toggle")
	}
}

func TestQuietSessionResetsOnNewSession(t *testing.T) {
	e := newTestEngine()
	p := &stubPlatformEngine{n: "test"}
	msg := &Message{SessionKey: "test:user1", ReplyCtx: "ctx"}

	// Enable per-session quiet
	e.cmdQuiet(p, msg, nil)

	// Simulate /new
	e.cleanupInteractiveState("test:user1")

	// State should be gone, quiet resets
	e.interactiveMu.Lock()
	state := e.interactiveStates["test:user1"]
	e.interactiveMu.Unlock()
	if state != nil {
		t.Fatal("expected interactiveState to be cleaned up")
	}

	// Global quiet should still be off
	e.quietMu.RLock()
	gq := e.quiet
	e.quietMu.RUnlock()
	if gq {
		t.Fatal("expected global quiet to be false")
	}
}

func TestQuietGlobalToggle(t *testing.T) {
	e := newTestEngine()
	p := &stubPlatformEngine{n: "test"}
	msg := &Message{SessionKey: "test:user1", ReplyCtx: "ctx"}

	// Default: global quiet is off
	if e.quiet {
		t.Fatal("expected global quiet to be false by default")
	}

	// /quiet global — toggle on
	e.cmdQuiet(p, msg, []string{"global"})
	e.quietMu.RLock()
	q := e.quiet
	e.quietMu.RUnlock()
	if !q {
		t.Fatal("expected global quiet to be true")
	}

	// /quiet global — toggle off
	e.cmdQuiet(p, msg, []string{"global"})
	e.quietMu.RLock()
	q = e.quiet
	e.quietMu.RUnlock()
	if q {
		t.Fatal("expected global quiet to be false after second toggle")
	}
}

func TestQuietGlobalPersistsAcrossSessions(t *testing.T) {
	e := newTestEngine()
	p := &stubPlatformEngine{n: "test"}
	msg := &Message{SessionKey: "test:user1", ReplyCtx: "ctx"}

	// Enable global quiet
	e.cmdQuiet(p, msg, []string{"global"})

	// Simulate /new
	e.cleanupInteractiveState("test:user1")

	// Global quiet should still be on
	e.quietMu.RLock()
	q := e.quiet
	e.quietMu.RUnlock()
	if !q {
		t.Fatal("expected global quiet to remain true after session cleanup")
	}
}

func TestQuietGlobalAndSessionCombined(t *testing.T) {
	e := newTestEngine()
	p := &stubPlatformEngine{n: "test"}
	msg := &Message{SessionKey: "test:user1", ReplyCtx: "ctx"}

	// Only global quiet on — should suppress
	e.cmdQuiet(p, msg, []string{"global"})
	e.quietMu.RLock()
	gq := e.quiet
	e.quietMu.RUnlock()
	if !gq {
		t.Fatal("expected global quiet on")
	}

	// Session quiet is off (no state yet) — global alone should be enough
	e.interactiveMu.Lock()
	state := e.interactiveStates["test:user1"]
	e.interactiveMu.Unlock()
	if state != nil {
		t.Fatal("expected no session state yet")
	}

	// Turn off global, turn on session
	e.cmdQuiet(p, msg, []string{"global"}) // global off
	e.cmdQuiet(p, msg, nil)                // session on

	e.quietMu.RLock()
	gq = e.quiet
	e.quietMu.RUnlock()
	if gq {
		t.Fatal("expected global quiet off")
	}

	e.interactiveMu.Lock()
	state = e.interactiveStates["test:user1"]
	e.interactiveMu.Unlock()
	state.mu.Lock()
	sq := state.quiet
	state.mu.Unlock()
	if !sq {
		t.Fatal("expected session quiet on")
	}
}

func TestReplyWithCard_FallsBackToTextWhenPlatformHasNoCardSupport(t *testing.T) {
	p := &stubPlatformEngine{n: "plain"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	card := NewCard().Title("Help", "blue").Markdown("Plain fallback").Build()

	e.replyWithCard(p, "ctx", card)

	if len(p.sent) != 1 {
		t.Fatalf("sent messages = %d, want 1", len(p.sent))
	}
	if got, want := p.sent[0], card.RenderText(); got != want {
		t.Fatalf("fallback text = %q, want %q", got, want)
	}
}

func TestReplyWithCard_UsesCardSenderWhenSupported(t *testing.T) {
	p := &stubCardPlatform{stubPlatformEngine: stubPlatformEngine{n: "card"}}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	card := NewCard().Markdown("Interactive").Build()

	e.replyWithCard(p, "ctx", card)

	if len(p.repliedCards) != 1 {
		t.Fatalf("replied cards = %d, want 1", len(p.repliedCards))
	}
	if len(p.sent) != 0 {
		t.Fatalf("plain replies = %d, want 0", len(p.sent))
	}
}

func TestCmdHelp_UsesLegacyTextOnPlatformWithoutCardSupport(t *testing.T) {
	p := &stubPlatformEngine{n: "plain"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangChinese)
	msg := &Message{SessionKey: "test:user1", ReplyCtx: "ctx"}

	e.cmdHelp(p, msg)

	if len(p.sent) != 1 {
		t.Fatalf("sent messages = %d, want 1", len(p.sent))
	}
	if got := p.sent[0]; got != e.i18n.T(MsgHelp) {
		t.Fatalf("help text = %q, want legacy help text", got)
	}
	if strings.Contains(p.sent[0], "cc-connect 帮助") {
		t.Fatalf("help text = %q, should not be card title fallback", p.sent[0])
	}
}

func TestCmdList_UsesLegacyTextOnPlatformWithoutCardSupport(t *testing.T) {
	p := &stubPlatformEngine{n: "plain"}
	sessions := []AgentSessionInfo{{ID: "session-a", Summary: "First session", MessageCount: 3, ModifiedAt: time.Date(2026, 3, 11, 2, 0, 0, 0, time.UTC)}}
	e := NewEngine("test", &stubListAgent{sessions: sessions}, []Platform{p}, "", LangEnglish)
	msg := &Message{SessionKey: "test:user1", ReplyCtx: "ctx"}

	e.cmdList(p, msg, nil)

	if len(p.sent) != 1 {
		t.Fatalf("sent messages = %d, want 1", len(p.sent))
	}
	if !strings.Contains(p.sent[0], "Sessions") {
		t.Fatalf("list text = %q, want legacy list title", p.sent[0])
	}
	if strings.Contains(p.sent[0], "[← 返回]") {
		t.Fatalf("list text = %q, should not be card fallback text", p.sent[0])
	}
}

func TestCmdCurrent_UsesLegacyTextOnPlatformWithoutCardSupport(t *testing.T) {
	p := &stubPlatformEngine{n: "plain"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	msg := &Message{SessionKey: "test:user1", ReplyCtx: "ctx"}
	session := e.sessions.GetOrCreateActive(msg.SessionKey)
	session.Name = "Focus"
	session.SetAgentSessionID("session-123")
	session.History = append(session.History, HistoryEntry{Role: "user", Content: "hello", Timestamp: time.Now()})

	e.cmdCurrent(p, msg)

	if len(p.sent) != 1 {
		t.Fatalf("sent messages = %d, want 1", len(p.sent))
	}
	if !strings.Contains(p.sent[0], "Current session") {
		t.Fatalf("current text = %q, want legacy current session text", p.sent[0])
	}
	if strings.Contains(p.sent[0], "cc-connect") {
		t.Fatalf("current text = %q, should not be card fallback title", p.sent[0])
	}
}

func TestCmdDelete_BatchCommaList(t *testing.T) {
	p := &stubPlatformEngine{n: "plain"}
	agent := &stubDeleteAgent{stubListAgent: stubListAgent{sessions: []AgentSessionInfo{
		{ID: "session-1", Summary: "One"},
		{ID: "session-2", Summary: "Two"},
		{ID: "session-3", Summary: "Three"},
		{ID: "session-4", Summary: "Four"},
	}}}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)
	msg := &Message{SessionKey: "test:user1", ReplyCtx: "ctx"}

	e.cmdDelete(p, msg, []string{"1,2,3"})

	if got, want := strings.Join(agent.deleted, ","), "session-1,session-2,session-3"; got != want {
		t.Fatalf("deleted = %q, want %q", got, want)
	}
	if len(p.sent) != 1 {
		t.Fatalf("sent messages = %d, want 1", len(p.sent))
	}
	if !strings.Contains(p.sent[0], "Session deleted: One") || !strings.Contains(p.sent[0], "Session deleted: Three") {
		t.Fatalf("reply = %q, want combined delete summary", p.sent[0])
	}
}

func TestCmdDelete_BatchRange(t *testing.T) {
	p := &stubPlatformEngine{n: "plain"}
	agent := &stubDeleteAgent{stubListAgent: stubListAgent{sessions: []AgentSessionInfo{
		{ID: "session-1", Summary: "One"},
		{ID: "session-2", Summary: "Two"},
		{ID: "session-3", Summary: "Three"},
		{ID: "session-4", Summary: "Four"},
		{ID: "session-5", Summary: "Five"},
		{ID: "session-6", Summary: "Six"},
		{ID: "session-7", Summary: "Seven"},
		{ID: "session-8", Summary: "Eight"},
	}}}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)
	msg := &Message{SessionKey: "test:user1", ReplyCtx: "ctx"}

	e.cmdDelete(p, msg, []string{"3-7"})

	if got, want := strings.Join(agent.deleted, ","), "session-3,session-4,session-5,session-6,session-7"; got != want {
		t.Fatalf("deleted = %q, want %q", got, want)
	}
}

func TestCmdDelete_BatchMixedSyntax(t *testing.T) {
	p := &stubPlatformEngine{n: "plain"}
	agent := &stubDeleteAgent{stubListAgent: stubListAgent{sessions: []AgentSessionInfo{
		{ID: "session-1", Summary: "One"},
		{ID: "session-2", Summary: "Two"},
		{ID: "session-3", Summary: "Three"},
		{ID: "session-4", Summary: "Four"},
		{ID: "session-5", Summary: "Five"},
		{ID: "session-6", Summary: "Six"},
		{ID: "session-7", Summary: "Seven"},
		{ID: "session-8", Summary: "Eight"},
	}}}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)
	msg := &Message{SessionKey: "test:user1", ReplyCtx: "ctx"}

	e.cmdDelete(p, msg, []string{"1,3-5,8"})

	if got, want := strings.Join(agent.deleted, ","), "session-1,session-3,session-4,session-5,session-8"; got != want {
		t.Fatalf("deleted = %q, want %q", got, want)
	}
}

func TestCmdDelete_InvalidExplicitBatchSyntaxShowsUsage(t *testing.T) {
	p := &stubPlatformEngine{n: "plain"}
	agent := &stubDeleteAgent{stubListAgent: stubListAgent{sessions: []AgentSessionInfo{
		{ID: "session-1", Summary: "One"},
		{ID: "session-2", Summary: "Two"},
		{ID: "session-3", Summary: "Three"},
	}}}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)
	msg := &Message{SessionKey: "test:user1", ReplyCtx: "ctx"}

	e.cmdDelete(p, msg, []string{"1,3-a,8"})

	if len(agent.deleted) != 0 {
		t.Fatalf("deleted = %v, want none", agent.deleted)
	}
	if len(p.sent) != 1 || p.sent[0] != e.i18n.T(MsgDeleteUsage) {
		t.Fatalf("sent = %v, want usage", p.sent)
	}
}

func TestCmdDelete_WhitespaceSeparatedArgsAreRejected(t *testing.T) {
	p := &stubPlatformEngine{n: "plain"}
	agent := &stubDeleteAgent{stubListAgent: stubListAgent{sessions: []AgentSessionInfo{
		{ID: "session-1", Summary: "One"},
		{ID: "session-2", Summary: "Two"},
		{ID: "session-3", Summary: "Three"},
	}}}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)
	msg := &Message{SessionKey: "test:user1", ReplyCtx: "ctx"}

	e.cmdDelete(p, msg, []string{"1", "2", "3"})

	if len(agent.deleted) != 0 {
		t.Fatalf("deleted = %v, want none", agent.deleted)
	}
	if len(p.sent) != 1 || p.sent[0] != e.i18n.T(MsgDeleteUsage) {
		t.Fatalf("sent = %v, want usage", p.sent)
	}
}

func TestCmdDelete_SingleSessionPrefixStillWorks(t *testing.T) {
	p := &stubPlatformEngine{n: "plain"}
	agent := &stubDeleteAgent{stubListAgent: stubListAgent{sessions: []AgentSessionInfo{
		{ID: "abc123456789", Summary: "One"},
		{ID: "def987654321", Summary: "Two"},
	}}}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)
	msg := &Message{SessionKey: "test:user1", ReplyCtx: "ctx"}

	e.cmdDelete(p, msg, []string{"abc123"})

	if got, want := strings.Join(agent.deleted, ","), "abc123456789"; got != want {
		t.Fatalf("deleted = %q, want %q", got, want)
	}
}

func TestCmdDelete_NoArgsOnCardPlatformShowsDeleteModeCard(t *testing.T) {
	p := &stubCardPlatform{stubPlatformEngine: stubPlatformEngine{n: "feishu"}}
	agent := &stubDeleteAgent{stubListAgent: stubListAgent{sessions: []AgentSessionInfo{
		{ID: "session-1", Summary: "One"},
		{ID: "session-2", Summary: "Two"},
	}}}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)
	msg := &Message{SessionKey: "feishu:user1", ReplyCtx: "ctx"}

	e.cmdDelete(p, msg, nil)

	if len(p.repliedCards) != 1 {
		t.Fatalf("replied cards = %d, want 1", len(p.repliedCards))
	}
	card := p.repliedCards[0]
	if got := countCardActionValues(card, "act:/delete-mode toggle "); got != 2 {
		t.Fatalf("toggle action count = %d, want 2", got)
	}
	if _, ok := findCardAction(card, "act:/delete-mode cancel"); !ok {
		t.Fatal("expected delete mode cancel action")
	}
}

func TestDeleteMode_ToggleSelectionReturnsUpdatedCard(t *testing.T) {
	p := &stubCardPlatform{stubPlatformEngine: stubPlatformEngine{n: "feishu"}}
	agent := &stubDeleteAgent{stubListAgent: stubListAgent{sessions: []AgentSessionInfo{
		{ID: "session-1", Summary: "One"},
		{ID: "session-2", Summary: "Two"},
	}}}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)
	msg := &Message{SessionKey: "feishu:user1", ReplyCtx: "ctx"}

	e.cmdDelete(p, msg, nil)
	card := e.handleCardNav("act:/delete-mode toggle session-2", msg.SessionKey)
	if card == nil {
		t.Fatal("expected card update after toggle")
	}
	if !strings.Contains(card.RenderText(), "1 selected") {
		t.Fatalf("card text = %q, want selected count", card.RenderText())
	}

	confirmCard := e.handleCardNav("act:/delete-mode confirm", msg.SessionKey)
	if confirmCard == nil {
		t.Fatal("expected confirmation card")
	}
	if !strings.Contains(confirmCard.RenderText(), "Two") {
		t.Fatalf("confirmation text = %q, want selected session", confirmCard.RenderText())
	}
}

func TestDeleteMode_ConfirmAndSubmitDeletesSelectedSessions(t *testing.T) {
	p := &stubCardPlatform{stubPlatformEngine: stubPlatformEngine{n: "feishu"}}
	agent := &stubDeleteAgent{stubListAgent: stubListAgent{sessions: []AgentSessionInfo{
		{ID: "session-1", Summary: "One"},
		{ID: "session-2", Summary: "Two"},
		{ID: "session-3", Summary: "Three"},
	}}}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)
	msg := &Message{SessionKey: "feishu:user1", ReplyCtx: "ctx"}

	e.cmdDelete(p, msg, nil)
	_ = e.handleCardNav("act:/delete-mode toggle session-1", msg.SessionKey)
	_ = e.handleCardNav("act:/delete-mode toggle session-3", msg.SessionKey)

	confirmCard := e.handleCardNav("act:/delete-mode confirm", msg.SessionKey)
	if confirmCard == nil {
		t.Fatal("expected confirmation card")
	}
	confirmText := confirmCard.RenderText()
	if !strings.Contains(confirmText, "One") || !strings.Contains(confirmText, "Three") {
		t.Fatalf("confirmation text = %q, want selected session names", confirmText)
	}

	resultCard := e.handleCardNav("act:/delete-mode submit", msg.SessionKey)
	if resultCard == nil {
		t.Fatal("expected result card after submit")
	}
	if got, want := strings.Join(agent.deleted, ","), "session-1,session-3"; got != want {
		t.Fatalf("deleted = %q, want %q", got, want)
	}
	if !strings.Contains(resultCard.RenderText(), "Session deleted: One") {
		t.Fatalf("result text = %q, want delete result", resultCard.RenderText())
	}
}

func TestDeleteMode_SubmitReportsMissingSelectedSessions(t *testing.T) {
	p := &stubCardPlatform{stubPlatformEngine: stubPlatformEngine{n: "feishu"}}
	agent := &stubDeleteAgent{stubListAgent: stubListAgent{sessions: []AgentSessionInfo{
		{ID: "session-1", Summary: "One"},
		{ID: "session-2", Summary: "Two"},
		{ID: "session-3", Summary: "Three"},
	}}}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)
	msg := &Message{SessionKey: "feishu:user1", ReplyCtx: "ctx"}

	e.cmdDelete(p, msg, nil)
	_ = e.handleCardNav("act:/delete-mode toggle session-1", msg.SessionKey)
	_ = e.handleCardNav("act:/delete-mode toggle session-3", msg.SessionKey)

	agent.sessions = []AgentSessionInfo{
		{ID: "session-1", Summary: "One"},
		{ID: "session-2", Summary: "Two"},
	}

	resultCard := e.handleCardNav("act:/delete-mode submit", msg.SessionKey)
	if resultCard == nil {
		t.Fatal("expected result card after submit")
	}
	resultText := resultCard.RenderText()
	if !strings.Contains(resultText, "Session deleted: One") {
		t.Fatalf("result text = %q, want deleted session line", resultText)
	}
	if !strings.Contains(resultText, "Missing selected session") || !strings.Contains(resultText, "session-3") {
		t.Fatalf("result text = %q, want missing selected session to be reported", resultText)
	}
}

func TestDeleteMode_CancelReturnsListCard(t *testing.T) {
	p := &stubCardPlatform{stubPlatformEngine: stubPlatformEngine{n: "feishu"}}
	agent := &stubDeleteAgent{stubListAgent: stubListAgent{sessions: []AgentSessionInfo{
		{ID: "session-1", Summary: "One"},
		{ID: "session-2", Summary: "Two"},
	}}}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)
	msg := &Message{SessionKey: "feishu:user1", ReplyCtx: "ctx"}

	e.cmdDelete(p, msg, nil)
	card := e.handleCardNav("act:/delete-mode cancel", msg.SessionKey)
	if card == nil {
		t.Fatal("expected list card after cancel")
	}
	if got := countCardActionValues(card, "act:/switch "); got != 2 {
		t.Fatalf("switch action count = %d, want 2", got)
	}
}

func TestDeleteMode_ConfirmWithoutSelectionShowsHint(t *testing.T) {
	p := &stubCardPlatform{stubPlatformEngine: stubPlatformEngine{n: "feishu"}}
	agent := &stubDeleteAgent{stubListAgent: stubListAgent{sessions: []AgentSessionInfo{
		{ID: "session-1", Summary: "One"},
		{ID: "session-2", Summary: "Two"},
	}}}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)
	msg := &Message{SessionKey: "feishu:user1", ReplyCtx: "ctx"}

	e.cmdDelete(p, msg, nil)
	card := e.handleCardNav("act:/delete-mode confirm", msg.SessionKey)
	if card == nil {
		t.Fatal("expected delete mode card when confirming empty selection")
	}
	if !strings.Contains(card.RenderText(), "Select at least one session.") {
		t.Fatalf("card text = %q, want empty-selection hint", card.RenderText())
	}
}

func TestDeleteMode_PageNavigationPreservesSelection(t *testing.T) {
	p := &stubCardPlatform{stubPlatformEngine: stubPlatformEngine{n: "feishu"}}
	sessions := make([]AgentSessionInfo, 0, 8)
	for i := 1; i <= 8; i++ {
		sessions = append(sessions, AgentSessionInfo{ID: fmt.Sprintf("session-%d", i), Summary: fmt.Sprintf("Session %d", i)})
	}
	agent := &stubDeleteAgent{stubListAgent: stubListAgent{sessions: sessions}}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)
	msg := &Message{SessionKey: "feishu:user1", ReplyCtx: "ctx"}

	e.cmdDelete(p, msg, nil)
	_ = e.handleCardNav("act:/delete-mode toggle session-1", msg.SessionKey)
	pageTwo := e.handleCardNav("act:/delete-mode page 2", msg.SessionKey)
	if pageTwo == nil {
		t.Fatal("expected page 2 card")
	}
	if !strings.Contains(pageTwo.RenderText(), "1 selected") {
		t.Fatalf("page 2 text = %q, want preserved selected count", pageTwo.RenderText())
	}
	pageOne := e.handleCardNav("act:/delete-mode page 1", msg.SessionKey)
	if pageOne == nil {
		t.Fatal("expected page 1 card")
	}
	btn, ok := findCardAction(pageOne, "act:/delete-mode toggle session-1")
	if !ok {
		t.Fatal("expected toggle action for session-1")
	}
	if btn.Type != "primary" {
		t.Fatalf("selected button type = %q, want primary", btn.Type)
	}
}

func TestDeleteMode_SubmitBlocksActiveSession(t *testing.T) {
	p := &stubCardPlatform{stubPlatformEngine: stubPlatformEngine{n: "feishu"}}
	agent := &stubDeleteAgent{stubListAgent: stubListAgent{sessions: []AgentSessionInfo{
		{ID: "session-1", Summary: "One"},
		{ID: "session-2", Summary: "Two"},
	}}}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)
	msg := &Message{SessionKey: "feishu:user1", ReplyCtx: "ctx"}
	e.sessions.GetOrCreateActive(msg.SessionKey).SetAgentSessionID("session-1")

	e.cmdDelete(p, msg, nil)
	_ = e.handleCardNav("act:/delete-mode toggle session-1", msg.SessionKey)
	resultCard := e.handleCardNav("act:/delete-mode submit", msg.SessionKey)
	if resultCard == nil {
		t.Fatal("expected result card")
	}
	if len(agent.deleted) != 0 {
		t.Fatalf("deleted = %v, want none", agent.deleted)
	}
	if !strings.Contains(resultCard.RenderText(), "Cannot delete the currently active session") {
		t.Fatalf("result text = %q, want active-session warning", resultCard.RenderText())
	}
}

func TestDeleteMode_ActiveSessionMarkedWithArrowAndNotSelectable(t *testing.T) {
	p := &stubCardPlatform{stubPlatformEngine: stubPlatformEngine{n: "feishu"}}
	agent := &stubDeleteAgent{stubListAgent: stubListAgent{sessions: []AgentSessionInfo{
		{ID: "session-1", Summary: "One"},
		{ID: "session-2", Summary: "Two"},
	}}}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)
	msg := &Message{SessionKey: "feishu:123:123", ReplyCtx: "ctx"}
	e.sessions.GetOrCreateActive(msg.SessionKey).SetAgentSessionID("session-1")

	e.cmdDelete(p, msg, nil)
	if len(p.repliedCards) != 1 {
		t.Fatalf("replied cards = %d, want 1", len(p.repliedCards))
	}
	card := p.repliedCards[0]
	if _, ok := findCardAction(card, "act:/delete-mode toggle session-1"); ok {
		t.Fatal("active session should not be toggle-selectable")
	}
	if _, ok := findCardAction(card, "act:/delete-mode noop session-1"); !ok {
		t.Fatal("expected noop action for active session")
	}
	if got := countCardActionValues(card, "act:/delete-mode toggle "); got != 1 {
		t.Fatalf("toggle action count = %d, want 1", got)
	}
	if !strings.Contains(card.RenderText(), "▶ **1.**") {
		t.Fatalf("card text = %q, want arrow marker for active session", card.RenderText())
	}
}

func TestDeleteMode_FormSubmitShowsConfirmThenDeletes(t *testing.T) {
	p := &stubCardPlatform{stubPlatformEngine: stubPlatformEngine{n: "feishu"}}
	agent := &stubDeleteAgent{stubListAgent: stubListAgent{sessions: []AgentSessionInfo{
		{ID: "session-1", Summary: "One"},
		{ID: "session-2", Summary: "Two"},
		{ID: "session-3", Summary: "Three"},
	}}}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)
	msg := &Message{SessionKey: "feishu:user1", ReplyCtx: "ctx"}

	e.cmdDelete(p, msg, nil)
	confirmCard := e.handleCardNav("act:/delete-mode form-submit session-1,session-3", msg.SessionKey)
	if confirmCard == nil {
		t.Fatal("expected confirm card after form-submit")
	}
	if len(agent.deleted) != 0 {
		t.Fatalf("deleted = %v, want none before confirm", agent.deleted)
	}
	confirmText := confirmCard.RenderText()
	if !strings.Contains(confirmText, "One") || !strings.Contains(confirmText, "Three") {
		t.Fatalf("confirm text = %q, want selected sessions", confirmText)
	}

	resultCard := e.handleCardNav("act:/delete-mode submit", msg.SessionKey)
	if resultCard == nil {
		t.Fatal("expected result card after submit")
	}
	if got, want := strings.Join(agent.deleted, ","), "session-1,session-3"; got != want {
		t.Fatalf("deleted = %q, want %q", got, want)
	}
	if !strings.Contains(resultCard.RenderText(), "Session deleted: One") {
		t.Fatalf("result text = %q, want delete result", resultCard.RenderText())
	}
}

func TestExecuteCardActionStop_PreservesQuietStateWithoutCleanupReinsert(t *testing.T) {
	e := newTestEngine()
	e.interactiveMu.Lock()
	e.interactiveStates["test:user1"] = &interactiveState{quiet: true}
	e.interactiveMu.Unlock()

	e.executeCardAction("/stop", "", "test:user1")

	e.interactiveMu.Lock()
	state := e.interactiveStates["test:user1"]
	e.interactiveMu.Unlock()
	if state == nil {
		t.Fatal("expected interactive state to remain for quiet preservation")
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.quiet {
		t.Fatal("expected quiet state to remain enabled")
	}
	if state.pending != nil {
		t.Fatal("expected pending permission to be cleared")
	}
}

func TestCmdLang_UsesInlineButtonsOnButtonOnlyPlatform(t *testing.T) {
	p := &stubInlineButtonPlatform{stubPlatformEngine: stubPlatformEngine{n: "inline-only"}}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)

	e.cmdLang(p, &Message{SessionKey: "test:123:123", ReplyCtx: "ctx"}, nil)

	if len(p.buttonRows) == 0 {
		t.Fatal("expected /lang to send inline buttons on button-only platform")
	}
	if got := p.buttonRows[0][0].Data; got != "cmd:/lang en" {
		t.Fatalf("first /lang button = %q, want %q", got, "cmd:/lang en")
	}
}

func TestCmdLang_UsesPlainTextChoicesOnPlatformWithoutCardsOrButtons(t *testing.T) {
	p := &stubPlatformEngine{n: "plain"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)

	e.cmdLang(p, &Message{SessionKey: "test:123:123", ReplyCtx: "ctx"}, nil)

	if len(p.sent) != 1 {
		t.Fatalf("sent messages = %d, want 1", len(p.sent))
	}
	if !strings.Contains(p.sent[0], "/lang en") || !strings.Contains(p.sent[0], "/lang auto") {
		t.Fatalf("lang text = %q, want plain-text language choices", p.sent[0])
	}
}

func TestCmdProvider_UsesLegacyTextOnPlatformWithoutCardSupport(t *testing.T) {
	p := &stubPlatformEngine{n: "plain"}
	agent := &stubProviderAgent{
		providers: []ProviderConfig{
			{Name: "openai", BaseURL: "https://api.openai.com", Model: "gpt-4.1"},
			{Name: "azure", BaseURL: "https://azure.example", Model: "gpt-4.1-mini"},
		},
		active: "openai",
	}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)

	e.cmdProvider(p, &Message{SessionKey: "test:123:123", ReplyCtx: "ctx"}, nil)

	if len(p.sent) != 1 {
		t.Fatalf("sent messages = %d, want 1", len(p.sent))
	}
	if !strings.Contains(p.sent[0], "Active provider") {
		t.Fatalf("provider text = %q, want current provider section", p.sent[0])
	}
	if !strings.Contains(p.sent[0], "openai") || !strings.Contains(p.sent[0], "azure") {
		t.Fatalf("provider text = %q, want provider list", p.sent[0])
	}
	if !strings.Contains(p.sent[0], "switch") {
		t.Fatalf("provider text = %q, want switch hint", p.sent[0])
	}
}

func TestCmdModel_UsesInlineButtonsOnButtonOnlyPlatform(t *testing.T) {
	p := &stubInlineButtonPlatform{stubPlatformEngine: stubPlatformEngine{n: "inline-only"}}
	agent := &stubModelModeAgent{}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)

	e.cmdModel(p, &Message{SessionKey: "test:123:123", ReplyCtx: "ctx"}, nil)

	if len(p.buttonRows) == 0 {
		t.Fatal("expected /model to send inline buttons on button-only platform")
	}
	if got := p.buttonRows[0][0].Data; got != "cmd:/model 1" {
		t.Fatalf("first /model button = %q, want %q", got, "cmd:/model 1")
	}
}

func TestCmdReasoning_UsesInlineButtonsOnButtonOnlyPlatform(t *testing.T) {
	p := &stubInlineButtonPlatform{stubPlatformEngine: stubPlatformEngine{n: "inline-only"}}
	agent := &stubModelModeAgent{}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)

	e.cmdReasoning(p, &Message{SessionKey: "test:123:123", ReplyCtx: "ctx"}, nil)

	if len(p.buttonRows) == 0 {
		t.Fatal("expected /reasoning to send inline buttons on button-only platform")
	}
	if got := p.buttonRows[0][0].Data; got != "cmd:/reasoning 1" {
		t.Fatalf("first /reasoning button = %q, want %q", got, "cmd:/reasoning 1")
	}
	if got := p.buttonRows[0][0].Text; got != "low" {
		t.Fatalf("first /reasoning button text = %q, want low", got)
	}
}

func TestCmdReasoning_SwitchesEffortAndResetsSession(t *testing.T) {
	p := &stubPlatformEngine{n: "plain"}
	agent := &stubModelModeAgent{}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)
	msg := &Message{SessionKey: "test:123:123", ReplyCtx: "ctx"}

	s := e.sessions.GetOrCreateActive(msg.SessionKey)
	s.SetAgentSessionID("existing-session")
	s.AddHistory("user", "hello")

	e.cmdReasoning(p, msg, []string{"3"})

	if agent.reasoningEffort != "high" {
		t.Fatalf("reasoning effort = %q, want high", agent.reasoningEffort)
	}
	if s.GetAgentSessionID() != "" {
		t.Fatalf("AgentSessionID = %q, want cleared", s.GetAgentSessionID())
	}
	if len(s.History) != 0 {
		t.Fatalf("history length = %d, want 0", len(s.History))
	}
	if len(p.sent) != 1 || !strings.Contains(p.sent[0], "Reasoning effort switched to `high`") {
		t.Fatalf("sent = %v, want reasoning changed message", p.sent)
	}
}

func TestCmdReasoning_RejectsMinimal(t *testing.T) {
	p := &stubPlatformEngine{n: "plain"}
	agent := &stubModelModeAgent{}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)
	msg := &Message{SessionKey: "test:123:123", ReplyCtx: "ctx"}

	e.cmdReasoning(p, msg, []string{"minimal"})

	if agent.reasoningEffort != "" {
		t.Fatalf("reasoning effort = %q, want unchanged empty", agent.reasoningEffort)
	}
	if len(p.sent) != 1 || !strings.Contains(p.sent[0], "/reasoning <number>") || strings.Contains(p.sent[0], "minimal") {
		t.Fatalf("sent = %v, want usage without minimal", p.sent)
	}
}

func TestCmdMode_UsesInlineButtonsOnButtonOnlyPlatform(t *testing.T) {
	p := &stubInlineButtonPlatform{stubPlatformEngine: stubPlatformEngine{n: "inline-only"}}
	agent := &stubModelModeAgent{}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)

	e.cmdMode(p, &Message{SessionKey: "test:123:123", ReplyCtx: "ctx"}, nil)

	if len(p.buttonRows) == 0 {
		t.Fatal("expected /mode to send inline buttons on button-only platform")
	}
	if got := p.buttonRows[0][0].Data; got != "cmd:/mode default" {
		t.Fatalf("first /mode button = %q, want %q", got, "cmd:/mode default")
	}
}

func TestCmdStatus_UsesLegacyTextOnPlatformWithoutCardSupport(t *testing.T) {
	p := &stubPlatformEngine{n: "plain"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	msg := &Message{SessionKey: "test:user1", ReplyCtx: "ctx"}

	e.cmdStatus(p, msg)

	if len(p.sent) != 1 {
		t.Fatalf("sent messages = %d, want 1", len(p.sent))
	}
	if !strings.Contains(p.sent[0], "Status") {
		t.Fatalf("status text = %q, want legacy status text", p.sent[0])
	}
	if strings.Contains(p.sent[0], "[← Back]") {
		t.Fatalf("status text = %q, should not be card fallback text", p.sent[0])
	}
}

func TestCmdUsage_UnsupportedAgent(t *testing.T) {
	p := &stubPlatformEngine{n: "plain"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	msg := &Message{SessionKey: "test:user1", ReplyCtx: "ctx"}

	e.handleCommand(p, msg, "/usage")

	if len(p.sent) != 1 {
		t.Fatalf("sent messages = %d, want 1", len(p.sent))
	}
	if !strings.Contains(strings.ToLower(p.sent[0]), "does not support") {
		t.Fatalf("sent = %q, want unsupported usage message", p.sent[0])
	}
}

func TestCmdUsage_Success(t *testing.T) {
	p := &stubPlatformEngine{n: "plain"}
	agent := &stubUsageAgent{
		report: &UsageReport{
			Provider: "codex",
			Email:    "dev@example.com",
			Plan:     "team",
			Buckets: []UsageBucket{
				{
					Name:         "Rate limit",
					Allowed:      true,
					LimitReached: false,
					Windows: []UsageWindow{
						{Name: "Primary", UsedPercent: 23, WindowSeconds: 18000, ResetAfterSeconds: 6665},
						{Name: "Secondary", UsedPercent: 42, WindowSeconds: 604800, ResetAfterSeconds: 512698},
					},
				},
				{
					Name:         "Code review",
					Allowed:      true,
					LimitReached: false,
					Windows: []UsageWindow{
						{Name: "Primary", UsedPercent: 0, WindowSeconds: 604800, ResetAfterSeconds: 604800},
					},
				},
			},
			Credits: &UsageCredits{
				HasCredits: false,
				Unlimited:  false,
			},
		},
	}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)
	msg := &Message{SessionKey: "test:user1", ReplyCtx: "ctx"}

	e.handleCommand(p, msg, "/usage")

	if len(p.sent) != 1 {
		t.Fatalf("sent messages = %d, want 1", len(p.sent))
	}
	got := p.sent[0]
	for _, want := range []string{
		"Account: dev@example.com (team)",
		"5h limit",
		"Remaining: 77%",
		"Resets: 1h 51m",
		"5h limit",
		"7d limit",
		"Remaining: 58%",
		"Resets: 5d 22h 24m",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("usage text = %q, want substring %q", got, want)
		}
	}
	if strings.Contains(got, "```") {
		t.Fatalf("usage text = %q, should not use code block on plain platform", got)
	}
}

func TestCmdUsage_UsesCardOnCardPlatform(t *testing.T) {
	p := &stubCardPlatform{stubPlatformEngine: stubPlatformEngine{n: "feishu"}}
	agent := &stubUsageAgent{
		report: &UsageReport{
			Email: "dev@example.com",
			Plan:  "team",
			Buckets: []UsageBucket{
				{
					Name:         "Rate limit",
					Allowed:      true,
					LimitReached: false,
					Windows: []UsageWindow{
						{Name: "Primary", UsedPercent: 23, WindowSeconds: 18000, ResetAfterSeconds: 6665},
						{Name: "Secondary", UsedPercent: 42, WindowSeconds: 604800, ResetAfterSeconds: 512698},
					},
				},
			},
		},
	}
	e := NewEngine("test", agent, []Platform{p}, "", LangChinese)
	msg := &Message{SessionKey: "test:user1", ReplyCtx: "ctx"}

	e.handleCommand(p, msg, "/usage")

	if len(p.repliedCards) != 1 {
		t.Fatalf("replied cards = %d, want 1", len(p.repliedCards))
	}
	if len(p.sent) != 0 {
		t.Fatalf("sent text = %v, want no plain text fallback", p.sent)
	}
	text := p.repliedCards[0].RenderText()
	for _, want := range []string{
		"账号：dev@example.com (team)",
		"5小时限额",
		"剩余：77%",
		"重置：1小时 51分钟",
		"7日限额",
		"剩余：58%",
		"重置：5天 22小时 24分钟",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("card text = %q, want substring %q", text, want)
		}
	}
}

func TestCmdUsage_LocalizedChinese(t *testing.T) {
	p := &stubPlatformEngine{n: "plain"}
	agent := &stubUsageAgent{
		report: &UsageReport{
			Email: "dev@example.com",
			Plan:  "team",
			Buckets: []UsageBucket{
				{
					Name:         "Rate limit",
					Allowed:      true,
					LimitReached: false,
					Windows: []UsageWindow{
						{Name: "Primary", UsedPercent: 23, WindowSeconds: 18000, ResetAfterSeconds: 6665},
						{Name: "Secondary", UsedPercent: 42, WindowSeconds: 604800, ResetAfterSeconds: 512698},
					},
				},
			},
		},
	}
	e := NewEngine("test", agent, []Platform{p}, "", LangChinese)
	msg := &Message{SessionKey: "test:user1", ReplyCtx: "ctx"}

	e.handleCommand(p, msg, "/usage")

	if len(p.sent) != 1 {
		t.Fatalf("sent messages = %d, want 1", len(p.sent))
	}
	got := p.sent[0]
	for _, want := range []string{
		"账号：dev@example.com (team)",
		"5小时限额",
		"剩余：77%",
		"重置：1小时 51分钟",
		"7日限额",
		"剩余：58%",
		"重置：5天 22小时 24分钟",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("usage text = %q, want substring %q", got, want)
		}
	}
	if strings.Contains(got, "```") {
		t.Fatalf("usage text = %q, should not use code block on plain platform", got)
	}
}

func TestCmdCommands_UsesLegacyTextOnPlatformWithoutCardSupport(t *testing.T) {
	p := &stubPlatformEngine{n: "plain"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	e.AddCommand("deploy", "Deploy app", "ship it", "", "", "config")

	e.cmdCommands(p, &Message{SessionKey: "test:user1", ReplyCtx: "ctx"}, nil)

	if len(p.sent) != 1 {
		t.Fatalf("sent messages = %d, want 1", len(p.sent))
	}
	if !strings.Contains(p.sent[0], "/deploy") {
		t.Fatalf("commands text = %q, want legacy command list", p.sent[0])
	}
	if strings.Contains(p.sent[0], "[← Back]") {
		t.Fatalf("commands text = %q, should not be card fallback text", p.sent[0])
	}
}

func TestCmdConfig_UsesLegacyTextOnPlatformWithoutCardSupport(t *testing.T) {
	p := &stubPlatformEngine{n: "plain"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)

	e.cmdConfig(p, &Message{SessionKey: "test:user1", ReplyCtx: "ctx"}, nil)

	if len(p.sent) != 1 {
		t.Fatalf("sent messages = %d, want 1", len(p.sent))
	}
	if !strings.Contains(p.sent[0], "thinking_max_len") {
		t.Fatalf("config text = %q, want legacy config list", p.sent[0])
	}
	if strings.Contains(p.sent[0], "[← Back]") {
		t.Fatalf("config text = %q, should not be card fallback text", p.sent[0])
	}
}

func TestCmdAlias_UsesLegacyTextOnPlatformWithoutCardSupport(t *testing.T) {
	p := &stubPlatformEngine{n: "plain"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	e.AddAlias("ls", "/list")

	e.cmdAlias(p, &Message{SessionKey: "test:user1", ReplyCtx: "ctx"}, nil)

	if len(p.sent) != 1 {
		t.Fatalf("sent messages = %d, want 1", len(p.sent))
	}
	if !strings.Contains(p.sent[0], "ls") || !strings.Contains(p.sent[0], "/list") {
		t.Fatalf("alias text = %q, want legacy alias list", p.sent[0])
	}
	if strings.Contains(p.sent[0], "[← Back]") {
		t.Fatalf("alias text = %q, should not be card fallback text", p.sent[0])
	}
}

func TestCmdSkills_UsesLegacyTextOnPlatformWithoutCardSupport(t *testing.T) {
	p := &stubPlatformEngine{n: "plain"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	temp := t.TempDir()
	skillDir := temp + "/demo"
	if err := os.Mkdir(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	if err := os.WriteFile(skillDir+"/SKILL.md", []byte("---\ndescription: Demo skill\n---\nDo demo"), 0o644); err != nil {
		t.Fatalf("write skill file: %v", err)
	}
	e.skills.SetDirs([]string{temp})

	e.cmdSkills(p, &Message{SessionKey: "test:user1", ReplyCtx: "ctx"})

	if len(p.sent) != 1 {
		t.Fatalf("sent messages = %d, want 1", len(p.sent))
	}
	if !strings.Contains(p.sent[0], "/demo") {
		t.Fatalf("skills text = %q, want legacy skills list", p.sent[0])
	}
	if strings.Contains(p.sent[0], "[← Back]") {
		t.Fatalf("skills text = %q, should not be card fallback text", p.sent[0])
	}
}

func TestRenderListCard_MakesEveryVisibleSessionClickable(t *testing.T) {
	sessions := make([]AgentSessionInfo, 0, 7)
	base := time.Date(2026, 3, 9, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 7; i++ {
		sessions = append(sessions, AgentSessionInfo{
			ID:           "agent-session-" + string(rune('A'+i)),
			Summary:      "Session summary",
			MessageCount: i + 1,
			ModifiedAt:   base.Add(time.Duration(i) * time.Minute),
		})
	}

	e := NewEngine("test", &stubListAgent{sessions: sessions}, []Platform{&stubPlatformEngine{n: "test"}}, "", LangEnglish)
	e.sessions.GetOrCreateActive("test:123:123").SetAgentSessionID(sessions[5].ID)

	card, err := e.renderListCard("test:123:123", 1)
	if err != nil {
		t.Fatalf("renderListCard returned error: %v", err)
	}

	if got := countCardActionValues(card, "act:/switch "); got != len(sessions) {
		t.Fatalf("switch action count = %d, want %d", got, len(sessions))
	}

	btn, ok := findCardAction(card, "act:/switch 6")
	if !ok {
		t.Fatal("expected active session switch action to exist")
	}
	if btn.Type != "primary" {
		t.Fatalf("active session button type = %q, want primary", btn.Type)
	}
}

func TestRenderHelpCard_DefaultsToSessionTab(t *testing.T) {
	e := NewEngine("test", &stubAgent{}, []Platform{&stubPlatformEngine{n: "test"}}, "", LangEnglish)

	card := e.renderHelpCard()
	text := card.RenderText()

	if got := countCardActionValues(card, "nav:/help "); got != 4 {
		t.Fatalf("help tab action count = %d, want 4", got)
	}
	btn, ok := findCardAction(card, "nav:/help session")
	if !ok {
		t.Fatal("expected session help tab to exist")
	}
	if btn.Type != "primary" {
		t.Fatalf("session help tab type = %q, want primary", btn.Type)
	}
	if btn.Text != "Session Management" {
		t.Fatalf("session help tab text = %q, want full title", btn.Text)
	}
	if !strings.Contains(text, "**/new**") {
		t.Fatalf("default help text = %q, want session commands", text)
	}
	if strings.Contains(text, "**Session Management**") {
		t.Fatalf("default help text = %q, should not repeat tab title in body", text)
	}
	if strings.Contains(text, "**/model**") {
		t.Fatalf("default help text = %q, should not include agent commands", text)
	}
}

func TestHandleCardNav_HelpSwitchesTabs(t *testing.T) {
	e := NewEngine("test", &stubAgent{}, []Platform{&stubPlatformEngine{n: "test"}}, "", LangEnglish)

	card := e.handleCardNav("nav:/help agent", "test:user1")
	if card == nil {
		t.Fatal("expected help nav card")
	}
	text := card.RenderText()

	if !strings.Contains(text, "**/model**") {
		t.Fatalf("agent help text = %q, want agent commands", text)
	}
	if strings.Contains(text, "**Agent Configuration**") {
		t.Fatalf("agent help text = %q, should not repeat tab title in body", text)
	}
	if strings.Contains(text, "**/new**") {
		t.Fatalf("agent help text = %q, should not include session commands", text)
	}
}

// --- AskUserQuestion tests ---

func testQuestions() []UserQuestion {
	return []UserQuestion{{
		Question: "Which database?",
		Header:   "Setup",
		Options: []UserQuestionOption{
			{Label: "PostgreSQL", Description: "Recommended for production"},
			{Label: "SQLite", Description: "Lightweight, file-based"},
			{Label: "MySQL", Description: "Popular open-source"},
		},
		MultiSelect: false,
	}}
}

func testMultiQuestions() []UserQuestion {
	return []UserQuestion{
		{
			Question: "Which database?",
			Header:   "Database",
			Options: []UserQuestionOption{
				{Label: "PostgreSQL"},
				{Label: "SQLite"},
			},
		},
		{
			Question: "Which framework?",
			Header:   "Framework",
			Options: []UserQuestionOption{
				{Label: "Gin"},
				{Label: "Echo"},
			},
		},
	}
}

func TestResolveAskQuestionAnswer_NumericIndex(t *testing.T) {
	e := newTestEngine()
	q := testQuestions()[0]
	got := e.resolveAskQuestionAnswer(q, "2")
	if got != "SQLite" {
		t.Errorf("expected SQLite, got %s", got)
	}
}

func TestResolveAskQuestionAnswer_ButtonCallback(t *testing.T) {
	e := newTestEngine()
	q := testQuestions()[0]
	got := e.resolveAskQuestionAnswer(q, "askq:0:1")
	if got != "PostgreSQL" {
		t.Errorf("expected PostgreSQL, got %s", got)
	}
}

func TestResolveAskQuestionAnswer_FreeText(t *testing.T) {
	e := newTestEngine()
	q := testQuestions()[0]
	got := e.resolveAskQuestionAnswer(q, "Redis")
	if got != "Redis" {
		t.Errorf("expected Redis, got %s", got)
	}
}

func TestResolveAskQuestionAnswer_MultiSelect(t *testing.T) {
	e := newTestEngine()
	q := testQuestions()[0]
	q.MultiSelect = true
	got := e.resolveAskQuestionAnswer(q, "1,3")
	if got != "PostgreSQL, MySQL" {
		t.Errorf("expected 'PostgreSQL, MySQL', got %s", got)
	}
}

func TestResolveAskQuestionAnswer_OutOfRange(t *testing.T) {
	e := newTestEngine()
	q := testQuestions()[0]
	got := e.resolveAskQuestionAnswer(q, "99")
	if got != "99" {
		t.Errorf("expected raw '99' for out-of-range, got %s", got)
	}
}

func TestBuildAskQuestionResponse(t *testing.T) {
	input := map[string]any{
		"questions": []any{map[string]any{"question": "Which?"}},
	}
	collected := map[int]string{0: "PostgreSQL", 1: "Gin"}
	result := buildAskQuestionResponse(input, testQuestions(), collected)
	answers, ok := result["answers"].(map[string]any)
	if !ok {
		t.Fatal("expected answers map")
	}
	if answers["0"] != "PostgreSQL" {
		t.Errorf("expected answer[0]=PostgreSQL, got %v", answers["0"])
	}
	if answers["1"] != "Gin" {
		t.Errorf("expected answer[1]=Gin, got %v", answers["1"])
	}
	if _, ok := result["questions"]; !ok {
		t.Error("expected original questions to be preserved")
	}
}

func TestSendAskQuestionPrompt_CardPlatform(t *testing.T) {
	e := newTestEngine()
	p := &stubCardPlatform{stubPlatformEngine: stubPlatformEngine{n: "feishu"}}
	e.sendAskQuestionPrompt(p, "ctx", testQuestions(), 0)

	if len(p.sentCards) != 1 {
		t.Fatalf("expected 1 card, got %d", len(p.sentCards))
	}
	card := p.sentCards[0]
	if card.Header == nil || card.Header.Color != "blue" {
		t.Errorf("expected blue header, got %+v", card.Header)
	}
	askqCount := countCardActionValues(card, "askq:")
	if askqCount != 3 {
		t.Errorf("expected 3 askq buttons, got %d", askqCount)
	}
}

func TestSendAskQuestionPrompt_CardPlatform_MultiQuestion_ShowsIndex(t *testing.T) {
	e := newTestEngine()
	p := &stubCardPlatform{stubPlatformEngine: stubPlatformEngine{n: "feishu"}}
	qs := testMultiQuestions()
	e.sendAskQuestionPrompt(p, "ctx", qs, 0)

	if len(p.sentCards) != 1 {
		t.Fatalf("expected 1 card, got %d", len(p.sentCards))
	}
	card := p.sentCards[0]
	if !strings.Contains(card.Header.Title, "(1/2)") {
		t.Errorf("expected (1/2) in title, got %s", card.Header.Title)
	}
}

func TestSendAskQuestionPrompt_InlineButtonPlatform(t *testing.T) {
	e := newTestEngine()
	p := &stubInlineButtonPlatform{stubPlatformEngine: stubPlatformEngine{n: "telegram"}}
	e.sendAskQuestionPrompt(p, "ctx", testQuestions(), 0)

	if len(p.buttonRows) != 3 {
		t.Fatalf("expected 3 button rows, got %d", len(p.buttonRows))
	}
	if p.buttonRows[0][0].Data != "askq:0:1" {
		t.Errorf("expected askq:0:1, got %s", p.buttonRows[0][0].Data)
	}
}

func TestSendAskQuestionPrompt_PlainPlatform(t *testing.T) {
	e := newTestEngine()
	p := &stubPlatformEngine{n: "plain"}
	e.sendAskQuestionPrompt(p, "ctx", testQuestions(), 0)

	if len(p.sent) != 1 {
		t.Fatal("expected 1 message")
	}
	msg := p.sent[0]
	if !strings.Contains(msg, "Which database?") {
		t.Errorf("expected question text, got %s", msg)
	}
	if !strings.Contains(msg, "1. **PostgreSQL**") {
		t.Errorf("expected numbered options, got %s", msg)
	}
}

func TestHandlePendingPermission_AskUserQuestion_SingleQuestion(t *testing.T) {
	e := newTestEngine()
	p := &stubPlatformEngine{n: "test"}
	rec := &recordingAgentSession{}

	state := &interactiveState{
		agentSession: rec,
		platform:     p,
		replyCtx:     "ctx",
		pending: &pendingPermission{
			RequestID: "req-1",
			ToolName:  "AskUserQuestion",
			ToolInput: map[string]any{
				"questions": []any{map[string]any{"question": "Which?"}},
			},
			Questions: testQuestions(),
			Resolved:  make(chan struct{}),
		},
	}
	e.interactiveMu.Lock()
	e.interactiveStates["test:chat:user1"] = state
	e.interactiveMu.Unlock()

	handled := e.handlePendingPermission(p, &Message{
		SessionKey: "test:chat:user1",
		UserID:     "user1",
		Content:    "2",
		ReplyCtx:   "ctx",
	}, "2")

	if !handled {
		t.Fatal("expected handlePendingPermission to return true")
	}
	if rec.calls != 1 {
		t.Fatalf("expected 1 RespondPermission call, got %d", rec.calls)
	}
	answers, ok := rec.lastResult.UpdatedInput["answers"].(map[string]any)
	if !ok {
		t.Fatal("expected answers in updatedInput")
	}
	if answers["0"] != "SQLite" {
		t.Errorf("expected answer=SQLite, got %v", answers["0"])
	}

	state.mu.Lock()
	if state.pending != nil {
		t.Error("expected pending to be cleared after response")
	}
	state.mu.Unlock()
}

func TestHandlePendingPermission_AskUserQuestion_MultiQuestion_Sequential(t *testing.T) {
	e := newTestEngine()
	p := &stubPlatformEngine{n: "test"}
	rec := &recordingAgentSession{}

	qs := testMultiQuestions()
	state := &interactiveState{
		agentSession: rec,
		platform:     p,
		replyCtx:     "ctx",
		pending: &pendingPermission{
			RequestID: "req-1",
			ToolName:  "AskUserQuestion",
			ToolInput: map[string]any{"questions": []any{}},
			Questions: qs,
			Resolved:  make(chan struct{}),
		},
	}
	e.interactiveMu.Lock()
	e.interactiveStates["test:chat:user1"] = state
	e.interactiveMu.Unlock()

	// Answer question 0 — should NOT resolve yet
	handled := e.handlePendingPermission(p, &Message{
		SessionKey: "test:chat:user1",
		UserID:     "user1",
		Content:    "1",
		ReplyCtx:   "ctx",
	}, "1")
	if !handled {
		t.Fatal("expected handled=true for question 0")
	}
	if rec.calls != 0 {
		t.Fatalf("should not have called RespondPermission yet, got %d calls", rec.calls)
	}
	state.mu.Lock()
	if state.pending == nil {
		t.Fatal("pending should still exist (more questions)")
	}
	if state.pending.CurrentQuestion != 1 {
		t.Errorf("expected CurrentQuestion=1, got %d", state.pending.CurrentQuestion)
	}
	state.mu.Unlock()

	// Answer question 1 — should resolve
	handled = e.handlePendingPermission(p, &Message{
		SessionKey: "test:chat:user1",
		UserID:     "user1",
		Content:    "2",
		ReplyCtx:   "ctx",
	}, "2")
	if !handled {
		t.Fatal("expected handled=true for question 1")
	}
	if rec.calls != 1 {
		t.Fatalf("expected 1 RespondPermission call, got %d", rec.calls)
	}
	answers, ok := rec.lastResult.UpdatedInput["answers"].(map[string]any)
	if !ok {
		t.Fatal("expected answers in updatedInput")
	}
	if answers["0"] != "PostgreSQL" {
		t.Errorf("expected answer[0]=PostgreSQL, got %v", answers["0"])
	}
	if answers["1"] != "Echo" {
		t.Errorf("expected answer[1]=Echo, got %v", answers["1"])
	}

	state.mu.Lock()
	if state.pending != nil {
		t.Error("expected pending to be cleared after all questions answered")
	}
	state.mu.Unlock()
}

func TestHandlePendingPermission_AskUserQuestion_SkipsPermFlow(t *testing.T) {
	e := newTestEngine()
	p := &stubPlatformEngine{n: "test"}
	rec := &recordingAgentSession{}

	state := &interactiveState{
		agentSession: rec,
		platform:     p,
		replyCtx:     "ctx",
		pending: &pendingPermission{
			RequestID: "req-1",
			ToolName:  "AskUserQuestion",
			ToolInput: map[string]any{
				"questions": []any{map[string]any{"question": "Which?"}},
			},
			Questions: testQuestions(),
			Resolved:  make(chan struct{}),
		},
	}
	e.interactiveMu.Lock()
	e.interactiveStates["test:chat:user1"] = state
	e.interactiveMu.Unlock()

	// "allow" should NOT be interpreted as permission allow; should be treated as free text answer
	handled := e.handlePendingPermission(p, &Message{
		SessionKey: "test:chat:user1",
		UserID:     "user1",
		Content:    "allow",
		ReplyCtx:   "ctx",
	}, "allow")

	if !handled {
		t.Fatal("expected handled=true")
	}
	answers, ok := rec.lastResult.UpdatedInput["answers"].(map[string]any)
	if !ok {
		t.Fatal("expected answers in updatedInput")
	}
	if answers["0"] != "allow" {
		t.Errorf("expected free text 'allow' as answer, got %v", answers["0"])
	}
}

// ──────────────────────────────────────────────────────────────
// Session routing / cleanup CAS tests
// ──────────────────────────────────────────────────────────────

// controllableAgentSession is an AgentSession stub whose session ID, liveness,
// and events channel can be controlled by the test.
type controllableAgentSession struct {
	sessionID string
	alive     bool
	events    chan Event
	closed    chan struct{} // closed when Close() is called
}

func newControllableSession(id string) *controllableAgentSession {
	return &controllableAgentSession{
		sessionID: id,
		alive:     true,
		events:    make(chan Event, 8),
		closed:    make(chan struct{}),
	}
}

func (s *controllableAgentSession) Send(_ string, _ []ImageAttachment, _ []FileAttachment) error {
	return nil
}
func (s *controllableAgentSession) RespondPermission(_ string, _ PermissionResult) error { return nil }
func (s *controllableAgentSession) Events() <-chan Event                                 { return s.events }
func (s *controllableAgentSession) CurrentSessionID() string                             { return s.sessionID }
func (s *controllableAgentSession) Alive() bool                                          { return s.alive }
func (s *controllableAgentSession) Close() error {
	s.alive = false
	close(s.events)
	select {
	case <-s.closed:
	default:
		close(s.closed)
	}
	return nil
}

// controllableAgent lets tests control which session is returned by StartSession.
type controllableAgent struct {
	nextSession AgentSession
}

func (a *controllableAgent) Name() string { return "controllable" }
func (a *controllableAgent) StartSession(_ context.Context, _ string) (AgentSession, error) {
	if a.nextSession != nil {
		return a.nextSession, nil
	}
	return newControllableSession("default"), nil
}
func (a *controllableAgent) ListSessions(_ context.Context) ([]AgentSessionInfo, error) {
	return nil, nil
}
func (a *controllableAgent) Stop() error { return nil }

// TestCleanupCAS_SkipsWhenStateReplaced verifies that cleanupInteractiveState
// with an expected state pointer is a no-op when the map entry has been replaced.
// This is the core of the /new race fix: old goroutine's cleanup must not delete
// a replacement state created by a new turn.
func TestCleanupCAS_SkipsWhenStateReplaced(t *testing.T) {
	e := newTestEngine()
	key := "test:user1"

	oldState := &interactiveState{agentSession: newControllableSession("old")}
	newState := &interactiveState{agentSession: newControllableSession("new")}

	// Place the NEW state in the map (simulating: /new already cleaned up and
	// a new turn created a replacement state).
	e.interactiveMu.Lock()
	e.interactiveStates[key] = newState
	e.interactiveMu.Unlock()

	// Old goroutine calls cleanup with the OLD state pointer — should be skipped.
	e.cleanupInteractiveState(key, oldState)

	e.interactiveMu.Lock()
	current := e.interactiveStates[key]
	e.interactiveMu.Unlock()

	if current != newState {
		t.Fatal("CAS cleanup deleted the replacement state — race not prevented")
	}
}

// TestCleanupCAS_DeletesWhenStateMatches verifies that cleanup proceeds normally
// when the expected state matches the current map entry.
func TestCleanupCAS_DeletesWhenStateMatches(t *testing.T) {
	e := newTestEngine()
	key := "test:user1"

	state := &interactiveState{agentSession: newControllableSession("s1")}

	e.interactiveMu.Lock()
	e.interactiveStates[key] = state
	e.interactiveMu.Unlock()

	e.cleanupInteractiveState(key, state)

	e.interactiveMu.Lock()
	current := e.interactiveStates[key]
	e.interactiveMu.Unlock()

	if current != nil {
		t.Fatal("expected state to be deleted when expected pointer matches")
	}
}

// TestCleanupCAS_UnconditionalWithoutExpected verifies that cleanup without an
// expected pointer always deletes (backward compat for command handlers).
func TestCleanupCAS_UnconditionalWithoutExpected(t *testing.T) {
	e := newTestEngine()
	key := "test:user1"

	state := &interactiveState{agentSession: newControllableSession("s1")}

	e.interactiveMu.Lock()
	e.interactiveStates[key] = state
	e.interactiveMu.Unlock()

	// No expected pointer — unconditional cleanup (used by /new, /switch).
	e.cleanupInteractiveState(key)

	e.interactiveMu.Lock()
	current := e.interactiveStates[key]
	e.interactiveMu.Unlock()

	if current != nil {
		t.Fatal("expected unconditional cleanup to delete state")
	}
}

// TestSessionMismatch_RecyclesStaleAgent verifies that getOrCreateInteractiveStateWith
// detects when the running agent session ID differs from the active Session's
// AgentSessionID and creates a fresh agent instead of reusing the stale one.
func TestSessionMismatch_RecyclesStaleAgent(t *testing.T) {
	newSess := newControllableSession("new-agent-id")
	agent := &controllableAgent{nextSession: newSess}
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)

	key := "test:user1"

	// Seed a live agent session with ID "old-agent-id".
	oldSess := newControllableSession("old-agent-id")
	e.interactiveMu.Lock()
	e.interactiveStates[key] = &interactiveState{
		agentSession: oldSess,
		platform:     p,
		replyCtx:     "ctx",
	}
	e.interactiveMu.Unlock()

	// The active Session now wants a DIFFERENT agent session ID.
	session := &Session{AgentSessionID: "new-agent-id"}

	state := e.getOrCreateInteractiveStateWith(key, p, "ctx", session, nil)

	if state.agentSession == oldSess {
		t.Fatal("expected stale agent session to be replaced")
	}
	if state.agentSession != newSess {
		t.Fatal("expected new agent session from StartSession")
	}

	// Old session should be closed asynchronously.
	select {
	case <-oldSess.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("old agent session was not closed after mismatch")
	}
}

// TestSessionMismatch_DoesNotLeakQuiet verifies that after a session mismatch,
// the new state gets defaultQuiet instead of inheriting quiet from the stale state.
func TestSessionMismatch_DoesNotLeakQuiet(t *testing.T) {
	agent := &controllableAgent{nextSession: newControllableSession("new-id")}
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)

	key := "test:user1"

	// Seed a stale state with quiet=true.
	e.interactiveMu.Lock()
	e.interactiveStates[key] = &interactiveState{
		agentSession: newControllableSession("old-id"),
		platform:     p,
		replyCtx:     "ctx",
		quiet:        true,
	}
	e.interactiveMu.Unlock()

	// Active session wants "new-id", which mismatches "old-id".
	session := &Session{AgentSessionID: "new-id"}

	state := e.getOrCreateInteractiveStateWith(key, p, "ctx", session, nil)

	state.mu.Lock()
	q := state.quiet
	state.mu.Unlock()
	if q {
		t.Fatal("quiet leaked from stale state into replacement — ok=false fix not working")
	}
}

// TestSessionMismatch_ReusesWhenIDsMatch verifies that getOrCreateInteractiveStateWith
// returns the existing state when agent session IDs match (no unnecessary recycling).
func TestSessionMismatch_ReusesWhenIDsMatch(t *testing.T) {
	agent := &controllableAgent{}
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)

	key := "test:user1"

	existingSess := newControllableSession("matching-id")
	existingState := &interactiveState{
		agentSession: existingSess,
		platform:     p,
		replyCtx:     "ctx",
	}
	e.interactiveMu.Lock()
	e.interactiveStates[key] = existingState
	e.interactiveMu.Unlock()

	session := &Session{AgentSessionID: "matching-id"}

	state := e.getOrCreateInteractiveStateWith(key, p, "ctx", session, nil)
	if state != existingState {
		t.Fatal("expected existing state to be reused when session IDs match")
	}
}

// TestSessionIDWriteback_ImmediateAfterStartSession verifies that after
// StartSession, the agent's CurrentSessionID is immediately written back
// to the Session's AgentSessionID when it was previously empty.
func TestSessionIDWriteback_ImmediateAfterStartSession(t *testing.T) {
	sess := newControllableSession("agent-uuid-123")
	agent := &controllableAgent{nextSession: sess}
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)

	key := "test:user1"
	session := &Session{AgentSessionID: ""} // empty — no prior binding

	e.getOrCreateInteractiveStateWith(key, p, "ctx", session, nil)

	got := session.GetAgentSessionID()

	if got != "agent-uuid-123" {
		t.Fatalf("AgentSessionID = %q, want %q — immediate writeback not working", got, "agent-uuid-123")
	}
}

// TestSessionIDWriteback_DoesNotOverwriteExisting verifies that immediate
// writeback does not clobber an existing AgentSessionID (e.g. from --resume).
func TestSessionIDWriteback_DoesNotOverwriteExisting(t *testing.T) {
	sess := newControllableSession("new-uuid")
	agent := &controllableAgent{nextSession: sess}
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)

	key := "test:user1"
	session := &Session{AgentSessionID: "existing-uuid"}

	e.getOrCreateInteractiveStateWith(key, p, "ctx", session, nil)

	got := session.GetAgentSessionID()

	if got != "existing-uuid" {
		t.Fatalf("AgentSessionID = %q, want %q — writeback should not overwrite", got, "existing-uuid")
	}
}

// TestStaleGoroutineCleanup_RaceSimulation simulates the full race scenario:
// old turn still processing → /new creates new Session → new turn starts →
// old turn exits and calls cleanup. Verifies the new state survives.
func TestStaleGoroutineCleanup_RaceSimulation(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	newSess := newControllableSession("new-agent")
	agent := &controllableAgent{nextSession: newSess}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)

	key := "test:user1"

	// Step 1: Old turn created state S1 with old agent.
	oldSess := newControllableSession("old-agent")
	oldState := &interactiveState{
		agentSession: oldSess,
		platform:     p,
		replyCtx:     "ctx",
	}
	e.interactiveMu.Lock()
	e.interactiveStates[key] = oldState
	e.interactiveMu.Unlock()

	// Step 2: /new runs — unconditional cleanup deletes S1.
	e.cleanupInteractiveState(key)

	// Step 3: New turn creates Session B and calls getOrCreateInteractiveStateWith.
	sessionB := &Session{AgentSessionID: ""}
	newState := e.getOrCreateInteractiveStateWith(key, p, "ctx", sessionB, nil)

	// Verify S2 is in the map.
	e.interactiveMu.Lock()
	current := e.interactiveStates[key]
	e.interactiveMu.Unlock()
	if current != newState {
		t.Fatal("new state not in map")
	}

	// Step 4: Old goroutine exits and calls cleanup with OLD state pointer.
	// This simulates processInteractiveEvents channelClosed path.
	e.cleanupInteractiveState(key, oldState)

	// Verify: new state must survive.
	e.interactiveMu.Lock()
	afterCleanup := e.interactiveStates[key]
	e.interactiveMu.Unlock()

	if afterCleanup != newState {
		t.Fatal("stale goroutine's cleanup deleted the replacement state — CAS not working")
	}
	if newState.agentSession.Alive() != true {
		t.Fatal("replacement agent session was killed by stale cleanup")
	}
}

func TestSplitMessageUTF8Safety(t *testing.T) {
	t.Run("ASCII short", func(t *testing.T) {
		result := splitMessage("hello", 10)
		if len(result) != 1 || result[0] != "hello" {
			t.Fatalf("expected single chunk 'hello', got %v", result)
		}
	})

	t.Run("CJK characters split at rune boundary", func(t *testing.T) {
		// 10 CJK characters (each 3 bytes in UTF-8), total 30 bytes
		input := "你好世界测试一二三四"
		if len([]rune(input)) != 10 {
			t.Fatalf("expected 10 runes, got %d", len([]rune(input)))
		}
		// maxLen=5 runes should split into 2 chunks of 5 runes each
		chunks := splitMessage(input, 5)
		if len(chunks) != 2 {
			t.Fatalf("expected 2 chunks, got %d: %v", len(chunks), chunks)
		}
		if chunks[0] != "你好世界测" {
			t.Errorf("chunk[0] = %q, want %q", chunks[0], "你好世界测")
		}
		if chunks[1] != "试一二三四" {
			t.Errorf("chunk[1] = %q, want %q", chunks[1], "试一二三四")
		}
	})

	t.Run("emoji split at rune boundary", func(t *testing.T) {
		// Emoji: 4 bytes each in UTF-8
		input := "😀😁😂🤣😄😅"
		runes := []rune(input)
		if len(runes) != 6 {
			t.Fatalf("expected 6 runes, got %d", len(runes))
		}
		chunks := splitMessage(input, 3)
		if len(chunks) != 2 {
			t.Fatalf("expected 2 chunks, got %d: %v", len(chunks), chunks)
		}
		if chunks[0] != "😀😁😂" {
			t.Errorf("chunk[0] = %q, want %q", chunks[0], "😀😁😂")
		}
		if chunks[1] != "🤣😄😅" {
			t.Errorf("chunk[1] = %q, want %q", chunks[1], "🤣😄😅")
		}
	})

	t.Run("prefers newline split", func(t *testing.T) {
		input := "abcde\nfghij"
		chunks := splitMessage(input, 8)
		if len(chunks) != 2 {
			t.Fatalf("expected 2 chunks, got %d: %v", len(chunks), chunks)
		}
		// Should split at newline (rune index 5), which is >= 8/2=4
		if chunks[0] != "abcde\n" {
			t.Errorf("chunk[0] = %q, want %q", chunks[0], "abcde\n")
		}
		if chunks[1] != "fghij" {
			t.Errorf("chunk[1] = %q, want %q", chunks[1], "fghij")
		}
	})

	t.Run("CJK with newline split", func(t *testing.T) {
		input := "你好\n世界测试一二三四"
		chunks := splitMessage(input, 5)
		if len(chunks) < 2 {
			t.Fatalf("expected at least 2 chunks, got %d: %v", len(chunks), chunks)
		}
		// First chunk should split at the newline
		if chunks[0] != "你好\n" {
			t.Errorf("chunk[0] = %q, want %q", chunks[0], "你好\n")
		}
	})
}

// ── setupMemoryFile / /cron setup / /bind setup ──────────────

type stubMemoryAgent struct {
	stubAgent
	memFile string
}

func (a *stubMemoryAgent) ProjectMemoryFile() string { return a.memFile }
func (a *stubMemoryAgent) GlobalMemoryFile() string  { return "" }

type stubNativePromptAgent struct {
	stubAgent
}

func (a *stubNativePromptAgent) HasSystemPromptSupport() bool { return true }

func TestSetupMemoryFile_WritesInstructions(t *testing.T) {
	tmpDir := t.TempDir()
	memFile := filepath.Join(tmpDir, "AGENTS.md")

	p := &stubPlatformEngine{n: "plain"}
	agent := &stubMemoryAgent{memFile: memFile}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)

	result, baseName, err := e.setupMemoryFile()
	if result != setupOK {
		t.Fatalf("result = %d, want setupOK; err = %v", result, err)
	}
	if baseName != "AGENTS.md" {
		t.Errorf("baseName = %q, want AGENTS.md", baseName)
	}

	content, _ := os.ReadFile(memFile)
	if !strings.Contains(string(content), ccConnectInstructionMarker) {
		t.Error("expected instruction marker in file")
	}
	if !strings.Contains(string(content), "cc-connect cron add") {
		t.Error("expected cron instructions in file")
	}
}

func TestSetupMemoryFile_Idempotent(t *testing.T) {
	tmpDir := t.TempDir()
	memFile := filepath.Join(tmpDir, "AGENTS.md")

	p := &stubPlatformEngine{n: "plain"}
	agent := &stubMemoryAgent{memFile: memFile}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)

	r1, _, _ := e.setupMemoryFile()
	if r1 != setupOK {
		t.Fatalf("first call: result = %d, want setupOK", r1)
	}

	r2, _, _ := e.setupMemoryFile()
	if r2 != setupExists {
		t.Fatalf("second call: result = %d, want setupExists", r2)
	}
}

func TestSetupMemoryFile_NativeAgent(t *testing.T) {
	p := &stubPlatformEngine{n: "plain"}
	agent := &stubNativePromptAgent{}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)

	result, _, _ := e.setupMemoryFile()
	if result != setupNative {
		t.Fatalf("result = %d, want setupNative", result)
	}
}

func TestSetupMemoryFile_NoMemorySupport(t *testing.T) {
	p := &stubPlatformEngine{n: "plain"}
	agent := &stubAgent{}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)

	result, _, _ := e.setupMemoryFile()
	if result != setupNoMemory {
		t.Fatalf("result = %d, want setupNoMemory", result)
	}
}

func TestCmdCronSetup_WritesAndReplies(t *testing.T) {
	tmpDir := t.TempDir()
	memFile := filepath.Join(tmpDir, "AGENTS.md")

	p := &stubPlatformEngine{n: "plain"}
	agent := &stubMemoryAgent{memFile: memFile}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)
	e.cronScheduler = &CronScheduler{}

	msg := &Message{SessionKey: "test:user1", ReplyCtx: "ctx"}
	e.cmdCron(p, msg, []string{"setup"})

	if len(p.sent) != 1 {
		t.Fatalf("sent = %d, want 1", len(p.sent))
	}
	if !strings.Contains(p.sent[0], "AGENTS.md") {
		t.Errorf("reply = %q, want to contain filename", p.sent[0])
	}
	if !strings.Contains(p.sent[0], "natural language") {
		t.Errorf("reply = %q, want cron-specific success message", p.sent[0])
	}

	content, _ := os.ReadFile(memFile)
	if !strings.Contains(string(content), ccConnectInstructionMarker) {
		t.Error("expected instructions written to file")
	}
}

func TestCmdCronSetup_NativeAgentSkips(t *testing.T) {
	p := &stubPlatformEngine{n: "plain"}
	agent := &stubNativePromptAgent{}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)
	e.cronScheduler = &CronScheduler{}

	msg := &Message{SessionKey: "test:user1", ReplyCtx: "ctx"}
	e.cmdCron(p, msg, []string{"setup"})

	if len(p.sent) != 1 {
		t.Fatalf("sent = %d, want 1", len(p.sent))
	}
	if !strings.Contains(p.sent[0], "natively supports") {
		t.Errorf("reply = %q, want native support message", p.sent[0])
	}
}

func TestCmdBindSetup_UsesSharedLogic(t *testing.T) {
	tmpDir := t.TempDir()
	memFile := filepath.Join(tmpDir, "AGENTS.md")

	p := &stubPlatformEngine{n: "plain"}
	agent := &stubMemoryAgent{memFile: memFile}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)

	msg := &Message{SessionKey: "test:user1", ReplyCtx: "ctx"}
	e.cmdBindSetup(p, msg)

	if len(p.sent) != 1 {
		t.Fatalf("sent = %d, want 1", len(p.sent))
	}
	if !strings.Contains(p.sent[0], "AGENTS.md") {
		t.Errorf("reply = %q, want to contain filename", p.sent[0])
	}

	content, _ := os.ReadFile(memFile)
	if !strings.Contains(string(content), ccConnectInstructionMarker) {
		t.Error("expected instructions written to file")
	}
}

func TestToolEmoji(t *testing.T) {
	tests := []struct {
		tool string
		want string
	}{
		{"Bash", "💻"},
		{"shell", "💻"},
		{"run_shell_command", "💻"},
		{"Read", "📖"},
		{"read_file", "📖"},
		{"Write", "✏️"},
		{"Edit", "✏️"},
		{"Grep", "🔍"},
		{"Glob", "📁"},
		{"WebSearch", "🔎"},
		{"WebFetch", "🌐"},
		{"Agent", "🤖"},
		{"AskUserQuestion", "❓"},
		{"SomeUnknownTool", "🔧"},
	}
	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			got := toolEmoji(tt.tool)
			if got != tt.want {
				t.Errorf("toolEmoji(%q) = %q, want %q", tt.tool, got, tt.want)
			}
		})
	}
}

func TestCompactToolInput(t *testing.T) {
	tests := []struct {
		name    string
		tool    string
		input   string
		maxLen  int
		workDir string
		want    string
	}{
		{"empty input", "Bash", "", 120, "", ""},
		{"simple command", "Bash", "ls -la", 120, "", "`ls -la`"},
		{"multiline takes first", "Bash", "line1\nline2\nline3", 120, "", "`line1`"},
		{"code block extraction", "Bash", "```bash\necho hello\n```", 120, "", "`echo hello`"},
		{"truncation", "Read", "a very long file path that exceeds max", 10, "", "`a very lon…`"},
		{"file path", "Read", "src/main.go", 120, "", "`src/main.go`"},
		{"search pattern", "Grep", "error handling", 120, "", "`error handling`"},
		{"strip workdir from path", "Read", "/home/user/project/src/main.go", 120, "/home/user/project", "`src/main.go`"},
		{"sibling dir uses ../", "Read", "/home/user/other/lib.go", 120, "/home/user/project", "`../other/lib.go`"},
		{"binary basename in shell", "Bash", "/usr/bin/python3 script.py", 120, "", "`python3 script.py`"},
		{"binary basename with workdir", "Bash", "/usr/local/bin/ruff check /home/user/project/src", 120, "/home/user/project", "`ruff check src`"},
		{"search JSON query extraction", "WebSearch", `{"query":"iPhone 12 купить Wildberries цена 2026"}`, 120, "", "`iPhone 12 купить Wildberries цена 2026`"},
		{"grep JSON pattern extraction", "Grep", `{"pattern":"error handling","path":"/src"}`, 120, "", "`error handling`"},
		{"search plain text unchanged", "WebSearch", "iPhone 12 купить", 120, "", "`iPhone 12 купить`"},
		{"JSON prompt extraction", "WebFetch", `{"prompt":"Найди все велосипедные фонари"}`, 120, "", "`Найди все велосипедные фонари`"},
		{"JSON command extraction", "Bash", `{"command":"ls -la"}`, 120, "", "`ls -la`"},
		{"JSON url extraction", "WebFetch", `{"url":"https://example.com"}`, 120, "", "`https://example.com`"},
		{"JSON array with objects", "TodoWrite", `{"todos":[{"content":"Read PA reports","activeForm":"Reading PA reports"},{"content":"Write summary"}]}`, 120, "", "`Reading PA reports (2 items)`"},
		{"JSON nested object", "SomeTool", `{"config":{"name":"my-project","debug":true}}`, 120, "", "`my-project`"},
		{"non-JSON unchanged", "Bash", "echo hello", 120, "", "`echo hello`"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := compactToolInput(tt.tool, tt.input, tt.maxLen, tt.workDir)
			if got != tt.want {
				t.Errorf("compactToolInput(%q, %q, %d, %q) = %q, want %q",
					tt.tool, tt.input, tt.maxLen, tt.workDir, got, tt.want)
			}
		})
	}
}

func TestShortenPaths(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		workDir string
		tool    string
		want    string
	}{
		{"strip project prefix", "/home/user/project/src/main.go", "/home/user/project", "Read", "src/main.go"},
		{"sibling dir", "/home/user/other/file.go", "/home/user/project", "Read", "../other/file.go"},
		{"unrelated path unchanged", "/opt/lib/foo.so", "/home/user/project", "Read", "/opt/lib/foo.so"},
		{"shell binary basename", "/usr/bin/python3 --version", "/home/user/project", "Bash", "python3 --version"},
		{"empty workdir no-op", "/home/user/project/src/main.go", "", "Read", "/home/user/project/src/main.go"},
		{"multiple paths", "cat /home/user/project/a.txt /home/user/project/b.txt", "/home/user/project", "Bash", "cat a.txt b.txt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shortenPaths(tt.input, tt.workDir, tt.tool)
			if got != tt.want {
				t.Errorf("shortenPaths(%q, %q, %q) = %q, want %q",
					tt.input, tt.workDir, tt.tool, got, tt.want)
			}
		})
	}
}

// --- Message queue tests ---

// stubPinnablePlatform supports both InlineButtonSender and PinnableMessage.
type stubPinnablePlatform struct {
	stubInlineButtonPlatform
	pinnedContent string
	pinnedHandle  any
	unpinned      bool
	editedContent string
}

func (p *stubPinnablePlatform) SendAndPin(_ context.Context, _ any, content string) (any, error) {
	p.pinnedContent = content
	p.pinnedHandle = "pinned-1"
	return p.pinnedHandle, nil
}

func (p *stubPinnablePlatform) EditPinned(_ context.Context, _ any, content string) error {
	p.editedContent = content
	return nil
}

func (p *stubPinnablePlatform) Unpin(_ context.Context, _ any) error {
	p.unpinned = true
	return nil
}

func TestFormatParseQueuePin(t *testing.T) {
	items := []string{"hello world", "multi\nline\nmessage", "simple"}
	text := formatQueuePin(items)

	if !isQueuePin(text) {
		t.Fatalf("expected isQueuePin to be true for %q", text)
	}
	if !strings.HasPrefix(text, QueuePinPrefix) {
		t.Fatalf("expected prefix %q, got %q", QueuePinPrefix, text[:30])
	}

	parsed := parseQueuePin(text)
	if len(parsed) != 3 {
		t.Fatalf("expected 3 items, got %d", len(parsed))
	}
	if parsed[0] != "hello world" {
		t.Fatalf("expected 'hello world', got %q", parsed[0])
	}
	if parsed[1] != "multi\nline\nmessage" {
		t.Fatalf("expected multiline restored, got %q", parsed[1])
	}
	if parsed[2] != "simple" {
		t.Fatalf("expected 'simple', got %q", parsed[2])
	}
}

func TestParseQueuePin_Empty(t *testing.T) {
	if items := parseQueuePin(""); items != nil {
		t.Fatalf("expected nil for empty, got %v", items)
	}
	if items := parseQueuePin("random text"); items != nil {
		t.Fatalf("expected nil for non-queue text, got %v", items)
	}
}

func TestFormatParseQueuePin_TrickyContent(t *testing.T) {
	// Content starting with "N. " should survive roundtrip
	items := []string{"5. этаж", "1. купи молоко\n2. купи хлеб", "normal"}
	text := formatQueuePin(items)
	parsed := parseQueuePin(text)
	if len(parsed) != 3 {
		t.Fatalf("expected 3 items, got %d: %v", len(parsed), parsed)
	}
	if parsed[0] != "5. этаж" {
		t.Fatalf("expected '5. этаж', got %q", parsed[0])
	}
	if parsed[1] != "1. купи молоко\n2. купи хлеб" {
		t.Fatalf("expected multiline with dots preserved, got %q", parsed[1])
	}
}

func TestEnqueueMessage_AddsToQueue(t *testing.T) {
	e := newTestEngine()
	p := &stubPinnablePlatform{stubInlineButtonPlatform: stubInlineButtonPlatform{stubPlatformEngine: stubPlatformEngine{n: "test"}}}
	sessionKey := "test:user1"

	// Create an interactive state (simulating a busy session)
	e.interactiveMu.Lock()
	e.interactiveStates[sessionKey] = &interactiveState{
		platform: p,
		replyCtx: "ctx",
	}
	e.interactiveMu.Unlock()

	msg := &Message{SessionKey: sessionKey, Content: "hello", ReplyCtx: "ctx", UserName: "user1"}
	e.enqueueMessage(p, msg, e.agent, e.sessions, sessionKey, "")

	e.interactiveMu.Lock()
	state := e.interactiveStates[sessionKey]
	e.interactiveMu.Unlock()

	state.mu.Lock()
	items := parseQueuePin(state.queue.cachedContent)
	state.mu.Unlock()

	if len(items) != 1 {
		t.Fatalf("expected 1 item in queue, got %d", len(items))
	}

	if p.pinnedContent == "" {
		t.Fatal("expected pinned message to be created")
	}
}

func TestEnqueueMessage_QueueFull(t *testing.T) {
	e := newTestEngine()
	p := &stubPinnablePlatform{stubInlineButtonPlatform: stubInlineButtonPlatform{stubPlatformEngine: stubPlatformEngine{n: "test"}}}
	sessionKey := "test:user1"

	// Create state with full queue (2 items already in pin)
	state := &interactiveState{
		platform: p,
		replyCtx: "ctx",
	}
	state.queue.maxSize = 2
	state.queue.cachedContent = formatQueuePin([]string{"msg1", "msg2"})

	e.interactiveMu.Lock()
	e.interactiveStates[sessionKey] = state
	e.interactiveMu.Unlock()

	msg := &Message{SessionKey: sessionKey, Content: "msg3", ReplyCtx: "ctx"}
	e.enqueueMessage(p, msg, e.agent, e.sessions, sessionKey, "")

	// Should have replied with queue full message
	found := false
	for _, s := range p.sent {
		if strings.Contains(s, "2") { // maxSize in message
			found = true
		}
	}
	if !found {
		t.Fatalf("expected queue full message, got: %v", p.sent)
	}
}

func TestHandleQueueResponse_Yes(t *testing.T) {
	e := newTestEngine()
	p := &stubPinnablePlatform{stubInlineButtonPlatform: stubInlineButtonPlatform{stubPlatformEngine: stubPlatformEngine{n: "test"}}}
	sessionKey := "test:user1"

	state := &interactiveState{
		platform: p,
		replyCtx: "ctx",
	}
	state.queue.confirmPending = true
	state.queue.cachedContent = formatQueuePin([]string{"queued message"})
	state.queue.agent = e.agent
	state.queue.sessions = e.sessions

	e.interactiveMu.Lock()
	e.interactiveStates[sessionKey] = state
	e.interactiveMu.Unlock()

	msg := &Message{SessionKey: sessionKey, Content: "queue:yes", ReplyCtx: "ctx", UserName: "user1"}
	handled := e.handleQueueResponse(p, msg, "queue:yes")

	if !handled {
		t.Fatal("expected queue response to be handled")
	}

	// Give goroutine a moment to start
	time.Sleep(50 * time.Millisecond)

	state.mu.Lock()
	remaining := parseQueuePin(state.queue.cachedContent)
	state.mu.Unlock()

	if len(remaining) != 0 {
		t.Fatalf("expected 0 items in queue after yes, got %d", len(remaining))
	}
}

func TestHandleQueueResponse_Skip(t *testing.T) {
	e := newTestEngine()
	p := &stubPinnablePlatform{stubInlineButtonPlatform: stubInlineButtonPlatform{stubPlatformEngine: stubPlatformEngine{n: "test"}}}
	sessionKey := "test:user1"

	state := &interactiveState{
		platform: p,
		replyCtx: "ctx",
	}
	state.queue.confirmPending = true
	state.queue.cachedContent = formatQueuePin([]string{"first", "second"})

	e.interactiveMu.Lock()
	e.interactiveStates[sessionKey] = state
	e.interactiveMu.Unlock()

	msg := &Message{SessionKey: sessionKey, Content: "queue:skip", ReplyCtx: "ctx"}
	handled := e.handleQueueResponse(p, msg, "queue:skip")

	if !handled {
		t.Fatal("expected queue response to be handled")
	}

	// Check that the skip message was sent
	found := false
	for _, s := range p.sent {
		if strings.Contains(s, "first") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected skip message mentioning 'first', got: %v", p.sent)
	}

	// Give goroutine time to process next
	time.Sleep(50 * time.Millisecond)

	state.mu.Lock()
	remaining := parseQueuePin(state.queue.cachedContent)
	state.mu.Unlock()

	if len(remaining) != 1 {
		t.Fatalf("expected 1 item remaining in queue after skip, got %d", len(remaining))
	}
}

func TestHandleQueueResponse_Clear(t *testing.T) {
	e := newTestEngine()
	p := &stubPinnablePlatform{stubInlineButtonPlatform: stubInlineButtonPlatform{stubPlatformEngine: stubPlatformEngine{n: "test"}}}
	sessionKey := "test:user1"

	state := &interactiveState{
		platform: p,
		replyCtx: "ctx",
	}
	state.queue.confirmPending = true
	state.queue.pinnedHandle = "pinned-1"
	state.queue.cachedContent = formatQueuePin([]string{"first", "second"})

	e.interactiveMu.Lock()
	e.interactiveStates[sessionKey] = state
	e.interactiveMu.Unlock()

	msg := &Message{SessionKey: sessionKey, Content: "queue:clear", ReplyCtx: "ctx"}
	handled := e.handleQueueResponse(p, msg, "queue:clear")

	if !handled {
		t.Fatal("expected queue response to be handled")
	}

	state.mu.Lock()
	hasCachedContent := state.queue.cachedContent != ""
	hasPinned := state.queue.pinnedHandle != nil
	state.mu.Unlock()

	if hasCachedContent {
		t.Fatal("expected cachedContent to be cleared")
	}
	if hasPinned {
		t.Fatal("expected pinned handle to be cleared")
	}
	if !p.unpinned {
		t.Fatal("expected Unpin to be called")
	}
}

func TestCleanupInteractiveState_PreservesQueuePin(t *testing.T) {
	e := newTestEngine()
	p := &stubPinnablePlatform{stubInlineButtonPlatform: stubInlineButtonPlatform{stubPlatformEngine: stubPlatformEngine{n: "test"}}}
	sessionKey := "test:user1"

	state := &interactiveState{
		platform: p,
		replyCtx: "ctx",
	}
	state.queue.pinnedHandle = "pinned-1"
	state.queue.cachedContent = formatQueuePin([]string{"queued"})

	e.interactiveMu.Lock()
	e.interactiveStates[sessionKey] = state
	e.interactiveMu.Unlock()

	e.cleanupInteractiveState(sessionKey)

	// Queue pin should NOT be unpinned — it is persistent storage
	if p.unpinned {
		t.Fatal("expected Unpin NOT to be called on cleanup (queue is persistent)")
	}
}

func TestFindQueueInteractiveKey_DirectMatch(t *testing.T) {
	e := newTestEngine()
	sessionKey := "test:user1"

	state := &interactiveState{}
	state.queue.cachedContent = formatQueuePin([]string{"queued"})

	e.interactiveMu.Lock()
	e.interactiveStates[sessionKey] = state
	e.interactiveMu.Unlock()

	found := e.findQueueInteractiveKey(sessionKey)
	if found != sessionKey {
		t.Fatalf("expected %q, got %q", sessionKey, found)
	}
}

func TestFindQueueInteractiveKey_WorkspacePrefix(t *testing.T) {
	e := newTestEngine()
	sessionKey := "test:user1"
	interactiveKey := "/tmp/ws:" + sessionKey

	state := &interactiveState{}
	state.queue.cachedContent = formatQueuePin([]string{"queued"})

	e.interactiveMu.Lock()
	e.interactiveStates[interactiveKey] = state
	e.interactiveMu.Unlock()

	found := e.findQueueInteractiveKey(sessionKey)
	if found != interactiveKey {
		t.Fatalf("expected %q, got %q", interactiveKey, found)
	}
}

func TestProcessNextInQueue_EmptyQueue(t *testing.T) {
	e := newTestEngine()
	sessionKey := "test:user1"

	state := &interactiveState{
		platform: &stubPinnablePlatform{stubInlineButtonPlatform: stubInlineButtonPlatform{stubPlatformEngine: stubPlatformEngine{n: "test"}}},
		replyCtx: "ctx",
	}

	e.interactiveMu.Lock()
	e.interactiveStates[sessionKey] = state
	e.interactiveMu.Unlock()

	// Should not panic on empty queue
	e.processNextInQueue(sessionKey)

	state.mu.Lock()
	pending := state.queue.confirmPending
	state.mu.Unlock()

	if pending {
		t.Fatal("expected confirmPending to remain false for empty queue")
	}
}

func TestProcessNextInQueue_SendsConfirmation(t *testing.T) {
	e := newTestEngine()
	p := &stubPinnablePlatform{stubInlineButtonPlatform: stubInlineButtonPlatform{stubPlatformEngine: stubPlatformEngine{n: "test"}}}
	sessionKey := "test:user1"

	state := &interactiveState{
		platform: p,
		replyCtx: "ctx",
	}
	state.queue.cachedContent = formatQueuePin([]string{"next message"})

	e.interactiveMu.Lock()
	e.interactiveStates[sessionKey] = state
	e.interactiveMu.Unlock()

	e.processNextInQueue(sessionKey)

	state.mu.Lock()
	pending := state.queue.confirmPending
	state.mu.Unlock()

	if !pending {
		t.Fatal("expected confirmPending to be true")
	}

	if p.buttonContent == "" {
		t.Fatal("expected confirmation buttons to be sent")
	}

	if !strings.Contains(p.buttonContent, "next message") {
		t.Fatalf("expected button content to contain queued message preview, got: %q", p.buttonContent)
	}

	if len(p.buttonRows) != 1 || len(p.buttonRows[0]) != 3 {
		t.Fatalf("expected 1 row of 3 buttons, got %d rows", len(p.buttonRows))
	}
}

// stubQueuePinReaderPlatform extends stubPinnablePlatform with QueuePinReader.
type stubQueuePinReaderPlatform struct {
	stubPinnablePlatform
	findContent string
	findHandle  any
	findErr     error
}

func (p *stubQueuePinReaderPlatform) FindQueuePin(_ context.Context, _ any) (string, any, error) {
	return p.findContent, p.findHandle, p.findErr
}

// TestHandleQueueResponseStateless_Clear tests that queue:clear works without in-memory state.
func TestHandleQueueResponseStateless_Clear(t *testing.T) {
	e := newTestEngine()
	content := formatQueuePin([]string{"msg1", "msg2"})
	p := &stubQueuePinReaderPlatform{
		stubPinnablePlatform: stubPinnablePlatform{stubInlineButtonPlatform: stubInlineButtonPlatform{stubPlatformEngine: stubPlatformEngine{n: "test"}}},
		findContent:          content,
		findHandle:           "pinned-1",
	}

	// NO interactiveState — simulates crashed/cleaned-up state
	msg := &Message{SessionKey: "test:user1", Content: "queue:clear", ReplyCtx: "ctx", UserName: "user1"}
	handled := e.handleQueueResponse(p, msg, "queue:clear")

	if !handled {
		t.Fatal("expected queue:clear to be handled in stateless mode")
	}
	if !p.unpinned {
		t.Fatal("expected Unpin to be called")
	}
}

// TestHandleQueueResponseStateless_Skip tests that queue:skip works without in-memory state.
func TestHandleQueueResponseStateless_Skip(t *testing.T) {
	e := newTestEngine()
	content := formatQueuePin([]string{"first", "second"})
	p := &stubQueuePinReaderPlatform{
		stubPinnablePlatform: stubPinnablePlatform{stubInlineButtonPlatform: stubInlineButtonPlatform{stubPlatformEngine: stubPlatformEngine{n: "test"}}},
		findContent:          content,
		findHandle:           "pinned-1",
	}

	msg := &Message{SessionKey: "test:user1", Content: "queue:skip", ReplyCtx: "ctx", UserName: "user1"}
	handled := e.handleQueueResponse(p, msg, "queue:skip")

	if !handled {
		t.Fatal("expected queue:skip to be handled in stateless mode")
	}

	// Give goroutine time to create state and show buttons
	time.Sleep(50 * time.Millisecond)

	// State should now exist with remaining items
	e.interactiveMu.Lock()
	state, ok := e.interactiveStates["test:user1"]
	e.interactiveMu.Unlock()

	if !ok || state == nil {
		t.Fatal("expected interactiveState to be created by stateless skip")
	}

	state.mu.Lock()
	remaining := parseQueuePin(state.queue.cachedContent)
	state.mu.Unlock()

	if len(remaining) != 1 || remaining[0] != "second" {
		t.Fatalf("expected 1 remaining item 'second', got %v", remaining)
	}
}

// TestHandleMessage_QueueCallbackNeverEnqueued ensures queue:* callbacks don't become queue items.
func TestHandleMessage_QueueCallbackNeverEnqueued(t *testing.T) {
	e := newTestEngine()
	p := &stubQueuePinReaderPlatform{
		stubPinnablePlatform: stubPinnablePlatform{stubInlineButtonPlatform: stubInlineButtonPlatform{stubPlatformEngine: stubPlatformEngine{n: "test"}}},
	}

	// No state, no pin — queue:clear should still be consumed (not enqueued)
	msg := &Message{
		SessionKey: "test:user1",
		Content:    "queue:clear",
		Platform:   "test",
		ReplyCtx:   "ctx",
		UserName:   "user1",
	}
	e.handleMessage(p, msg)

	// The message should NOT have been enqueued
	e.interactiveMu.Lock()
	state := e.interactiveStates["test:user1"]
	e.interactiveMu.Unlock()

	if state != nil {
		state.mu.Lock()
		items := parseQueuePin(state.queue.cachedContent)
		state.mu.Unlock()
		if len(items) > 0 {
			t.Fatalf("queue:clear was enqueued as text: %v", items)
		}
	}
}

// TestWriteQueuePin_EmptyDiscoversPinHandle tests that writeQueuePin discovers and
// deletes the pinned message even when pinnedHandle is nil.
func TestWriteQueuePin_EmptyDiscoversPinHandle(t *testing.T) {
	e := newTestEngine()
	p := &stubQueuePinReaderPlatform{
		stubPinnablePlatform: stubPinnablePlatform{stubInlineButtonPlatform: stubInlineButtonPlatform{stubPlatformEngine: stubPlatformEngine{n: "test"}}},
		findContent:          formatQueuePin([]string{"old"}),
		findHandle:           "discovered-pin",
	}

	state := &interactiveState{
		platform: p,
		replyCtx: "ctx",
	}
	// pinnedHandle is nil — simulates lost handle
	state.queue.cachedContent = formatQueuePin([]string{"old"})

	e.writeQueuePin(state, p, "ctx", []string{}) // empty items

	if !p.unpinned {
		t.Fatal("expected Unpin to be called via pin discovery")
	}
}

// ── command scoping tests ──────────────────────────────────────

func TestIsPrivateChat(t *testing.T) {
	tests := []struct {
		key  string
		want bool
	}{
		{"telegram:123:123", true},          // private DM
		{"telegram:456:456", true},          // another private DM
		{"telegram:-100123:456", false},     // group chat
		{"telegram:-100123:topic:42:456", false}, // forum topic
		{"telegram:-100123:topic:42", false},     // shared forum topic
		{"telegram:-100123", false},              // shared group
		{"feishu:abc:def", false},                // non-numeric channel
		{"slack:C123:U1", false},                 // Slack channel (non-numeric)
		{"test:123:123", true},                   // test private
		{"test:user1", false},                    // non-numeric → group
		{"", false},                              // empty
		{"x", false},                             // too short
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			if got := isPrivateChat(tt.key); got != tt.want {
				t.Fatalf("isPrivateChat(%q) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}

func TestFilterSessionsByChat(t *testing.T) {
	e := newTestEngine()

	// Create two sessions in "group" chat, each with an agent session ID
	groupKey := "telegram:-100123:456"
	s1 := e.sessions.NewSession(groupKey, "s1")
	s1.SetAgentSessionID("agent-1")
	s2 := e.sessions.NewSession(groupKey, "s2")
	s2.SetAgentSessionID("agent-2")

	allSessions := []AgentSessionInfo{
		{ID: "agent-1", Summary: "One"},
		{ID: "agent-2", Summary: "Two"},
		{ID: "agent-3", Summary: "Three (from another chat)"},
	}

	filtered := e.filterSessionsByChat(allSessions, e.sessions, groupKey)
	if len(filtered) != 2 {
		t.Fatalf("filtered len = %d, want 2", len(filtered))
	}
	if filtered[0].ID != "agent-1" || filtered[1].ID != "agent-2" {
		t.Fatalf("filtered IDs = [%s, %s], want [agent-1, agent-2]", filtered[0].ID, filtered[1].ID)
	}
}

func TestFilterSessionsByChat_EmptyKnown_ReturnsAll(t *testing.T) {
	e := newTestEngine()

	allSessions := []AgentSessionInfo{
		{ID: "agent-1", Summary: "One"},
		{ID: "agent-2", Summary: "Two"},
	}

	// No local sessions for this key → backward compat, return all
	filtered := e.filterSessionsByChat(allSessions, e.sessions, "telegram:-100123:789")
	if len(filtered) != 2 {
		t.Fatalf("filtered len = %d, want 2 (backward compat)", len(filtered))
	}
}

func TestCmdList_GroupChatFiltersSessionsByChat(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	agent := &stubListAgent{sessions: []AgentSessionInfo{
		{ID: "agent-1", Summary: "Chat1 Session", MessageCount: 1, ModifiedAt: time.Now()},
		{ID: "agent-2", Summary: "Chat2 Session", MessageCount: 1, ModifiedAt: time.Now()},
		{ID: "agent-3", Summary: "Other Session", MessageCount: 1, ModifiedAt: time.Now()},
	}}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)

	// Simulate group chat with only agent-1 known
	groupKey := "telegram:-100123:456"
	s := e.sessions.NewSession(groupKey, "s1")
	s.SetAgentSessionID("agent-1")

	msg := &Message{SessionKey: groupKey, ReplyCtx: "ctx"}
	e.cmdList(p, msg, nil)

	if len(p.sent) == 0 {
		t.Fatal("expected /list to send a response")
	}
	if !strings.Contains(p.sent[0], "Chat1 Session") {
		t.Fatalf("expected own session in list, got: %q", p.sent[0])
	}
	if strings.Contains(p.sent[0], "Other Session") {
		t.Fatalf("expected other session to be filtered out, got: %q", p.sent[0])
	}
}

func TestCmdList_PrivateChatShowsAllSessions(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	agent := &stubListAgent{sessions: []AgentSessionInfo{
		{ID: "agent-1", Summary: "Session One", MessageCount: 1, ModifiedAt: time.Now()},
		{ID: "agent-2", Summary: "Session Two", MessageCount: 1, ModifiedAt: time.Now()},
	}}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)

	privateKey := "telegram:123:123"
	msg := &Message{SessionKey: privateKey, ReplyCtx: "ctx"}
	e.cmdList(p, msg, nil)

	if len(p.sent) == 0 {
		t.Fatal("expected /list to send a response")
	}
	if !strings.Contains(p.sent[0], "Session One") || !strings.Contains(p.sent[0], "Session Two") {
		t.Fatalf("expected all sessions in private chat, got: %q", p.sent[0])
	}
}

func TestGroupChatBlocksMutationCommands(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)

	groupKey := "telegram:-100123:456"

	blockedCmds := []string{"/model", "/mode", "/reasoning", "/provider", "/lang"}
	for _, cmd := range blockedCmds {
		p.sent = nil
		msg := &Message{SessionKey: groupKey, ReplyCtx: "ctx"}
		e.handleCommand(p, msg, cmd)

		if len(p.sent) == 0 {
			t.Fatalf("%s: expected blocked message in group chat", cmd)
		}
		if !strings.Contains(p.sent[0], "private chat") {
			t.Fatalf("%s: expected private-chat-only message, got: %q", cmd, p.sent[0])
		}
	}
}

func TestGroupChatAllowsSafeCommands(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)

	groupKey := "telegram:-100123:456"

	// These should NOT produce "private chat" error
	safeCmds := []string{"/help", "/version", "/status", "/current"}
	for _, cmd := range safeCmds {
		p.sent = nil
		msg := &Message{SessionKey: groupKey, ReplyCtx: "ctx"}
		e.handleCommand(p, msg, cmd)

		for _, s := range p.sent {
			if strings.Contains(s, "private chat") {
				t.Fatalf("%s: should be allowed in group chat, got: %q", cmd, s)
			}
		}
	}
}

func TestPrivateChatAllowsAllCommands(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)

	privateKey := "telegram:123:123"

	cmds := []string{"/model", "/mode", "/lang", "/help", "/version"}
	for _, cmd := range cmds {
		p.sent = nil
		msg := &Message{SessionKey: privateKey, ReplyCtx: "ctx"}
		e.handleCommand(p, msg, cmd)

		for _, s := range p.sent {
			if strings.Contains(s, "private chat") {
				t.Fatalf("%s: should be allowed in private chat, got: %q", cmd, s)
			}
		}
	}
}

// ── config workspace tests ─────────────────────────────────────

func TestExtractChannelKey(t *testing.T) {
	tests := []struct {
		sessionKey string
		want       string
	}{
		{"telegram:-100123:456", "-100123"},
		{"telegram:-100123:topic:42:456", "-100123:topic:42"},
		{"telegram:-100123:topic:42", "-100123:topic:42"},
		{"telegram:-100123", "-100123"},
		{"slack:C123:U1", "C123"},
		{"feishu:abc:def", "abc"},
		{"x", ""},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.sessionKey, func(t *testing.T) {
			got := extractChannelKey(tt.sessionKey)
			if got != tt.want {
				t.Fatalf("extractChannelKey(%q) = %q, want %q", tt.sessionKey, got, tt.want)
			}
		})
	}
}

func TestConfigWorkspaces_IsolatesTwoChats(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	p := &stubPlatformEngine{n: "telegram"}
	globalAgent := &stubListAgent{sessions: []AgentSessionInfo{
		{ID: "global-1", Summary: "Global Session", MessageCount: 1, ModifiedAt: time.Now()},
	}}
	e := NewEngine("test", globalAgent, []Platform{p}, "", LangEnglish)

	// Set config workspaces (two chats → different dirs)
	e.SetConfigWorkspaces([]ConfigWorkspace{
		{ChannelKey: "-200001", WorkDir: dir1, Name: "delumo"},
		{ChannelKey: "-200002", WorkDir: dir2, Name: "raissa"},
	})

	// Pre-populate workspace agents
	ws1 := e.workspacePool.GetOrCreate(dir1)
	ws1.agent = &stubListAgent{sessions: []AgentSessionInfo{
		{ID: "delumo-1", Summary: "Delumo Page", MessageCount: 2, ModifiedAt: time.Now()},
	}}
	ws1.sessions = NewSessionManager("")

	ws2 := e.workspacePool.GetOrCreate(dir2)
	ws2.agent = &stubListAgent{sessions: []AgentSessionInfo{
		{ID: "raissa-1", Summary: "Raissa Project", MessageCount: 5, ModifiedAt: time.Now()},
	}}
	ws2.sessions = NewSessionManager("")

	// /list in chat 1 → delumo only
	p.sent = nil
	e.cmdList(p, &Message{SessionKey: "telegram:-200001:456", ReplyCtx: "ctx"}, nil)
	if len(p.sent) == 0 {
		t.Fatal("delumo: no output")
	}
	if !strings.Contains(p.sent[0], "Delumo Page") {
		t.Fatalf("delumo: expected own session, got: %q", p.sent[0])
	}
	if strings.Contains(p.sent[0], "Raissa Project") || strings.Contains(p.sent[0], "Global") {
		t.Fatalf("delumo: session leak: %q", p.sent[0])
	}

	// /list in chat 2 → raissa only
	p.sent = nil
	e.cmdList(p, &Message{SessionKey: "telegram:-200002:456", ReplyCtx: "ctx"}, nil)
	if !strings.Contains(p.sent[0], "Raissa Project") {
		t.Fatalf("raissa: expected own session, got: %q", p.sent[0])
	}
	if strings.Contains(p.sent[0], "Delumo Page") {
		t.Fatalf("raissa: session leak: %q", p.sent[0])
	}

	// Private chat → global agent (no config workspace match)
	p.sent = nil
	e.cmdList(p, &Message{SessionKey: "telegram:123:123", ReplyCtx: "ctx"}, nil)
	if len(p.sent) == 0 {
		t.Fatal("private: no output")
	}
	if !strings.Contains(p.sent[0], "Global Session") {
		t.Fatalf("private: expected global session, got: %q", p.sent[0])
	}
}

func TestConfigWorkspaces_TopicAndChat(t *testing.T) {
	dirTopic := t.TempDir()
	dirChat := t.TempDir()

	p := &stubPlatformEngine{n: "telegram"}
	globalAgent := &stubAgent{}
	e := NewEngine("test", globalAgent, []Platform{p}, "", LangEnglish)

	// Topic-specific binding and chat-wide binding for same chat
	e.SetConfigWorkspaces([]ConfigWorkspace{
		{ChannelKey: "-100123:topic:42", WorkDir: dirTopic, Name: "topic42"},
		{ChannelKey: "-100123", WorkDir: dirChat, Name: "chat-wide"},
	})

	ws1 := e.workspacePool.GetOrCreate(dirTopic)
	ws1.agent = &stubListAgent{sessions: []AgentSessionInfo{
		{ID: "t1", Summary: "Topic Session", MessageCount: 1, ModifiedAt: time.Now()},
	}}
	ws1.sessions = NewSessionManager("")

	ws2 := e.workspacePool.GetOrCreate(dirChat)
	ws2.agent = &stubListAgent{sessions: []AgentSessionInfo{
		{ID: "c1", Summary: "Chat Session", MessageCount: 1, ModifiedAt: time.Now()},
	}}
	ws2.sessions = NewSessionManager("")

	// Topic 42 → gets topic-specific workspace
	agent1, _, _, _ := e.commandContext(p, &Message{SessionKey: "telegram:-100123:topic:42:456"})
	if agent1 == globalAgent {
		t.Fatal("topic 42 got global agent")
	}

	// Same chat, no topic → gets chat-wide workspace
	agent2, _, _, _ := e.commandContext(p, &Message{SessionKey: "telegram:-100123:456"})
	if agent2 == globalAgent {
		t.Fatal("chat-wide got global agent")
	}

	// Different workspaces
	if agent1 == agent2 {
		t.Fatal("topic and chat-wide should have different agents")
	}
}

func TestConfigWorkspaces_SwitchCannotReachOtherWorkspace(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	p := &stubPlatformEngine{n: "telegram"}
	globalAgent := &stubAgent{}
	e := NewEngine("test", globalAgent, []Platform{p}, "", LangEnglish)

	e.SetConfigWorkspaces([]ConfigWorkspace{
		{ChannelKey: "-200001", WorkDir: dir1, Name: "project-a"},
		{ChannelKey: "-200002", WorkDir: dir2, Name: "project-b"},
	})

	// Workspace A has sessions a1, a2
	wsA := e.workspacePool.GetOrCreate(dir1)
	wsA.agent = &stubListAgent{sessions: []AgentSessionInfo{
		{ID: "a1", Summary: "Session A1", MessageCount: 1, ModifiedAt: time.Now()},
		{ID: "a2", Summary: "Session A2", MessageCount: 1, ModifiedAt: time.Now()},
	}}
	wsA.sessions = NewSessionManager("")

	// Workspace B has sessions b1
	wsB := e.workspacePool.GetOrCreate(dir2)
	wsB.agent = &stubListAgent{sessions: []AgentSessionInfo{
		{ID: "b1", Summary: "Session B1", MessageCount: 1, ModifiedAt: time.Now()},
	}}
	wsB.sessions = NewSessionManager("")

	// In chat A: /switch 1 → switches to a1 (OK)
	p.sent = nil
	msgA := &Message{SessionKey: "telegram:-200001:456", ReplyCtx: "ctx"}
	e.cmdSwitch(p, msgA, []string{"1"})
	if len(p.sent) == 0 {
		t.Fatal("expected /switch response in chat A")
	}
	if !strings.Contains(p.sent[0], "Session A1") {
		t.Fatalf("chat A: /switch 1 should get A1, got: %q", p.sent[0])
	}

	// In chat B: only 1 session (b1), so /switch 2 should fail
	p.sent = nil
	msgB := &Message{SessionKey: "telegram:-200002:456", ReplyCtx: "ctx"}
	e.cmdSwitch(p, msgB, []string{"2"})
	if len(p.sent) == 0 {
		t.Fatal("expected /switch response in chat B")
	}
	// Should report no match — can't see chat A's sessions
	if strings.Contains(p.sent[0], "Session A") {
		t.Fatalf("chat B: /switch 2 leaked chat A sessions: %q", p.sent[0])
	}
}

func TestConfigWorkspaces_ListShowsOnlyOwnSessions(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	p := &stubPlatformEngine{n: "telegram"}
	globalAgent := &stubListAgent{sessions: []AgentSessionInfo{
		{ID: "g1", Summary: "Global One", MessageCount: 1, ModifiedAt: time.Now()},
		{ID: "g2", Summary: "Global Two", MessageCount: 1, ModifiedAt: time.Now()},
	}}
	e := NewEngine("test", globalAgent, []Platform{p}, "", LangEnglish)

	e.SetConfigWorkspaces([]ConfigWorkspace{
		{ChannelKey: "-300001", WorkDir: dir1, Name: "alpha"},
		{ChannelKey: "-300002", WorkDir: dir2, Name: "beta"},
	})

	wsAlpha := e.workspacePool.GetOrCreate(dir1)
	wsAlpha.agent = &stubListAgent{sessions: []AgentSessionInfo{
		{ID: "alpha-1", Summary: "Alpha Session", MessageCount: 3, ModifiedAt: time.Now()},
	}}
	wsAlpha.sessions = NewSessionManager("")

	wsBeta := e.workspacePool.GetOrCreate(dir2)
	wsBeta.agent = &stubListAgent{sessions: []AgentSessionInfo{
		{ID: "beta-1", Summary: "Beta Session", MessageCount: 7, ModifiedAt: time.Now()},
		{ID: "beta-2", Summary: "Beta Other", MessageCount: 2, ModifiedAt: time.Now()},
	}}
	wsBeta.sessions = NewSessionManager("")

	// /list in alpha → only alpha
	p.sent = nil
	e.cmdList(p, &Message{SessionKey: "telegram:-300001:456", ReplyCtx: "ctx"}, nil)
	if !strings.Contains(p.sent[0], "Alpha Session") {
		t.Fatalf("alpha: expected own session, got: %q", p.sent[0])
	}
	if strings.Contains(p.sent[0], "Beta") || strings.Contains(p.sent[0], "Global") {
		t.Fatalf("alpha: session leak: %q", p.sent[0])
	}

	// /list in beta → only beta (2 sessions)
	p.sent = nil
	e.cmdList(p, &Message{SessionKey: "telegram:-300002:456", ReplyCtx: "ctx"}, nil)
	if !strings.Contains(p.sent[0], "Beta Session") || !strings.Contains(p.sent[0], "Beta Other") {
		t.Fatalf("beta: expected both beta sessions, got: %q", p.sent[0])
	}
	if strings.Contains(p.sent[0], "Alpha") || strings.Contains(p.sent[0], "Global") {
		t.Fatalf("beta: session leak: %q", p.sent[0])
	}

	// /list in private chat → global sessions only
	p.sent = nil
	e.cmdList(p, &Message{SessionKey: "telegram:123:123", ReplyCtx: "ctx"}, nil)
	if !strings.Contains(p.sent[0], "Global One") || !strings.Contains(p.sent[0], "Global Two") {
		t.Fatalf("private: expected global sessions, got: %q", p.sent[0])
	}
	if strings.Contains(p.sent[0], "Alpha") || strings.Contains(p.sent[0], "Beta") {
		t.Fatalf("private: workspace session leak: %q", p.sent[0])
	}
}

func TestConfigWorkspaces_EachGetsOwnSessionManager(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	p := &stubPlatformEngine{n: "telegram"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)

	e.SetConfigWorkspaces([]ConfigWorkspace{
		{ChannelKey: "-400001", WorkDir: dir1},
		{ChannelKey: "-400002", WorkDir: dir2},
	})

	wsA := e.workspacePool.GetOrCreate(dir1)
	wsA.agent = &stubAgent{}
	wsA.sessions = NewSessionManager("")

	wsB := e.workspacePool.GetOrCreate(dir2)
	wsB.agent = &stubAgent{}
	wsB.sessions = NewSessionManager("")

	// Create session in workspace A
	msgA := &Message{SessionKey: "telegram:-400001:456", ReplyCtx: "ctx"}
	_, sessionsA, _, _ := e.commandContext(p, msgA)
	sA := sessionsA.GetOrCreateActive(msgA.SessionKey)
	sA.SetAgentSessionID("agent-ws-a")

	// Create session in workspace B
	msgB := &Message{SessionKey: "telegram:-400002:456", ReplyCtx: "ctx"}
	_, sessionsB, _, _ := e.commandContext(p, msgB)
	sB := sessionsB.GetOrCreateActive(msgB.SessionKey)
	sB.SetAgentSessionID("agent-ws-b")

	// Session managers are different
	if sessionsA == sessionsB {
		t.Fatal("workspaces should have different session managers")
	}
	if sessionsA == e.sessions || sessionsB == e.sessions {
		t.Fatal("workspace sessions should differ from global")
	}

	// Each session manager knows only its own agent session
	knownA := sessionsA.KnownAgentSessionIDs(msgA.SessionKey)
	if !knownA["agent-ws-a"] {
		t.Fatal("workspace A sessions should know agent-ws-a")
	}
	if knownA["agent-ws-b"] {
		t.Fatal("workspace A sessions should NOT know agent-ws-b")
	}

	knownB := sessionsB.KnownAgentSessionIDs(msgB.SessionKey)
	if !knownB["agent-ws-b"] {
		t.Fatal("workspace B sessions should know agent-ws-b")
	}
	if knownB["agent-ws-a"] {
		t.Fatal("workspace B sessions should NOT know agent-ws-a")
	}
}

