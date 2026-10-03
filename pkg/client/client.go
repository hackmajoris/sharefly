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

	"github.com/hackmajoris/go-share/pkg/share"
)

type Share struct {
	share.Share
	URL string `json:"url"`
}

type Client struct {
	BaseURL string
}

func (c *Client) Upload(path, ttl string) (Share, error) {
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		_, err := Archive(path, pw)
		pw.CloseWithError(err)
		done <- err
	}()

	q := url.Values{"ttl": {ttl}, "name": {filepath.Base(filepath.Clean(path))}}
	req, err := http.NewRequest(http.MethodPost, c.endpoint("/shares?"+q.Encode()), pr)
	if err != nil {
		pr.Close()
		<-done
		return Share{}, err
	}
	req.Header.Set("Content-Type", "application/gzip")
	resp, err := http.DefaultClient.Do(req)
	pr.Close()
	if archErr := <-done; archErr != nil && !errors.Is(archErr, io.ErrClosedPipe) {
		if resp != nil {
			resp.Body.Close()
		}
		return Share{}, archErr
	}
	var sh Share
	return sh, c.handle(resp, err, http.StatusCreated, &sh)
}

func (c *Client) List() ([]Share, error) {
	resp, err := http.Get(c.endpoint("/shares"))
	var shares []Share
	return shares, c.handle(resp, err, http.StatusOK, &shares)
}

func (c *Client) Delete(id string) error {
	req, err := http.NewRequest(http.MethodDelete, c.endpoint("/shares/"+url.PathEscape(id)), nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	return c.handle(resp, err, http.StatusNoContent, nil)
}

func (c *Client) Renew(id, ttl string) (Share, error) {
	body, err := json.Marshal(map[string]string{"ttl": ttl})
	if err != nil {
		return Share{}, err
	}
	resp, err := http.Post(c.endpoint("/shares/"+url.PathEscape(id)+"/renew"), "application/json", bytes.NewReader(body))
	var sh Share
	return sh, c.handle(resp, err, http.StatusOK, &sh)
}

func (c *Client) endpoint(path string) string {
	return strings.TrimSuffix(c.BaseURL, "/") + path
}

func (c *Client) handle(resp *http.Response, err error, want int, out any) error {
	if err != nil {
		return fmt.Errorf("can't reach go-share server at %s (tailscale up? server running?): %w", c.BaseURL, err)
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
