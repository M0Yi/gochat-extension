package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"github.com/m0yi/gochat-server/internal/store"
	"github.com/m0yi/gochat-server/internal/types"
)

func TestChatSessionAcceptsBearerToken(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	channelStore, cleanup := newTestChannelStore(t)
	defer cleanup()

	channel, err := channelStore.CreateChannel("Test Channel", "")
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}

	tokenStr := signTestChatToken(t, "test-secret", channel.ID, channel.Name)

	router := gin.New()
	router.GET("/api/chat/session", ChatSession(nil, channelStore, "test-secret"))

	req := httptest.NewRequest(http.MethodGet, "/api/chat/session", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp types.ChatSessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if !resp.Valid {
		t.Fatalf("expected valid session response")
	}
	if resp.ChannelID != channel.ID {
		t.Fatalf("expected channel id %q, got %q", channel.ID, resp.ChannelID)
	}
	if resp.ChannelName != channel.Name {
		t.Fatalf("expected channel name %q, got %q", channel.Name, resp.ChannelName)
	}
	if !resp.Enabled {
		t.Fatalf("expected enabled channel")
	}
	if resp.Online {
		t.Fatalf("expected offline session when pluginWS is nil")
	}
}

func TestChatSessionRejectsInvalidSignature(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	channelStore, cleanup := newTestChannelStore(t)
	defer cleanup()

	channel, err := channelStore.CreateChannel("Test Channel", "")
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}

	tokenStr := signTestChatToken(t, "wrong-secret", channel.ID, channel.Name)

	router := gin.New()
	router.GET("/api/chat/session", ChatSession(nil, channelStore, "test-secret"))

	req := httptest.NewRequest(http.MethodGet, "/api/chat/session", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestChatWSRegisterAllowsMultipleClientsOnSameChannel(t *testing.T) {
	t.Parallel()

	serverConn1 := &websocket.Conn{}
	serverConn2 := &websocket.Conn{}

	chatWS := NewChatWS(nil)
	chatWS.Register(serverConn1, "channel-1", "user-a", types.ClientInfo{ConnectedAt: time.Now()})
	chatWS.Register(serverConn1, "channel-1", "user-a", types.ClientInfo{
		Type:     "web",
		Version:  "1.0.0",
		Metadata: map[string]string{"browser": "test"},
	})
	chatWS.Register(serverConn2, "channel-1", "user-b", types.ClientInfo{
		Type:        "web",
		Version:     "1.0.1",
		ConnectedAt: time.Now(),
	})

	clients := chatWS.GetAllClients()
	if len(clients) != 2 {
		t.Fatalf("expected 2 chat clients, got %d", len(clients))
	}
	if !chatWS.IsOnline("channel-1") {
		t.Fatalf("expected channel to be online")
	}
}

func TestBuildChatSessionResponseNormalizesPluginStatuses(t *testing.T) {
	t.Parallel()

	pluginWS := NewPluginWS(nil, nil, nil, nil)
	pluginWS.channels["channel-1"] = &pluginConn{
		channelID: "channel-1",
		runtime: types.PluginRuntimeStatus{
			Status:     "working",
			AgentCount: 2,
			Version:    "v1.2.3",
		},
		connectedAt: time.Now(),
		lastSeen:    time.Now(),
	}

	resp := buildChatSessionResponse(pluginWS, &types.Channel{
		ID:      "channel-1",
		Name:    "Test Channel",
		Enabled: true,
	}, &ChatClaims{ChannelID: "channel-1", UserID: "tester"})

	if resp.WorkStatus != "writing" {
		t.Fatalf("expected normalized workStatus=writing, got %q", resp.WorkStatus)
	}
	if resp.OfficeStatus != "working" {
		t.Fatalf("expected normalized officeStatus=working, got %q", resp.OfficeStatus)
	}
	if !resp.Online {
		t.Fatalf("expected online session")
	}
}

func newTestChannelStore(t *testing.T) (*store.ChannelStore, func()) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "gochat.db")
	channelStore, err := store.NewChannelStore(dbPath)
	if err != nil {
		t.Fatalf("new channel store: %v", err)
	}

	return channelStore, func() {
		_ = channelStore.Close()
	}
}

func signTestChatToken(t *testing.T, jwtSecret, channelID, userID string) string {
	t.Helper()

	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, ChatClaims{
		ChannelID: channelID,
		UserID:    userID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	})

	tokenStr, err := token.SignedString([]byte(jwtSecret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return tokenStr
}
