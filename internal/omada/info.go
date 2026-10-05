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
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// Info is the unauthenticated controller identity endpoint. It is the only
// call that works before an Open API application exists, and it is where the
// omadacId needed by every other call comes from.
type Info struct {
	ControllerVer string `json:"controllerVer"`
	APIVer        string `json:"apiVer"`
	Configured    bool   `json:"configured"`
	Type          int    `json:"type"`
	OmadacID      string `json:"omadacId"`
	OmadaCloudURL string `json:"omadaCloudUrl"`
}

// FetchInfo calls GET {controller}/api/info.
func FetchInfo(ctx context.Context, controller string, insecure bool) (*Info, error) {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	c := &Client{
		cfg: Config{Controller: strings.TrimRight(controller, "/")},
		hc:  &http.Client{Transport: tr, Timeout: 15 * time.Second},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.Controller+"/api/info", nil)
	if err != nil {
		return nil, err
	}
	raw, err := c.send(req, "/api/info")
	if err != nil {
		return nil, err
	}
	var info Info
	if err := json.Unmarshal(raw, &info); err != nil {
		return nil, err
	}
	return &info, nil
}
