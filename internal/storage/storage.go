package storage

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"rccmanager/internal/config"
)

// keys look like avatars/12.png
type Store interface {
	Put(ctx context.Context, key string, data []byte, contentType string) (publicURL string, err error)
	PublicURL(key string) string
	String() string
}

func New(cfg *config.Config) (Store, error) {
	if err := Validate(cfg); err != nil {
		return nil, fmt.Errorf("storage: %w", err)
	}
	return NewR2(cfg.R2AccountID, cfg.R2Endpoint, cfg.R2Bucket, cfg.R2Prefix, cfg.R2AccessKeyID, cfg.R2SecretAccessKey, cfg.CDNURL), nil
}

// s3 api with sigv4 by hand, no sdk
type R2 struct {
	endpoint  string
	bucket    string
	prefix    string
	accessKey string
	secretKey string
	cdnURL    string
	http      *http.Client
}

func NewR2(accountID, endpoint, bucket, prefix, accessKey, secretKey, cdnURL string) *R2 {
	if endpoint == "" {
		endpoint = "https://" + accountID + ".r2.cloudflarestorage.com"
	}
	return &R2{
		endpoint:  strings.TrimRight(endpoint, "/"),
		bucket:    bucket,
		prefix:    strings.Trim(prefix, "/"),
		accessKey: accessKey,
		secretKey: secretKey,
		cdnURL:    strings.TrimRight(cdnURL, "/"),
		http: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

func (r *R2) String() string {
	return "r2 " + r.bucket + " at " + r.cdnURL
}

func (r *R2) PublicURL(key string) string {
	return r.cdnURL + "/" + key
}

func (r *R2) objectKey(key string) string {
	if r.prefix == "" {
		return key
	}
	return r.prefix + "/" + key
}

func (r *R2) Put(ctx context.Context, key string, data []byte, contentType string) (string, error) {
	u, err := url.Parse(r.endpoint)
	if err != nil {
		return "", err
	}
	path := "/" + escapePath(r.bucket) + "/" + escapePath(r.objectKey(key))

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, r.endpoint+path, strings.NewReader(string(data)))
	if err != nil {
		return "", err
	}
	req.ContentLength = int64(len(data))
	req.Header.Set("Content-Type", contentType)
	// cache hard, the site busts with ?v=
	req.Header.Set("Cache-Control", "public, max-age=31536000")
	r.sign(req, u.Host, path, data)

	resp, err := r.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("r2 put %s: %w", key, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return "", fmt.Errorf("r2 put %s: HTTP %d: %s", key, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return r.PublicURL(key), nil
}

// r2 wants region auto
func (r *R2) sign(req *http.Request, host, canonicalURI string, body []byte) {
	const region, service = "auto", "s3"
	t := time.Now().UTC()
	amzDate := t.Format("20060102T150405Z")
	dateStamp := t.Format("20060102")
	payloadHash := sha256Hex(body)

	req.Header.Set("Host", host)
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadHash)

	// sorted by lowercase name or the signature is wrong
	signed := []string{"cache-control", "content-type", "host", "x-amz-content-sha256", "x-amz-date"}
	var canonicalHeaders strings.Builder
	for _, h := range signed {
		v := req.Header.Get(h)
		if h == "host" {
			v = host
		}
		canonicalHeaders.WriteString(h + ":" + strings.TrimSpace(v) + "\n")
	}
	signedHeaders := strings.Join(signed, ";")

	canonicalRequest := strings.Join([]string{
		http.MethodPut,
		canonicalURI,
		"", // query
		canonicalHeaders.String(),
		signedHeaders,
		payloadHash,
	}, "\n")

	scope := dateStamp + "/" + region + "/" + service + "/aws4_request"
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	kDate := hmacSHA256([]byte("AWS4"+r.secretKey), dateStamp)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	kSigning := hmacSHA256(kService, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(kSigning, stringToSign))

	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+r.accessKey+"/"+scope+
		", SignedHeaders="+signedHeaders+", Signature="+signature)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, msg string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(msg))
	return m.Sum(nil)
}

// rfc 3986 unreserved and / stay, sigv4 is picky
func escapePath(p string) string {
	const unreserved = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.~"
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		c := p[i]
		if c == '/' || strings.IndexByte(unreserved, c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func Validate(cfg *config.Config) error {
	var missing []string
	if cfg.R2AccountID == "" && cfg.R2Endpoint == "" {
		missing = append(missing, "R2_ACCOUNT_ID")
	}
	for _, kv := range []struct{ k, v string }{
		{"R2_BUCKET", cfg.R2Bucket},
		{"R2_ACCESS_KEY_ID", cfg.R2AccessKeyID},
		{"R2_SECRET_ACCESS_KEY", cfg.R2SecretAccessKey},
		{"CDN_URL", cfg.CDNURL},
	} {
		if kv.v == "" {
			missing = append(missing, kv.k)
		}
	}
	if len(missing) > 0 {
		return errors.New("missing " + strings.Join(missing, ", "))
	}
	return nil
}
