// Copyright (C) 2026 Lauri Heiskanen
// SPDX-License-Identifier: GPL-3.0-or-later
//
// This program is free software: you can redistribute it and/or modify it
// under the terms of the GNU General Public License as published by the Free
// Software Foundation, either version 3 of the License, or (at your option)
// any later version. It is distributed WITHOUT ANY WARRANTY; see the GNU
// General Public License in LICENSE for details.

// Package omada is a thin client for the TP-Link Omada controller Open API
// (controller v5.5+; developed against 6.3.0.45 at 192.168.1.10:8043).
//
// It handles the three things that are annoying to redo per call — the
// client_credentials token dance, on-disk token caching so a short-lived CLI
// does not re-authenticate on every invocation, and result paging — and
// deliberately does nothing else. Responses come back as raw JSON because the
// Open API's field set varies by controller version; imposing Go structs on it
// here would mean silently dropping fields the caller might want.
package omada

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config is everything needed to reach one controller.
type Config struct {
	APIURL       string // Interface Access Address, e.g. https://192.168.1.10:443
	OmadacID     string // from GET /api/info, field omadacId
	ClientID     string // Open API application client id
	ClientSecret string // Open API application client secret
	Insecure     bool   // skip TLS verification (controllers ship a self-signed cert)
	CacheDir     string // where the access token is cached; "" disables caching
	Timeout      time.Duration
}

// Client talks to one controller. Not safe for concurrent use.
type Client struct {
	cfg    Config
	hc     *http.Client
	token  string
	expiry time.Time
}

// New validates the config and prepares an HTTP client. It does not contact
// the controller; the first request fetches a token.
func New(cfg Config) (*Client, error) {
	if cfg.APIURL == "" {
		return nil, errors.New("api url is empty")
	}
	if cfg.OmadacID == "" {
		return nil, errors.New("omadac id is empty")
	}
	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil, errors.New("client id/secret are empty: create an Open API application under Global View -> Settings -> Platform Integration")
	}
	cfg.APIURL = strings.TrimRight(cfg.APIURL, "/")
	if cfg.Timeout == 0 {
		cfg.Timeout = 20 * time.Second
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if cfg.Insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &Client{cfg: cfg, hc: &http.Client{Transport: tr, Timeout: cfg.Timeout}}, nil
}

// envelope is the wrapper every Omada endpoint — Open API and legacy alike —
// puts around its payload. errorCode 0 means success; msg carries the reason
// otherwise.
type envelope struct {
	ErrorCode int             `json:"errorCode"`
	Msg       string          `json:"msg"`
	Result    json.RawMessage `json:"result"`
}

// APIError is a non-zero errorCode from the controller.
type APIError struct {
	Code int
	Msg  string
	Path string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("omada api %s: errorCode %d: %s", e.Path, e.Code, e.Msg)
}

// tokenExpired reports whether an error means "your access token is no longer
// valid" — the one case worth retrying automatically.
func tokenExpired(err error) bool {
	var ae *APIError
	if !errors.As(err, &ae) {
		return false
	}
	// -44112 invalid/expired access token, -44109 token not found.
	return ae.Code == -44112 || ae.Code == -44109
}

// Get calls an Open API path relative to /openapi/v1/{omadacId} and returns
// the decoded result field. A leading slash is optional.
func (c *Client) Get(ctx context.Context, path string, query url.Values) (json.RawMessage, error) {
	res, err := c.do(ctx, http.MethodGet, path, query, nil)
	if err != nil && tokenExpired(err) {
		c.token, c.expiry = "", time.Time{}
		c.dropCachedToken()
		res, err = c.do(ctx, http.MethodGet, path, query, nil)
	}
	return res, err
}

// page is the shape of a paged Open API result.
type page struct {
	TotalRows   int               `json:"totalRows"`
	CurrentPage int               `json:"currentPage"`
	CurrentSize int               `json:"currentSize"`
	Data        []json.RawMessage `json:"data"`
}

