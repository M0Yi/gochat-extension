package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/m0yi/gochat-server/internal/crypto"
	"github.com/m0yi/gochat-server/internal/store"
	"github.com/m0yi/gochat-server/internal/types"
	"github.com/m0yi/gochat-server/internal/uploader"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type wsClient struct {
	info types.WSClientInfo
	conn *websocket.Conn
}

type WSHub struct {
	mu      sync.Mutex
	clients map[*websocket.Conn]*wsClient
}

func NewWSHub() *WSHub {
	return &WSHub{
		clients: make(map[*websocket.Conn]*wsClient),
	}
}

func (h *WSHub) Register(conn *websocket.Conn, clientID, clientName, remoteAddr string) {
	if clientID == "" {
		clientID = uuid.New().String()[:8]
	}
	if clientName == "" {
		clientName = "client-" + clientID
	}

	client := &wsClient{
		info: types.WSClientInfo{
			ClientID:    clientID,
			ClientName:  clientName,
			RemoteAddr:  remoteAddr,
			ConnectedAt: time.Now(),
		},
		conn: conn,
	}

	h.mu.Lock()
	h.clients[conn] = client
	h.mu.Unlock()

	log.Printf("[ws] client connected: %s (%s) from %s, total: %d", clientID, clientName, remoteAddr, len(h.clients))

	h.Broadcast(types.WSMessage{
		Type:     "client_joined",
		ClientID: clientID,
	})
	h.broadcastClientList()
}

func (h *WSHub) Unregister(conn *websocket.Conn) {
	h.mu.Lock()
	client, ok := h.clients[conn]
	if ok {
		delete(h.clients, conn)
	}
	h.mu.Unlock()

	conn.Close()

	if ok {
		log.Printf("[ws] client disconnected: %s (%s), total: %d", client.info.ClientID, client.info.ClientName, len(h.clients))
		h.Broadcast(types.WSMessage{
			Type:     "client_left",
			ClientID: client.info.ClientID,
		})
		h.broadcastClientList()
	}
}

func (h *WSHub) ListClients() []types.WSClientInfo {
	h.mu.Lock()
	defer h.mu.Unlock()

	result := make([]types.WSClientInfo, 0, len(h.clients))
	for _, c := range h.clients {
		result = append(result, c.info)
	}
	return result
}

func (h *WSHub) Broadcast(msg types.WSMessage) {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("[ws] marshal error: %v", err)
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	for conn := range h.clients {
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			log.Printf("[ws] write error: %v", err)
			conn.Close()
			delete(h.clients, conn)
		}
	}
}

func (h *WSHub) broadcastClientList() {
	clients := h.ListClients()
	h.Broadcast(types.WSMessage{
		Type:    "client_list",
		Clients: clients,
	})
}

func HandleWS(hub *WSHub) gin.HandlerFunc {
	return func(c *gin.Context) {
		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			log.Printf("[ws] upgrade error: %v", err)
			return
		}

		clientID := c.Query("clientId")
		clientName := c.Query("clientName")
		remoteAddr := c.ClientIP()

		hub.Register(conn, clientID, clientName, remoteAddr)
		defer hub.Unregister(conn)

		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				break
			}

			var msg types.WSMessage
			if err := json.Unmarshal(message, &msg); err != nil {
				continue
			}

			if msg.Type == "ping" {
				pong, _ := json.Marshal(types.WSMessage{Type: "pong"})
				if err := conn.WriteMessage(websocket.TextMessage, pong); err != nil {
					break
				}
			} else if msg.Type == "audio" {
				hub.Broadcast(msg)
			}
		}
	}
}

func HandleListClients(hub *WSHub, pt *PluginTracker) gin.HandlerFunc {
	return func(c *gin.Context) {
		devices := hub.ListClients()
		plugins := pt.ListPlugins()
		c.JSON(http.StatusOK, gin.H{
			"plugins": plugins,
			"devices": devices,
			"counts": gin.H{
				"plugins": len(plugins),
				"devices": len(devices),
			},
		})
	}
}

type pluginConn struct {
	channelID   string
	channelName string
	conn        *websocket.Conn
	connectedAt time.Time
	lastSeen    time.Time
	clientInfo  types.ClientInfo
	runtime     types.PluginRuntimeStatus
	writeMu     sync.Mutex
}

type PluginWS struct {
	mu           sync.Mutex
	channels     map[string]*pluginConn
	channelStore *store.ChannelStore
	store        *store.Store
	uploader     uploader.Uploader
	onReply      func(reply types.OutboundReply)
	onReplyEvent func(channelID string, payload []byte)
	onStatus     func(channelID string, runtime types.PluginRuntimeStatus, online bool)
}

