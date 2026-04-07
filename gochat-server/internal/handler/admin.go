package handler

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/m0yi/gochat-server/internal/config"
	"github.com/m0yi/gochat-server/internal/store"
	"github.com/m0yi/gochat-server/internal/types"
	"github.com/m0yi/gochat-server/internal/uploader"
)

type AdminConfig struct {
	Username       string
	Password       string
	PublicURL      string
	UploadDir      string
	MaxUploadSize  int64
	ServerPort     string
	DBPath         string
	WebhookURL     string
	WebhookSecret  string
	CallbackSecret string
	AdminJWTSecret string
	S3Bucket       string
	S3Region       string
	S3Endpoint     string
	S3AccessKey    string
	S3SecretKey    string
	S3PublicURL    string
	S3ForcePath    bool
}

type AdminHandler struct {
	adminStore   *store.AdminStore
	store        *store.Store
	taskStore    *store.TaskStore
	channelStore *store.ChannelStore
	pluginWS     *PluginWS
	jwtSecret    string
	cfg          AdminConfig
	startTime    time.Time
}

func NewAdminHandler(as *store.AdminStore, s *store.Store, ts *store.TaskStore, cs *store.ChannelStore, jwtSecret string, cfg AdminConfig, pluginWS *PluginWS) *AdminHandler {
	return &AdminHandler{
		adminStore:   as,
		store:        s,
		taskStore:    ts,
		channelStore: cs,
		pluginWS:     pluginWS,
		jwtSecret:    jwtSecret,
		cfg:          cfg,
		startTime:    time.Now(),
	}
}

type Claims struct {
	Username string `json:"username"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

func (ah *AdminHandler) generateToken(username, role string) (string, error) {
	claims := Claims{
		Username: username,
		Role:     role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(ah.jwtSecret))
}

func (ah *AdminHandler) parseToken(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		return []byte(ah.jwtSecret), nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}
	return claims, nil
}

func (ah *AdminHandler) AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		auth := c.GetHeader("Authorization")
		if auth == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, types.ErrorResponse{Error: "missing token"})
			return
		}
		tokenStr := strings.TrimPrefix(auth, "Bearer ")
		claims, err := ah.parseToken(tokenStr)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, types.ErrorResponse{Error: "invalid token"})
			return
		}
		c.Set("adminUser", claims.Username)
		c.Set("adminRole", claims.Role)
		c.Next()
	}
}

func (ah *AdminHandler) Login(c *gin.Context) {
	var req types.AdminLoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "invalid request"})
		return
	}

	user, err := ah.adminStore.Authenticate(req.Username, req.Password)
	if err != nil {
		c.JSON(http.StatusUnauthorized, types.ErrorResponse{Error: err.Error()})
		return
	}

	token, err := ah.generateToken(user.Username, user.Role)
	if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "token generation failed"})
		return
	}

	ah.adminStore.AddAuditLog("login", "user "+user.Username+" logged in", user.Username)

	c.JSON(http.StatusOK, types.AdminLoginResponse{Token: token})
}

func (ah *AdminHandler) SimpleLogin(c *gin.Context) {
	var req types.AdminLoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "invalid request"})
		return
	}

	if subtle.ConstantTimeCompare([]byte(req.Username), []byte(ah.cfg.Username)) != 1 ||
		subtle.ConstantTimeCompare([]byte(req.Password), []byte(ah.cfg.Password)) != 1 {
		c.JSON(http.StatusUnauthorized, types.ErrorResponse{Error: "invalid credentials"})
		return
	}

	token, err := ah.generateToken(req.Username, "superadmin")
	if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "token generation failed"})
		return
	}

	c.JSON(http.StatusOK, types.AdminLoginResponse{Token: token})
}

func (ah *AdminHandler) ValidateToken(c *gin.Context) {
	claims, exists := c.Get("adminUser")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"valid": false})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"valid":    true,
		"username": claims,
	})
}

func (ah *AdminHandler) ChangePassword(c *gin.Context) {
	var req struct {
		OldPassword string `json:"oldPassword" binding:"required"`
		NewPassword string `json:"newPassword" binding:"required,min=6"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "invalid request"})
		return
	}

	username, _ := c.Get("adminUser")
	user, err := ah.adminStore.Authenticate(username.(string), req.OldPassword)
	if err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "current password incorrect"})
		return
	}

	if err := ah.adminStore.ResetPassword(user.ID, req.NewPassword); err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: err.Error()})
		return
	}

	ah.adminStore.AddAuditLog("change_password", "changed own password", username.(string))
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (ah *AdminHandler) GetStats(c *gin.Context) {
	convs := ah.store.ListConversations()
	totalMsgs := 0
	for _, cv := range convs {
		totalMsgs += cv.MessageCount
	}

	totalFiles := 0
	var totalSize int64
	filepath.Walk(ah.cfg.UploadDir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			totalFiles++
			totalSize += info.Size()
		}
		return nil
	})

	stats := types.AdminStats{
		TotalConversations: len(convs),
		TotalMessages:      totalMsgs,
		TotalTasks:         ah.adminStore.TotalTasks(),
		TotalChannels:      ah.channelStore.TotalChannels(),
		TotalFiles:         totalFiles,
		TotalFileSize:      totalSize,
		UptimeSeconds:      int64(time.Since(ah.startTime).Seconds()),
	}

	c.JSON(http.StatusOK, stats)
}

