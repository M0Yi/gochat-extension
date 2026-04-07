package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"time"
)

const maxTimestampSkew = 300

func SignPayload(secret, timestamp, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	payload := timestamp + "." + body
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

func VerifySignature(secret, signature, timestamp, body string) error {
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid timestamp: %w", err)
	}
	now := time.Now().Unix()
	if math.Abs(float64(now-ts)) > maxTimestampSkew {
		return fmt.Errorf("timestamp too old (skew=%ds)", now-ts)
	}
	expected := SignPayload(secret, timestamp, body)
	if !hmac.Equal([]byte(signature), []byte(expected)) {
		return fmt.Errorf("signature mismatch")
	}
	return nil
}

func SignRequest(secret, body string) (signature, timestamp string) {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	return SignPayload(secret, ts, body), ts
}
