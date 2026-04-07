package handler

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"math/rand/v2"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const (
	audioBinaryProtocolVersion = 2
	audioBinaryFrameHeaderSize = 16
	audioBinaryFrameTypeOpus   = 0
	audioFormatOpus            = "opus"
	audioFormatPCM16LE         = "pcm16le"
	audioFormatPCM             = "pcm"
	audioSTTModeOffline        = "offline"
	audioSTTMode2Pass          = "2pass"
	defaultAudioSTTTimeout     = 45 * time.Second
	defaultAudioMaxBytes       = 4 << 20
)

type AudioFrameHeader struct {
	Version     uint16
	FrameType   uint16
	TimestampMs uint32
	PayloadSize uint32
}

type chatAudioSession struct {
	Format        string
	STTMode       string
	SampleRate    int
	Channels      int
	FrameDuration int
	Packets       [][]byte
	TotalBytes    int
	StartedAt     time.Time
	stream        *chatAudioStreamBridge
}

type ChatAudioRuntime struct {
	funASRURL       string
	funASROnlineURL string
	ffmpegBin       string
	timeout         time.Duration
	maxBytes        int
	dialer          websocket.Dialer
	transcribe      func(ctx context.Context, session *chatAudioSession) (string, error)
}

func NewChatAudioRuntime(funASRURL, ffmpegBin string, timeout time.Duration) *ChatAudioRuntime {
	runtime := &ChatAudioRuntime{
		funASRURL: strings.TrimSpace(funASRURL),
		ffmpegBin: strings.TrimSpace(ffmpegBin),
		timeout:   timeout,
		maxBytes:  defaultAudioMaxBytes,
		dialer:    websocket.Dialer{HandshakeTimeout: 10 * time.Second},
	}
	if runtime.ffmpegBin == "" {
		runtime.ffmpegBin = "ffmpeg"
	}
	if runtime.timeout <= 0 {
		runtime.timeout = defaultAudioSTTTimeout
	}
	return runtime
}

func (r *ChatAudioRuntime) SetOnlineURL(funASRURL string) {
	if r == nil {
		return
	}
	r.funASROnlineURL = strings.TrimSpace(funASRURL)
}

func (r *ChatAudioRuntime) Enabled() bool {
	return r != nil && (r.funASRURL != "" || r.funASROnlineURL != "")
}

func (r *ChatAudioRuntime) OfflineEnabled() bool {
	return r != nil && r.funASRURL != ""
}

func (r *ChatAudioRuntime) OnlineEnabled() bool {
	return r != nil && r.funASROnlineURL != ""
}

func (r *ChatAudioRuntime) SupportsMode(mode string) bool {
	switch normalizeAudioSTTMode(mode) {
	case audioSTTMode2Pass:
		return r.OnlineEnabled()
	default:
		return r.OfflineEnabled()
	}
}

func (r *ChatAudioRuntime) Transcribe(ctx context.Context, session *chatAudioSession) (string, error) {
	if r == nil || !r.Enabled() {
		return "", fmt.Errorf("audio STT is not configured")
	}
	if r.transcribe != nil {
		return r.transcribe(ctx, session)
	}
	mode := normalizeAudioSTTMode(session.STTMode)
	if mode == audioSTTMode2Pass {
		return "", fmt.Errorf("streaming audio should be finalized via audio stream bridge")
	}

	wavBytes, err := r.sessionToWAV(ctx, session)
	if err != nil {
		return "", err
	}
	return r.transcribeWAVWithFunASR(ctx, wavBytes)
}

func (r *ChatAudioRuntime) StartStream(ctx context.Context, session *chatAudioSession, onPartial func(text, phase string)) (*chatAudioStreamBridge, error) {
	if r == nil || !r.OnlineEnabled() {
		return nil, fmt.Errorf("audio online STT is not configured")
	}
	if session == nil {
		return nil, fmt.Errorf("audio session is nil")
	}
	return newChatAudioStreamBridge(ctx, r, session, onPartial)
}

