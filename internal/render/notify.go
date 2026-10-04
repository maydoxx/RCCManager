package render

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

// the site keeps a media table, same PASSWORD both ways

type renderedFile struct {
	Key  string `json:"key"`
	Size int    `json:"size"`
}

type renderedBody struct {
	Kind  Kind           `json:"kind"`
	ID    int64          `json:"id"`
	Files []renderedFile `json:"files"`
}

var notifyClient = &http.Client{
	Timeout: 10 * time.Second,
}

func (q *Queue) notify(ctx context.Context, kind Kind, id int64, files []renderedFile) {
	if q.cfg.SiteInternalURL == "" {
		return
	}
	body, _ := json.Marshal(renderedBody{
		Kind:  kind,
		ID:    id,
		Files: files,
	})
	url := q.cfg.SiteInternalURL + "/api/rcc/rendered"

	var last error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Duration(attempt*2) * time.Second):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			last = err
			break
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+q.cfg.Password)
		resp, err := notifyClient.Do(req)
		if err != nil {
			last = err
			continue
		}
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		resp.Body.Close()
		if resp.StatusCode/100 == 2 {
			return
		}
		last = fmt.Errorf("HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(msg))
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusNotFound {
			break // wrong password or an old site
		}
	}
	log.Printf("couldnt tell the site about %s %d: %v", kind, id, last)
}
