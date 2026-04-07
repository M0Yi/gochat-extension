package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
)

const (
	audioBinaryProtocolVersion = 2
	audioBinaryFrameTypeAudio  = 0
	audioBinaryFrameHeaderSize = 16
)

type chatClaims struct {
	ChannelID string `json:"channelId"`
	UserID    string `json:"userId"`
	jwt.RegisteredClaims
}

func main() {
	var (
		serverURL   = flag.String("server", "ws://127.0.0.1:9751/ws/chat", "GoChat chat websocket URL without token")
		channelID   = flag.String("channel", "", "GoChat channel id")
		userID      = flag.String("user", "audio-smoke", "User id embedded in JWT")
		jwtSecret   = flag.String("jwt-secret", "gochat-admin-secret-change-me", "JWT signing secret")
		audioPath   = flag.String("audio", "", "Path to audio file")
		mode        = flag.String("mode", "offline", "Transcription mode: offline or 2pass")
		sampleRate  = flag.Int("sample-rate", 16000, "Audio sample rate for raw PCM input")
		channels    = flag.Int("channels", 1, "Audio channel count for raw PCM input")
		frameMs     = flag.Int("frame-ms", 20, "Opus frame duration in milliseconds for offline mode")
		chunkMs     = flag.Int("chunk-ms", 60, "PCM chunk duration in milliseconds for 2pass mode")
		readTimeout = flag.Duration("read-timeout", 2*time.Minute, "Maximum time to wait for STT result")
	)
	flag.Parse()

	if *channelID == "" {
		log.Fatal("-channel is required")
	}
	if *audioPath == "" {
		log.Fatal("-audio is required")
	}

	token, err := issueToken(*channelID, *userID, *jwtSecret)
	if err != nil {
		log.Fatalf("issue token: %v", err)
	}

	wsURL, err := withToken(*serverURL, token)
	if err != nil {
		log.Fatalf("build ws url: %v", err)
	}

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		log.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(map[string]any{
		"type":          "hello",
		"clientType":    "audio-smoke",
		"clientVersion": "2.0.0",
	}); err != nil {
		log.Fatalf("send hello: %v", err)
	}

	switch strings.ToLower(strings.TrimSpace(*mode)) {
	case "", "offline":
		if err := runOfflineSmoke(conn, *audioPath, *sampleRate, *channels, *frameMs, *readTimeout); err != nil {
			log.Fatal(err)
		}
	case "2pass", "online":
		if err := run2PassSmoke(conn, *audioPath, *sampleRate, *channels, *chunkMs, *readTimeout); err != nil {
			log.Fatal(err)
		}
	default:
		log.Fatalf("unsupported mode %q", *mode)
	}
}

func runOfflineSmoke(conn *websocket.Conn, audioPath string, sampleRate, channels, frameMs int, readTimeout time.Duration) error {
	packets, err := extractOpusPackets(audioPath)
	if err != nil {
		return fmt.Errorf("extract opus packets: %w", err)
	}
	if len(packets) == 0 {
		return fmt.Errorf("no opus packets found in %s", audioPath)
	}

	if err := conn.WriteJSON(map[string]any{
		"type":          "audio.start",
		"format":        "opus",
		"sttMode":       "offline",
		"sampleRate":    sampleRate,
		"channels":      channels,
		"frameDuration": frameMs,
	}); err != nil {
		return fmt.Errorf("send audio.start: %w", err)
	}

	for i, packet := range packets {
		frame := buildAudioFrame(uint32(i*frameMs), packet)
		if err := conn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
			return fmt.Errorf("send audio frame %d: %w", i, err)
		}
	}

	if err := conn.WriteJSON(map[string]any{"type": "audio.stop"}); err != nil {
		return fmt.Errorf("send audio.stop: %w", err)
	}

	text, err := readUntilSTT(conn, readTimeout)
	if err != nil {
		return err
	}
	fmt.Println(text)
	return nil
}