func parseBinaryProtocol2Frame(data []byte) (AudioFrameHeader, []byte, error) {
	if len(data) < audioBinaryFrameHeaderSize {
		return AudioFrameHeader{}, nil, fmt.Errorf("binary frame too short")
	}
	header := AudioFrameHeader{
		Version:     binary.BigEndian.Uint16(data[0:2]),
		FrameType:   binary.BigEndian.Uint16(data[2:4]),
		TimestampMs: binary.BigEndian.Uint32(data[8:12]),
		PayloadSize: binary.BigEndian.Uint32(data[12:16]),
	}
	if header.Version != audioBinaryProtocolVersion {
		return AudioFrameHeader{}, nil, fmt.Errorf("unsupported binary protocol version %d", header.Version)
	}
	if header.FrameType != audioBinaryFrameTypeOpus {
		return AudioFrameHeader{}, nil, fmt.Errorf("unsupported binary frame type %d", header.FrameType)
	}
	end := audioBinaryFrameHeaderSize + int(header.PayloadSize)
	if end > len(data) {
		return AudioFrameHeader{}, nil, fmt.Errorf("binary frame payload truncated")
	}
	return header, data[audioBinaryFrameHeaderSize:end], nil
}

func normalizeAudioFormat(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", audioFormatOpus:
		return audioFormatOpus
	case audioFormatPCM, audioFormatPCM16LE, "pcm16", "s16le", "pcm_s16le":
		return audioFormatPCM16LE
	default:
		return strings.ToLower(strings.TrimSpace(format))
	}
}

func normalizeAudioSTTMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", audioSTTModeOffline:
		return audioSTTModeOffline
	case audioSTTMode2Pass, "online":
		return audioSTTMode2Pass
	default:
		return strings.ToLower(strings.TrimSpace(mode))
	}
}

func newChatAudioSession(format string, sampleRate, channels, frameDuration int, sttMode string) (*chatAudioSession, error) {
	format = normalizeAudioFormat(format)
	if format != audioFormatOpus && format != audioFormatPCM16LE {
		return nil, fmt.Errorf("unsupported audio format %q", format)
	}
	sttMode = normalizeAudioSTTMode(sttMode)
	if sttMode != audioSTTModeOffline && sttMode != audioSTTMode2Pass {
		return nil, fmt.Errorf("unsupported audio sttMode %q", sttMode)
	}
	if sttMode == audioSTTMode2Pass && format != audioFormatPCM16LE {
		return nil, fmt.Errorf("audio sttMode %q requires format %q", sttMode, audioFormatPCM16LE)
	}
	if sampleRate <= 0 {
		return nil, fmt.Errorf("invalid sampleRate")
	}
	if channels <= 0 {
		return nil, fmt.Errorf("invalid channels")
	}
	if frameDuration <= 0 {
		return nil, fmt.Errorf("invalid frameDuration")
	}
	return &chatAudioSession{
		Format:        format,
		STTMode:       sttMode,
		SampleRate:    sampleRate,
		Channels:      channels,
		FrameDuration: frameDuration,
		Packets:       make([][]byte, 0, 128),
		StartedAt:     time.Now(),
	}, nil
}

func (s *chatAudioSession) AppendPacket(packet []byte, maxBytes int) error {
	if len(packet) == 0 {
		return fmt.Errorf("empty audio packet")
	}
	if s.Format == audioFormatPCM16LE && len(packet)%2 != 0 {
		return fmt.Errorf("pcm payload must align to 16-bit samples")
	}
	nextTotal := s.TotalBytes + len(packet)
	if maxBytes > 0 && nextTotal > maxBytes {
		return fmt.Errorf("audio payload too large")
	}
	copied := make([]byte, len(packet))
	copy(copied, packet)
	s.Packets = append(s.Packets, copied)
	s.TotalBytes = nextTotal
	return nil
}

func (r *ChatAudioRuntime) sessionToWAV(ctx context.Context, session *chatAudioSession) ([]byte, error) {
	switch session.Format {
	case audioFormatPCM16LE:
		return encodePCMChunksToWAV(session.Packets, session.SampleRate, session.Channels)
	case audioFormatOpus:
		return r.decodeOpusPacketsToWAV(ctx, session)
	default:
		return nil, fmt.Errorf("unsupported audio format %q", session.Format)
	}
}