func (ah *AdminHandler) GetStatsWithClients(_ interface{}, hub *WSHub, pt *PluginTracker) gin.HandlerFunc {
	return func(c *gin.Context) {
		convs := ah.store.ListConversations()
		totalMsgs := 0
		for _, cv := range convs {
			totalMsgs += cv.MessageCount
		}

		totalFiles := 0
		var totalSize int64
		filepath.Walk(ah.cfg.UploadDir, func(path string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() {
				totalFiles++
				totalSize += info.Size()
			}
			return nil
		})

		devices := hub.ListClients()
		plugins := pt.ListPlugins()

		stats := types.AdminStats{
			TotalConversations: len(convs),
			TotalMessages:      totalMsgs,
			TotalTasks:         ah.adminStore.TotalTasks(),
			TotalChannels:      ah.channelStore.TotalChannels(),
			OnlineDevices:      len(devices),
			OnlinePlugins:      len(plugins),
			TotalFiles:         totalFiles,
			TotalFileSize:      totalSize,
			UptimeSeconds:      int64(time.Since(ah.startTime).Seconds()),
		}

		c.JSON(http.StatusOK, stats)
	}
}

func (ah *AdminHandler) ListConversations(c *gin.Context) {
	convs := ah.store.ListConversations()
	c.JSON(http.StatusOK, convs)
}

func (ah *AdminHandler) GetConversationDetail(c *gin.Context) {
	convID := c.Param("conversationId")
	if convID == "" {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "conversationId required"})
		return
	}

	info, ok := ah.store.GetConversation(convID)
	if !ok {
		c.JSON(http.StatusNotFound, types.ErrorResponse{Error: "conversation not found"})
		return
	}

	msgs := ah.store.GetMessages(convID, 200)

	c.JSON(http.StatusOK, types.AdminConversationDetail{
		ID:           info.ID,
		Name:         info.Name,
		CreatedAt:    info.CreatedAt,
		LastActive:   info.LastActive,
		MessageCount: info.MessageCount,
		Messages:     msgs,
	})
}

func (ah *AdminHandler) DeleteConversation(c *gin.Context) {
	convID := c.Param("conversationId")
	operator, _ := c.Get("adminUser")

	ah.store.DeleteConversation(convID)

	ah.adminStore.AddAuditLog("delete_conversation", "deleted conversation "+convID, operator.(string))
	log.Printf("[admin] %s deleted conversation %s", operator, convID)

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (ah *AdminHandler) ListUsers(c *gin.Context) {
	users, err := ah.adminStore.ListUsers()
	if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: err.Error()})
		return
	}
	c.JSON(http.StatusOK, users)
}