func run2PassSmoke(conn *websocket.Conn, audioPath string, defaultSampleRate, defaultChannels, chunkMs int, readTimeout time.Duration) error {
	pcmBytes, sampleRate, channels, err := extractPCMInput(audioPath, defaultSampleRate, defaultChannels)
	if err != nil {
		return fmt.Errorf("extract pcm input: %w", err)
	}
	if len(pcmBytes) == 0 {
		return fmt.Errorf("pcm payload is empty")
	}
	if chunkMs <= 0 {
		return fmt.Errorf("invalid -chunk-ms value")
	}

	if err := conn.WriteJSON(map[string]any{
		"type":          "audio.start",
		"format":        "pcm16le",
		"sttMode":       "2pass",
		"sampleRate":    sampleRate,
		"channels":      channels,
		"frameDuration": chunkMs,
	}); err != nil {
		return fmt.Errorf("send audio.start: %w", err)
	}

	bytesPerChunk := sampleRate * channels * 2 * chunkMs / 1000
	if bytesPerChunk <= 0 {
		return fmt.Errorf("invalid pcm chunk size")
	}

	timestampMs := 0
	for offset := 0; offset < len(pcmBytes); offset += bytesPerChunk {
		end := offset + bytesPerChunk
		if end > len(pcmBytes) {
			end = len(pcmBytes)
		}
		frame := buildAudioFrame(uint32(timestampMs), pcmBytes[offset:end])
		if err := conn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
			return fmt.Errorf("send pcm chunk at offset %d: %w", offset, err)
		}
		timestampMs += chunkMs
	}

	if err := conn.WriteJSON(map[string]any{"type": "audio.stop"}); err != nil {
		return fmt.Errorf("send audio.stop: %w", err)
	}

	text, err := readUntilSTT(conn, readTimeout)
	if err != nil {
		return err
	}
	fmt.Println(text)
	return nil
}

func readUntilSTT(conn *websocket.Conn, timeout time.Duration) (string, error) {
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return "", fmt.Errorf("read websocket: %w", err)
		}

		var payload map[string]any
		if err := json.Unmarshal(data, &payload); err != nil {
			log.Printf("non-json message: %s", string(data))
			continue
		}

		typ, _ := payload["type"].(string)
		switch typ {
		case "stt.partial":
			text, _ := payload["text"].(string)
			if text != "" {
				log.Printf("partial: %s", text)
			}
		case "stt":
			text, _ := payload["text"].(string)
			return text, nil
		case "error":
			text, _ := payload["text"].(string)
			return "", fmt.Errorf("server error: %s", text)
		default:
			log.Printf("message: %s", string(data))
		}
	}
}

func issueToken(channelID, userID, jwtSecret string) (string, error) {
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, chatClaims{
		ChannelID: channelID,
		UserID:    userID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	})
	return token.SignedString([]byte(jwtSecret))
}

func withToken(rawURL, token string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set("token", token)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func buildAudioFrame(timestampMs uint32, payload []byte) []byte {
	frame := make([]byte, audioBinaryFrameHeaderSize+len(payload))
	binary.BigEndian.PutUint16(frame[0:2], audioBinaryProtocolVersion)
	binary.BigEndian.PutUint16(frame[2:4], audioBinaryFrameTypeAudio)
	binary.BigEndian.PutUint32(frame[8:12], timestampMs)
	binary.BigEndian.PutUint32(frame[12:16], uint32(len(payload)))
	copy(frame[audioBinaryFrameHeaderSize:], payload)
	return frame
}

func extractPCMInput(path string, defaultSampleRate, defaultChannels int) ([]byte, int, int, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".pcm":
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, 0, 0, err
		}
		return data, defaultSampleRate, defaultChannels, nil
	case ".wav":
		return extractPCMFromWAV(path)
	default:
		return nil, 0, 0, fmt.Errorf("2pass mode expects .wav or .pcm input, got %s", path)
	}
}

