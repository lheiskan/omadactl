// Copyright (C) 2026 Lauri Heiskanen
// SPDX-License-Identifier: GPL-3.0-or-later
//
// This program is free software: you can redistribute it and/or modify it
// under the terms of the GNU General Public License as published by the Free
// Software Foundation, either version 3 of the License, or (at your option)
// any later version. It is distributed WITHOUT ANY WARRANTY; see the GNU
// General Public License in LICENSE for details.

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// fileConfig is ~/.config/omada/config.json. Every field can be overridden by
// an OMADA_* environment variable, and the controller URL additionally by a
// flag, so credentials stay out of shell history and process listings.
type fileConfig struct {
	Controller   string `json:"controller"`
	OmadacID     string `json:"omadac_id"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	// Username/password are a controller account, used only by the web-API
	// commands. A Viewer-role account is enough for everything omadactl reads.
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Insecure *bool  `json:"insecure,omitempty"`
	Site     string `json:"site,omitempty"`
}

// configPath and cacheDir follow the XDG convention rather than Go's
// os.UserConfigDir/os.UserCacheDir, which on macOS point into
// ~/Library/Application Support and ~/Library/Caches. A hand-edited config for
// a CLI belongs somewhere a person would think to look.
func configPath() string {
	if p := os.Getenv("OMADA_CONFIG"); p != "" {
		return p
	}
	return filepath.Join(xdgDir("XDG_CONFIG_HOME", ".config"), "omada", "config.json")
}

func cacheDir() string {
	return filepath.Join(xdgDir("XDG_CACHE_HOME", ".cache"), "omada")
}

func xdgDir(env, fallback string) string {
	if v := os.Getenv(env); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, fallback)
}

func loadConfig() (fileConfig, error) {
	var fc fileConfig
	path := configPath()
	if path != "" {
		raw, err := os.ReadFile(path)
		switch {
		case err == nil:
			if err := json.Unmarshal(raw, &fc); err != nil {
				return fc, fmt.Errorf("%s: %w", path, err)
			}
		case !os.IsNotExist(err):
			return fc, err
		}
	}
	if v := os.Getenv("OMADA_CONTROLLER"); v != "" {
		fc.Controller = v
	}
	if v := os.Getenv("OMADA_ID"); v != "" {
		fc.OmadacID = v
	}
	if v := os.Getenv("OMADA_CLIENT_ID"); v != "" {
		fc.ClientID = v
	}
	if v := os.Getenv("OMADA_CLIENT_SECRET"); v != "" {
		fc.ClientSecret = v
	}
	if v := os.Getenv("OMADA_USERNAME"); v != "" {
		fc.Username = v
	}
	if v := os.Getenv("OMADA_PASSWORD"); v != "" {
		fc.Password = v
	}
	if v := os.Getenv("OMADA_SITE"); v != "" {
		fc.Site = v
	}
	if v := os.Getenv("OMADA_INSECURE"); v != "" {
		b := v == "1" || strings.EqualFold(v, "true")
		fc.Insecure = &b
	}
	return fc, nil
}

// insecureDefault: controllers ship a self-signed certificate, so verification
// is off unless the user has installed a real one and says so.
func (fc fileConfig) insecure() bool {
	if fc.Insecure == nil {
		return true
	}
	return *fc.Insecure
}
