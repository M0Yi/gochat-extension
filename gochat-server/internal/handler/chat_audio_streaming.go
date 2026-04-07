package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	audio2PassChunkInterval   = 10
	audio2PassSettleDelay     = 350 * time.Millisecond
	audio2PassFallbackDelay   = 1500 * time.Millisecond
	audio2PassWriteTimeout    = 10 * time.Second
	audio2PassCloseTimeout    = 1 * time.Second
	audio2PassChunkLookBack   = 4
	audio2PassDecoderLookBack = 0
)

var audio2PassChunkSize = []int{5, 10, 5}

type funASRStreamResponse struct {
	Mode    string `json:"mode"`
	Text    string `json:"text"`
	IsFinal bool   `json:"is_final"`
}

type chatAudioStreamBridge struct {
	conn      *websocket.Conn
	onPartial func(text, phase string)

	writeMu   sync.Mutex
	closeOnce sync.Once
	done      chan struct{}
	updateCh  chan struct{}

	mu            sync.Mutex
	closed        bool
	stopSent      bool
	readErr       error
	committedText string
	onlineText    string
	lastPreview   string
	lastUpdate    time.Time
}

func newChatAudioStreamBridge(ctx context.Context, runtime *ChatAudioRuntime, session *chatAudioSession, onPartial func(text, phase string)) (*chatAudioStreamBridge, error) {
	if runtime == nil || !runtime.OnlineEnabled() {
		return nil, fmt.Errorf("audio online STT is not configured")
	}
	if session == nil {
		return nil, fmt.Errorf("audio session is nil")
	}
	if normalizeAudioSTTMode(session.STTMode) != audioSTTMode2Pass {
		return nil, fmt.Errorf("audio session is not in 2pass mode")
	}
	if normalizeAudioFormat(session.Format) != audioFormatPCM16LE {
		return nil, fmt.Errorf("audio stream bridge requires format %q", audioFormatPCM16LE)
	}
	if _, err := url.Parse(runtime.funASROnlineURL); err != nil {
		return nil, fmt.Errorf("invalid FunASR online URL: %w", err)
	}

	conn, _, err := runtime.dialer.DialContext(ctx, runtime.funASROnlineURL, nil)
	if err != nil {
		return nil, fmt.Errorf("connect FunASR online: %w", err)
	}

	bridge := &chatAudioStreamBridge{
		conn:      conn,
		onPartial: onPartial,
		done:      make(chan struct{}),
		updateCh:  make(chan struct{}, 1),
	}

	startPayload := map[string]any{
		"mode":                    audioSTTMode2Pass,
		"chunk_size":              audio2PassChunkSize,
		"chunk_interval":          audio2PassChunkInterval,
		"encoder_chunk_look_back": audio2PassChunkLookBack,
		"decoder_chunk_look_back": audio2PassDecoderLookBack,
		"audio_fs":                session.SampleRate,
		"wav_name":                "gochat-stream",
		"wav_format":              audioFormatPCM,
		"is_speaking":             true,
		"hotwords":                "",
		"itn":                     true,
	}
	if err := bridge.sendJSON(startPayload); err != nil {
		bridge.Abort()
		return nil, fmt.Errorf("send FunASR online start payload: %w", err)
	}

	go bridge.readLoop()
	return bridge, nil
}

func (b *chatAudioStreamBridge) AppendAudio(payload []byte) error {
	if len(payload) == 0 {
		return fmt.Errorf("pcm payload is empty")
	}
	if err := b.sendBinary(payload); err != nil {
		return fmt.Errorf("send pcm payload: %w", err)
	}
	return nil
}

func (b *chatAudioStreamBridge) Finish(ctx context.Context) (string, error) {
	if err := b.sendJSON(map[string]any{"is_speaking": false}); err != nil {
		return "", fmt.Errorf("send FunASR online stop payload: %w", err)
	}

	b.mu.Lock()
	b.stopSent = true
	b.mu.Unlock()
	b.signalUpdate()

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		text, onlineText, lastUpdate, closed, readErr := b.snapshot()
		text = strings.TrimSpace(text)
		idle := time.Since(lastUpdate)

		if text != "" {
			if strings.TrimSpace(onlineText) == "" && idle >= audio2PassSettleDelay {
				return text, nil
			}
			if idle >= audio2PassFallbackDelay {
				return text, nil
			}
		}

		if closed {
			if text != "" {
				return text, nil
			}
			if readErr != nil {
				return "", fmt.Errorf("read FunASR stream: %w", readErr)
			}
			return "", fmt.Errorf("FunASR stream closed without transcript")
		}

		select {
		case <-ctx.Done():
			if text != "" {
				return text, nil
			}
			return "", ctx.Err()
		case <-b.updateCh:
		case <-b.done:
		case <-ticker.C:
		}
	}
}

