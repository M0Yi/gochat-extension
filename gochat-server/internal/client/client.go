package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	appcrypto "github.com/m0yi/gochat-server/internal/crypto"
	"github.com/m0yi/gochat-server/internal/types"
)

type OpenClawClient struct {
	httpClient *http.Client
	mu         sync.RWMutex
}

func New() *OpenClawClient {
	return &OpenClawClient{
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *OpenClawClient) SendToChannel(msg types.InboundMessage, webhookURL, secret string) error {
	if webhookURL == "" {
		return fmt.Errorf("channel has no webhook URL configured")
	}

	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}

	signature, timestamp := appcrypto.SignRequest(secret, string(body))

	req, err := http.NewRequest(http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GoChat-Signature", signature)
	req.Header.Set("X-GoChat-Timestamp", timestamp)
	req.Header.Set("X-GoChat-Channel-ID", msg.ChannelID)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send to openclaw: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("openclaw returned %d: %s", resp.StatusCode, string(respBody))
	}

	log.Printf("[openclaw] sent message %s to conv %s via channel %s", msg.MessageID, msg.ConversationID, msg.ChannelID)
	return nil
}
