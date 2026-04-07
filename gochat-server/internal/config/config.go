package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	SettingUploadMode    = "upload_mode"
	SettingUploadDir     = "upload_dir"
	SettingMaxUploadSize = "max_upload_size_bytes"
	SettingPublicURL     = "public_url"
	SettingS3Bucket      = "s3_bucket"
	SettingS3Region      = "s3_region"
	SettingS3Endpoint    = "s3_endpoint"
	SettingS3AccessKey   = "s3_access_key"
	SettingS3SecretKey   = "s3_secret_key"
	SettingS3PublicURL   = "s3_public_url"
	SettingS3ForcePath   = "s3_force_path"
	UploadModeLocal      = "local"
	UploadModeS3         = "s3"
)

type Config struct {
	WebhookSecret      string
	CallbackSecret     string
	OpenClawWebhookURL string
	ServerPort         string
	PublicURL          string
	UploadDir          string
	MaxUploadSize      int64
	DBPath             string
	AdminUsername      string
	AdminPassword      string
	AdminJWTSecret     string
	AudioSTTURL        string
	AudioSTTOnlineURL  string
	AudioFFmpegBin     string
	AudioSTTTimeoutSec int

	S3Bucket    string
	S3Region    string
	S3Endpoint  string
	S3AccessKey string
	S3SecretKey string
	S3PublicURL string
	S3ForcePath bool
}

func Load() (*Config, error) {
	cfg := &Config{
		WebhookSecret:      getEnv("GOCHAT_WEBHOOK_SECRET", ""),
		CallbackSecret:     getEnv("GOCHAT_CALLBACK_SECRET", ""),
		OpenClawWebhookURL: getEnv("GOCHAT_OPENCLAW_WEBHOOK_URL", "http://localhost:8790/gochat-webhook"),
		ServerPort:         getEnv("GOCHAT_SERVER_PORT", "9750"),
		PublicURL:          getEnv("GOCHAT_PUBLIC_URL", ""),
		UploadDir:          getEnv("GOCHAT_UPLOAD_DIR", "./uploads"),
		MaxUploadSize:      getEnvInt64("GOCHAT_MAX_UPLOAD_SIZE", 500<<20),
		DBPath:             getEnv("GOCHAT_DB_PATH", "./gochat.db"),
		AdminUsername:      getEnv("GOCHAT_ADMIN_USERNAME", "admin"),
		AdminPassword:      getEnv("GOCHAT_ADMIN_PASSWORD", "admin"),
		AdminJWTSecret:     getEnv("GOCHAT_ADMIN_JWT_SECRET", "gochat-admin-secret-change-me"),
		AudioSTTURL:        getEnv("GOCHAT_AUDIO_STT_URL", ""),
		AudioSTTOnlineURL:  getEnv("GOCHAT_AUDIO_STT_ONLINE_URL", ""),
		AudioFFmpegBin:     getEnv("GOCHAT_AUDIO_FFMPEG_BIN", "ffmpeg"),
		AudioSTTTimeoutSec: getEnvInt("GOCHAT_AUDIO_STT_TIMEOUT_SEC", 45),

		S3Bucket:    getEnv("GOCHAT_S3_BUCKET", ""),
		S3Region:    getEnv("GOCHAT_S3_REGION", "us-east-1"),
		S3Endpoint:  getEnv("GOCHAT_S3_ENDPOINT", ""),
		S3AccessKey: getEnv("GOCHAT_S3_ACCESS_KEY", ""),
		S3SecretKey: getEnv("GOCHAT_S3_SECRET_KEY", ""),
		S3PublicURL: getEnv("GOCHAT_S3_PUBLIC_URL", ""),
		S3ForcePath: getEnvBool("GOCHAT_S3_FORCE_PATH", false),
	}
	if cfg.WebhookSecret == "" {
		return nil, fmt.Errorf("GOCHAT_WEBHOOK_SECRET is required")
	}
	if cfg.CallbackSecret == "" {
		return nil, fmt.Errorf("GOCHAT_CALLBACK_SECRET is required")
	}
	if err := os.MkdirAll(cfg.UploadDir, 0o755); err != nil {
		return nil, fmt.Errorf("create upload dir: %w", err)
	}
	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	v := getEnv(key, "")
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func getEnvInt64(key string, fallback int64) int64 {
	v := getEnv(key, "")
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return fallback
	}
	return n
}

func getEnvBool(key string, fallback bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if v == "" {
		return fallback
	}
	return v == "true" || v == "1" || v == "yes"
}

func ServerAddr(cfg *Config) string {
	addr := cfg.ServerPort
	if !strings.Contains(addr, ":") {
		addr = ":" + addr
	}
	return addr
}

func ResolvePublicURL(cfg *Config) string {
	if cfg.PublicURL != "" {
		return cfg.PublicURL
	}
	return "http://localhost" + ServerAddr(cfg)
}

func ResolveUploadMode(cfg *Config) string {
	if strings.TrimSpace(cfg.S3Bucket) != "" {
		return UploadModeS3
	}
	return UploadModeLocal
}

func ApplyUploadSettings(cfg *Config, lookup func(string) (string, bool, error)) error {
	if cfg == nil || lookup == nil {
		return nil
	}

	mode, ok, err := lookup(SettingUploadMode)
	if err != nil {
		return err
	}
	if ok {
		mode = strings.ToLower(strings.TrimSpace(mode))
	}

	if value, ok, err := lookup(SettingUploadDir); err != nil {
		return err
	} else if ok && strings.TrimSpace(value) != "" {
		cfg.UploadDir = strings.TrimSpace(value)
	}

	if value, ok, err := lookup(SettingMaxUploadSize); err != nil {
		return err
	} else if ok && strings.TrimSpace(value) != "" {
		n, parseErr := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if parseErr != nil {
			return fmt.Errorf("parse %s: %w", SettingMaxUploadSize, parseErr)
		}
		if n > 0 {
			cfg.MaxUploadSize = n
		}
	}

	if value, ok, err := lookup(SettingPublicURL); err != nil {
		return err
	} else if ok {
		cfg.PublicURL = strings.TrimSpace(value)
	}

	for _, field := range []struct {
		key string
		dst *string
	}{
		{SettingS3Bucket, &cfg.S3Bucket},
		{SettingS3Region, &cfg.S3Region},
		{SettingS3Endpoint, &cfg.S3Endpoint},
		{SettingS3AccessKey, &cfg.S3AccessKey},
		{SettingS3SecretKey, &cfg.S3SecretKey},
		{SettingS3PublicURL, &cfg.S3PublicURL},
	} {
		value, ok, lookupErr := lookup(field.key)
		if lookupErr != nil {
			return lookupErr
		}
		if ok {
			*field.dst = strings.TrimSpace(value)
		}
	}

	if value, ok, err := lookup(SettingS3ForcePath); err != nil {
		return err
	} else if ok && strings.TrimSpace(value) != "" {
		cfg.S3ForcePath = getEnvBoolValue(value, cfg.S3ForcePath)
	}

	switch mode {
	case UploadModeLocal:
		cfg.S3Bucket = ""
	case UploadModeS3:
		// Keep configured S3 fields. Validation happens at the admin handler.
	}

	if err := os.MkdirAll(cfg.UploadDir, 0o755); err != nil {
		return fmt.Errorf("create upload dir: %w", err)
	}
	return nil
}

func getEnvBoolValue(value string, fallback bool) bool {
	v := strings.ToLower(strings.TrimSpace(value))
	if v == "" {
		return fallback
	}
	return v == "true" || v == "1" || v == "yes"
}
