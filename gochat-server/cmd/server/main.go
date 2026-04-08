package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/m0yi/gochat-server/internal/client"
	"github.com/m0yi/gochat-server/internal/config"
	"github.com/m0yi/gochat-server/internal/handler"
	"github.com/m0yi/gochat-server/internal/store"
	"github.com/m0yi/gochat-server/internal/types"
	"github.com/m0yi/gochat-server/internal/uploader"
)

func getBaseDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	dir := filepath.Dir(exe)
	if _, err := os.Stat(filepath.Join(dir, "web", "static", "admin.html")); err == nil {
		return dir
	}
	return "."
}

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("[gochat] no .env file found, using environment variables")
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	baseDir := getBaseDir()

	s := store.New()

	taskStore, err := store.NewTaskStore(cfg.DBPath)
	if err != nil {
		log.Fatalf("task store: %v", err)
	}
	defer taskStore.Close()

	channelStore, err := store.NewChannelStore(cfg.DBPath)
	if err != nil {
		log.Fatalf("channel store: %v", err)
	}
	defer channelStore.Close()

	pairCodeStore, err := store.NewPairCodeStore(cfg.DBPath)
	if err != nil {
		log.Fatalf("pair code store: %v", err)
	}
	defer pairCodeStore.Close()

	adminStore, err := store.NewAdminStore(cfg.DBPath)
	if err != nil {
		log.Fatalf("admin store: %v", err)
	}
	defer adminStore.Close()

	if err := config.ApplyUploadSettings(cfg, adminStore.GetSettingValue); err != nil {
		log.Fatalf("apply upload settings: %v", err)
	}

	oc := client.New()
	cb := handler.New(cfg.CallbackSecret, s, func(reply types.OutboundReply) {
		log.Printf("[main] reply for conv %s (channel %s): %s", reply.ConversationID, reply.ChannelID, reply.Text)
	})

	wsHub := handler.NewWSHub()
	pluginTracker := handler.NewPluginTracker()

	publicURL := config.ResolvePublicURL(cfg)

	var up uploader.Uploader
	if cfg.S3Bucket != "" {
		uploadPublicURL := cfg.S3PublicURL
		if uploadPublicURL == "" {
			uploadPublicURL = publicURL
		}
		s3up := uploader.NewS3Uploader(cfg.S3Endpoint, cfg.S3Region, cfg.S3AccessKey, cfg.S3SecretKey, cfg.S3Bucket, uploadPublicURL, cfg.S3ForcePath)
		up = s3up
	} else {
		up = uploader.NewLocalUploader(cfg.UploadDir, publicURL)
	}

	pluginWS := handler.NewPluginWS(channelStore, s, up, nil)

	chatWS := handler.NewChatWS(pluginWS)
	audioRuntime := handler.NewChatAudioRuntime(cfg.AudioSTTURL, cfg.AudioFFmpegBin, time.Duration(cfg.AudioSTTTimeoutSec)*time.Second)
	audioRuntime.SetOnlineURL(cfg.AudioSTTOnlineURL)
	chatWS.SetAudioRuntime(audioRuntime)

	pluginWSOnReply := func(reply types.OutboundReply) {
		replyMsg, _ := json.Marshal(map[string]interface{}{
			"type":           "reply",
			"text":           reply.Text,
			"conversationId": reply.ConversationID,
			"replyTo":        reply.ReplyTo,
			"mediaUrl":       reply.MediaURL,
			"timestamp":      reply.Timestamp,
		})
		if err := chatWS.SendToClient(reply.ChannelID, replyMsg); err != nil {
			log.Printf("[main] chat forward error: %v", err)
		}
	}
	pluginWS.SetReplyHandler(pluginWSOnReply)
	pluginWS.SetReplyEventHandler(func(channelID string, payload []byte) {
		if err := chatWS.SendToClient(channelID, payload); err != nil {
			log.Printf("[main] chat reply event forward error: %v", err)
		}
	})
	pluginWS.SetStatusHandler(func(channelID string, runtime types.PluginRuntimeStatus, online bool) {
		currentModel := ""
		modelSource := ""
		command := ""
		commandArgs := ""
		if runtime.Metadata != nil {
			currentModel = runtime.Metadata["currentModel"]
			modelSource = runtime.Metadata["modelSource"]
			command = runtime.Metadata["command"]
			commandArgs = runtime.Metadata["commandArgs"]
		}
		statusMsg, _ := json.Marshal(map[string]interface{}{
			"type":         "status",
			"channelId":    channelID,
			"online":       online,
			"version":      runtime.Version,
			"agentCount":   runtime.AgentCount,
			"workStatus":   runtime.Status,
			"officeStatus": handler.NormalizeOfficeStatusForExternal(runtime.Status),
			"currentModel": currentModel,
			"modelSource":  modelSource,
			"command":      command,
			"commandArgs":  commandArgs,
			"metadata":     runtime.Metadata,
			"uptime":       runtime.Uptime,
			"timestamp":    time.Now().UnixMilli(),
		})
		if err := chatWS.SendToClient(channelID, statusMsg); err != nil {
			log.Printf("[main] chat status forward error: %v", err)
		}
	})

	api := handler.NewAPI(oc, s, channelStore, cfg.UploadDir, cfg.MaxUploadSize, publicURL, up, pluginWS)
	taskAPI := handler.NewTaskAPI(taskStore, wsHub)
	pairingHandler := handler.NewPairingHandler(channelStore, pairCodeStore, pluginWS, cfg.AdminJWTSecret, 86400)

	adminHandler := handler.NewAdminHandler(adminStore, s, taskStore, channelStore, cfg.AdminJWTSecret, handler.AdminConfig{
		Username:       cfg.AdminUsername,
		Password:       cfg.AdminPassword,
		PublicURL:      cfg.PublicURL,
		UploadDir:      cfg.UploadDir,
		MaxUploadSize:  cfg.MaxUploadSize,
		ServerPort:     cfg.ServerPort,
		DBPath:         cfg.DBPath,
		WebhookURL:     cfg.OpenClawWebhookURL,
		WebhookSecret:  cfg.WebhookSecret,
		CallbackSecret: cfg.CallbackSecret,
		AdminJWTSecret: cfg.AdminJWTSecret,
		S3Bucket:       cfg.S3Bucket,
		S3Region:       cfg.S3Region,
		S3Endpoint:     cfg.S3Endpoint,
		S3AccessKey:    cfg.S3AccessKey,
		S3SecretKey:    cfg.S3SecretKey,
		S3PublicURL:    cfg.S3PublicURL,
		S3ForcePath:    cfg.S3ForcePath,
	}, pluginWS)

	serveHTML := func(filename string) func(c *gin.Context) {
		return func(c *gin.Context) {
			path := filepath.Join(baseDir, "web", "static", filename)
			data, err := os.ReadFile(path)
			if err != nil {
				c.String(http.StatusInternalServerError, "file not found: "+path)
				return
			}
			c.Data(http.StatusOK, "text/html; charset=utf-8", data)
		}
	}

	r := gin.Default()
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		AllowCredentials: true,
	}))

	r.GET("/", serveHTML("index.html"))
	r.GET("/admin", serveHTML("admin.html"))
	r.GET("/app", serveHTML("app.html"))

	apiGroup := r.Group("/api")
	{
		apiGroup.POST("/openclaw/reply", gin.WrapF(cb.HandleCallback))
		apiGroup.POST("/send", api.SendMessage)
		apiGroup.GET("/conversations", api.ListConversations)
		apiGroup.GET("/conversations/:conversationId/messages", api.GetMessages)
		apiGroup.POST("/upload/presign", api.PresignUpload)
		apiGroup.POST("/upload/confirm", api.ConfirmUpload)

		apiGroup.GET("/conversations/:conversationId/tasks", taskAPI.ListTasks)
		apiGroup.POST("/conversations/:conversationId/tasks", taskAPI.CreateTask)
		apiGroup.POST("/conversations/:conversationId/tasks/:taskId/toggle", taskAPI.ToggleTask)
		apiGroup.DELETE("/conversations/:conversationId/tasks/:taskId", taskAPI.DeleteTask)
		apiGroup.POST("/conversations/:conversationId/tasks/clear-completed", taskAPI.ClearCompletedTasks)
		apiGroup.GET("/conversations/:conversationId/tasks/summary", taskAPI.TaskSummary)

		apiGroup.GET("/clients", handler.HandleListClients(wsHub, pluginTracker))
		apiGroup.POST("/plugin/heartbeat", handler.HandlePluginHeartbeat(pluginTracker, wsHub))
		apiGroup.POST("/plugin/register", handler.HandlePluginRegister(channelStore))
		apiGroup.POST("/plugin/pair/claim", pairingHandler.Claim)
	}

	chatGroup := r.Group("/api/chat")
	{
		chatGroup.POST("/login", handler.ChatLogin(pluginWS, channelStore, cfg.AdminJWTSecret, 86400))
		chatGroup.GET("/session", handler.ChatSession(pluginWS, channelStore, cfg.AdminJWTSecret))
		chatGroup.POST("/pair/start", pairingHandler.Start)
		chatGroup.GET("/pair/session", pairingHandler.Session)
	}

	r.GET("/ws/chat", handler.HandleChatWS(chatWS, cfg.AdminJWTSecret))

	adminGroup := r.Group("/api/admin")
	{
		adminGroup.POST("/login", adminHandler.Login)
		adminGroup.GET("/validate", adminHandler.AuthMiddleware(), adminHandler.ValidateToken)
	}

	adminAuth := r.Group("/api/admin")
	adminAuth.Use(adminHandler.AuthMiddleware())
	{
		adminAuth.GET("/stats", adminHandler.GetStatsWithClients(nil, wsHub, pluginTracker))
		adminAuth.GET("/conversations", adminHandler.ListConversations)
		adminAuth.GET("/conversations/:conversationId", adminHandler.GetConversationDetail)
		adminAuth.DELETE("/conversations/:conversationId", adminHandler.DeleteConversation)
		adminAuth.GET("/users", adminHandler.ListUsers)
		adminAuth.POST("/users", adminHandler.CreateUser)
		adminAuth.DELETE("/users/:userId", adminHandler.DeleteUser)
		adminAuth.POST("/users/:userId/ban", adminHandler.BanUser)
		adminAuth.POST("/users/:userId/reset-password", adminHandler.ResetPassword)
		adminAuth.GET("/config", adminHandler.GetSystemConfig)
		adminAuth.GET("/upload-config", adminHandler.GetUploadConfig)
		adminAuth.PUT("/upload-config", adminHandler.UpdateUploadConfig)
		adminAuth.POST("/upload-config/test", adminHandler.TestUploadConfig)
		adminAuth.GET("/settings", adminHandler.GetSettings)
		adminAuth.POST("/settings", adminHandler.UpdateSetting)
		adminAuth.GET("/audit-logs", adminHandler.ListAuditLogs)
		adminAuth.GET("/files", adminHandler.ListFiles)
		adminAuth.DELETE("/files/:filename", adminHandler.DeleteFile)
		adminAuth.POST("/broadcast", adminHandler.BroadcastMessage)
		adminAuth.GET("/export", adminHandler.ExportData)
		adminAuth.GET("/clients", adminHandler.HandleAdminClients(wsHub, pluginTracker, chatWS, pluginWS))
		adminAuth.GET("/tasks", adminHandler.GetTasksForAdmin)
		adminAuth.POST("/change-password", adminHandler.ChangePassword)
		adminAuth.GET("/test-connection", adminHandler.TestConnection)

		adminAuth.GET("/channels", adminHandler.ListChannels)
		adminAuth.POST("/channels", adminHandler.CreateChannel)
		adminAuth.GET("/channels/:channelId", adminHandler.GetChannel)
		adminAuth.PUT("/channels/:channelId", adminHandler.UpdateChannel)
		adminAuth.DELETE("/channels/:channelId", adminHandler.DeleteChannel)
		adminAuth.POST("/channels/:channelId/regenerate-secret", adminHandler.RegenerateChannelSecret)
		adminAuth.POST("/channels/:channelId/toggle", adminHandler.ToggleChannel)
	}

	r.GET("/ws/tasks", handler.HandleWS(wsHub))
	r.GET("/ws/plugin", pluginWS.HandlePluginWS())
	r.GET("/files/:filename", api.ServeFile)
	r.PUT("/api/upload/put/:token", api.HandleUploadPut)

	// Serve pixel office assets
	staticDir := filepath.Join(baseDir, "web", "static")
	r.Static("/static", staticDir)

	addr := config.ServerAddr(cfg)
	log.Printf("[gochat] server starting on %s", addr)
	log.Printf("[gochat] webhook: %s", cfg.OpenClawWebhookURL)
	log.Printf("[gochat] uploads: %s", cfg.UploadDir)
	log.Printf("[gochat] db: %s", cfg.DBPath)
	log.Printf("[gochat] channels: %d", channelStore.TotalChannels())
	log.Printf("[gochat] admin: http://localhost%s/admin", addr)

	if err := r.Run(addr); err != nil {
		log.Fatalf("server: %v", err)
	}
}
