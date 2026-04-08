package types

import "time"

type Attachment struct {
	URL      string `json:"url"`
	Type     string `json:"type"`
	Name     string `json:"name,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Size     int64  `json:"size,omitempty"`
}

type Channel struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Secret     string    `json:"secret"`
	WebhookURL string    `json:"webhookUrl"`
	Enabled    bool      `json:"enabled"`
	CreatedAt  time.Time `json:"createdAt"`
}

type InboundMessage struct {
	Type             string       `json:"type"`
	MessageID        string       `json:"messageId"`
	ConversationID   string       `json:"conversationId"`
	ConversationName string       `json:"conversationName"`
	ChannelID        string       `json:"channelId"`
	SenderID         string       `json:"senderId"`
	SenderName       string       `json:"senderName"`
	Text             string       `json:"text"`
	Attachments      []Attachment `json:"attachments,omitempty"`
	ReplyTo          string       `json:"replyTo,omitempty"`
	Timestamp        int64        `json:"timestamp"`
	IsGroupChat      bool         `json:"isGroupChat"`
}

type OutboundReply struct {
	Text           string `json:"text"`
	ConversationID string `json:"conversationId"`
	ChannelID      string `json:"channelId"`
	ReplyTo        string `json:"replyTo,omitempty"`
	MediaURL       string `json:"mediaUrl,omitempty"`
	Timestamp      int64  `json:"timestamp"`
}

type OutboundAck struct {
	MessageID string `json:"messageId"`
	Timestamp int64  `json:"timestamp"`
}

type SendMessageRequest struct {
	ConversationID   string       `json:"conversationId" binding:"required"`
	ConversationName string       `json:"conversationName,omitempty"`
	ChannelID        string       `json:"channelId"`
	SenderID         string       `json:"senderId"`
	SenderName       string       `json:"senderName,omitempty"`
	Text             string       `json:"text"`
	Attachments      []Attachment `json:"attachments,omitempty"`
	ReplyTo          string       `json:"replyTo,omitempty"`
	IsGroupChat      bool         `json:"isGroupChat"`
}

type ConversationInfo struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	ChannelID    string    `json:"channelId,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
	LastActive   time.Time `json:"lastActive"`
	MessageCount int       `json:"messageCount"`
}

type StoredMessage struct {
	ID             string       `json:"id"`
	ConversationID string       `json:"conversationId"`
	ChannelID      string       `json:"channelId,omitempty"`
	Direction      string       `json:"direction"`
	SenderID       string       `json:"senderId"`
	SenderName     string       `json:"senderName"`
	Text           string       `json:"text"`
	Attachments    []Attachment `json:"attachments,omitempty"`
	ReplyTo        string       `json:"replyTo,omitempty"`
	Timestamp      time.Time    `json:"timestamp"`
}

type SendAPIResponse struct {
	MessageID string `json:"messageId"`
	Timestamp int64  `json:"timestamp"`
	OK        bool   `json:"ok"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

type Task struct {
	ID             string     `json:"id"`
	ConversationID string     `json:"conversationId"`
	Title          string     `json:"title"`
	Description    string     `json:"description,omitempty"`
	Done           bool       `json:"done"`
	CreatedAt      time.Time  `json:"createdAt"`
	DoneAt         *time.Time `json:"doneAt,omitempty"`
}

type TaskSummary struct {
	Total     int `json:"total"`
	Pending   int `json:"pending"`
	Completed int `json:"completed"`
}

type CreateTaskRequest struct {
	Title       string `json:"title" binding:"required"`
	Description string `json:"description,omitempty"`
}

type TaskListResponse struct {
	Tasks []Task `json:"tasks"`
}

type WSClientInfo struct {
	ClientID    string    `json:"clientId"`
	ClientName  string    `json:"clientName"`
	RemoteAddr  string    `json:"remoteAddr"`
	ConnectedAt time.Time `json:"connectedAt"`
}

type PluginInfo struct {
	PluginID    string    `json:"pluginId"`
	Name        string    `json:"name"`
	RemoteAddr  string    `json:"remoteAddr"`
	ConnectedAt time.Time `json:"connectedAt"`
	LastSeen    time.Time `json:"lastSeen"`
}

type HeartbeatRequest struct {
	PluginID string `json:"pluginId"`
	Name     string `json:"name"`
}

type ConnectedClientsResponse struct {
	Plugins []PluginInfo   `json:"plugins"`
	Devices []WSClientInfo `json:"devices"`
}

type AdminStats struct {
	TotalConversations int   `json:"totalConversations"`
	TotalMessages      int   `json:"totalMessages"`
	TotalTasks         int   `json:"totalTasks"`
	TotalChannels      int   `json:"totalChannels"`
	OnlineDevices      int   `json:"onlineDevices"`
	OnlinePlugins      int   `json:"onlinePlugins"`
	TotalFiles         int   `json:"totalFiles"`
	TotalFileSize      int64 `json:"totalFileSize"`
	UptimeSeconds      int64 `json:"uptimeSeconds"`
}

type AdminUser struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"createdAt"`
	LastLogin time.Time `json:"lastLogin,omitempty"`
	Banned    bool      `json:"banned"`
}

type AdminConversationDetail struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	CreatedAt    time.Time       `json:"createdAt"`
	LastActive   time.Time       `json:"lastActive"`
	MessageCount int             `json:"messageCount"`
	Messages     []StoredMessage `json:"messages,omitempty"`
}

