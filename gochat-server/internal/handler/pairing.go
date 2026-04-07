package handler

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/m0yi/gochat-server/internal/store"
	"github.com/m0yi/gochat-server/internal/types"
)

const (
	pairCodeTTL               = 15 * time.Minute
	defaultInstallScriptURL   = "https://raw.githubusercontent.com/M0Yi/gochat-extension/main/install.sh"
	defaultInstallScriptPSURL = "https://raw.githubusercontent.com/M0Yi/gochat-extension/main/install.ps1"
	defaultRelayHTTPBaseURL   = "https://fund.moyi.vip"
	defaultRelayPluginWSPath  = "/ws/plugin"
)

type PairingHandler struct {
	channelStore  *store.ChannelStore
	pairCodeStore *store.PairCodeStore
	pluginWS      *PluginWS
	jwtSecret     string
	chatExpiresIn int64
	defaultName   string
}

func NewPairingHandler(channelStore *store.ChannelStore, pairCodeStore *store.PairCodeStore, pluginWS *PluginWS, jwtSecret string, chatExpiresIn int64) *PairingHandler {
	return &PairingHandler{
		channelStore:  channelStore,
		pairCodeStore: pairCodeStore,
		pluginWS:      pluginWS,
		jwtSecret:     jwtSecret,
		chatExpiresIn: chatExpiresIn,
		defaultName:   "My OpenClaw",
	}
}

func (h *PairingHandler) Start(c *gin.Context) {
	h.cleanupExpiredPairs()

	var req struct {
		Name string `json:"name"`
	}
	_ = c.ShouldBindJSON(&req)

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = h.defaultName
	}

	channel, err := h.channelStore.CreateChannel(name, "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: err.Error()})
		return
	}

	session, err := h.pairCodeStore.Create(channel.ID, pairCodeTTL)
	if err != nil {
		_ = h.channelStore.DeleteChannel(channel.ID)
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: err.Error()})
		return
	}

	installCommand := buildPairInstallCommand(requestBaseURL(c), session.Code)
	installCommandWindows := buildPairInstallPSCommand(requestBaseURL(c), session.Code)
	c.JSON(http.StatusOK, gin.H{
		"code":                  session.Code,
		"sessionToken":          session.SessionToken,
		"channelId":             channel.ID,
		"channelName":           channel.Name,
		"expiresIn":             int64(pairCodeTTL / time.Second),
		"expiresAt":             session.ExpiresAt.Unix(),
		"installCommand":        installCommand,
		"installCommandWindows": installCommandWindows,
	})
}

func (h *PairingHandler) Claim(c *gin.Context) {
	h.cleanupExpiredPairs()

	var req struct {
		Code string `json:"code" binding:"required"`
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "invalid request"})
		return
	}

	claimedBy := strings.TrimSpace(req.Name)
	if claimedBy == "" {
		claimedBy = "OpenClaw GoChat Plugin"
	}

	session, err := h.pairCodeStore.Claim(req.Code, claimedBy)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		} else if strings.Contains(err.Error(), "already used") {
			status = http.StatusConflict
		}
		c.JSON(status, types.ErrorResponse{Error: err.Error()})
		return
	}

	channel, err := h.channelStore.GetChannel(session.ChannelID)
	if err != nil {
		c.JSON(http.StatusNotFound, types.ErrorResponse{Error: "channel not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"channelId":        channel.ID,
		"secret":           channel.Secret,
		"name":             channel.Name,
		"relayPlatformUrl": websocketBaseURL(requestBaseURL(c)) + defaultRelayPluginWSPath,
		"claimedAt":        session.ClaimedAt.Unix(),
		"expiresAt":        session.ExpiresAt.Unix(),
	})
}

