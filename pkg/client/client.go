package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/hackmajoris/sharefly/pkg/share"
)

type Client struct {
	BaseURL string
}

var (
	apiClient    = &http.Client{Timeout: 30 * time.Second}
	uploadClient = &http.Client{}
)

func (c *Client) Upload(path, ttl string) (sh share.Link, skipped []string, err error) {
	if _, _, err := share.ParseTTL(ttl); err != nil {
		return sh, nil, err
	}
	fi, err := validate(path)
	if err != nil {
		return sh, nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return sh, nil, err
	}
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		var err error
		skipped, err = archive(path, fi, pw)
		pw.CloseWithError(err)
		done <- err
	}()

	q := url.Values{"ttl": {ttl}, "name": {filepath.Base(abs)}}
	req, err := http.NewRequest(http.MethodPost, c.endpoint("/shares?"+q.Encode()), pr)
	if err != nil {
		pr.Close()
		<-done
		return sh, nil, err
	}
	req.Header.Set("Content-Type", "application/gzip")
	resp, err := uploadClient.Do(req)
	pr.Close()
	if archErr := <-done; archErr != nil && !errors.Is(archErr, io.ErrClosedPipe) {
		if resp != nil {
			resp.Body.Close()
		}
		return sh, nil, archErr
	}
	return sh, skipped, c.handle(resp, err, http.StatusCreated, &sh)
}

func (c *Client) List() ([]share.Link, error) {
	resp, err := apiClient.Get(c.endpoint("/shares"))
	var shares []share.Link
	return shares, c.handle(resp, err, http.StatusOK, &shares)
}

func (c *Client) Delete(id string) error {
	req, err := http.NewRequest(http.MethodDelete, c.endpoint("/shares/"+url.PathEscape(id)), nil)
	if err != nil {
		return err
	}
	resp, err := apiClient.Do(req)
	return c.handle(resp, err, http.StatusNoContent, nil)
}

func (c *Client) Renew(id, ttl string) (share.Link, error) {
	body, err := json.Marshal(map[string]string{"ttl": ttl})
	if err != nil {
		return share.Link{}, err
	}
	resp, err := apiClient.Post(c.endpoint("/shares/"+url.PathEscape(id)+"/renew"), "application/json", bytes.NewReader(body))
	var sh share.Link
	return sh, c.handle(resp, err, http.StatusOK, &sh)
}

func (c *Client) endpoint(path string) string {
	return strings.TrimSuffix(c.BaseURL, "/") + path
}

func (c *Client) handle(resp *http.Response, err error, want int, out any) error {
	if err != nil {
		return fmt.Errorf("can't reach sharefly server at %s (tailscale up? server running?): %w", c.BaseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		var apiErr struct {
			Error string `json:"error"`
		}
		if json.NewDecoder(resp.Body).Decode(&apiErr) == nil && apiErr.Error != "" {
			return errors.New(apiErr.Error)
		}
		return fmt.Errorf("server returned %s", resp.Status)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
