package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// apiClient talks to the api service (call routing decisions) and media (RTP anchoring) over the
// back network, authenticated with the shared internal token.
type apiClient struct {
	apiURL, mediaURL, token string
	http                    http.Client
}

func newAPIClient(apiURL, mediaURL, token string) *apiClient {
	return &apiClient{apiURL: apiURL, mediaURL: mediaURL, token: token, http: http.Client{Timeout: 5 * time.Second}}
}

func (c *apiClient) do(ctx context.Context, method, url string, in, out any) (int, error) {
	var body *bytes.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	} else {
		body = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("X-Internal-Token", c.token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	if out != nil && res.StatusCode == http.StatusOK {
		_ = json.NewDecoder(res.Body).Decode(out)
	}
	return res.StatusCode, nil
}

type createdCall struct {
	CallID       string `json:"call_id"`
	State        string `json:"state"`
	Outcome      string `json:"outcome"`
	CalleeUserID int64  `json:"callee_user_id"`
}

// createCall asks the api to route a call placed from a desk phone. Status 404 = unknown extension, 409 = caller busy.
func (c *apiClient) createCall(ctx context.Context, callerUser int64, toExt string) (*createdCall, int, error) {
	var out createdCall
	code, err := c.do(ctx, http.MethodPost, c.apiURL+"/internal/calls", map[string]any{"caller_user_id": callerUser, "to_extension": toExt}, &out)
	return &out, code, err
}

func (c *apiClient) answered(ctx context.Context, callID string) error {
	code, err := c.do(ctx, http.MethodPost, c.apiURL+"/internal/calls/"+callID+"/answered", nil, nil)
	if err == nil && code != http.StatusNoContent {
		err = fmt.Errorf("api answered: %d", code)
	}
	return err
}

func (c *apiClient) hangup(ctx context.Context, callID string, byUser int64, as string) {
	_, _ = c.do(ctx, http.MethodPost, c.apiURL+"/internal/calls/"+callID+"/hangup", map[string]any{"by_user_id": byUser, "as": as}, nil)
}

type mediaLeg struct {
	ID   string `json:"leg_id"`
	Port int    `json:"port"`
}

func (c *apiClient) newLeg(ctx context.Context, callID string) (*mediaLeg, error) {
	var l mediaLeg
	code, err := c.do(ctx, http.MethodPost, c.mediaURL+"/legs", map[string]string{"call_id": callID}, &l)
	if err == nil && code != http.StatusOK {
		err = fmt.Errorf("media leg: %d", code)
	}
	return &l, err
}

func (c *apiClient) activateLeg(ctx context.Context, legID, remote string) error {
	code, err := c.do(ctx, http.MethodPost, c.mediaURL+"/legs/"+legID+"/activate", map[string]string{"remote": remote}, nil)
	if err == nil && code != http.StatusNoContent {
		err = fmt.Errorf("media activate: %d", code)
	}
	return err
}

func (c *apiClient) deleteLeg(ctx context.Context, legID string) {
	_, _ = c.do(ctx, http.MethodDelete, c.mediaURL+"/legs/"+legID, nil, nil)
}