func extractPCMFromWAV(path string) ([]byte, int, int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, 0, err
	}
	if len(data) < 12 || !bytes.Equal(data[:4], []byte("RIFF")) || !bytes.Equal(data[8:12], []byte("WAVE")) {
		return nil, 0, 0, fmt.Errorf("invalid wav header")
	}

	var (
		audioFormat   uint16
		channels      uint16
		bitsPerSample uint16
		sampleRate    uint32
		pcmData       []byte
	)

	for offset := 12; offset+8 <= len(data); {
		chunkID := string(data[offset : offset+4])
		chunkSize := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		chunkStart := offset + 8
		chunkEnd := chunkStart + chunkSize
		if chunkEnd > len(data) {
			return nil, 0, 0, fmt.Errorf("truncated wav chunk %q", chunkID)
		}

		switch chunkID {
		case "fmt ":
			if chunkSize < 16 {
				return nil, 0, 0, fmt.Errorf("wav fmt chunk too short")
			}
			audioFormat = binary.LittleEndian.Uint16(data[chunkStart : chunkStart+2])
			channels = binary.LittleEndian.Uint16(data[chunkStart+2 : chunkStart+4])
			sampleRate = binary.LittleEndian.Uint32(data[chunkStart+4 : chunkStart+8])
			bitsPerSample = binary.LittleEndian.Uint16(data[chunkStart+14 : chunkStart+16])
		case "data":
			pcmData = make([]byte, chunkSize)
			copy(pcmData, data[chunkStart:chunkEnd])
		}

		offset = chunkEnd
		if chunkSize%2 == 1 {
			offset++
		}
	}

	if audioFormat != 1 {
		return nil, 0, 0, fmt.Errorf("unsupported wav encoding %d", audioFormat)
	}
	if bitsPerSample != 16 {
		return nil, 0, 0, fmt.Errorf("unsupported wav bit depth %d", bitsPerSample)
	}
	if len(pcmData) == 0 {
		return nil, 0, 0, fmt.Errorf("wav data chunk is empty")
	}

	return pcmData, int(sampleRate), int(channels), nil
}

func extractOpusPackets(path string) ([][]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var (
		packets [][]byte
		partial []byte
		offset  int
	)

	for offset < len(data) {
		if offset+27 > len(data) {
			return nil, fmt.Errorf("truncated ogg page header at offset %d", offset)
		}
		if !bytes.Equal(data[offset:offset+4], []byte("OggS")) {
			return nil, fmt.Errorf("invalid ogg capture pattern at offset %d", offset)
		}

		segmentCount := int(data[offset+26])
		segmentTableStart := offset + 27
		segmentTableEnd := segmentTableStart + segmentCount
		if segmentTableEnd > len(data) {
			return nil, fmt.Errorf("truncated ogg segment table at offset %d", offset)
		}

		payloadSize := 0
		for _, size := range data[segmentTableStart:segmentTableEnd] {
			payloadSize += int(size)
		}
		payloadStart := segmentTableEnd
		payloadEnd := payloadStart + payloadSize
		if payloadEnd > len(data) {
			return nil, fmt.Errorf("truncated ogg payload at offset %d", offset)
		}

		payload := data[payloadStart:payloadEnd]
		payloadOffset := 0
		for _, size := range data[segmentTableStart:segmentTableEnd] {
			end := payloadOffset + int(size)
			if end > len(payload) {
				return nil, fmt.Errorf("invalid ogg segment payload at offset %d", offset)
			}
			partial = append(partial, payload[payloadOffset:end]...)
			payloadOffset = end
			if size == 255 {
				continue
			}
			if len(partial) > 0 &&
				!bytes.HasPrefix(partial, []byte("OpusHead")) &&
				!bytes.HasPrefix(partial, []byte("OpusTags")) {
				packet := make([]byte, len(partial))
				copy(packet, partial)
				packets = append(packets, packet)
			}
			partial = partial[:0]
		}

		offset = payloadEnd
	}

	if len(partial) > 0 {
		return nil, fmt.Errorf("incomplete opus packet at end of file")
	}
	return packets, nil
}