func encodePCMChunksToWAV(chunks [][]byte, sampleRate, channels int) ([]byte, error) {
	if len(chunks) == 0 {
		return nil, fmt.Errorf("audio session is empty")
	}
	if sampleRate <= 0 {
		return nil, fmt.Errorf("invalid sampleRate")
	}
	if channels <= 0 {
		return nil, fmt.Errorf("invalid channels")
	}
	dataSize := 0
	for _, chunk := range chunks {
		if len(chunk)%2 != 0 {
			return nil, fmt.Errorf("pcm payload must align to 16-bit samples")
		}
		dataSize += len(chunk)
	}

	var out bytes.Buffer
	header := make([]byte, 44)
	copy(header[0:4], []byte("RIFF"))
	binary.LittleEndian.PutUint32(header[4:8], uint32(36+dataSize))
	copy(header[8:12], []byte("WAVE"))
	copy(header[12:16], []byte("fmt "))
	binary.LittleEndian.PutUint32(header[16:20], 16)
	binary.LittleEndian.PutUint16(header[20:22], 1)
	binary.LittleEndian.PutUint16(header[22:24], uint16(channels))
	binary.LittleEndian.PutUint32(header[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(header[28:32], uint32(sampleRate*channels*2))
	binary.LittleEndian.PutUint16(header[32:34], uint16(channels*2))
	binary.LittleEndian.PutUint16(header[34:36], 16)
	copy(header[36:40], []byte("data"))
	binary.LittleEndian.PutUint32(header[40:44], uint32(dataSize))
	out.Write(header)
	for _, chunk := range chunks {
		out.Write(chunk)
	}
	return out.Bytes(), nil
}

func (r *ChatAudioRuntime) decodeOpusPacketsToWAV(ctx context.Context, session *chatAudioSession) ([]byte, error) {
	if len(session.Packets) == 0 {
		return nil, fmt.Errorf("audio session is empty")
	}

	oggBytes, err := encodeOpusPacketsAsOgg(session.Packets, session.SampleRate, session.Channels, session.FrameDuration)
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, r.ffmpegBin,
		"-hide_banner",
		"-loglevel", "error",
		"-f", "ogg",
		"-i", "pipe:0",
		"-ar", fmt.Sprintf("%d", session.SampleRate),
		"-ac", fmt.Sprintf("%d", session.Channels),
		"-f", "wav",
		"pipe:1",
	)
	cmd.Stdin = bytes.NewReader(oggBytes)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errText := strings.TrimSpace(stderr.String())
		if errText == "" {
			errText = err.Error()
		}
		return nil, fmt.Errorf("ffmpeg decode failed: %s", errText)
	}
	return stdout.Bytes(), nil
}