func (b *chatAudioStreamBridge) Abort() {
	if b == nil {
		return
	}
	b.closeOnce.Do(func() {
		b.writeMu.Lock()
		_ = b.conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(audio2PassCloseTimeout),
		)
		b.writeMu.Unlock()
		_ = b.conn.Close()
	})
}

func (b *chatAudioStreamBridge) readLoop() {
	defer func() {
		b.mu.Lock()
		b.closed = true
		b.mu.Unlock()
		b.signalUpdate()
		close(b.done)
	}()

	for {
		msgType, data, err := b.conn.ReadMessage()
		if err != nil {
			b.mu.Lock()
			b.readErr = err
			b.mu.Unlock()
			return
		}
		if msgType != websocket.TextMessage && msgType != websocket.BinaryMessage {
			continue
		}

		var resp funASRStreamResponse
		if err := json.Unmarshal(data, &resp); err != nil {
			continue
		}
		b.applyResponse(resp)
	}
}

func (b *chatAudioStreamBridge) applyResponse(resp funASRStreamResponse) {
	mode := strings.ToLower(strings.TrimSpace(resp.Mode))
	text := strings.TrimSpace(resp.Text)

	var (
		preview string
		notify  bool
	)

	b.mu.Lock()
	switch mode {
	case "2pass-online", "online":
		if text != "" {
			b.onlineText = mergeStreamingText(b.onlineText, text)
		}
	case "2pass-offline", "offline":
		b.onlineText = ""
		if text != "" {
			b.committedText = mergeStreamingText(b.committedText, text)
		}
	default:
		if text != "" {
			b.onlineText = ""
			b.committedText = mergeStreamingText(b.committedText, text)
		}
	}

	if text != "" || mode != "" {
		b.lastUpdate = time.Now()
	}

	preview = b.composePreviewLocked()
	if preview != "" && preview != b.lastPreview {
		b.lastPreview = preview
		notify = true
	}
	b.mu.Unlock()

	if notify && b.onPartial != nil {
		b.onPartial(preview, mode)
	}
	if text != "" || mode != "" {
		b.signalUpdate()
	}
}

func (b *chatAudioStreamBridge) snapshot() (text, onlineText string, lastUpdate time.Time, closed bool, readErr error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.composePreviewLocked(), b.onlineText, b.lastUpdate, b.closed, b.readErr
}

func (b *chatAudioStreamBridge) composePreviewLocked() string {
	return mergeStreamingText(b.committedText, b.onlineText)
}

func (b *chatAudioStreamBridge) signalUpdate() {
	select {
	case b.updateCh <- struct{}{}:
	default:
	}
}

func (b *chatAudioStreamBridge) sendJSON(payload any) error {
	b.writeMu.Lock()
	defer b.writeMu.Unlock()
	_ = b.conn.SetWriteDeadline(time.Now().Add(audio2PassWriteTimeout))
	return b.conn.WriteJSON(payload)
}

func (b *chatAudioStreamBridge) sendBinary(payload []byte) error {
	b.writeMu.Lock()
	defer b.writeMu.Unlock()
	_ = b.conn.SetWriteDeadline(time.Now().Add(audio2PassWriteTimeout))
	return b.conn.WriteMessage(websocket.BinaryMessage, payload)
}

func mergeStreamingText(base, next string) string {
	base = strings.TrimSpace(base)
	next = strings.TrimSpace(next)
	if base == "" {
		return next
	}
	if next == "" {
		return base
	}
	if strings.HasSuffix(base, next) {
		return base
	}
	if strings.HasPrefix(next, base) {
		return next
	}

	baseRunes := []rune(base)
	nextRunes := []rune(next)
	maxOverlap := len(baseRunes)
	if len(nextRunes) < maxOverlap {
		maxOverlap = len(nextRunes)
	}
	for overlap := maxOverlap; overlap > 0; overlap-- {
		if string(baseRunes[len(baseRunes)-overlap:]) == string(nextRunes[:overlap]) {
			return string(append(baseRunes, nextRunes[overlap:]...))
		}
	}
	return base + next
}
