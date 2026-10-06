// Copyright (C) 2026 Lauri Heiskanen
// SPDX-License-Identifier: GPL-3.0-or-later
//
// This program is free software: you can redistribute it and/or modify it
// under the terms of the GNU General Public License as published by the Free
// Software Foundation, either version 3 of the License, or (at your option)
// any later version. It is distributed WITHOUT ANY WARRANTY; see the GNU
// General Public License in LICENSE for details.

package omada

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

// WebClient talks to the controller's internal API — the one the web UI itself
// calls, under /{omadacId}/api/v2/. It exposes considerably more than the Open
// API does (site events, alerts, dashboards, per-AP radio detail), at the cost
// of being unversioned: TP-Link can change it in any firmware release. Prefer
// Client where an Open API endpoint exists.
//
// Auth is a session: POST login, keep the TPOMADA_SESSIONID cookie, and echo
// the returned token back as a Csrf-Token header on every later request.
type WebClient struct {
	cfg   WebConfig
	hc    *http.Client
	token string
}

// WebConfig is the credentials and address for the internal API.
type WebConfig struct {
	APIURL   string
	OmadacID string
	Username string
	Password string
	Insecure bool
	Timeout  time.Duration
}

// NewWeb prepares a client with a cookie jar. It does not log in; the first
// request does that.
func NewWeb(cfg WebConfig) (*WebClient, error) {
	if cfg.APIURL == "" {
		return nil, errors.New("api url is empty")
	}
	if cfg.OmadacID == "" {
		return nil, errors.New("omadac id is empty")
	}
	if cfg.Username == "" || cfg.Password == "" {
		return nil, errors.New("username/password are empty: add them to the config to use the web API")
	}
	cfg.APIURL = strings.TrimRight(cfg.APIURL, "/")
	if cfg.Timeout == 0 {
		cfg.Timeout = 20 * time.Second
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if cfg.Insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	return &WebClient{cfg: cfg, hc: &http.Client{Transport: tr, Timeout: cfg.Timeout, Jar: jar}}, nil
}

// prefix is the path every internal endpoint hangs off.
func (w *WebClient) prefix() string {
	return w.cfg.APIURL + "/" + w.cfg.OmadacID + "/api/v2/"
}

// Login establishes the session. The session lives in the cookie jar, so this
// is called once per process rather than cached to disk — a session cookie is
// a bearer credential with a controller-side idle timeout, and re-logging in
// costs one request.
func (w *WebClient) Login(ctx context.Context) error {
	body, err := json.Marshal(map[string]string{
		"username": w.cfg.Username,
		"password": w.cfg.Password,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.prefix()+"login", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	raw, err := w.send(req, "login")
	if err != nil {
		return err
	}
	var res struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("decoding login result: %w", err)
	}
	if res.Token == "" {
		return errors.New("controller returned an empty session token")
	}
	w.token = res.Token
	return nil
}

// Get calls an internal API path relative to /{omadacId}/api/v2/, logging in
// first if needed.
func (w *WebClient) Get(ctx context.Context, path string, query url.Values) (json.RawMessage, error) {
	if w.token == "" {
		if err := w.Login(ctx); err != nil {
			return nil, err
		}
	}
	u := w.prefix() + strings.TrimLeft(path, "/")
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Csrf-Token", w.token)
	return w.send(req, path)
}

// GetAll is Get with paging. The internal API names its paging parameters
// currentPage/currentPageSize rather than page/pageSize.
func (w *WebClient) GetAll(ctx context.Context, path string, query url.Values) (Result, error) {
	return getAllPaged(ctx, w.Get, path, query, "currentPage", "currentPageSize")
}

// send shares the envelope handling in client.go, so a non-zero errorCode
// surfaces as the same *APIError either API surface produces.
func (w *WebClient) send(req *http.Request, path string) (json.RawMessage, error) {
	shim := &Client{hc: w.hc}
	return shim.send(req, path)
}