func (ah *AdminHandler) CreateUser(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required,min=6"`
		Role     string `json:"role"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "invalid request: " + err.Error()})
		return
	}
	if req.Role == "" {
		req.Role = "admin"
	}

	operator, _ := c.Get("adminUser")
	user, err := ah.adminStore.CreateUser(req.Username, req.Password, req.Role)
	if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: err.Error()})
		return
	}

	ah.adminStore.AddAuditLog("create_user", "created user "+req.Username, operator.(string))
	c.JSON(http.StatusCreated, user)
}

func (ah *AdminHandler) DeleteUser(c *gin.Context) {
	userID := c.Param("userId")
	operator, _ := c.Get("adminUser")

	if err := ah.adminStore.DeleteUser(userID); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: err.Error()})
		return
	}

	ah.adminStore.AddAuditLog("delete_user", "deleted user "+userID, operator.(string))
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (ah *AdminHandler) BanUser(c *gin.Context) {
	userID := c.Param("userId")
	var req struct {
		Banned bool `json:"banned"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "invalid request"})
		return
	}

	operator, _ := c.Get("adminUser")
	if err := ah.adminStore.BanUser(userID, req.Banned); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: err.Error()})
		return
	}

	action := "unban_user"
	if req.Banned {
		action = "ban_user"
	}
	ah.adminStore.AddAuditLog(action, fmt.Sprintf("user %s banned=%v", userID, req.Banned), operator.(string))
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (ah *AdminHandler) ResetPassword(c *gin.Context) {
	userID := c.Param("userId")
	var req struct {
		Password string `json:"password" binding:"required,min=6"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "invalid request: " + err.Error()})
		return
	}

	operator, _ := c.Get("adminUser")
	if err := ah.adminStore.ResetPassword(userID, req.Password); err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: err.Error()})
		return
	}

	ah.adminStore.AddAuditLog("reset_password", "reset password for user "+userID, operator.(string))
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (ah *AdminHandler) GetSystemConfig(c *gin.Context) {
	cfg := types.AdminSystemConfig{
		WebhookURL:     ah.cfg.WebhookURL,
		ServerPort:     ah.cfg.ServerPort,
		PublicURL:      ah.cfg.PublicURL,
		UploadDir:      ah.cfg.UploadDir,
		DBPath:         ah.cfg.DBPath,
		MaxUploadSize:  ah.cfg.MaxUploadSize,
		CORSEnabled:    true,
		AuthEnabled:    true,
		WebhookSecret:  ah.cfg.WebhookSecret,
		CallbackSecret: ah.cfg.CallbackSecret,
		JWTSecret:      ah.cfg.AdminJWTSecret,
	}
	c.JSON(http.StatusOK, cfg)
}

func (ah *AdminHandler) resolveUploadConfig() (types.AdminUploadConfig, error) {
	active := config.Config{
		PublicURL:     ah.cfg.PublicURL,
		UploadDir:     ah.cfg.UploadDir,
		MaxUploadSize: ah.cfg.MaxUploadSize,
		S3Bucket:      ah.cfg.S3Bucket,
		S3Region:      ah.cfg.S3Region,
		S3Endpoint:    ah.cfg.S3Endpoint,
		S3AccessKey:   ah.cfg.S3AccessKey,
		S3SecretKey:   ah.cfg.S3SecretKey,
		S3PublicURL:   ah.cfg.S3PublicURL,
		S3ForcePath:   ah.cfg.S3ForcePath,
	}
	resolved := active
	if err := config.ApplyUploadSettings(&resolved, ah.adminStore.GetSettingValue); err != nil {
		return types.AdminUploadConfig{}, err
	}
	return types.AdminUploadConfig{
		Mode:              config.ResolveUploadMode(&resolved),
		UploadDir:         resolved.UploadDir,
		MaxUploadSize:     resolved.MaxUploadSize,
		PublicURL:         resolved.PublicURL,
		S3Bucket:          resolved.S3Bucket,
		S3Region:          resolved.S3Region,
		S3Endpoint:        resolved.S3Endpoint,
		S3AccessKey:       resolved.S3AccessKey,
		S3SecretKey:       resolved.S3SecretKey,
		S3PublicURL:       resolved.S3PublicURL,
		S3ForcePath:       resolved.S3ForcePath,
		RestartRequired:   !uploadConfigsEqual(active, resolved),
		ActiveMode:        config.ResolveUploadMode(&active),
		ActiveUploadDir:   active.UploadDir,
		ActivePublicURL:   active.PublicURL,
		ActiveMaxUpload:   active.MaxUploadSize,
		ActiveS3Bucket:    active.S3Bucket,
		ActiveS3Region:    active.S3Region,
		ActiveS3Endpoint:  active.S3Endpoint,
		ActiveS3PublicURL: active.S3PublicURL,
		ActiveS3ForcePath: active.S3ForcePath,
	}, nil
}

func uploadConfigsEqual(a, b config.Config) bool {
	return a.PublicURL == b.PublicURL &&
		a.UploadDir == b.UploadDir &&
		a.MaxUploadSize == b.MaxUploadSize &&
		a.S3Bucket == b.S3Bucket &&
		a.S3Region == b.S3Region &&
		a.S3Endpoint == b.S3Endpoint &&
		a.S3AccessKey == b.S3AccessKey &&
		a.S3SecretKey == b.S3SecretKey &&
		a.S3PublicURL == b.S3PublicURL &&
		a.S3ForcePath == b.S3ForcePath
}

func (ah *AdminHandler) GetUploadConfig(c *gin.Context) {
	cfg, err := ah.resolveUploadConfig()
	if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: err.Error()})
		return
	}
	c.JSON(http.StatusOK, cfg)
}

func (ah *AdminHandler) UpdateUploadConfig(c *gin.Context) {
	var req uploadConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "invalid request"})
		return
	}
	if err := validateUploadConfigRequest(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: err.Error()})
		return
	}

	settings := map[string]string{
		config.SettingUploadMode:    req.Mode,
		config.SettingUploadDir:     strings.TrimSpace(req.UploadDir),
		config.SettingMaxUploadSize: fmt.Sprintf("%d", req.MaxUploadSize),
		config.SettingPublicURL:     strings.TrimSpace(req.PublicURL),
		config.SettingS3Bucket:      strings.TrimSpace(req.S3Bucket),
		config.SettingS3Region:      strings.TrimSpace(req.S3Region),
		config.SettingS3Endpoint:    strings.TrimSpace(req.S3Endpoint),
		config.SettingS3AccessKey:   strings.TrimSpace(req.S3AccessKey),
		config.SettingS3SecretKey:   strings.TrimSpace(req.S3SecretKey),
		config.SettingS3PublicURL:   strings.TrimSpace(req.S3PublicURL),
		config.SettingS3ForcePath:   fmt.Sprintf("%t", req.S3ForcePath),
	}
	for key, value := range settings {
		if err := ah.adminStore.SetSetting(key, value); err != nil {
			c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: err.Error()})
			return
		}
	}

	operator, _ := c.Get("adminUser")
	ah.adminStore.AddAuditLog("update_upload_config", fmt.Sprintf("set upload mode to %s", req.Mode), operator.(string))

	cfg, err := ah.resolveUploadConfig()
	if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: err.Error()})
		return
	}
	c.JSON(http.StatusOK, cfg)
}

type uploadConfigRequest struct {
	Mode          string `json:"mode" binding:"required"`
	UploadDir     string `json:"uploadDir"`
	MaxUploadSize int64  `json:"maxUploadSize"`
	PublicURL     string `json:"publicUrl"`
	S3Bucket      string `json:"s3Bucket"`
	S3Region      string `json:"s3Region"`
	S3Endpoint    string `json:"s3Endpoint"`
	S3AccessKey   string `json:"s3AccessKey"`
	S3SecretKey   string `json:"s3SecretKey"`
	S3PublicURL   string `json:"s3PublicUrl"`
	S3ForcePath   bool   `json:"s3ForcePath"`
}

func validateUploadConfigRequest(req *uploadConfigRequest) error {
	req.Mode = strings.ToLower(strings.TrimSpace(req.Mode))
	switch req.Mode {
	case config.UploadModeLocal, config.UploadModeS3:
	default:
		return fmt.Errorf("unsupported upload mode")
	}
	if strings.TrimSpace(req.UploadDir) == "" {
		return fmt.Errorf("uploadDir is required")
	}
	if req.MaxUploadSize <= 0 {
		return fmt.Errorf("maxUploadSize must be greater than 0")
	}
	if req.Mode == config.UploadModeS3 && strings.TrimSpace(req.S3Bucket) == "" {
		return fmt.Errorf("s3Bucket is required for S3 mode")
	}
	return nil
}

func (ah *AdminHandler) TestUploadConfig(c *gin.Context) {
	var req uploadConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "invalid request"})
		return
	}
	if err := validateUploadConfigRequest(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: err.Error()})
		return
	}

	if req.Mode == config.UploadModeLocal {
		if err := os.MkdirAll(strings.TrimSpace(req.UploadDir), 0o755); err != nil {
			c.JSON(http.StatusOK, gin.H{
				"ok":      false,
				"target":  "upload",
				"mode":    req.Mode,
				"message": err.Error(),
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"ok":      true,
			"target":  "upload",
			"mode":    req.Mode,
			"message": "Local upload directory is writable",
		})
		return
	}

	uploadPublicURL := strings.TrimSpace(req.S3PublicURL)
	if uploadPublicURL == "" {
		uploadPublicURL = strings.TrimSpace(req.PublicURL)
	}
	tester := uploader.NewS3Uploader(
		strings.TrimSpace(req.S3Endpoint),
		strings.TrimSpace(req.S3Region),
		strings.TrimSpace(req.S3AccessKey),
		strings.TrimSpace(req.S3SecretKey),
		strings.TrimSpace(req.S3Bucket),
		uploadPublicURL,
		req.S3ForcePath,
	)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	testURL, deleteWarning, err := tester.TestConnection(ctx)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"ok":      false,
			"target":  "upload",
			"mode":    req.Mode,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":        true,
		"target":    "upload",
		"mode":      req.Mode,
		"message":   "S3 upload and public access test succeeded",
		"url":       testURL,
		"warning":   deleteWarning,
		"cleanupOk": deleteWarning == "",
	})
}

func (ah *AdminHandler) GetSettings(c *gin.Context) {
	settings := map[string]string{}
	for _, key := range []string{"maintenance_mode", "max_upload_size", "rate_limit"} {
		val, _ := ah.adminStore.GetSetting(key)
		if val != "" {
			settings[key] = val
		}
	}
	c.JSON(http.StatusOK, settings)
}

func (ah *AdminHandler) UpdateSetting(c *gin.Context) {
	var req struct {
		Key   string `json:"key" binding:"required"`
		Value string `json:"value"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "invalid request"})
		return
	}

	operator, _ := c.Get("adminUser")
	if err := ah.adminStore.SetSetting(req.Key, req.Value); err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: err.Error()})
		return
	}

	ah.adminStore.AddAuditLog("update_setting", fmt.Sprintf("set %s", req.Key), operator.(string))
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (ah *AdminHandler) ListFiles(c *gin.Context) {
	files := []map[string]interface{}{}
	entries, err := os.ReadDir(ah.cfg.UploadDir)
	if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: "read uploads dir failed"})
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, map[string]interface{}{
			"name":     e.Name(),
			"size":     info.Size(),
			"modified": info.ModTime(),
			"url":      fmt.Sprintf("/files/%s", e.Name()),
		})
	}
	c.JSON(http.StatusOK, files)
}

