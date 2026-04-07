package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/m0yi/gochat-server/internal/store"
)

func TestPairingFlowStartClaimAndConnect(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	channelStore, cleanupChannels := newTestChannelStore(t)
	defer cleanupChannels()

	pairCodeStore, cleanupPairs := newTestPairCodeStore(t)
	defer cleanupPairs()

	pluginWS := NewPluginWS(channelStore, nil, nil, nil)
	pairing := NewPairingHandler(channelStore, pairCodeStore, pluginWS, "test-secret", 3600)

	router := gin.New()
	router.POST("/api/chat/pair/start", pairing.Start)
	router.POST("/api/plugin/pair/claim", pairing.Claim)
	router.GET("/api/chat/pair/session", pairing.Session)

	startReq := httptest.NewRequest(http.MethodPost, "/api/chat/pair/start", strings.NewReader(`{"name":"Desk AI"}`))
	startReq.Header.Set("Content-Type", "application/json")
	startRec := httptest.NewRecorder()
	router.ServeHTTP(startRec, startReq)

	if startRec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", startRec.Code, startRec.Body.String())
	}

	var startResp struct {
		Code                  string `json:"code"`
		SessionToken          string `json:"sessionToken"`
		ChannelID             string `json:"channelId"`
		ChannelName           string `json:"channelName"`
		InstallCommand        string `json:"installCommand"`
		InstallCommandWindows string `json:"installCommandWindows"`
	}
	if err := json.Unmarshal(startRec.Body.Bytes(), &startResp); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	if len(startResp.Code) != 6 {
		t.Fatalf("expected 6-digit code, got %q", startResp.Code)
	}
	if startResp.SessionToken == "" {
		t.Fatalf("expected session token")
	}
	if startResp.InstallCommand == "" {
		t.Fatalf("expected unix install command")
	}
	if startResp.InstallCommandWindows == "" {
		t.Fatalf("expected windows install command")
	}
	if !strings.Contains(startResp.InstallCommand, "raw.githubusercontent.com/M0Yi/gochat-extension/main/install.sh") {
		t.Fatalf("expected unix install command to point at plugin repo root script, got %q", startResp.InstallCommand)
	}
	if !strings.Contains(startResp.InstallCommandWindows, "raw.githubusercontent.com/M0Yi/gochat-extension/main/install.ps1") {
		t.Fatalf("expected windows install command to point at plugin repo root script, got %q", startResp.InstallCommandWindows)
	}
	if strings.Contains(startResp.InstallCommand, "extensions/gochat/install.sh") {
		t.Fatalf("expected unix install command to avoid workspace-relative path, got %q", startResp.InstallCommand)
	}
	if strings.Contains(startResp.InstallCommandWindows, "extensions/gochat/install.ps1") {
		t.Fatalf("expected windows install command to avoid workspace-relative path, got %q", startResp.InstallCommandWindows)
	}
	if !strings.Contains(startResp.InstallCommandWindows, "[scriptblock]::Create") {
		t.Fatalf("expected PowerShell install command to invoke scriptblock, got %q", startResp.InstallCommandWindows)
	}
	if strings.Contains(startResp.InstallCommandWindows, "iex -Args") {
		t.Fatalf("expected PowerShell install command to avoid iex -Args, got %q", startResp.InstallCommandWindows)
	}

	claimReq := httptest.NewRequest(http.MethodPost, "/api/plugin/pair/claim", strings.NewReader(`{"code":"`+startResp.Code+`","name":"MacBook Pro"}`))
	claimReq.Header.Set("Content-Type", "application/json")
	claimRec := httptest.NewRecorder()
	router.ServeHTTP(claimRec, claimReq)

	if claimRec.Code != http.StatusOK {
		t.Fatalf("expected claim status 200, got %d: %s", claimRec.Code, claimRec.Body.String())
	}

	var claimResp struct {
		ChannelID string `json:"channelId"`
		Secret    string `json:"secret"`
	}
	if err := json.Unmarshal(claimRec.Body.Bytes(), &claimResp); err != nil {
		t.Fatalf("decode claim response: %v", err)
	}
	if claimResp.ChannelID != startResp.ChannelID {
		t.Fatalf("expected claimed channel %q, got %q", startResp.ChannelID, claimResp.ChannelID)
	}
	if claimResp.Secret == "" {
		t.Fatalf("expected secret in claim response")
	}

	sessionReq := httptest.NewRequest(http.MethodGet, "/api/chat/pair/session?sessionToken="+startResp.SessionToken, nil)
	sessionRec := httptest.NewRecorder()
	router.ServeHTTP(sessionRec, sessionReq)

	if sessionRec.Code != http.StatusOK {
		t.Fatalf("expected session status 200, got %d: %s", sessionRec.Code, sessionRec.Body.String())
	}

	var sessionResp struct {
		Status string `json:"status"`
		Token  string `json:"token"`
		Online bool   `json:"online"`
	}
	if err := json.Unmarshal(sessionRec.Body.Bytes(), &sessionResp); err != nil {
		t.Fatalf("decode session response: %v", err)
	}
	if sessionResp.Status != "claimed" {
		t.Fatalf("expected claimed status before plugin is online, got %q", sessionResp.Status)
	}
	if sessionResp.Online {
		t.Fatalf("expected offline session before plugin connection")
	}
	if sessionResp.Token != "" {
		t.Fatalf("expected no chat token before plugin connection")
	}

	pluginWS.channels[startResp.ChannelID] = &pluginConn{
		channelID:   startResp.ChannelID,
		channelName: startResp.ChannelName,
		connectedAt: time.Now(),
		lastSeen:    time.Now(),
	}

	connectedReq := httptest.NewRequest(http.MethodGet, "/api/chat/pair/session?sessionToken="+startResp.SessionToken, nil)
	connectedRec := httptest.NewRecorder()
	router.ServeHTTP(connectedRec, connectedReq)

	if connectedRec.Code != http.StatusOK {
		t.Fatalf("expected connected session status 200, got %d: %s", connectedRec.Code, connectedRec.Body.String())
	}

	var connectedResp struct {
		Status string `json:"status"`
		Token  string `json:"token"`
		Online bool   `json:"online"`
	}
	if err := json.Unmarshal(connectedRec.Body.Bytes(), &connectedResp); err != nil {
		t.Fatalf("decode connected response: %v", err)
	}
	if connectedResp.Status != "connected" {
		t.Fatalf("expected connected status, got %q", connectedResp.Status)
	}
	if !connectedResp.Online {
		t.Fatalf("expected online session after plugin connection")
	}
	if connectedResp.Token == "" {
		t.Fatalf("expected chat token once plugin is online")
	}
}

func TestBuildPairInstallPSCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		baseURL string
		code    string
		want    []string
		notWant []string
	}{
		{
			name:    "default relay",
			baseURL: defaultRelayHTTPBaseURL,
			code:    "482913",
			want: []string{
				"[scriptblock]::Create",
				"irm '" + defaultInstallScriptPSURL + "'",
				"-Code '482913'",
			},
			notWant: []string{
				"iex -Args",
				"$env:GOCHAT_RELAY_HTTP_URL",
			},
		},
		{
			name:    "custom relay with quotes",
			baseURL: "https://relay.example.com/edge'o",
			code:    "48'2913",
			want: []string{
				"$env:GOCHAT_RELAY_HTTP_URL='https://relay.example.com/edge''o'",
				"$env:GOCHAT_RELAY_WS_URL='wss://relay.example.com/edge''o/ws/plugin'",
				"irm '" + defaultInstallScriptPSURL + "'",
				"-Code '48''2913'",
			},
			notWant: []string{
				"iex -Args",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := buildPairInstallPSCommand(tt.baseURL, tt.code)
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Fatalf("expected command to contain %q, got %q", want, got)
				}
			}
			for _, notWant := range tt.notWant {
				if strings.Contains(got, notWant) {
					t.Fatalf("expected command to avoid %q, got %q", notWant, got)
				}
			}
		})
	}
}

func newTestPairCodeStore(t *testing.T) (*store.PairCodeStore, func()) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "gochat-pair.db")
	pairCodeStore, err := store.NewPairCodeStore(dbPath)
	if err != nil {
		t.Fatalf("new pair code store: %v", err)
	}

	return pairCodeStore, func() {
		_ = pairCodeStore.Close()
	}
}
