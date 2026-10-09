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

// ErrUnreachable wraps connection failures so callers can tell "no server" from API errors.
var ErrUnreachable = errors.New("can't reach sharefly server")

type Client struct {
	BaseURL string
}

var (
	apiClient    = &http.Client{Timeout: 30 * time.Second}
	uploadClient = &http.Client{}
)

// Upload shares path. With password, the server generates one and returns it once in sh.Password. With once,
// the share can be opened by one visitor.
func (c *Client) Upload(path, ttl string, password, once bool) (sh share.Link, skipped []string, err error) {
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
	if password {
		q.Set("password", "1")
	}
	if once {
		q.Set("once", "1")
	}
	req, err := http.NewRequest(http.MethodPost, c.endpoint("/shares?"+q.Encode()), pr)
	if err != nil {
		_ = pr.Close()
		<-done
		return sh, nil, err
	}
	req.Header.Set("Content-Type", "application/gzip")
	resp, err := uploadClient.Do(req)
	_ = pr.Close()
	if archErr := <-done; archErr != nil && !errors.Is(archErr, io.ErrClosedPipe) {
		if resp != nil {
			_ = resp.Body.Close()
		}
		return sh, nil, archErr
	}
	if err := c.handle(resp, err, http.StatusCreated, &sh); err != nil {
		return sh, skipped, err
	}
	// a server without password support ignores the parameter and publishes the share unprotected
	if password && (!sh.Protected || sh.Password == "") {
		if derr := c.Delete(sh.ID); derr != nil {
			return share.Link{}, skipped, fmt.Errorf("the server doesn't support --password (older version?) and published %s unprotected; delete it: sharefly rm %s (%v)", sh.ID, sh.ID, derr)
		}
		return share.Link{}, skipped, errors.New("the server doesn't support --password (older version?); the unprotected share was deleted. Restart it with this version: sharefly stop")
	}
	// likewise for once: an older server would publish a share anyone could open any number of times
	if once && !sh.Once {
		if derr := c.Delete(sh.ID); derr != nil {
			return share.Link{}, skipped, fmt.Errorf("the server doesn't support --once (older version?) and published %s for unlimited views; delete it: sharefly rm %s (%v)", sh.ID, sh.ID, derr)
		}
		return share.Link{}, skipped, errors.New("the server doesn't support --once (older version?); the share was deleted. Restart it with this version: sharefly stop")
	}
	return sh, skipped, nil
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

// PublicURL returns the base URL the server currently builds links from.
func (c *Client) PublicURL() (string, error) {
	resp, err := apiClient.Get(c.endpoint("/status"))
	var st struct {
		PublicURL string `json:"public_url"`
	}
	return st.PublicURL, c.handle(resp, err, http.StatusOK, &st)
}

func (c *Client) endpoint(path string) string {
	return strings.TrimSuffix(c.BaseURL, "/") + path
}

func (c *Client) handle(resp *http.Response, err error, want int, out any) error {
	if err != nil {
		return fmt.Errorf("%w at %s (tailscale up? server running?): %w", ErrUnreachable, c.BaseURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
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
