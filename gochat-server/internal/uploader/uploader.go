package uploader

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type UploadResult struct {
	URL      string `json:"url"`
	Name     string `json:"name"`
	MimeType string `json:"mimeType"`
	Size     int64  `json:"size"`
	Type     string `json:"type"`
}

type PresignResult struct {
	UploadURL string            `json:"uploadUrl"`
	FileKey   string            `json:"fileKey"`
	Method    string            `json:"method"`
	Headers   map[string]string `json:"headers"`
	FileURL   string            `json:"fileUrl"`
	Filename  string            `json:"filename"`
}

type Uploader interface {
	Upload(ctx context.Context, name string, data io.Reader, size int64, contentType string) (*UploadResult, error)
	Presign(ctx context.Context, filename, contentType string) (*PresignResult, error)
	Confirm(ctx context.Context, fileKey string) (*UploadResult, error)
	DownloadAndReupload(ctx context.Context, remoteURL string) (*UploadResult, error)
}

type pendingUpload struct {
	Filename    string
	ContentType string
	FileKey     string
	FileURL     string
	CreatedAt   time.Time
}

var (
	pendingMu    sync.RWMutex
	pendingStore = make(map[string]*pendingUpload)
)

func StorePending(token string, p *pendingUpload) {
	pendingMu.Lock()
	pendingStore[token] = p
	pendingMu.Unlock()
	go func() {
		time.Sleep(30 * time.Minute)
		pendingMu.Lock()
		delete(pendingStore, token)
		pendingMu.Unlock()
	}()
}

func GetAndDeletePending(token string) *pendingUpload {
	pendingMu.Lock()
	defer pendingMu.Unlock()
	p := pendingStore[token]
	delete(pendingStore, token)
	return p
}

func classifyAttachmentType(filename, contentType string) string {
	ct := strings.ToLower(contentType)
	if ct == "" {
		ext := strings.ToLower(filepath.Ext(filename))
		switch ext {
		case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".svg":
			return "image"
		case ".mp3", ".wav", ".ogg", ".m4a", ".webm":
			return "audio"
		case ".mp4", ".mov", ".avi":
			return "video"
		default:
			return "file"
		}
	}
	switch {
	case strings.HasPrefix(ct, "image/"):
		return "image"
	case strings.HasPrefix(ct, "audio/"):
		return "audio"
	case strings.HasPrefix(ct, "video/"):
		return "video"
	default:
		return "file"
	}
}

type LocalUploader struct {
	uploadDir string
	publicURL string
}

func NewLocalUploader(uploadDir, publicURL string) *LocalUploader {
	return &LocalUploader{uploadDir: uploadDir, publicURL: publicURL}
}

func (l *LocalUploader) Upload(_ context.Context, name string, data io.Reader, size int64, contentType string) (*UploadResult, error) {
	buf := new(bytes.Buffer)
	if _, err := io.Copy(buf, data); err != nil {
		return nil, fmt.Errorf("read data: %w", err)
	}
	content := buf.Bytes()
	hash := sha256.Sum256(content)
	ext := filepath.Ext(name)
	filename := hex.EncodeToString(hash[:])[:16] + ext

	if err := os.MkdirAll(l.uploadDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir: %w", err)
	}
	destPath := filepath.Join(l.uploadDir, filename)
	if err := os.WriteFile(destPath, content, 0o644); err != nil {
		return nil, fmt.Errorf("write file: %w", err)
	}

	fileURL := strings.TrimRight(l.publicURL, "/") + "/files/" + filename
	return &UploadResult{
		URL:      fileURL,
		Name:     filename,
		MimeType: contentType,
		Type:     classifyAttachmentType(filename, contentType),
		Size:     int64(len(content)),
	}, nil
}

func (l *LocalUploader) Presign(_ context.Context, filename, contentType string) (*PresignResult, error) {
	ext := filepath.Ext(filename)
	now := time.Now()
	fileKey := fmt.Sprintf("%d%s", now.UnixNano(), ext)
	hash := sha256.Sum256([]byte(fileKey))
	token := hex.EncodeToString(hash[:])[:24]

	destName := token + ext
	fileURL := strings.TrimRight(l.publicURL, "/") + "/files/" + destName
	uploadURL := "/api/upload/put/" + token

	StorePending(token, &pendingUpload{
		Filename:    filename,
		ContentType: contentType,
		FileKey:     destName,
		FileURL:     fileURL,
		CreatedAt:   now,
	})

	return &PresignResult{
		UploadURL: uploadURL,
		FileKey:   token,
		Method:    "PUT",
		Headers: map[string]string{
			"Content-Type": contentType,
		},
		FileURL:  fileURL,
		Filename: filename,
	}, nil
}