func NewPluginWS(cs *store.ChannelStore, s *store.Store, u uploader.Uploader, onReply func(reply types.OutboundReply)) *PluginWS {
	return &PluginWS{
		channels:     make(map[string]*pluginConn),
		channelStore: cs,
		store:        s,
		uploader:     u,
		onReply:      onReply,
	}
}

func (p *PluginWS) IsOnline(channelID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.channels[channelID]
	return ok
}

func (p *PluginWS) GetChannelStatus(channelID string) *types.ClientInfo {
	p.mu.Lock()
	defer p.mu.Unlock()
	pc, ok := p.channels[channelID]
	if !ok {
		return nil
	}
	info := pc.clientInfo
	return &info
}

func (p *PluginWS) GetChannelRuntimeStatus(channelID string) *types.PluginRuntimeStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	pc, ok := p.channels[channelID]
	if !ok {
		return nil
	}
	rt := pc.runtime
	return &rt
}

func (p *PluginWS) GetChannelConnInfo(channelID string) (connectedAt time.Time, lastSeen time.Time, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pc, exists := p.channels[channelID]
	if !exists {
		return time.Time{}, time.Time{}, false
	}
	return pc.connectedAt, pc.lastSeen, true
}

func (p *PluginWS) SetReplyHandler(handler func(reply types.OutboundReply)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onReply = handler
}

func (p *PluginWS) SetReplyEventHandler(handler func(channelID string, payload []byte)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onReplyEvent = handler
}

func (p *PluginWS) SetStatusHandler(handler func(channelID string, runtime types.PluginRuntimeStatus, online bool)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onStatus = handler
}

func normalizePluginWorkStatus(raw string) string {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "", "idle", "standby", "ready":
		return "idle"
	case "working", "writing":
		return "writing"
	case "research", "researching":
		return "researching"
	case "run", "running", "executing", "receive", "receiving":
		return "executing"
	case "sync", "syncing", "reply", "replying":
		return "syncing"
	case "error", "failed", "failure":
		return "error"
	default:
		return strings.TrimSpace(strings.ToLower(raw))
	}
}

func normalizeOfficeStatus(raw string) string {
	switch normalizePluginWorkStatus(raw) {
	case "idle":
		return "idle"
	case "syncing":
		return "syncing"
	case "error":
		return "error"
	default:
		return "working"
	}
}

func NormalizeOfficeStatusForExternal(raw string) string {
	return normalizeOfficeStatus(raw)
}

func (p *PluginWS) notifyStatus(channelID string, runtime types.PluginRuntimeStatus, online bool) {
	p.mu.Lock()
	handler := p.onStatus
	p.mu.Unlock()

	if handler == nil {
		return
	}

	runtime.Status = normalizePluginWorkStatus(runtime.Status)
	handler(channelID, runtime, online)
}

func (p *PluginWS) SendToChannel(channelID string, msg types.InboundMessage) error {
	p.mu.Lock()
	pc, ok := p.channels[channelID]
	p.mu.Unlock()

	if !ok {
		return fmt.Errorf("channel offline")
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}

	pc.writeMu.Lock()
	defer pc.writeMu.Unlock()
	return pc.conn.WriteMessage(websocket.TextMessage, data)
}

func (p *PluginWS) ListOnlineChannels() []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	result := make([]string, 0, len(p.channels))
	for id := range p.channels {
		result = append(result, id)
	}
	return result
}

