package handler

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"time"

	appcrypto "github.com/m0yi/gochat-server/internal/crypto"
	"github.com/m0yi/gochat-server/internal/store"
	"github.com/m0yi/gochat-server/internal/types"
)

type Handler struct {
	callbackSecret string
	store          *store.Store
	onReply        func(reply types.OutboundReply)
}

func New(callbackSecret string, s *store.Store, onReply func(reply types.OutboundReply)) *Handler {
	return &Handler{
		callbackSecret: callbackSecret,
		store:          s,
		onReply:        onReply,
	}
}

func (h *Handler) HandleCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	signature := r.Header.Get("X-GoChat-Signature")
	timestamp := r.Header.Get("X-GoChat-Timestamp")
	if signature == "" || timestamp == "" {
		http.Error(w, "missing signature headers", http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 10<<20))
	if err != nil {
		http.Error(w, "read body failed", http.StatusBadRequest)
		return
	}

	if err := appcrypto.VerifySignature(h.callbackSecret, signature, timestamp, string(body)); err != nil {
		log.Printf("[callback] signature verification failed: %v", err)
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	var reply types.OutboundReply
	if err := json.Unmarshal(body, &reply); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	h.store.GetOrCreateConversation(reply.ConversationID, "")
	h.store.AddMessage(types.StoredMessage{
		ConversationID: reply.ConversationID,
		Direction:      "outbound",
		Text:           reply.Text,
		ReplyTo:        reply.ReplyTo,
		Timestamp:      timeFromMillis(reply.Timestamp),
	})

	if h.onReply != nil {
		h.onReply(reply)
	}

	log.Printf("[callback] received reply for conv %s", reply.ConversationID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(types.OutboundAck{
		MessageID: generateID(),
		Timestamp: timeNowMillis(),
	})
}

func generateID() string {
	return time.Now().Format("20060102-150405.000")
}

func timeNowMillis() int64 {
	return time.Now().UnixMilli()
}

func timeFromMillis(ms int64) time.Time {
	if ms == 0 {
		return time.Now()
	}
	return time.UnixMilli(ms)
}
