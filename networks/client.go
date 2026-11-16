package networks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"
)

// ClientBuilder configures a LauncherClient.
type ClientBuilder struct {
	deviceID   string
	userToken  string
	timeout    time.Duration
	retries    int
	httpClient *http.Client
}

// NewClientBuilder creates a default ClientBuilder.
func NewClientBuilder() *ClientBuilder {
	return &ClientBuilder{
		timeout: 10 * time.Second,
		retries: 3,
	}
}

// DeviceID sets the x-hg-launcher-device-id header.
func (b *ClientBuilder) DeviceID(id string) *ClientBuilder {
	b.deviceID = id
	return b
}

// UserToken sets the x-hg-user-token header.
func (b *ClientBuilder) UserToken(token string) *ClientBuilder {
	b.userToken = token
	return b
}

// Timeout sets the HTTP client timeout.
func (b *ClientBuilder) Timeout(timeout time.Duration) *ClientBuilder {
	b.timeout = timeout
	return b
}

// Retries sets the number of retry attempts on network/server errors (minimum 1).
func (b *ClientBuilder) Retries(retries int) *ClientBuilder {
	b.retries = retries
	return b
}

// HTTPClient allows using a custom http.Client.
func (b *ClientBuilder) HTTPClient(client *http.Client) *ClientBuilder {
	b.httpClient = client
	return b
}

// Build creates a new LauncherClient.
func (b *ClientBuilder) Build() (*LauncherClient, error) {
	client := b.httpClient
	if client == nil {
		transport := &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   3 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 5 * time.Second,
			DisableCompression:  false,
		}
		client = &http.Client{
			Transport: transport,
			Timeout:   b.timeout,
		}
	}

	retries := b.retries
	if retries < 1 {
		retries = 1
	}

	return &LauncherClient{
		http:      client,
		deviceID:  b.deviceID,
		userToken: b.userToken,
		retries:   retries,
		seq:       atomic.Uint64{},
	}, nil
}

// LauncherClient is an HTTP client for Gryphline Launcher API.
type LauncherClient struct {
	http      *http.Client
	deviceID  string
	userToken string
	retries   int
	seq       atomic.Uint64
}

// NewLauncherClient creates a LauncherClient with default options.
func NewLauncherClient() (*LauncherClient, error) {
	return NewClientBuilder().Build()
}

// SubRequest defines one sub-request within a batch proxy payload.
type SubRequest struct {
	Kind string
	Req  any
}

// GetLatestGame queries the latest game version and download packages for Endfield.
func (c *LauncherClient) GetLatestGame(ctx context.Context, version string) (*GameRsp, error) {
	req := NewEndfieldGameRequest(version)
	return c.GetLatestGameWith(ctx, &req)
}

// GetLatestGameWith queries game update info with a custom GameRequest.
func (c *LauncherClient) GetLatestGameWith(ctx context.Context, req *GameRequest) (*GameRsp, error) {
	raw, err := c.CallRaw(ctx, GameEndpoint, "get_latest_game", req)
	if err != nil {
		return nil, fmt.Errorf("get_latest_game request failed: %w", err)
	}

	var rsp GameRsp
	if err := json.Unmarshal(raw, &rsp); err != nil {
		return nil, fmt.Errorf("failed to parse get_latest_game_rsp: %w", err)
	}
	return &rsp, nil
}

func (c *LauncherClient) GetLatestLauncher(ctx context.Context, version string) (*LauncherRsp, error) {
	req := NewEndfieldLauncherRequest(version)
	return c.GetLatestLauncherWith(ctx, &req)
}

func (c *LauncherClient) GetLatestLauncherWith(ctx context.Context, req *LauncherRequest) (*LauncherRsp, error) {
	raw, err := c.CallRaw(ctx, GameEndpoint, "get_latest_launcher", req)
	if err != nil {
		return nil, fmt.Errorf("get_latest_launcher request failed: %w", err)
	}

	var rsp LauncherRsp
	if err := json.Unmarshal(raw, &rsp); err != nil {
		return nil, fmt.Errorf("failed to parse get_latest_launcher_rsp: %w", err)
	}
	return &rsp, nil
}

// GetBanners fetches carousel promotional banners with image URLs and destination links.
func (c *LauncherClient) GetBanners(ctx context.Context, req *WebCommonReq) (*BannersRsp, error) {
	raw, err := c.CallRaw(ctx, WebEndpoint, "get_banner", req)
	if err != nil {
		return nil, err
	}
	var rsp BannersRsp
	if err := json.Unmarshal(raw, &rsp); err != nil {
		return nil, fmt.Errorf("failed to parse get_banner_rsp: %w", err)
	}
	return &rsp, nil
}