func (p *PluginWS) HandlePluginWS() gin.HandlerFunc {
	return func(c *gin.Context) {
		channelID := c.Query("channelId")
		ts := c.Query("ts")
		sig := c.Query("sig")

		if channelID == "" || ts == "" || sig == "" {
			c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "missing channelId, ts, or sig"})
			return
		}

		ch, err := p.channelStore.GetChannel(channelID)
		if err != nil {
			c.JSON(http.StatusNotFound, types.ErrorResponse{Error: "channel not found"})
			return
		}
		if !ch.Enabled {
			c.JSON(http.StatusForbidden, types.ErrorResponse{Error: "channel is disabled"})
			return
		}

		if err := crypto.VerifySignature(ch.Secret, sig, ts, channelID); err != nil {
			c.JSON(http.StatusUnauthorized, types.ErrorResponse{Error: "invalid signature"})
			return
		}

		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			log.Printf("[plugin-ws] upgrade error: %v", err)
			return
		}

		pc := &pluginConn{
			channelID:   channelID,
			channelName: ch.Name,
			conn:        conn,
			connectedAt: time.Now(),
			lastSeen:    time.Now(),
			runtime: types.PluginRuntimeStatus{
				Status: "idle",
			},
		}

		p.mu.Lock()
		if old, ok := p.channels[channelID]; ok {
			old.conn.Close()
		}
		p.channels[channelID] = pc
		p.mu.Unlock()

		log.Printf("[plugin-ws] channel %s (%s) connected", channelID, ch.Name)
		p.notifyStatus(channelID, pc.runtime, true)

		defer func() {
			p.mu.Lock()
			if cur, ok := p.channels[channelID]; ok && cur == pc {
				delete(p.channels, channelID)
			}
			p.mu.Unlock()
			conn.Close()
			log.Printf("[plugin-ws] channel %s disconnected", channelID)
			p.notifyStatus(channelID, types.PluginRuntimeStatus{}, false)
		}()

		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				break
			}

			var msg struct {
				Type           string `json:"type"`
				Text           string `json:"text"`
				ConversationID string `json:"conversationId"`
				ReplyTo        string `json:"replyTo"`
				MediaURL       string `json:"mediaUrl"`
				Timestamp      int64  `json:"timestamp"`
			}
			if err := json.Unmarshal(message, &msg); err != nil {
				continue
			}

			pc.lastSeen = time.Now()

			switch msg.Type {
			case "ping":
				pong, _ := json.Marshal(map[string]string{"type": "pong"})
				pc.writeMu.Lock()
				conn.WriteMessage(websocket.TextMessage, pong)
				pc.writeMu.Unlock()
			case "status":
				var statusPayload struct {
					Type       string            `json:"type"`
					ClientType string            `json:"clientType"`
					Version    string            `json:"version"`
					AgentCount int               `json:"agentCount"`
					Status     string            `json:"status"`
					Uptime     int64             `json:"uptime"`
					Metadata   map[string]string `json:"metadata,omitempty"`
				}
				if err := json.Unmarshal(message, &statusPayload); err == nil {
					clientType := statusPayload.ClientType
					if clientType == "" {
						clientType = statusPayload.Type
					}
					normalizedStatus := normalizePluginWorkStatus(statusPayload.Status)
					if pc.clientInfo.Type == "" {
						pc.clientInfo = types.ClientInfo{
							Type:        clientType,
							Version:     statusPayload.Version,
							Metadata:    statusPayload.Metadata,
							ConnectedAt: pc.connectedAt,
						}
					}
					pc.runtime = types.PluginRuntimeStatus{
						Version:    statusPayload.Version,
						AgentCount: statusPayload.AgentCount,
						Status:     normalizedStatus,
						Uptime:     statusPayload.Uptime,
					}
					log.Printf("[plugin-ws] status update from channel %s: clientType=%s version=%s agents=%d status=%s uptime=%ds",
						channelID, clientType, statusPayload.Version, statusPayload.AgentCount, normalizedStatus, statusPayload.Uptime)
					p.notifyStatus(channelID, pc.runtime, true)
				}
			case "reply":
				reply := types.OutboundReply{
					Text:           msg.Text,
					ConversationID: msg.ConversationID,
					ChannelID:      channelID,
					ReplyTo:        msg.ReplyTo,
					MediaURL:       msg.MediaURL,
					Timestamp:      msg.Timestamp,
				}
				p.store.GetOrCreateConversation(reply.ConversationID, "")

				var storeAttachments []types.Attachment
				if reply.MediaURL != "" {
					storeAttachments = []types.Attachment{{
						URL:  reply.MediaURL,
						Type: classifyAttachmentType(reply.MediaURL, ""),
					}}
				}

				p.store.AddMessage(types.StoredMessage{
					ConversationID: reply.ConversationID,
					ChannelID:      channelID,
					Direction:      "outbound",
					Text:           reply.Text,
					Attachments:    storeAttachments,
					ReplyTo:        reply.ReplyTo,
					Timestamp:      timeFromMillis(reply.Timestamp),
				})
				if p.onReply != nil {
					p.onReply(reply)
				}
				if reply.MediaURL != "" {
					log.Printf("[plugin-ws] reply from channel %s for conv %s mediaUrl=%s text=%q", channelID, reply.ConversationID, reply.MediaURL, reply.Text)
				} else {
					log.Printf("[plugin-ws] reply from channel %s for conv %s text=%q", channelID, reply.ConversationID, reply.Text)
				}
			case "reply.start", "reply.delta", "reply.end":
				p.mu.Lock()
				handler := p.onReplyEvent
				p.mu.Unlock()
				if handler != nil {
					payload := make([]byte, len(message))
					copy(payload, message)
					handler(channelID, payload)
				}
				log.Printf("[plugin-ws] %s from channel %s for conv %s", msg.Type, channelID, msg.ConversationID)
			}
		}
	}
}

