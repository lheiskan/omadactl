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
	"encoding/json"
	"fmt"
	"strings"
)

// Site is the subset of a site record needed to address the other endpoints.
// The two API surfaces disagree on the key — the Open API says siteId, the
// internal API says id — so both are decoded and ID() picks whichever came.
type Site struct {
	SiteID string `json:"siteId"`
	AltID  string `json:"id"`
	Name   string `json:"name"`
}

// ID is the site identifier to put in a URL path.
func (s Site) ID() string {
	if s.SiteID != "" {
		return s.SiteID
	}
	return s.AltID
}

// Sites lists the sites this Open API application can see.
func (c *Client) Sites(ctx context.Context) ([]Site, error) {
	res, err := c.GetAll(ctx, "sites", nil)
	if err != nil {
		return nil, err
	}
	return decodeSites(res), nil
}

// Sites lists the sites this account can see, over the internal API.
func (w *WebClient) Sites(ctx context.Context) ([]Site, error) {
	res, err := w.GetAll(ctx, "sites", nil)
	if err != nil {
		return nil, err
	}
	return decodeSites(res), nil
}

func decodeSites(res Result) []Site {
	out := make([]Site, 0, len(res.Rows))
	for _, row := range res.Rows {
		var s Site
		if err := json.Unmarshal(row, &s); err != nil {
			continue
		}
		if s.ID() != "" {
			out = append(out, s)
		}
	}
	return out
}

// ResolveSite turns a site name, a site id, or "" into a site id.
func (c *Client) ResolveSite(ctx context.Context, nameOrID string) (string, error) {
	sites, err := c.Sites(ctx)
	if err != nil {
		return "", err
	}
	return resolveSite(sites, nameOrID)
}

// ResolveSite turns a site name, a site id, or "" into a site id.
func (w *WebClient) ResolveSite(ctx context.Context, nameOrID string) (string, error) {
	sites, err := w.Sites(ctx)
	if err != nil {
		return "", err
	}
	return resolveSite(sites, nameOrID)
}

// resolveSite treats "" as "the only site", which is the common case on a home
// network. More than one site is an error rather than a guess, so a silently
// wrong-site answer is impossible.
func resolveSite(sites []Site, nameOrID string) (string, error) {
	if len(sites) == 0 {
		return "", fmt.Errorf("no sites visible to this account (check its site scope in the controller)")
	}
	if nameOrID == "" {
		if len(sites) > 1 {
			names := make([]string, 0, len(sites))
			for _, s := range sites {
				names = append(names, s.Name)
			}
			return "", fmt.Errorf("controller has %d sites (%s): pass -site", len(sites), strings.Join(names, ", "))
		}
		return sites[0].ID(), nil
	}
	for _, s := range sites {
		if s.ID() == nameOrID || strings.EqualFold(s.Name, nameOrID) {
			return s.ID(), nil
		}
	}
	return "", fmt.Errorf("no site named or identified by %q", nameOrID)
}
