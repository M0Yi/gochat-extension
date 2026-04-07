package handler

import (
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestParseBinaryProtocol2Frame(t *testing.T) {
	t.Parallel()

	payload := []byte{0x11, 0x22, 0x33}
	frame := make([]byte, 16+len(payload))
	binary.BigEndian.PutUint16(frame[0:2], 2)
	binary.BigEndian.PutUint16(frame[2:4], 0)
	binary.BigEndian.PutUint32(frame[8:12], 1234)
	binary.BigEndian.PutUint32(frame[12:16], uint32(len(payload)))
	copy(frame[16:], payload)

	header, parsed, err := parseBinaryProtocol2Frame(frame)
	if err != nil {
		t.Fatalf("parse frame: %v", err)
	}
	if header.Version != 2 {
		t.Fatalf("expected version 2, got %d", header.Version)
	}
	if header.FrameType != 0 {
		t.Fatalf("expected frame type 0, got %d", header.FrameType)
	}
	if string(parsed) != string(payload) {
		t.Fatalf("expected payload %v, got %v", payload, parsed)
	}
}

func TestEncodeOpusPacketsAsOgg(t *testing.T) {
	t.Parallel()

	ogg, err := encodeOpusPacketsAsOgg([][]byte{
		{0x01, 0x02, 0x03},
		{0x04, 0x05},
	}, 16000, 1, 20)
	if err != nil {
		t.Fatalf("encode ogg: %v", err)
	}
	if len(ogg) == 0 {
		t.Fatalf("expected non-empty ogg output")
	}
	if string(ogg[:4]) != "OggS" {
		t.Fatalf("expected OggS header, got %q", string(ogg[:4]))
	}
}

func TestTranscribeWAVWithFunASRReturnsImmediatelyAfterOfflineTranscript(t *testing.T) {
	t.Parallel()

	upgrader := websocket.Upgrader{CheckOrigin: func(_ *http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()

		if _, _, err := conn.ReadMessage(); err != nil {
			t.Errorf("read start payload: %v", err)
			return
		}
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Errorf("read wav payload: %v", err)
			return
		}
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Errorf("read stop payload: %v", err)
			return
		}

		if err := conn.WriteJSON(map[string]any{
			"mode":     "offline",
			"text":     "欢迎大家来体验",
			"is_final": false,
		}); err != nil {
			t.Errorf("write transcript: %v", err)
			return
		}

		time.Sleep(750 * time.Millisecond)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	runtime := NewChatAudioRuntime(wsURL, "ffmpeg", 2*time.Second)

	start := time.Now()
	got, err := runtime.transcribeWAVWithFunASR(t.Context(), []byte("RIFFfakewav"))
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("transcribe: %v", err)
	}
	if got != "欢迎大家来体验" {
		t.Fatalf("expected transcript, got %q", got)
	}
	if elapsed >= 500*time.Millisecond {
		t.Fatalf("expected immediate return after transcript, took %s", elapsed)
	}
}

func TestNewChatAudioSessionRejects2PassWithoutPCM16LE(t *testing.T) {
	t.Parallel()

	_, err := newChatAudioSession("opus", 16000, 1, 20, "2pass")
	if err == nil {
		t.Fatal("expected 2pass session with opus to fail")
	}
	if !strings.Contains(err.Error(), "requires format") {
		t.Fatalf("expected format error, got %v", err)
	}
}

func TestEncodePCMChunksToWAV(t *testing.T) {
	t.Parallel()

	wavBytes, err := encodePCMChunksToWAV([][]byte{
		{0x00, 0x00, 0xff, 0x7f},
		{0x00, 0x80, 0x34, 0x12},
	}, 16000, 1)
	if err != nil {
		t.Fatalf("encode wav: %v", err)
	}
	if len(wavBytes) < 44 {
		t.Fatalf("expected wav header, got %d bytes", len(wavBytes))
	}
	if string(wavBytes[:4]) != "RIFF" {
		t.Fatalf("expected RIFF header, got %q", string(wavBytes[:4]))
	}
	if string(wavBytes[8:12]) != "WAVE" {
		t.Fatalf("expected WAVE header, got %q", string(wavBytes[8:12]))
	}
}