type chatConn struct {
	connID     string
	conn       *websocket.Conn
	channelID  string
	userID     string
	clientInfo types.ClientInfo
	sendMu     sync.Mutex
}

type ChatClaims struct {
	ChannelID string `json:"channelId"`
	UserID    string `json:"userId"`
	jwt.RegisteredClaims
}

type ChatWS struct {
	mu      sync.Mutex
	clients map[string]map[*websocket.Conn]*chatConn
	plugin  *PluginWS
	audio   *ChatAudioRuntime
}

func NewChatWS(plugin *PluginWS) *ChatWS {
	return &ChatWS{
		clients: make(map[string]map[*websocket.Conn]*chatConn),
		plugin:  plugin,
	}
}

func (c *ChatWS) SetAudioRuntime(audio *ChatAudioRuntime) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.audio = audio
}

func (c *ChatWS) Register(conn *websocket.Conn, channelID, userID string, clientInfo types.ClientInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()

	channelClients, ok := c.clients[channelID]
	if !ok {
		channelClients = make(map[*websocket.Conn]*chatConn)
		c.clients[channelID] = channelClients
	}

	if existing, ok := channelClients[conn]; ok {
		if userID != "" {
			existing.userID = userID
		}
		if clientInfo.Type != "" {
			existing.clientInfo.Type = clientInfo.Type
		}
		if clientInfo.Version != "" {
			existing.clientInfo.Version = clientInfo.Version
		}
		if clientInfo.Metadata != nil {
			existing.clientInfo.Metadata = clientInfo.Metadata
		}
		if !clientInfo.ConnectedAt.IsZero() {
			existing.clientInfo.ConnectedAt = clientInfo.ConnectedAt
		}
		return
	}

	if clientInfo.ConnectedAt.IsZero() {
		clientInfo.ConnectedAt = time.Now()
	}

	channelClients[conn] = &chatConn{
		connID:     uuid.New().String(),
		conn:       conn,
		channelID:  channelID,
		userID:     userID,
		clientInfo: clientInfo,
	}
	log.Printf("[chat-ws] client connected: channelID=%s, channelClients=%d, total=%d", channelID, len(channelClients), c.totalClientsLocked())
}

func (c *ChatWS) GetClientInfo(channelID string) *types.ClientInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	channelClients, ok := c.clients[channelID]
	if !ok {
		return nil
	}
	for _, cc := range channelClients {
		info := cc.clientInfo
		return &info
	}
	return nil
}

func (c *ChatWS) Unregister(channelID string, conn *websocket.Conn) {
	c.mu.Lock()
	channelClients, ok := c.clients[channelID]
	if !ok {
		c.mu.Unlock()
		return
	}
	if _, exists := channelClients[conn]; !exists {
		c.mu.Unlock()
		return
	}
	delete(channelClients, conn)
	channelCount := len(channelClients)
	if channelCount == 0 {
		delete(c.clients, channelID)
	}
	total := c.totalClientsLocked()
	c.mu.Unlock()

	conn.Close()
	log.Printf("[chat-ws] client disconnected: channelID=%s, channelClients=%d, total=%d", channelID, channelCount, total)
}

func (c *ChatWS) SendToClient(channelID string, msg []byte) error {
	c.mu.Lock()
	channelClients, ok := c.clients[channelID]
	if !ok || len(channelClients) == 0 {
		c.mu.Unlock()
		return fmt.Errorf("chat client not connected for channel %s", channelID)
	}

	recipients := make([]*chatConn, 0, len(channelClients))
	for _, cc := range channelClients {
		recipients = append(recipients, cc)
	}
	c.mu.Unlock()

	delivered := 0
	var lastErr error
	for _, cc := range recipients {
		cc.sendMu.Lock()
		err := cc.conn.WriteMessage(websocket.TextMessage, msg)
		cc.sendMu.Unlock()
		if err != nil {
			lastErr = err
			log.Printf("[chat-ws] write error on channel %s conn=%s: %v", channelID, cc.connID, err)
			c.Unregister(channelID, cc.conn)
			continue
		}
		delivered++
	}

	if delivered == 0 {
		if lastErr != nil {
			return fmt.Errorf("chat clients unavailable for channel %s: %w", channelID, lastErr)
		}
		return fmt.Errorf("chat client not connected for channel %s", channelID)
	}
	return nil
}