func (h *PairingHandler) Session(c *gin.Context) {
	h.cleanupExpiredPairs()

	sessionToken := strings.TrimSpace(c.Query("sessionToken"))
	if sessionToken == "" {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "missing sessionToken"})
		return
	}

	session, err := h.pairCodeStore.GetBySessionToken(sessionToken)
	if err != nil {
		c.JSON(http.StatusNotFound, types.ErrorResponse{Error: "pair session not found"})
		return
	}

	channel, err := h.channelStore.GetChannel(session.ChannelID)
	if err != nil {
		c.JSON(http.StatusNotFound, types.ErrorResponse{Error: "channel not found"})
		return
	}

	status := "pending"
	online := h.pluginWS != nil && h.pluginWS.IsOnline(channel.ID)
	if session.ClaimedAt != nil {
		status = "claimed"
	}
	if online {
		status = "connected"
	}

	resp := gin.H{
		"code":        session.Code,
		"status":      status,
		"channelId":   channel.ID,
		"channelName": channel.Name,
		"online":      online,
		"expiresAt":   session.ExpiresAt.Unix(),
		"claimedBy":   session.ClaimedBy,
	}
	if session.ClaimedAt != nil {
		resp["claimedAt"] = session.ClaimedAt.Unix()
	}

	if online {
		loginResp, err := issueChatLoginResponse(channel, h.jwtSecret, h.chatExpiresIn)
		if err != nil {
			c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to generate token"})
			return
		}
		resp["token"] = loginResp.Token
		resp["expiresIn"] = loginResp.ExpiresIn
		resp["loginExpiresAt"] = loginResp.ExpiresAt
	}

	c.JSON(http.StatusOK, resp)
}

func (h *PairingHandler) cleanupExpiredPairs() {
	if h.channelStore == nil || h.pairCodeStore == nil {
		return
	}

	now := time.Now()
	channelIDs, err := h.pairCodeStore.ExpiredUnclaimedChannelIDs(now)
	if err == nil {
		for _, channelID := range channelIDs {
			_ = h.channelStore.DeleteChannel(channelID)
		}
	}
	_ = h.pairCodeStore.CleanupExpired(now)
}

func requestBaseURL(c *gin.Context) string {
	scheme := strings.TrimSpace(c.GetHeader("X-Forwarded-Proto"))
	if scheme == "" {
		if c.Request.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}

	host := strings.TrimSpace(c.GetHeader("X-Forwarded-Host"))
	if host == "" {
		host = strings.TrimSpace(c.Request.Host)
	}

	return fmt.Sprintf("%s://%s", scheme, host)
}

func websocketBaseURL(httpBase string) string {
	if strings.HasPrefix(httpBase, "https://") {
		return "wss://" + strings.TrimPrefix(httpBase, "https://")
	}
	if strings.HasPrefix(httpBase, "http://") {
		return "ws://" + strings.TrimPrefix(httpBase, "http://")
	}
	return httpBase
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func powershellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func buildPairInstallCommand(httpBaseURL, code string) string {
	normalizedHTTP := strings.TrimRight(strings.TrimSpace(httpBaseURL), "/")
	if normalizedHTTP == "" || normalizedHTTP == defaultRelayHTTPBaseURL {
		return fmt.Sprintf("curl -sL %s | bash -s -- %s", defaultInstallScriptURL, code)
	}

	wsURL := websocketBaseURL(normalizedHTTP) + defaultRelayPluginWSPath
	return fmt.Sprintf(
		"curl -sL %s | env GOCHAT_RELAY_HTTP_URL=%s GOCHAT_RELAY_WS_URL=%s bash -s -- %s",
		defaultInstallScriptURL,
		shellQuote(normalizedHTTP),
		shellQuote(wsURL),
		shellQuote(code),
	)
}

func buildPairInstallPSCommand(httpBaseURL, code string) string {
	normalizedHTTP := strings.TrimRight(strings.TrimSpace(httpBaseURL), "/")
	if normalizedHTTP == "" || normalizedHTTP == defaultRelayHTTPBaseURL {
		return fmt.Sprintf(
			"& ([scriptblock]::Create((irm %s))) -Code %s",
			powershellSingleQuote(defaultInstallScriptPSURL),
			powershellSingleQuote(code),
		)
	}

	wsURL := websocketBaseURL(normalizedHTTP) + defaultRelayPluginWSPath
	return fmt.Sprintf(
		"$env:GOCHAT_RELAY_HTTP_URL=%s; $env:GOCHAT_RELAY_WS_URL=%s; & ([scriptblock]::Create((irm %s))) -Code %s",
		powershellSingleQuote(normalizedHTTP),
		powershellSingleQuote(wsURL),
		powershellSingleQuote(defaultInstallScriptPSURL),
		powershellSingleQuote(code),
	)
}