type AdminSystemConfig struct {
	WebhookURL     string `json:"webhookUrl"`
	ServerPort     string `json:"serverPort"`
	PublicURL      string `json:"publicUrl"`
	UploadDir      string `json:"uploadDir"`
	DBPath         string `json:"dbPath"`
	MaxUploadSize  int64  `json:"maxUploadSize"`
	CORSEnabled    bool   `json:"corsEnabled"`
	AuthEnabled    bool   `json:"authEnabled"`
	WebhookSecret  string `json:"webhookSecret"`
	CallbackSecret string `json:"callbackSecret"`
	JWTSecret      string `json:"jwtSecret"`
}

type AdminUploadConfig struct {
	Mode              string `json:"mode"`
	UploadDir         string `json:"uploadDir"`
	MaxUploadSize     int64  `json:"maxUploadSize"`
	PublicURL         string `json:"publicUrl"`
	S3Bucket          string `json:"s3Bucket"`
	S3Region          string `json:"s3Region"`
	S3Endpoint        string `json:"s3Endpoint"`
	S3AccessKey       string `json:"s3AccessKey"`
	S3SecretKey       string `json:"s3SecretKey"`
	S3PublicURL       string `json:"s3PublicUrl"`
	S3ForcePath       bool   `json:"s3ForcePath"`
	RestartRequired   bool   `json:"restartRequired"`
	ActiveMode        string `json:"activeMode"`
	ActiveUploadDir   string `json:"activeUploadDir"`
	ActivePublicURL   string `json:"activePublicUrl"`
	ActiveMaxUpload   int64  `json:"activeMaxUploadSize"`
	ActiveS3Bucket    string `json:"activeS3Bucket"`
	ActiveS3Region    string `json:"activeS3Region"`
	ActiveS3Endpoint  string `json:"activeS3Endpoint"`
	ActiveS3PublicURL string `json:"activeS3PublicUrl"`
	ActiveS3ForcePath bool   `json:"activeS3ForcePath"`
}

type AdminLoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type AdminLoginResponse struct {
	Token string `json:"token"`
}

type WSMessage struct {
	Type       string      `json:"type"`
	Title      string      `json:"title,omitempty"`
	Date       string      `json:"date,omitempty"`
	Tasks      interface{} `json:"tasks,omitempty"`
	ClientID   string      `json:"clientId,omitempty"`
	Clients    interface{} `json:"clients,omitempty"`
	Plugins    interface{} `json:"plugins,omitempty"`
	Attachment *Attachment `json:"attachment,omitempty"`
	Timestamp  int64       `json:"timestamp,omitempty"`
}

type ChatLoginRequest struct {
	ChannelID string `json:"channelId" binding:"required"`
	Secret    string `json:"secret" binding:"required"`
}

type ChatLoginResponse struct {
	Token       string `json:"token"`
	ChannelID   string `json:"channelId"`
	ChannelName string `json:"channelName"`
	ExpiresIn   int64  `json:"expiresIn"`
	ExpiresAt   int64  `json:"expiresAt,omitempty"`
}

type ChatSessionResponse struct {
	Valid        bool              `json:"valid"`
	ChannelID    string            `json:"channelId"`
	ChannelName  string            `json:"channelName"`
	UserID       string            `json:"userId,omitempty"`
	Enabled      bool              `json:"enabled"`
	Online       bool              `json:"online"`
	Version      string            `json:"version,omitempty"`
	AgentCount   int               `json:"agentCount,omitempty"`
	WorkStatus   string            `json:"workStatus,omitempty"`
	OfficeStatus string            `json:"officeStatus,omitempty"`
	CurrentModel string            `json:"currentModel,omitempty"`
	ModelSource  string            `json:"modelSource,omitempty"`
	Command      string            `json:"command,omitempty"`
	CommandArgs  string            `json:"commandArgs,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	ConnectedAt  time.Time         `json:"connectedAt,omitempty"`
	LastSeen     time.Time         `json:"lastSeen,omitempty"`
	ExpiresAt    int64             `json:"expiresAt,omitempty"`
}

type ChatMessage struct {
	Type           string       `json:"type"`
	Text           string       `json:"text"`
	ConversationID string       `json:"conversationId"`
	ReplyTo        string       `json:"replyTo,omitempty"`
	Timestamp      int64        `json:"timestamp"`
	Attachments    []Attachment `json:"attachments,omitempty"`
}

type ClientInfo struct {
	Type        string            `json:"type"`
	Version     string            `json:"version,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	ConnectedAt time.Time         `json:"connectedAt,omitempty"`
}

// PluginRuntimeStatus holds plugin-specific runtime data (agent count, work status, etc.).
type PluginRuntimeStatus struct {
	Version    string            `json:"version"`
	AgentCount int               `json:"agentCount"`
	Status     string            `json:"status"`
	Uptime     int64             `json:"uptime"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

type AdminClient struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	ClientType  string            `json:"clientType"`
	Version     string            `json:"version,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	RemoteAddr  string            `json:"remoteAddr,omitempty"`
	ChannelID   string            `json:"channelId,omitempty"`
	ConnectedAt time.Time         `json:"connectedAt"`
	LastSeenAt  time.Time         `json:"lastSeenAt,omitempty"`
	Source      string            `json:"source"` // "pluginWS" | "chatWS" | "hub" | "pluginTracker"
}