func (c *ChatWS) SendToConnection(channelID string, conn *websocket.Conn, msg []byte) error {
	c.mu.Lock()
	channelClients, ok := c.clients[channelID]
	if !ok {
		c.mu.Unlock()
		return fmt.Errorf("chat client not connected for channel %s", channelID)
	}
	cc, ok := channelClients[conn]
	c.mu.Unlock()
	if !ok {
		return fmt.Errorf("connection not registered for channel %s", channelID)
	}

	cc.sendMu.Lock()
	defer cc.sendMu.Unlock()
	return cc.conn.WriteMessage(websocket.TextMessage, msg)
}

func (c *ChatWS) IsOnline(channelID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	channelClients, ok := c.clients[channelID]
	return ok && len(channelClients) > 0
}

func (c *ChatWS) GetAllClients() []types.ClientInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]types.ClientInfo, 0, c.totalClientsLocked())
	for _, channelClients := range c.clients {
		for _, cc := range channelClients {
			if cc.clientInfo.Type == "" {
				continue
			}
			info := cc.clientInfo
			result = append(result, info)
		}
	}
	return result
}

type chatClientSummary struct {
	ConnID     string
	ChannelID  string
	UserID     string
	RemoteAddr string
	ClientInfo types.ClientInfo
}

func (c *ChatWS) ListChatClients() []chatClientSummary {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]chatClientSummary, 0, c.totalClientsLocked())
	for channelID, channelClients := range c.clients {
		for _, cc := range channelClients {
			result = append(result, chatClientSummary{
				ConnID:     cc.connID,
				ChannelID:  channelID,
				UserID:     cc.userID,
				RemoteAddr: cc.conn.RemoteAddr().String(),
				ClientInfo: cc.clientInfo,
			})
		}
	}
	return result
}

func (c *ChatWS) totalClientsLocked() int {
	total := 0
	for _, channelClients := range c.clients {
		total += len(channelClients)
	}
	return total
}

func getChatToken(c *gin.Context) string {
	if token := strings.TrimSpace(c.Query("token")); token != "" {
		return token
	}
	auth := strings.TrimSpace(c.GetHeader("Authorization"))
	if auth == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	return auth
}

func parseChatToken(tokenStr, jwtSecret string) (*ChatClaims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &ChatClaims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return []byte(jwtSecret), nil
	})
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*ChatClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}
	if claims.ChannelID == "" {
		return nil, fmt.Errorf("token missing channelId")
	}
	return claims, nil
}

func buildChatSessionResponse(pluginWS *PluginWS, ch *types.Channel, claims *ChatClaims) types.ChatSessionResponse {
	resp := types.ChatSessionResponse{
		Valid:       true,
		ChannelID:   ch.ID,
		ChannelName: ch.Name,
		UserID:      claims.UserID,
		Enabled:     ch.Enabled,
	}

	if claims.ExpiresAt != nil {
		resp.ExpiresAt = claims.ExpiresAt.Time.Unix()
	}

	if pluginWS == nil {
		return resp
	}

	resp.Online = pluginWS.IsOnline(ch.ID)
	if rt := pluginWS.GetChannelRuntimeStatus(ch.ID); rt != nil {
		resp.Version = rt.Version
		resp.AgentCount = rt.AgentCount
		resp.WorkStatus = normalizePluginWorkStatus(rt.Status)
		resp.OfficeStatus = normalizeOfficeStatus(rt.Status)
	}
	if connectedAt, lastSeen, ok := pluginWS.GetChannelConnInfo(ch.ID); ok {
		resp.ConnectedAt = connectedAt
		resp.LastSeen = lastSeen
	}

	return resp
}

type pluginClientSummary struct {
	ChannelID  string
	ClientInfo types.ClientInfo
	RemoteAddr string
}

func (p *PluginWS) ListPluginClients() []pluginClientSummary {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := make([]pluginClientSummary, 0, len(p.channels))
	for channelID, pc := range p.channels {
		if pc.clientInfo.Type == "" {
			continue
		}
		result = append(result, pluginClientSummary{
			ChannelID:  channelID,
			ClientInfo: pc.clientInfo,
			RemoteAddr: pc.conn.RemoteAddr().String(),
		})
	}
	return result
}