// GetMainBgImage fetches the launcher background still image and looping ambient video.
func (c *LauncherClient) GetMainBgImage(ctx context.Context, req *WebCommonReq) (*MainBgImageRsp, error) {
	raw, err := c.CallRaw(ctx, WebEndpoint, "get_main_bg_image", req)
	if err != nil {
		return nil, err
	}
	var rsp MainBgImageRsp
	if err := json.Unmarshal(raw, &rsp); err != nil {
		return nil, fmt.Errorf("failed to parse get_main_bg_image_rsp: %w", err)
	}
	return &rsp, nil
}

// GetSingleEnt fetches featured version visual graphics, logos, and call-to-action buttons.
func (c *LauncherClient) GetSingleEnt(ctx context.Context, req *WebCommonReq) (*SingleEntRsp, error) {
	raw, err := c.CallRaw(ctx, WebEndpoint, "get_single_ent", req)
	if err != nil {
		return nil, err
	}
	var rsp SingleEntRsp
	if err := json.Unmarshal(raw, &rsp); err != nil {
		return nil, fmt.Errorf("failed to parse get_single_ent_rsp: %w", err)
	}
	return &rsp, nil
}

// GetAnnouncements queries categorized game announcements (Notices, Events, News).
func (c *LauncherClient) GetAnnouncements(ctx context.Context, req *WebCommonReq) (*AnnouncementsRsp, error) {
	raw, err := c.CallRaw(ctx, WebEndpoint, "get_announcement", req)
	if err != nil {
		return nil, err
	}
	var rsp AnnouncementsRsp
	if err := json.Unmarshal(raw, &rsp); err != nil {
		return nil, fmt.Errorf("failed to parse get_announcement_rsp: %w", err)
	}
	return &rsp, nil
}

// GetSidebar queries official social media links, companion tools, and customer service.
func (c *LauncherClient) GetSidebar(ctx context.Context, req *WebCommonReq) (*SidebarRsp, error) {
	raw, err := c.CallRaw(ctx, WebEndpoint, "get_sidebar", req)
	if err != nil {
		return nil, err
	}
	var rsp SidebarRsp
	if err := json.Unmarshal(raw, &rsp); err != nil {
		return nil, fmt.Errorf("failed to parse get_sidebar_rsp: %w", err)
	}
	return &rsp, nil
}

// GetUrlConfig queries payment and questionnaire URLs.
func (c *LauncherClient) GetUrlConfig(ctx context.Context, req *UrlConfigReq) (map[string]any, error) {
	raw, err := c.CallRaw(ctx, WebEndpoint, "get_url_config", req)
	if err != nil {
		return nil, err
	}
	var res map[string]any
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	return res, nil
}

// GetLauncherMedia fetches all banners, background media, version splash art, news, and sidebar links in a SINGLE batch round-trip.
func (c *LauncherClient) GetLauncherMedia(ctx context.Context, req *WebCommonReq) (*LauncherMediaBundle, error) {
	subReqs := []SubRequest{
		{Kind: "get_banner", Req: req},
		{Kind: "get_main_bg_image", Req: req},
		{Kind: "get_single_ent", Req: req},
		{Kind: "get_announcement", Req: req},
		{Kind: "get_sidebar", Req: req},
	}

	batchRes, err := c.CallBatchRaw(ctx, WebEndpoint, subReqs)
	if err != nil {
		return nil, fmt.Errorf("GetLauncherMedia batch request failed: %w", err)
	}

	bundle := &LauncherMediaBundle{}

	if raw, ok := batchRes["get_banner"]; ok {
		var rsp BannersRsp
		if err := json.Unmarshal(raw, &rsp); err == nil {
			bundle.Banners = rsp.Banners
		}
	}

	if raw, ok := batchRes["get_main_bg_image"]; ok {
		var rsp MainBgImageRsp
		if err := json.Unmarshal(raw, &rsp); err == nil {
			bundle.MainBgImage = rsp.MainBgImage
		}
	}

	if raw, ok := batchRes["get_single_ent"]; ok {
		var rsp SingleEntRsp
		if err := json.Unmarshal(raw, &rsp); err == nil {
			bundle.SingleEnt = rsp.SingleEnt
		}
	}

	if raw, ok := batchRes["get_announcement"]; ok {
		var rsp AnnouncementsRsp
		if err := json.Unmarshal(raw, &rsp); err == nil {
			bundle.Announcements = rsp.Tabs
		}
	}

	if raw, ok := batchRes["get_sidebar"]; ok {
		var rsp SidebarRsp
		if err := json.Unmarshal(raw, &rsp); err == nil {
			bundle.Sidebar = rsp.Sidebars
		}
	}

	return bundle, nil
}