// Result is one endpoint's payload, already normalised into "a list of
// things" or "one thing". The Open API is inconsistent about this — some
// endpoints wrap rows in a paged envelope, some return a bare JSON array, some
// return a single object — and a caller that has to guess will eventually emit
// an object where its consumer expected an array.
type Result struct {
	IsList bool              // true when the endpoint returned a collection
	Rows   []json.RawMessage // the elements, when IsList
	Raw    json.RawMessage   // the whole payload, when !IsList
}

// GetAll fetches a path, walking every page if the endpoint is paged, and
// reports which of the three shapes came back.
func (c *Client) GetAll(ctx context.Context, path string, query url.Values) (Result, error) {
	return getAllPaged(ctx, c.Get, path, query, "page", "pageSize")
}

// getAllPaged walks a paged endpoint using whatever the two API surfaces call
// their paging parameters — the Open API says page/pageSize, the web API says
// currentPage/currentPageSize — and normalises the three possible payload
// shapes into a Result.
func getAllPaged(
	ctx context.Context,
	get func(context.Context, string, url.Values) (json.RawMessage, error),
	path string,
	query url.Values,
	pageParam, sizeParam string,
) (Result, error) {
	const pageSize = 100
	if query == nil {
		query = url.Values{}
	}
	var rows []json.RawMessage
	for pageNum := 1; ; pageNum++ {
		q := url.Values{}
		for k, v := range query {
			q[k] = v
		}
		q.Set(pageParam, strconv.Itoa(pageNum))
		q.Set(sizeParam, strconv.Itoa(pageSize))

		raw, err := get(ctx, path, q)
		if err != nil {
			return Result{}, err
		}

		var p page
		if err := json.Unmarshal(raw, &p); err != nil || p.Data == nil {
			// Not a paged envelope. A bare array is still a list; anything
			// else is a single object.
			var arr []json.RawMessage
			if err := json.Unmarshal(raw, &arr); err == nil {
				return Result{IsList: true, Rows: arr}, nil
			}
			return Result{Raw: raw}, nil
		}

		rows = append(rows, p.Data...)
		if len(p.Data) < pageSize || len(rows) >= p.TotalRows {
			return Result{IsList: true, Rows: rows}, nil
		}
	}
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body io.Reader) (json.RawMessage, error) {
	if err := c.ensureToken(ctx); err != nil {
		return nil, err
	}
	u := c.url(path)
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	// Omada's Open API uses this non-standard Authorization form, not Bearer.
	req.Header.Set("Authorization", "AccessToken="+c.token)
	req.Header.Set("Content-Type", "application/json")
	return c.send(req, path)
}

// url resolves a caller-supplied path against the Open API. Most endpoints live
// under /openapi/v1/{omadacId}/, but not all — the log-notification settings,
// for instance, are v2 — so a path may name its own version ("v2/sites/...")
// or be given fully qualified ("openapi/v2/...").
func (c *Client) url(path string) string {
	p := strings.TrimLeft(path, "/")
	switch {
	case strings.HasPrefix(p, "openapi/"):
		return c.cfg.APIURL + "/" + p
	case strings.HasPrefix(p, "v1/"), strings.HasPrefix(p, "v2/"):
		ver, rest, _ := strings.Cut(p, "/")
		return c.cfg.APIURL + "/openapi/" + ver + "/" + c.cfg.OmadacID + "/" + rest
	default:
		return c.cfg.APIURL + "/openapi/v1/" + c.cfg.OmadacID + "/" + p
	}
}

func (c *Client) send(req *http.Request, path string) (json.RawMessage, error) {
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		snippet := string(raw)
		if len(snippet) > 200 {
			snippet = snippet[:200] + "..."
		}
		// An HTML body from an Omada controller means one thing in practice: the
		// request went to the web UI port rather than the API address. The UI
		// answers every credentialed call with a 500 HTML page, identically
		// whether credentials are valid, wrong or absent -- which reads exactly
		// like an auth failure and sends people debugging the wrong thing.
		if looksLikeHTML(raw) {
			// The HTML itself tells the reader nothing useful, so do not paste 200
			// characters of Tomcat markup over the actual explanation.
			return nil, fmt.Errorf("omada api %s: http %d with an HTML body, which is "+
				"what the web UI port returns for API calls. The api url must be the "+
				"Interface Access Address from the Open API application's View dialog, "+
				"not the web UI address", path, resp.StatusCode)
		}
		return nil, fmt.Errorf("omada api %s: http %d, unparseable response: %s", path, resp.StatusCode, snippet)
	}
	if env.ErrorCode != 0 {
		return nil, &APIError{Code: env.ErrorCode, Msg: env.Msg, Path: path}
	}
	return env.Result, nil
}