func HandleChatWS(cws *ChatWS, jwtSecret string) gin.HandlerFunc {
	return func(g *gin.Context) {
		tokenStr := getChatToken(g)
		if tokenStr == "" {
			g.JSON(http.StatusUnauthorized, types.ErrorResponse{Error: "missing token"})
			return
		}

		claims, err := parseChatToken(tokenStr, jwtSecret)
		if err != nil {
			log.Printf("[chat-ws] rejected token from %s: %v", g.ClientIP(), err)
			g.JSON(http.StatusUnauthorized, types.ErrorResponse{Error: "invalid token"})
			return
		}

		channelID := claims.ChannelID
		userID := claims.UserID
		if channelID == "" {
			g.JSON(http.StatusUnauthorized, types.ErrorResponse{Error: "token missing channelId"})
			return
		}

		conn, err := upgrader.Upgrade(g.Writer, g.Request, nil)
		if err != nil {
			log.Printf("[chat-ws] upgrade error: %v", err)
			return
		}

		cws.Register(conn, channelID, userID, types.ClientInfo{ConnectedAt: time.Now()})

		var audioSession *chatAudioSession

		defer func() {
			if audioSession != nil && audioSession.stream != nil {
				audioSession.stream.Abort()
			}
			cws.Unregister(channelID, conn)
		}()

		for {
			messageType, message, err := conn.ReadMessage()
			if err != nil {
				break
			}

			if messageType == websocket.BinaryMessage {
				if audioSession == nil {
					writeConnJSON(cws, channelID, conn, map[string]string{"type": "error", "text": "audio session not started"})
					continue
				}
				_, payload, err := parseBinaryProtocol2Frame(message)
				if err != nil {
					writeConnJSON(cws, channelID, conn, map[string]string{"type": "error", "text": err.Error()})
					continue
				}
				maxBytes := defaultAudioMaxBytes
				if cws.audio != nil && cws.audio.maxBytes > 0 {
					maxBytes = cws.audio.maxBytes
				}
				if err := audioSession.AppendPacket(payload, maxBytes); err != nil {
					writeConnJSON(cws, channelID, conn, map[string]string{"type": "error", "text": err.Error()})
					if audioSession.stream != nil {
						audioSession.stream.Abort()
					}
					audioSession = nil
					continue
				}
				if audioSession.stream != nil {
					if err := audioSession.stream.AppendAudio(payload); err != nil {
						writeConnJSON(cws, channelID, conn, map[string]string{"type": "error", "text": err.Error()})
						audioSession.stream.Abort()
						audioSession = nil
					}
				}
				continue
			}

			var chatMsg struct {
				Type           string `json:"type"`
				Text           string `json:"text"`
				ConversationID string `json:"conversationId"`
				ReplyTo        string `json:"replyTo"`
				Timestamp      int64  `json:"timestamp"`
				Attachments    []struct {
					URL      string `json:"url"`
					Type     string `json:"type"`
					Name     string `json:"name,omitempty"`
					MimeType string `json:"mimeType,omitempty"`
				} `json:"attachments"`
				ClientType     string            `json:"clientType"`
				ClientVersion  string            `json:"clientVersion"`
				ClientMetadata map[string]string `json:"clientMetadata"`
				Format         string            `json:"format"`
				STTMode        string            `json:"sttMode"`
				SampleRate     int               `json:"sampleRate"`
				Channels       int               `json:"channels"`
				FrameDuration  int               `json:"frameDuration"`
			}
			if err := json.Unmarshal(message, &chatMsg); err != nil {
				continue
			}

			if chatMsg.Type == "hello" {
				cws.Register(conn, channelID, userID, types.ClientInfo{
					Type:     chatMsg.ClientType,
					Version:  chatMsg.ClientVersion,
					Metadata: chatMsg.ClientMetadata,
				})
				log.Printf("[chat-ws] client hello: channelID=%s type=%s version=%s", channelID, chatMsg.ClientType, chatMsg.ClientVersion)
				continue
			}

			if chatMsg.Type == "ping" {
				writeConnJSON(cws, channelID, conn, map[string]string{"type": "pong"})
				continue
			}

			if chatMsg.Type == "audio.start" {
				if audioSession != nil && audioSession.stream != nil {
					audioSession.stream.Abort()
				}
				if cws.audio == nil || !cws.audio.Enabled() {
					writeConnJSON(cws, channelID, conn, map[string]string{"type": "error", "text": "audio STT is not configured"})
					continue
				}
				session, err := newChatAudioSession(chatMsg.Format, chatMsg.SampleRate, chatMsg.Channels, chatMsg.FrameDuration, chatMsg.STTMode)
				if err != nil {
					writeConnJSON(cws, channelID, conn, map[string]string{"type": "error", "text": err.Error()})
					continue
				}
				if !cws.audio.SupportsMode(session.STTMode) {
					writeConnJSON(cws, channelID, conn, map[string]string{"type": "error", "text": "audio STT mode is not configured"})
					continue
				}
				if session.STTMode == audioSTTMode2Pass {
					startCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					stream, err := cws.audio.StartStream(startCtx, session, func(text, phase string) {
						writeConnJSON(cws, channelID, conn, map[string]string{
							"type":  "stt.partial",
							"text":  text,
							"phase": phase,
						})
					})
					cancel()
					if err != nil {
						writeConnJSON(cws, channelID, conn, map[string]string{"type": "error", "text": "audio stream init failed: " + err.Error()})
						continue
					}
					session.stream = stream
				}
				audioSession = session
				log.Printf("[chat-ws] audio session started: channel=%s mode=%s format=%s sampleRate=%d channels=%d frameMs=%d", channelID, session.STTMode, session.Format, session.SampleRate, session.Channels, session.FrameDuration)
				continue
			}

			if chatMsg.Type == "audio.stop" {
				if audioSession == nil {
					writeConnJSON(cws, channelID, conn, map[string]string{"type": "error", "text": "audio session not started"})
					continue
				}
				session := audioSession
				audioSession = nil
				if len(session.Packets) == 0 {
					writeConnJSON(cws, channelID, conn, map[string]string{"type": "error", "text": "audio session is empty"})
					continue
				}
				if cws.audio == nil || !cws.audio.SupportsMode(session.STTMode) {
					writeConnJSON(cws, channelID, conn, map[string]string{"type": "error", "text": "audio STT is not configured"})
					continue
				}

				ctx, cancel := context.WithTimeout(context.Background(), cws.audio.timeout)
				var transcript string
				if session.stream != nil {
					transcript, err = session.stream.Finish(ctx)
					session.stream.Abort()
				} else {
					transcript, err = cws.audio.Transcribe(ctx, session)
				}
				cancel()
				if err != nil {
					writeConnJSON(cws, channelID, conn, map[string]string{"type": "error", "text": "audio transcription failed: " + err.Error()})
					continue
				}
				transcript = strings.TrimSpace(transcript)
				if transcript == "" {
					writeConnJSON(cws, channelID, conn, map[string]string{"type": "error", "text": "audio transcription returned empty text"})
					continue
				}
				writeConnJSON(cws, channelID, conn, map[string]string{"type": "stt", "text": transcript})
				if err := forwardInboundMessage(cws, channelID, userID, transcript, "default", "", time.Now().UnixMilli(), nil); err != nil {
					log.Printf("[chat-ws] failed to forward transcribed message: %v", err)
					writeConnJSON(cws, channelID, conn, map[string]string{"type": "error", "text": "channel offline"})
				}
				continue
			}

			if chatMsg.Type != "message" {
				continue
			}

			if chatMsg.ConversationID == "" {
				chatMsg.ConversationID = "default"
			}
			if chatMsg.Timestamp == 0 {
				chatMsg.Timestamp = time.Now().UnixMilli()
			}

			var attachments []types.Attachment
			if len(chatMsg.Attachments) > 0 {
				for _, a := range chatMsg.Attachments {
					attachURL := a.URL
					if cws.plugin.uploader != nil && attachURL != "" {
						if result, err := cws.plugin.uploader.DownloadAndReupload(context.Background(), attachURL); err == nil {
							log.Printf("[chat-ws] reuploaded attachment: %s -> %s", attachURL, result.URL)
							attachURL = result.URL
						} else {
							log.Printf("[chat-ws] failed to reupload attachment %s: %v, using original URL", attachURL, err)
						}
					}
					attachments = append(attachments, types.Attachment{
						URL:      attachURL,
						Type:     a.Type,
						Name:     a.Name,
						MimeType: a.MimeType,
					})
				}
			}
			if attachments == nil {
				attachments = []types.Attachment{}
			}

			if err := forwardInboundMessage(cws, channelID, userID, chatMsg.Text, chatMsg.ConversationID, chatMsg.ReplyTo, chatMsg.Timestamp, attachments); err != nil {
				log.Printf("[chat-ws] failed to forward to plugin: %v", err)
				writeConnJSON(cws, channelID, conn, map[string]string{"type": "error", "text": "channel offline"})
			} else {
				log.Printf("[chat-ws] forwarded message to plugin: channel=%s conv=%s", channelID, chatMsg.ConversationID)
			}
		}
	}
}