func (l *LocalUploader) Confirm(_ context.Context, token string) (*UploadResult, error) {
	p := GetAndDeletePending(token)
	if p == nil {
		return nil, fmt.Errorf("upload token expired or invalid")
	}
	destPath := filepath.Join(l.uploadDir, p.FileKey)
	if _, err := os.Stat(destPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("file not uploaded yet")
	}
	return &UploadResult{
		URL:      p.FileURL,
		Name:     p.FileKey,
		MimeType: p.ContentType,
		Type:     classifyAttachmentType(p.Filename, p.ContentType),
	}, nil
}

func (l *LocalUploader) HandlePut(token string, data io.Reader, maxBytes int64) error {
	p := GetAndDeletePending(token)
	if p == nil {
		return fmt.Errorf("upload token expired or invalid")
	}
	if err := os.MkdirAll(l.uploadDir, 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	destPath := filepath.Join(l.uploadDir, p.FileKey)
	out, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer out.Close()

	reader := data
	if maxBytes > 0 {
		reader = io.LimitReader(data, maxBytes+1)
	}

	written, err := io.Copy(out, reader)
	if err != nil {
		os.Remove(destPath)
		return fmt.Errorf("write file: %w", err)
	}
	if maxBytes > 0 && written > maxBytes {
		os.Remove(destPath)
		return fmt.Errorf("upload exceeds max size of %d bytes", maxBytes)
	}
	StorePending(token, p)
	return nil
}

func (l *LocalUploader) DownloadAndReupload(ctx context.Context, remoteURL string) (*UploadResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, remoteURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download failed with status: %d", resp.StatusCode)
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	u, err := url.Parse(remoteURL)
	if err != nil {
		return nil, fmt.Errorf("parse URL: %w", err)
	}
	filename := filepath.Base(u.Path)
	if filename == "" || filename == "." {
		filename = "attachment"
	}

	return l.Upload(ctx, filename, resp.Body, resp.ContentLength, contentType)
}

type S3Uploader struct {
	endpoint  string
	region    string
	accessKey string
	secretKey string
	bucket    string
	publicURL string
	forcePath bool
}

func NewS3Uploader(endpoint, region, accessKey, secretKey, bucket, publicURL string, forcePath bool) *S3Uploader {
	return &S3Uploader{
		endpoint:  endpoint,
		region:    region,
		accessKey: accessKey,
		secretKey: secretKey,
		bucket:    bucket,
		publicURL: publicURL,
		forcePath: forcePath,
	}
}

func (s *S3Uploader) buildS3URL(key string) string {
	host := s.endpoint
	if host == "" {
		host = fmt.Sprintf("s3.%s.amazonaws.com", s.region)
	}
	host = strings.TrimPrefix(strings.TrimPrefix(host, "https://"), "http://")
	if s.forcePath {
		return fmt.Sprintf("https://%s/%s/%s", host, s.bucket, key)
	}
	return fmt.Sprintf("https://%s.%s/%s", s.bucket, host, key)
}

func (s *S3Uploader) buildPublicURL(key string) string {
	if s.publicURL != "" {
		return strings.TrimRight(s.publicURL, "/") + "/" + key
	}
	return s.buildS3URL(key)
}

func (s *S3Uploader) Upload(ctx context.Context, name string, data io.Reader, size int64, contentType string) (*UploadResult, error) {
	buf, err := io.ReadAll(data)
	if err != nil {
		return nil, fmt.Errorf("read data: %w", err)
	}
	now := time.Now().UTC()
	key := fmt.Sprintf("gochat/%s/%s", now.Format("2006/01/02"), name)
	reqURL := s.buildS3URL(key)

	req, err := http.NewRequestWithContext(ctx, "PUT", reqURL, bytes.NewReader(buf))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Host", req.URL.Host)
	req.Header.Set("X-Amz-Content-Sha256", hex.EncodeToString(sha256Sum(buf)))
	signV4(req, s.accessKey, s.secretKey, s.region, "s3", now, buf)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("s3 put: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("s3 put failed (%d): %s", resp.StatusCode, string(body))
	}

	return &UploadResult{
		URL:      s.buildPublicURL(key),
		Name:     name,
		MimeType: contentType,
		Type:     classifyAttachmentType(name, contentType),
		Size:     size,
	}, nil
}

func (s *S3Uploader) Presign(_ context.Context, filename, contentType string) (*PresignResult, error) {
	now := time.Now().UTC()
	key := fmt.Sprintf("gochat/%s/%s", now.Format("2006/01/02"), filename)
	reqURL := s.buildS3URL(key)

	parsedURL, _ := url.Parse(reqURL)
	canonicalURI := awsEncodePath(parsedURL.Path)
	host := parsedURL.Host

	expires := 3600
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")
	credentialScope := fmt.Sprintf("%s/%s/s3/aws4_request", dateStamp, s.region)
	unsignedPayload := "UNSIGNED-PAYLOAD"

	signedHeaders := "content-type;host;x-amz-content-sha256"
	canonicalHeaders := fmt.Sprintf("content-type:%s\nhost:%s\nx-amz-content-sha256:%s\n", contentType, host, unsignedPayload)
	canonicalQueryString := strings.Join([]string{
		"X-Amz-Algorithm=" + awsPercentEncode("AWS4-HMAC-SHA256"),
		"X-Amz-Credential=" + awsPercentEncode(s.accessKey+"/"+credentialScope),
		"X-Amz-Date=" + awsPercentEncode(amzDate),
		"X-Amz-Expires=" + awsPercentEncode(fmt.Sprintf("%d", expires)),
		"X-Amz-SignedHeaders=" + awsPercentEncode(signedHeaders),
	}, "&")

	canonicalRequest := fmt.Sprintf("PUT\n%s\n%s\n%s\n%s\nUNSIGNED-PAYLOAD",
		canonicalURI, canonicalQueryString, canonicalHeaders, signedHeaders)

	stringToSign := fmt.Sprintf("AWS4-HMAC-SHA256\n%s\n%s\n%s",
		amzDate, credentialScope, hex.EncodeToString(sha256Sum([]byte(canonicalRequest))))

	signingKey := hmacChain([]byte("AWS4"+s.secretKey), dateStamp, s.region, "s3", "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))

	realQueryString := canonicalQueryString + "&X-Amz-Signature=" + awsPercentEncode(signature)

	presignedURL := fmt.Sprintf("%s?%s", reqURL, realQueryString)

	return &PresignResult{
		UploadURL: presignedURL,
		FileKey:   key,
		Method:    "PUT",
		Headers: map[string]string{
			"Content-Type":         contentType,
			"X-Amz-Content-Sha256": unsignedPayload,
		},
		FileURL:  s.buildPublicURL(key),
		Filename: filename,
	}, nil
}

func (s *S3Uploader) Confirm(_ context.Context, fileKey string) (*UploadResult, error) {
	parts := strings.Split(fileKey, "/")
	name := parts[len(parts)-1]
	return &UploadResult{
		URL:  s.buildPublicURL(fileKey),
		Name: name,
		Type: classifyAttachmentType(name, ""),
	}, nil
}

func (s *S3Uploader) DownloadAndReupload(ctx context.Context, remoteURL string) (*UploadResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, remoteURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download failed with status: %d", resp.StatusCode)
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	u, err := url.Parse(remoteURL)
	if err != nil {
		return nil, fmt.Errorf("parse URL: %w", err)
	}
	filename := filepath.Base(u.Path)
	if filename == "" || filename == "." {
		filename = "attachment"
	}

	return s.Upload(ctx, filename, resp.Body, resp.ContentLength, contentType)
}

func (s *S3Uploader) TestConnection(ctx context.Context) (string, string, error) {
	key := fmt.Sprintf("gochat/_healthcheck/%d.txt", time.Now().UTC().UnixNano())
	reqURL := s.buildS3URL(key)
	publicURL := s.buildPublicURL(key)
	body := []byte("gochat s3 healthcheck")

	putReq, err := http.NewRequestWithContext(ctx, http.MethodPut, reqURL, bytes.NewReader(body))
	if err != nil {
		return "", "", fmt.Errorf("create put request: %w", err)
	}
	putReq.Header.Set("Content-Type", "text/plain")
	putReq.Header.Set("Host", putReq.URL.Host)
	putReq.Header.Set("X-Amz-Content-Sha256", hex.EncodeToString(sha256Sum(body)))
	signV4(putReq, s.accessKey, s.secretKey, s.region, "s3", time.Now().UTC(), body)

	putResp, err := http.DefaultClient.Do(putReq)
	if err != nil {
		return "", "", fmt.Errorf("s3 put test failed: %w", err)
	}
	defer putResp.Body.Close()
	if putResp.StatusCode < 200 || putResp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(putResp.Body)
		return "", "", fmt.Errorf("s3 put test failed (%d): %s", putResp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, publicURL, nil)
	if err != nil {
		return "", "", fmt.Errorf("create public get request: %w", err)
	}
	getResp, err := http.DefaultClient.Do(getReq)
	if err != nil {
		return publicURL, "", fmt.Errorf("public access test failed: %w", err)
	}
	getBody, _ := io.ReadAll(io.LimitReader(getResp.Body, 4096))
	getResp.Body.Close()
	if getResp.StatusCode < 200 || getResp.StatusCode >= 300 {
		return publicURL, "", fmt.Errorf("public access test failed (%d): %s", getResp.StatusCode, strings.TrimSpace(string(getBody)))
	}

	deleteWarning := ""
	deleteReq, err := http.NewRequestWithContext(ctx, http.MethodDelete, reqURL, nil)
	if err == nil {
		deleteReq.Header.Set("Content-Type", "text/plain")
		deleteReq.Header.Set("Host", deleteReq.URL.Host)
		deleteReq.Header.Set("X-Amz-Content-Sha256", hex.EncodeToString(sha256Sum(nil)))
		signV4(deleteReq, s.accessKey, s.secretKey, s.region, "s3", time.Now().UTC(), nil)
		if deleteResp, deleteErr := http.DefaultClient.Do(deleteReq); deleteErr == nil {
			deleteBody, _ := io.ReadAll(io.LimitReader(deleteResp.Body, 4096))
			deleteResp.Body.Close()
			if deleteResp.StatusCode < 200 || deleteResp.StatusCode >= 300 {
				deleteWarning = fmt.Sprintf("test object delete failed (%d): %s", deleteResp.StatusCode, strings.TrimSpace(string(deleteBody)))
			}
		} else {
			deleteWarning = fmt.Sprintf("test object delete failed: %v", deleteErr)
		}
	} else {
		deleteWarning = fmt.Sprintf("create delete request failed: %v", err)
	}

	return publicURL, deleteWarning, nil
}

func signV4(req *http.Request, accessKey, secretKey, region, service string, now time.Time, body []byte) {
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")
	req.Header.Set("X-Amz-Date", amzDate)
	payloadHash := hex.EncodeToString(sha256Sum(body))
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)

	signedHeaderKeys := []string{"content-type", "host", "x-amz-content-sha256", "x-amz-date"}
	canonicalHeaders := ""
	for _, h := range signedHeaderKeys {
		val := req.Header.Get(h)
		if h == "host" {
			val = req.URL.Host
		}
		canonicalHeaders += strings.ToLower(h) + ":" + strings.TrimSpace(val) + "\n"
	}
	signedHeaders := strings.Join(signedHeaderKeys, ";")
	canonicalURI := req.URL.Path
	if canonicalURI == "" {
		canonicalURI = "/"
	}
	canonicalRequest := strings.Join([]string{
		req.Method, canonicalURI, req.URL.RawQuery,
		canonicalHeaders, signedHeaders, payloadHash,
	}, "\n")

	credentialScope := fmt.Sprintf("%s/%s/%s/aws4_request", dateStamp, region, service)
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256", amzDate, credentialScope,
		hex.EncodeToString(sha256Sum([]byte(canonicalRequest))),
	}, "\n")

	signingKey := hmacChain([]byte("AWS4"+secretKey), dateStamp, region, service, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))
	authHeader := fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		accessKey, credentialScope, signedHeaders, signature)
	req.Header.Set("Authorization", authHeader)
}

func awsPercentEncode(value string) string {
	encoded := url.QueryEscape(value)
	encoded = strings.ReplaceAll(encoded, "+", "%20")
	encoded = strings.ReplaceAll(encoded, "*", "%2A")
	return strings.ReplaceAll(encoded, "%7E", "~")
}

func awsEncodePath(path string) string {
	if path == "" {
		return "/"
	}
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		if i == 0 && segment == "" {
			continue
		}
		segments[i] = awsPercentEncode(segment)
	}
	encoded := strings.Join(segments, "/")
	if !strings.HasPrefix(encoded, "/") {
		encoded = "/" + encoded
	}
	return encoded
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func hmacChain(key []byte, args ...string) []byte {
	for _, a := range args {
		key = hmacSHA256(key, []byte(a))
	}
	return key
}

func sha256Sum(data []byte) []byte {
	h := sha256.Sum256(data)
	return h[:]
}