func (ah *AdminHandler) DeleteFile(c *gin.Context) {
	filename := c.Param("filename")
	cleanName := filepath.Base(filename)
	if cleanName != filename {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "invalid filename"})
		return
	}

	operator, _ := c.Get("adminUser")
	path := filepath.Join(ah.cfg.UploadDir, cleanName)
	if err := os.Remove(path); err != nil {
		c.JSON(http.StatusNotFound, types.ErrorResponse{Error: "file not found"})
		return
	}

	ah.adminStore.AddAuditLog("delete_file", "deleted file "+cleanName, operator.(string))
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (ah *AdminHandler) ListAuditLogs(c *gin.Context) {
	logs, err := ah.adminStore.ListAuditLogs(100)
	if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: err.Error()})
		return
	}
	c.JSON(http.StatusOK, logs)
}

func (ah *AdminHandler) BroadcastMessage(c *gin.Context) {
	var req struct {
		Text string `json:"text" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "invalid request"})
		return
	}

	operator, _ := c.Get("adminUser")
	ah.adminStore.AddAuditLog("broadcast", "broadcast: "+req.Text, operator.(string))
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (ah *AdminHandler) ExportData(c *gin.Context) {
	convs := ah.store.ListConversations()
	exportData := map[string]interface{}{
		"conversations": convs,
		"exportedAt":    time.Now(),
	}

	operator, _ := c.Get("adminUser")
	ah.adminStore.AddAuditLog("export_data", "exported system data", operator.(string))

	c.Header("Content-Disposition", "attachment; filename=gochat-export.json")
	c.JSON(http.StatusOK, exportData)
}

func (ah *AdminHandler) GetTasksForAdmin(c *gin.Context) {
	convID := c.Query("conversationId")
	if convID != "" {
		tasks, err := ah.taskStore.ListTasks(convID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: err.Error()})
			return
		}
		c.JSON(http.StatusOK, types.TaskListResponse{Tasks: tasks})
		return
	}

	c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "conversationId required"})
}

func (ah *AdminHandler) HandleAdminClients(hub *WSHub, pt *PluginTracker, chatWS *ChatWS, pluginWS *PluginWS) gin.HandlerFunc {
	return func(c *gin.Context) {
		var clients []types.AdminClient

		for _, d := range hub.ListClients() {
			clients = append(clients, types.AdminClient{
				ID:          d.ClientID,
				Name:        d.ClientName,
				ClientType:  "device",
				RemoteAddr:  d.RemoteAddr,
				ConnectedAt: d.ConnectedAt,
				Source:      "hub",
			})
		}

		for _, p := range pt.ListPlugins() {
			clients = append(clients, types.AdminClient{
				ID:          p.PluginID,
				Name:        p.Name,
				ClientType:  "plugin",
				RemoteAddr:  p.RemoteAddr,
				ConnectedAt: p.ConnectedAt,
				LastSeenAt:  p.LastSeen,
				Source:      "pluginTracker",
			})
		}

		for _, cc := range chatWS.ListChatClients() {
			if cc.ClientInfo.Type == "" {
				continue
			}
			clients = append(clients, types.AdminClient{
				ID:          "chat-" + cc.ConnID,
				Name:        cc.UserID,
				ClientType:  cc.ClientInfo.Type,
				Version:     cc.ClientInfo.Version,
				Metadata:    cc.ClientInfo.Metadata,
				ChannelID:   cc.ChannelID,
				RemoteAddr:  cc.RemoteAddr,
				ConnectedAt: cc.ClientInfo.ConnectedAt,
				Source:      "chatWS",
			})
		}

		for _, pc := range pluginWS.ListPluginClients() {
			clients = append(clients, types.AdminClient{
				ID:          "plugin-" + pc.ChannelID,
				Name:        pc.ChannelID,
				ClientType:  pc.ClientInfo.Type,
				Version:     pc.ClientInfo.Version,
				Metadata:    pc.ClientInfo.Metadata,
				ChannelID:   pc.ChannelID,
				RemoteAddr:  pc.RemoteAddr,
				ConnectedAt: pc.ClientInfo.ConnectedAt,
				Source:      "pluginWS",
			})
		}

		c.JSON(http.StatusOK, gin.H{"clients": clients})
	}
}

func (ah *AdminHandler) TestConnection(c *gin.Context) {
	target := c.DefaultQuery("target", "openclaw")

	if target == "openclaw" {
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Get(ah.cfg.WebhookURL)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"ok":      false,
				"target":  "openclaw",
				"message": err.Error(),
			})
			return
		}
		defer resp.Body.Close()
		c.JSON(http.StatusOK, gin.H{
			"ok":      true,
			"target":  "openclaw",
			"status":  resp.StatusCode,
			"message": fmt.Sprintf("OpenClaw returned HTTP %d", resp.StatusCode),
		})
		return
	}

	c.JSON(http.StatusBadRequest, gin.H{"ok": false, "message": "unknown target"})
}

func (ah *AdminHandler) ListChannels(c *gin.Context) {
	channels, err := ah.channelStore.ListChannels()
	if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: err.Error()})
		return
	}

	type channelWithStatus struct {
		types.Channel
		Online         bool              `json:"online"`
		ClientType     string            `json:"clientType,omitempty"`
		ClientVersion  string            `json:"clientVersion,omitempty"`
		ClientMetadata map[string]string `json:"clientMetadata,omitempty"`
		Version        string            `json:"version,omitempty"`
		AgentCount     int               `json:"agentCount,omitempty"`
		WorkStatus     string            `json:"workStatus,omitempty"`
		ConnectedAt    *time.Time        `json:"connectedAt,omitempty"`
		LastSeen       *time.Time        `json:"lastSeen,omitempty"`
	}

	result := make([]channelWithStatus, 0, len(channels))
	for _, ch := range channels {
		online := false
		clientType := ""
		clientVersion := ""
		clientMetadata := make(map[string]string)
		version := ""
		agentCount := 0
		workStatus := ""
		var connectedAt *time.Time
		var lastSeen *time.Time

		if ah.pluginWS != nil {
			online = ah.pluginWS.IsOnline(ch.ID)
			if ci := ah.pluginWS.GetChannelStatus(ch.ID); ci != nil {
				clientType = ci.Type
				clientVersion = ci.Version
				clientMetadata = ci.Metadata
			}
			if rt := ah.pluginWS.GetChannelRuntimeStatus(ch.ID); rt != nil {
				version = rt.Version
				agentCount = rt.AgentCount
				workStatus = rt.Status
			}
			if ca, ls, ok := ah.pluginWS.GetChannelConnInfo(ch.ID); ok {
				connectedAt = &ca
				lastSeen = &ls
			}
		}
		result = append(result, channelWithStatus{
			Channel:        ch,
			Online:         online,
			ClientType:     clientType,
			ClientVersion:  clientVersion,
			ClientMetadata: clientMetadata,
			Version:        version,
			AgentCount:     agentCount,
			WorkStatus:     workStatus,
			ConnectedAt:    connectedAt,
			LastSeen:       lastSeen,
		})
	}
	c.JSON(http.StatusOK, result)
}

func (ah *AdminHandler) CreateChannel(c *gin.Context) {
	var req struct {
		Name       string `json:"name" binding:"required"`
		WebhookURL string `json:"webhookUrl"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "invalid request: " + err.Error()})
		return
	}

	operator, _ := c.Get("adminUser")
	channel, err := ah.channelStore.CreateChannel(req.Name, req.WebhookURL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: err.Error()})
		return
	}

	ah.adminStore.AddAuditLog("create_channel", "created channel "+channel.ID, operator.(string))
	c.JSON(http.StatusCreated, channel)
}