func forwardInboundMessage(
	cws *ChatWS,
	channelID string,
	userID string,
	text string,
	conversationID string,
	replyTo string,
	timestamp int64,
	attachments []types.Attachment,
) error {
	if conversationID == "" {
		conversationID = "default"
	}
	if timestamp == 0 {
		timestamp = time.Now().UnixMilli()
	}
	if attachments == nil {
		attachments = []types.Attachment{}
	}
	inbound := types.InboundMessage{
		Type:           "message",
		MessageID:      uuid.New().String(),
		ConversationID: conversationID,
		ChannelID:      channelID,
		SenderID:       userID,
		SenderName:     "User",
		Text:           text,
		Attachments:    attachments,
		ReplyTo:        replyTo,
		Timestamp:      timestamp,
	}
	return cws.plugin.SendToChannel(channelID, inbound)
}

func ChatSession(pluginWS *PluginWS, channelStore *store.ChannelStore, jwtSecret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenStr := getChatToken(c)
		if tokenStr == "" {
			c.JSON(http.StatusUnauthorized, types.ErrorResponse{Error: "missing token"})
			return
		}

		claims, err := parseChatToken(tokenStr, jwtSecret)
		if err != nil {
			log.Printf("[chat-session] rejected token from %s: %v", c.ClientIP(), err)
			c.JSON(http.StatusUnauthorized, types.ErrorResponse{Error: "invalid token"})
			return
		}

		ch, err := channelStore.GetChannel(claims.ChannelID)
		if err != nil {
			c.JSON(http.StatusUnauthorized, types.ErrorResponse{Error: "channel not found"})
			return
		}
		if !ch.Enabled {
			c.JSON(http.StatusForbidden, types.ErrorResponse{Error: "channel is disabled"})
			return
		}

		c.JSON(http.StatusOK, buildChatSessionResponse(pluginWS, ch, claims))
	}
}