func (r *ChatAudioRuntime) transcribeWAVWithFunASR(ctx context.Context, wavBytes []byte) (string, error) {
	if len(wavBytes) == 0 {
		return "", fmt.Errorf("wav payload is empty")
	}
	if _, err := url.Parse(r.funASRURL); err != nil {
		return "", fmt.Errorf("invalid FunASR URL: %w", err)
	}

	conn, _, err := r.dialer.DialContext(ctx, r.funASRURL, nil)
	if err != nil {
		return "", fmt.Errorf("connect FunASR: %w", err)
	}
	defer conn.Close()

	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	startPayload := map[string]any{
		"mode":        "offline",
		"wav_name":    "audio",
		"wav_format":  "wav",
		"is_speaking": true,
		"hotwords":    "",
		"itn":         true,
	}
	if err := conn.WriteJSON(startPayload); err != nil {
		return "", fmt.Errorf("send FunASR start payload: %w", err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, wavBytes); err != nil {
		return "", fmt.Errorf("send wav payload: %w", err)
	}
	if err := conn.WriteJSON(map[string]any{"is_speaking": false}); err != nil {
		return "", fmt.Errorf("send FunASR stop payload: %w", err)
	}

	deadline := time.Now().Add(r.timeout)
	_ = conn.SetReadDeadline(deadline)
	var transcript string
	for {
		msgType, data, err := conn.ReadMessage()
		if err != nil {
			if transcript != "" {
				return transcript, nil
			}
			return "", fmt.Errorf("read FunASR response: %w", err)
		}
		if msgType != websocket.TextMessage && msgType != websocket.BinaryMessage {
			continue
		}
		var resp struct {
			Mode    string `json:"mode"`
			Text    string `json:"text"`
			IsFinal bool   `json:"is_final"`
		}
		if err := json.Unmarshal(data, &resp); err != nil {
			continue
		}
		text := strings.TrimSpace(resp.Text)
		if text == "" {
			continue
		}
		transcript = text
		// Offline mode often returns a transcript and keeps the websocket open.
		// Return as soon as we have text instead of waiting for the read timeout.
		if resp.IsFinal || strings.EqualFold(resp.Mode, "offline") {
			return transcript, nil
		}
	}
}

func encodeOpusPacketsAsOgg(packets [][]byte, sampleRate, channels, frameDuration int) ([]byte, error) {
	if len(packets) == 0 {
		return nil, fmt.Errorf("no opus packets")
	}
	if channels <= 0 || channels > 255 {
		return nil, fmt.Errorf("invalid channel count")
	}

	serial := rand.Uint32()
	sequence := uint32(0)
	granule := uint64(0)
	samplesPerPacket := uint64(sampleRate * frameDuration / 1000)

	var out bytes.Buffer

	head := make([]byte, 19)
	copy(head[0:8], []byte("OpusHead"))
	head[8] = 1
	head[9] = byte(channels)
	binary.LittleEndian.PutUint16(head[10:12], 312)
	binary.LittleEndian.PutUint32(head[12:16], uint32(sampleRate))
	binary.LittleEndian.PutUint16(head[16:18], 0)
	head[18] = 0

	writeOggPage(&out, serial, sequence, 0, 0x02, [][]byte{head})
	sequence++

	tags := append([]byte("OpusTags"), make([]byte, 8)...)
	writeOggPage(&out, serial, sequence, 0, 0x00, [][]byte{tags})
	sequence++

	for i, packet := range packets {
		granule += samplesPerPacket
		headerType := byte(0)
		if i == len(packets)-1 {
			headerType = 0x04
		}
		writeOggPage(&out, serial, sequence, granule, headerType, [][]byte{packet})
		sequence++
	}

	return out.Bytes(), nil
}

func writeOggPage(out *bytes.Buffer, serial, sequence uint32, granule uint64, headerType byte, packets [][]byte) {
	segments := make([]byte, 0, 64)
	payload := make([]byte, 0, 1024)
	for _, packet := range packets {
		remaining := len(packet)
		offset := 0
		for remaining >= 255 {
			segments = append(segments, 255)
			payload = append(payload, packet[offset:offset+255]...)
			offset += 255
			remaining -= 255
		}
		segments = append(segments, byte(remaining))
		payload = append(payload, packet[offset:]...)
	}

	header := make([]byte, 27+len(segments))
	copy(header[0:4], []byte("OggS"))
	header[4] = 0
	header[5] = headerType
	binary.LittleEndian.PutUint64(header[6:14], granule)
	binary.LittleEndian.PutUint32(header[14:18], serial)
	binary.LittleEndian.PutUint32(header[18:22], sequence)
	binary.LittleEndian.PutUint32(header[22:26], 0)
	header[26] = byte(len(segments))
	copy(header[27:], segments)

	page := append(header, payload...)
	crc := oggCRC(page)
	binary.LittleEndian.PutUint32(page[22:26], crc)
	out.Write(page)
}

func oggCRC(page []byte) uint32 {
	var crc uint32
	for _, b := range page {
		crc ^= uint32(b) << 24
		for i := 0; i < 8; i++ {
			if crc&0x80000000 != 0 {
				crc = (crc << 1) ^ 0x04c11db7
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

func writeConnJSON(cws *ChatWS, channelID string, conn *websocket.Conn, payload any) {
	if cws == nil || conn == nil {
		return
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	if err := cws.SendToConnection(channelID, conn, data); err != nil {
		log.Printf("[chat-ws] write conn json error: %v", err)
	}
}
