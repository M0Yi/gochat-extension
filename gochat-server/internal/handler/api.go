package handler

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/m0yi/gochat-server/internal/client"
	"github.com/m0yi/gochat-server/internal/store"
	"github.com/m0yi/gochat-server/internal/types"
	"github.com/m0yi/gochat-server/internal/uploader"
)

type API struct {
	openclawClient *client.OpenClawClient
	store          *store.Store
	channelStore   *store.ChannelStore
	pluginWS       *PluginWS
	uploader       uploader.Uploader
	uploadDir      string
	maxUploadSize  int64
	serverBaseURL  string
}

func NewAPI(oc *client.OpenClawClient, s *store.Store, cs *store.ChannelStore, uploadDir string, maxUploadSize int64, serverBaseURL string, u uploader.Uploader, pluginWS *PluginWS) *API {
	return &API{
		openclawClient: oc,
		store:          s,
		channelStore:   cs,
		pluginWS:       pluginWS,
		uploader:       u,
		uploadDir:      uploadDir,
		maxUploadSize:  maxUploadSize,
		serverBaseURL:  serverBaseURL,
	}
}

func (a *API) SendMessage(c *gin.Context) {
	var req types.SendMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "invalid request: " + err.Error()})
		return
	}

	if req.ConversationID == "" {
		req.ConversationID = "default"
	}
	if req.SenderID == "" {
		req.SenderID = "web-user"
	}

	msg := types.InboundMessage{
		Type:             "message",
		MessageID:        generateID(),
		ConversationID:   req.ConversationID,
		ConversationName: req.ConversationName,
		ChannelID:        req.ChannelID,
		SenderID:         req.SenderID,
		SenderName:       req.SenderName,
		Text:             req.Text,
		Attachments:      req.Attachments,
		ReplyTo:          req.ReplyTo,
		Timestamp:        time.Now().UnixMilli(),
		IsGroupChat:      req.IsGroupChat,
	}

	a.store.GetOrCreateConversation(msg.ConversationID, msg.ConversationName)
	a.store.AddMessage(types.StoredMessage{
		ConversationID: msg.ConversationID,
		ChannelID:      msg.ChannelID,
		Direction:      "inbound",
		SenderID:       msg.SenderID,
		SenderName:     msg.SenderName,
		Text:           msg.Text,
		Attachments:    msg.Attachments,
		ReplyTo:        msg.ReplyTo,
		Timestamp:      timeFromMillis(msg.Timestamp),
	})

	if req.ChannelID != "" {
		ch, err := a.channelStore.GetChannel(req.ChannelID)
		if err != nil {
			log.Printf("[api] channel lookup failed for %s: %v", req.ChannelID, err)
			c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "channel not found: " + req.ChannelID})
			return
		}
		if !ch.Enabled {
			c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "channel is disabled"})
			return
		}
		if a.pluginWS != nil {
			if err := a.pluginWS.SendToChannel(req.ChannelID, msg); err != nil {
				c.JSON(http.StatusBadGateway, types.ErrorResponse{Error: "channel offline"})
				return
			}
		} else if err := a.openclawClient.SendToChannel(msg, ch.WebhookURL, ch.Secret); err != nil {
			log.Printf("[api] send to channel %s failed: %v", ch.ID, err)
			c.JSON(http.StatusBadGateway, types.ErrorResponse{Error: "send failed: " + err.Error()})
			return
		}
	} else {
		if err := a.openclawClient.SendToChannel(msg, "", ""); err != nil {
			log.Printf("[api] send failed (no channel): %v", err)
		}
	}

	c.JSON(http.StatusOK, types.SendAPIResponse{
		MessageID: msg.MessageID,
		Timestamp: msg.Timestamp,
		OK:        true,
	})
}

func (a *API) ListConversations(c *gin.Context) {
	convs := a.store.ListConversations()
	c.JSON(http.StatusOK, convs)
}

func (a *API) GetMessages(c *gin.Context) {
	convID := c.Param("conversationId")
	if convID == "" {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "conversationId required"})
		return
	}
	msgs := a.store.GetMessages(convID, 100)
	c.JSON(http.StatusOK, msgs)
}

func (a *API) PresignUpload(c *gin.Context) {
	var req struct {
		Filename    string `json:"filename" binding:"required"`
		ContentType string `json:"contentType"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "invalid request: " + err.Error()})
		return
	}

	if req.ContentType == "" {
		req.ContentType = "application/octet-stream"
	}

	ctx := context.Background()
	result, err := a.uploader.Presign(ctx, req.Filename, req.ContentType)
	if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "presign failed: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

func (a *API) ConfirmUpload(c *gin.Context) {
	var req struct {
		FileKey string `json:"fileKey" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "invalid request: " + err.Error()})
		return
	}

	ctx := context.Background()
	result, err := a.uploader.Confirm(ctx, req.FileKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "confirm failed: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, types.Attachment{
		URL:      result.URL,
		Type:     result.Type,
		Name:     result.Name,
		MimeType: result.MimeType,
		Size:     result.Size,
	})
}

func (a *API) HandleUploadPut(c *gin.Context) {
	token := c.Param("token")
	if token == "" {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "missing token"})
		return
	}

	localUp, ok := a.uploader.(*uploader.LocalUploader)
	if !ok {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "direct upload not supported for S3 mode"})
		return
	}

	if a.maxUploadSize > 0 && c.Request.ContentLength > a.maxUploadSize {
		c.JSON(http.StatusRequestEntityTooLarge, types.ErrorResponse{Error: fmt.Sprintf("upload exceeds max size of %d bytes", a.maxUploadSize)})
		return
	}

	if err := localUp.HandlePut(token, c.Request.Body, a.maxUploadSize); err != nil {
		if strings.Contains(err.Error(), "exceeds max size") {
			c.JSON(http.StatusRequestEntityTooLarge, types.ErrorResponse{Error: err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "upload failed: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (a *API) ServeFile(c *gin.Context) {
	fileName := c.Param("filename")
	if fileName == "" {
		c.JSON(http.StatusNotFound, types.ErrorResponse{Error: "file not found"})
		return
	}

	cleanName := filepath.Base(fileName)
	if cleanName != fileName {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "invalid filename"})
		return
	}

	filePath := filepath.Join(a.uploadDir, cleanName)
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, types.ErrorResponse{Error: "file not found"})
		return
	}

	c.File(filePath)
}

func classifyAttachmentType(filename, contentType string) string {
	ct := strings.ToLower(contentType)
	if ct == "" {
		ct = strings.ToLower(filepath.Ext(filename))
	}

	switch {
	case strings.HasPrefix(ct, "image/") || ct == ".jpg" || ct == ".jpeg" || ct == ".png" || ct == ".gif" || ct == ".webp":
		return "image"
	case strings.HasPrefix(ct, "audio/") || ct == ".mp3" || ct == ".wav" || ct == ".ogg" || ct == ".m4a":
		return "audio"
	case strings.HasPrefix(ct, "video/") || ct == ".mp4" || ct == ".webm" || ct == ".mov":
		return "video"
	default:
		return "file"
	}
}