func ChatLogin(pluginWS *PluginWS, channelStore *store.ChannelStore, jwtSecret string, expiresIn int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req types.ChatLoginRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "invalid request"})
			return
		}

		ch, err := channelStore.GetChannel(req.ChannelID)
		if err != nil {
			c.JSON(http.StatusUnauthorized, types.ErrorResponse{Error: "invalid channelId or secret"})
			return
		}
		if ch.Secret != req.Secret {
			c.JSON(http.StatusUnauthorized, types.ErrorResponse{Error: "invalid channelId or secret"})
			return
		}
		if !ch.Enabled {
			c.JSON(http.StatusForbidden, types.ErrorResponse{Error: "channel is disabled"})
			return
		}

		online := pluginWS.IsOnline(req.ChannelID)
		if !online {
			c.JSON(http.StatusServiceUnavailable, types.ErrorResponse{Error: "plugin not connected — please ensure OpenClaw is running"})
			return
		}

		now := time.Now()
		loginResp, err := issueChatLoginResponse(ch, jwtSecret, expiresIn)
		if err != nil {
			c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "failed to generate token"})
			return
		}
		loginResp.ExpiresAt = now.Add(time.Duration(expiresIn) * time.Second).Unix()
		c.JSON(http.StatusOK, loginResp)
	}
}

func issueChatLoginResponse(ch *types.Channel, jwtSecret string, expiresIn int64) (types.ChatLoginResponse, error) {
	now := time.Now()
	expiresAt := now.Add(time.Duration(expiresIn) * time.Second)
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, ChatClaims{
		ChannelID: ch.ID,
		UserID:    ch.Name,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	})
	tokenStr, err := token.SignedString([]byte(jwtSecret))
	if err != nil {
		return types.ChatLoginResponse{}, err
	}

	return types.ChatLoginResponse{
		Token:       tokenStr,
		ChannelID:   ch.ID,
		ChannelName: ch.Name,
		ExpiresIn:   expiresIn,
		ExpiresAt:   expiresAt.Unix(),
	}, nil
}

func classifyMediaType(u string) string {
	lower := strings.ToLower(u)
	for _, ext := range []string{".jpg", ".jpeg", ".png", ".gif", ".webp", ".svg"} {
		if strings.Contains(lower, ext) {
			return "image"
		}
	}
	for _, ext := range []string{".mp3", ".wav", ".ogg", ".m4a", ".webm"} {
		if strings.Contains(lower, ext) {
			return "audio"
		}
	}
	for _, ext := range []string{".mp4", ".mov", ".avi"} {
		if strings.Contains(lower, ext) {
			return "video"
		}
	}
	return "file"
}