// WebCall executes an arbitrary web proxy sub-request.
func (c *LauncherClient) WebCall(ctx context.Context, kind string, req any) (json.RawMessage, error) {
	return c.CallRaw(ctx, WebEndpoint, kind, req)
}

// CallRaw performs a single batch-proxy call with the specified kind and payload.
func (c *LauncherClient) CallRaw(ctx context.Context, endpoint, kind string, req any) (json.RawMessage, error) {
	batchMap, err := c.CallBatchRaw(ctx, endpoint, []SubRequest{{Kind: kind, Req: req}})
	if err != nil {
		return nil, err
	}
	raw, ok := batchMap[kind]
	if !ok {
		return nil, fmt.Errorf("result for kind %s missing in response", kind)
	}
	return raw, nil
}

// CallBatchRaw executes multiple sub-requests in a single HTTP POST batch.
func (c *LauncherClient) CallBatchRaw(ctx context.Context, endpoint string, reqs []SubRequest) (map[string]json.RawMessage, error) {
	seq := strconv.FormatUint(c.seq.Add(1), 10)

	proxyReqs := make([]any, 0, len(reqs))
	for _, r := range reqs {
		proxyReqs = append(proxyReqs, map[string]any{
			"kind":                        r.Kind,
			fmt.Sprintf("%s_req", r.Kind): r.Req,
		})
	}

	bodyObj := map[string]any{
		"seq":        seq,
		"proxy_reqs": proxyReqs,
	}

	bodyBytes, err := json.Marshal(bodyObj)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal batch request: %w", err)
	}

	respBytes, err := c.postJSONWithRetry(ctx, endpoint, bodyBytes)
	if err != nil {
		return nil, fmt.Errorf("batch request to %s failed: %w", endpoint, err)
	}

	var root map[string]json.RawMessage
	if err := json.Unmarshal(respBytes, &root); err != nil {
		return nil, fmt.Errorf("failed to unmarshal batch response: %w", err)
	}

	proxyRspsRaw, ok := root["proxy_rsps"]
	if !ok || len(proxyRspsRaw) == 0 {
		if msgRaw, exists := root["message"]; exists {
			var msg string
			_ = json.Unmarshal(msgRaw, &msg)
			return nil, fmt.Errorf("server error: %s (response: %s)", msg, string(respBytes))
		}
		return nil, fmt.Errorf("response missing proxy_rsps: %s", string(respBytes))
	}

	var rspsList []map[string]json.RawMessage
	if err := json.Unmarshal(proxyRspsRaw, &rspsList); err != nil {
		return nil, fmt.Errorf("failed to parse proxy_rsps array: %w", err)
	}

	results := make(map[string]json.RawMessage, len(rspsList))
	for _, item := range rspsList {
		var kind string
		if kindRaw, ok := item["kind"]; ok {
			_ = json.Unmarshal(kindRaw, &kind)
		}

		if kind == "" {
			continue
		}

		rspKey := fmt.Sprintf("%s_rsp", kind)
		if val, found := item[rspKey]; found && len(val) > 0 {
			results[kind] = val
			continue
		}

		if val, found := item[kind]; found && len(val) > 0 {
			results[kind] = val
			continue
		}

		if msgRaw, found := item["message"]; found {
			var msg string
			_ = json.Unmarshal(msgRaw, &msg)
			return nil, fmt.Errorf("server error for %s: %s", kind, msg)
		}
	}

	return results, nil
}

func (c *LauncherClient) postJSONWithRetry(ctx context.Context, url string, body []byte) ([]byte, error) {
	delay := 300 * time.Millisecond

	var lastErr error
	for attempt := 1; attempt <= c.retries; attempt++ {
		res, retryable, err := c.doPost(ctx, url, body)
		if err == nil {
			return res, nil
		}
		lastErr = err

		if !retryable || attempt == c.retries {
			return nil, lastErr
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
			delay *= 2
		}
	}

	return nil, lastErr
}

func (c *LauncherClient) doPost(ctx context.Context, url string, body []byte) ([]byte, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, false, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0")

	if c.deviceID != "" {
		req.Header.Set("x-hg-launcher-device-id", c.deviceID)
	}
	if c.userToken != "" {
		req.Header.Set("x-hg-user-token", c.userToken)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, isRetryableError(err), err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, true, fmt.Errorf("error reading response body: %w", err)
	}

	if resp.StatusCode >= 500 {
		return nil, true, fmt.Errorf("server HTTP %d: %s", resp.StatusCode, string(respBytes))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBytes))
	}

	return respBytes, false, nil
}

func isRetryableError(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	return false
}