// --- token handling -------------------------------------------------------

type tokenResult struct {
	AccessToken  string `json:"accessToken"`
	TokenType    string `json:"tokenType"`
	ExpiresIn    int    `json:"expiresIn"`
	RefreshToken string `json:"refreshToken"`
}

type cachedToken struct {
	AccessToken string    `json:"access_token"`
	Expiry      time.Time `json:"expiry"`
}

func (c *Client) ensureToken(ctx context.Context) error {
	// Refresh a minute early so a long call does not expire mid-flight.
	if c.token != "" && time.Now().Before(c.expiry.Add(-time.Minute)) {
		return nil
	}
	if tok, exp, ok := c.readCachedToken(); ok {
		c.token, c.expiry = tok, exp
		return nil
	}
	return c.fetchToken(ctx)
}

// fetchToken performs the client_credentials grant.
func (c *Client) fetchToken(ctx context.Context) error {
	body, err := json.Marshal(map[string]string{
		"omadacId":      c.cfg.OmadacID,
		"client_id":     c.cfg.ClientID,
		"client_secret": c.cfg.ClientSecret,
	})
	if err != nil {
		return err
	}
	u := c.cfg.APIURL + "/openapi/authorize/token?grant_type=client_credentials"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	raw, err := c.send(req, "/openapi/authorize/token")
	if err != nil {
		return err
	}
	var tr tokenResult
	if err := json.Unmarshal(raw, &tr); err != nil {
		return fmt.Errorf("decoding token result: %w", err)
	}
	if tr.AccessToken == "" {
		return errors.New("controller returned an empty access token")
	}
	c.token = tr.AccessToken
	c.expiry = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	c.writeCachedToken()
	return nil
}

// Token exposes the current access token and its expiry, fetching one if
// needed. Useful for a connectivity check.
func (c *Client) Token(ctx context.Context) (string, time.Time, error) {
	if err := c.ensureToken(ctx); err != nil {
		return "", time.Time{}, err
	}
	return c.token, c.expiry, nil
}

// cacheFile names the token cache per controller+application, so several
// controllers or credentials do not clobber each other.
func (c *Client) cacheFile() string {
	if c.cfg.CacheDir == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(c.cfg.APIURL + "|" + c.cfg.OmadacID + "|" + c.cfg.ClientID))
	return filepath.Join(c.cfg.CacheDir, "token-"+hex.EncodeToString(sum[:8])+".json")
}

func (c *Client) readCachedToken() (string, time.Time, bool) {
	path := c.cacheFile()
	if path == "" {
		return "", time.Time{}, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", time.Time{}, false
	}
	var ct cachedToken
	if err := json.Unmarshal(raw, &ct); err != nil || ct.AccessToken == "" {
		return "", time.Time{}, false
	}
	if time.Now().After(ct.Expiry.Add(-time.Minute)) {
		return "", time.Time{}, false
	}
	return ct.AccessToken, ct.Expiry, true
}

func (c *Client) writeCachedToken() {
	path := c.cacheFile()
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	raw, err := json.Marshal(cachedToken{AccessToken: c.token, Expiry: c.expiry})
	if err != nil {
		return
	}
	// Token grants full API access — keep it owner-readable only.
	_ = os.WriteFile(path, raw, 0o600)
}

func (c *Client) dropCachedToken() {
	if path := c.cacheFile(); path != "" {
		_ = os.Remove(path)
	}
}

// looksLikeHTML reports whether a response body is an HTML document rather than
// the JSON envelope every real Omada endpoint returns.
func looksLikeHTML(raw []byte) bool {
	head := strings.ToLower(strings.TrimSpace(string(raw)))
	return strings.HasPrefix(head, "<!doctype html") || strings.HasPrefix(head, "<html")
}