func (ah *AdminHandler) GetChannel(c *gin.Context) {
	id := c.Param("channelId")
	channel, err := ah.channelStore.GetChannel(id)
	if err != nil {
		c.JSON(http.StatusNotFound, types.ErrorResponse{Error: err.Error()})
		return
	}
	c.JSON(http.StatusOK, channel)
}

func (ah *AdminHandler) UpdateChannel(c *gin.Context) {
	id := c.Param("channelId")
	var req struct {
		Name       string `json:"name" binding:"required"`
		WebhookURL string `json:"webhookUrl"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "invalid request: " + err.Error()})
		return
	}

	operator, _ := c.Get("adminUser")
	channel, err := ah.channelStore.UpdateChannel(id, req.Name, req.WebhookURL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: err.Error()})
		return
	}

	ah.adminStore.AddAuditLog("update_channel", "updated channel "+id, operator.(string))
	c.JSON(http.StatusOK, channel)
}

func (ah *AdminHandler) DeleteChannel(c *gin.Context) {
	id := c.Param("channelId")
	operator, _ := c.Get("adminUser")

	if err := ah.channelStore.DeleteChannel(id); err != nil {
		c.JSON(http.StatusNotFound, types.ErrorResponse{Error: err.Error()})
		return
	}

	ah.adminStore.AddAuditLog("delete_channel", "deleted channel "+id, operator.(string))
	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"warning": "此操作需要重启 OpenClaw 才能生效。如果 OpenClaw 正在运行，请手动重启。",
	})
}

func (ah *AdminHandler) RegenerateChannelSecret(c *gin.Context) {
	id := c.Param("channelId")
	operator, _ := c.Get("adminUser")

	channel, err := ah.channelStore.RegenerateSecret(id)
	if err != nil {
		c.JSON(http.StatusNotFound, types.ErrorResponse{Error: err.Error()})
		return
	}

	ah.adminStore.AddAuditLog("regenerate_channel_secret", "regenerated secret for channel "+id, operator.(string))
	c.JSON(http.StatusOK, gin.H{
		"channel": channel,
		"warning": "此操作需要重启 OpenClaw 才能生效。旧密钥将立即失效，新密钥需要 OpenClaw 重启后生效。",
	})
}

func (ah *AdminHandler) ToggleChannel(c *gin.Context) {
	id := c.Param("channelId")
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "invalid request"})
		return
	}

	operator, _ := c.Get("adminUser")
	if err := ah.channelStore.SetEnabled(id, req.Enabled); err != nil {
		c.JSON(http.StatusNotFound, types.ErrorResponse{Error: err.Error()})
		return
	}

	action := "disable_channel"
	if req.Enabled {
		action = "enable_channel"
	}
	ah.adminStore.AddAuditLog(action, fmt.Sprintf("channel %s enabled=%v", id, req.Enabled), operator.(string))
	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"warning": "此操作需要重启 OpenClaw 才能生效。如果 OpenClaw 正在运行，请手动重启。",
	})
}

var (
	_ = json.Marshal
	_ = log.Printf
	_ = fmt.Sprintf
	_ = subtle.ConstantTimeCompare
)