func TestMergeStreamingText(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		base string
		next string
		want string
	}{
		{name: "append delta", base: "欢迎", next: "大家", want: "欢迎大家"},
		{name: "dedupe suffix", base: "欢迎大家", next: "大家", want: "欢迎大家"},
		{name: "replace prefix", base: "欢迎", next: "欢迎大家", want: "欢迎大家"},
		{name: "overlap", base: "欢迎大家", next: "大家来体验", want: "欢迎大家来体验"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := mergeStreamingText(tc.base, tc.next); got != tc.want {
				t.Fatalf("mergeStreamingText(%q, %q) = %q, want %q", tc.base, tc.next, got, tc.want)
			}
		})
	}
}

func TestChatAudioStreamBridgeReturns2PassTranscript(t *testing.T) {
	t.Parallel()

	upgrader := websocket.Upgrader{CheckOrigin: func(_ *http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()

		var startPayload map[string]any
		if err := conn.ReadJSON(&startPayload); err != nil {
			t.Errorf("read start payload: %v", err)
			return
		}
		if got, _ := startPayload["mode"].(string); got != "2pass" {
			t.Errorf("expected 2pass mode, got %q", got)
			return
		}
		if got, _ := startPayload["wav_format"].(string); got != "pcm" {
			t.Errorf("expected pcm wav_format, got %q", got)
			return
		}

		if _, payload, err := conn.ReadMessage(); err != nil {
			t.Errorf("read first pcm frame: %v", err)
			return
		} else if len(payload) == 0 {
			t.Error("expected first pcm payload")
			return
		}
		if err := conn.WriteJSON(map[string]any{"mode": "2pass-online", "text": "欢迎"}); err != nil {
			t.Errorf("write first partial: %v", err)
			return
		}

		if _, payload, err := conn.ReadMessage(); err != nil {
			t.Errorf("read second pcm frame: %v", err)
			return
		} else if len(payload) == 0 {
			t.Error("expected second pcm payload")
			return
		}
		if err := conn.WriteJSON(map[string]any{"mode": "2pass-online", "text": "大家"}); err != nil {
			t.Errorf("write second partial: %v", err)
			return
		}

		var stopPayload map[string]any
		if err := conn.ReadJSON(&stopPayload); err != nil {
			t.Errorf("read stop payload: %v", err)
			return
		}
		if speaking, ok := stopPayload["is_speaking"].(bool); !ok || speaking {
			t.Errorf("expected stop payload with is_speaking=false, got %#v", stopPayload["is_speaking"])
			return
		}

		time.Sleep(120 * time.Millisecond)
		if err := conn.WriteJSON(map[string]any{"mode": "2pass-offline", "text": "欢迎大家来体验"}); err != nil {
			t.Errorf("write final transcript: %v", err)
			return
		}

		time.Sleep(150 * time.Millisecond)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	runtime := NewChatAudioRuntime("", "ffmpeg", 3*time.Second)
	runtime.SetOnlineURL(wsURL)

	session, err := newChatAudioSession("pcm16le", 16000, 1, 60, "2pass")
	if err != nil {
		t.Fatalf("newChatAudioSession: %v", err)
	}

	var (
		mu       sync.Mutex
		partials []string
	)
	stream, err := runtime.StartStream(t.Context(), session, func(text, _ string) {
		mu.Lock()
		partials = append(partials, text)
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	defer stream.Abort()

	if err := stream.AppendAudio([]byte{0x00, 0x00, 0x01, 0x00}); err != nil {
		t.Fatalf("AppendAudio first: %v", err)
	}
	if err := stream.AppendAudio([]byte{0x02, 0x00, 0x03, 0x00}); err != nil {
		t.Fatalf("AppendAudio second: %v", err)
	}

	got, err := stream.Finish(t.Context())
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if got != "欢迎大家来体验" {
		t.Fatalf("expected final transcript, got %q", got)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(partials) < 2 {
		t.Fatalf("expected partial updates, got %v", partials)
	}
	if partials[len(partials)-1] != "欢迎大家来体验" {
		t.Fatalf("expected final preview to match transcript, got %v", partials)
	}
}
