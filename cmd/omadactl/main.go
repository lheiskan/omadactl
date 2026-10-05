// Copyright (C) 2026 Lauri Heiskanen
// SPDX-License-Identifier: GPL-3.0-or-later
//
// This program is free software: you can redistribute it and/or modify it
// under the terms of the GNU General Public License as published by the Free
// Software Foundation, either version 3 of the License, or (at your option)
// any later version. It is distributed WITHOUT ANY WARRANTY; see the GNU
// General Public License in LICENSE for details.

// Command omadactl reads a TP-Link Omada controller and prints JSON on stdout.
// It is built to be called by an LLM agent: every subcommand emits one JSON
// document, errors go to stderr with a non-zero exit, and nothing is written
// anywhere except a cached access token.
//
// Two API surfaces are available. The Open API (client_id/client_secret) is
// versioned and stable but exposes a limited endpoint set. The internal web API
// (username/password) is what the controller's own UI calls: far richer, but
// unversioned and liable to change with firmware. Commands pick whichever one
// carries the data they need.
//
// Configuration lives in ~/.config/omada/config.json (see config.go) or
// OMADA_* environment variables.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/lheiskan/omadactl/internal/omada"
)

const usage = `omadactl reads an Omada controller and prints JSON.

usage: omadactl [flags] <command> [args]

commands using the Open API (need client_id/client_secret):
  info              controller identity and omadacId (needs no credentials)
  token             fetch an access token and report its expiry
  sites             list the sites this application can see
  clients           list connected clients, with rssi/rate/ap/traffic
  client <mac>      full detail for one client
  devices           list adopted devices (APs, switches, gateway)
  ap <mac>          full detail for one access point, incl. radio settings
  insight           known clients seen historically, with last-seen and totals
  events            site event log (-since, default 24h)
                    optional: -query 'filters.module=System|Device|Client'
  get <path>        GET any Open API path under /openapi/v1/{omadacId}
                    e.g. get 'sites/{site}/clients'   ({site} is substituted)

commands using the internal web API (need username/password):
  alerts            site alert log
  dashboard         site overview counters
  web <path>        GET any internal path under /{omadacId}/api/v2/
                    e.g. web 'sites/{site}/eaps/AA-BB-CC-DD-EE-FF'

flags:
  -controller URL   controller base URL (default: from config)
  -site NAME|ID     site to query (default: the only site)
  -since DURATION   time window for 'events' and 'alerts' (default 24h)
  -insecure         skip TLS verification (default true; controllers self-sign)
  -compact          emit one-line JSON instead of indented
  -query k=v        extra query parameter for 'get' and 'web', repeatable

Flags may appear before or after the command.
`

// webCommands are served by the internal API rather than the Open API.
var webCommands = map[string]bool{
	"alerts":    true,
	"dashboard": true,
	"web":       true,
}

type queryFlag url.Values

func (q queryFlag) String() string { return "" }

func (q queryFlag) Set(v string) error {
	k, val, ok := strings.Cut(v, "=")
	if !ok {
		return fmt.Errorf("expected key=value, got %q", v)
	}
	url.Values(q).Add(k, val)
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error: "+err.Error())
		os.Exit(1)
	}
}

func run() error {
	fs := flag.NewFlagSet("omadactl", flag.ContinueOnError)
	var (
		controller = fs.String("controller", "", "controller base URL, e.g. https://192.168.1.10:443")
		site       = fs.String("site", "", "site name or id (default: the only site)")
		insecure   = fs.Bool("insecure", true, "skip TLS verification")
		compact    = fs.Bool("compact", false, "emit one-line JSON")
		since      = fs.Duration("since", 24*time.Hour, "time window for 'events' and 'alerts'")
		query      = url.Values{}
	)
	fs.Var(queryFlag(query), "query", "extra query parameter for 'get' and 'web', as key=value")
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }

	// Go's flag package stops at the first non-flag argument, which would
	// silently drop `omadactl get <path> -query k=v` — the natural ordering,
	// and one whose failure looks like a server error rather than a dropped
	// flag. Parse repeatedly instead, peeling off one positional each time, so
	// flags may appear anywhere.
	var args []string
	rest := os.Args[1:]
	for {
		if err := fs.Parse(rest); err != nil {
			return err
		}
		rest = fs.Args()
		if len(rest) == 0 {
			break
		}
		args = append(args, rest[0])
		rest = rest[1:]
	}

	if len(args) == 0 {
		fs.Usage()
		return errors.New("no command given")
	}

	fc, err := loadConfig()
	if err != nil {
		return err
	}
	if *controller != "" {
		fc.Controller = *controller
	}
	if *site != "" {
		fc.Site = *site
	}
	// An explicitly passed -insecure=false must win over the config default.
	if isFlagSet(fs, "insecure") {
		fc.Insecure = insecure
	}
	if fc.Controller == "" {
		return fmt.Errorf("no controller URL: pass -controller or set it in %s", configPath())
	}

	out := &emitter{compact: *compact}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := args[0]

	// info needs no credentials, so handle it before anything is constructed.
	if cmd == "info" {
		info, err := omada.FetchInfo(ctx, fc.Controller, fc.insecure())
		if err != nil {
			return err
		}
		return out.emit(info)
	}

	if fc.OmadacID == "" {
		return fmt.Errorf("no omadac id: run 'omadactl info' and put its omadacId in %s", configPath())
	}
	if webCommands[cmd] {
		return runWeb(ctx, args, fc, query, *since, out)
	}
	return runOpen(ctx, args, fc, query, *since, out)
}

// runOpen serves the commands backed by the Open API.
func runOpen(ctx context.Context, args []string, fc fileConfig, query url.Values, since time.Duration, out *emitter) error {
	cmd := args[0]
	c, err := omada.New(omada.Config{
		Controller:   fc.Controller,
		OmadacID:     fc.OmadacID,
		ClientID:     fc.ClientID,
		ClientSecret: fc.ClientSecret,
		Insecure:     fc.insecure(),
		CacheDir:     cacheDir(),
	})
	if err != nil {
		return err
	}

	switch cmd {
	case "token":
		tok, exp, err := c.Token(ctx)
		if err != nil {
			return err
		}
		return out.emit(map[string]any{
			"ok":         true,
			"expires_at": exp.Format(time.RFC3339),
			"expires_in": int(time.Until(exp).Seconds()),
			// The token itself is a full-access credential; report only a stub.
			"token_prefix": tok[:min(6, len(tok))] + "...",
		})

	case "sites":
		res, err := c.GetAll(ctx, "sites", nil)
		if err != nil {
			return err
		}
		return out.emitList(res)

	case "clients", "devices":
		siteID, err := c.ResolveSite(ctx, fc.Site)
		if err != nil {
			return err
		}
		res, err := c.GetAll(ctx, "sites/"+siteID+"/"+cmd, nil)
		if err != nil {
			return err
		}
		return out.emitList(res)

	case "insight":
		siteID, err := c.ResolveSite(ctx, fc.Site)
		if err != nil {
			return err
		}
		res, err := c.GetAll(ctx, "sites/"+siteID+"/insight/clients", nil)
		if err != nil {
			return err
		}
		return out.emitList(res)

	case "events":
		siteID, err := c.ResolveSite(ctx, fc.Site)
		if err != nil {
			return err
		}
		// Documented as required: the endpoint rejects a call without an
		// explicit window. page/pageSize are added by GetAll.
		now := time.Now()
		query.Set("filters.timeStart", strconv.FormatInt(now.Add(-since).UnixMilli(), 10))
		query.Set("filters.timeEnd", strconv.FormatInt(now.UnixMilli(), 10))
		res, err := c.GetAll(ctx, "sites/"+siteID+"/logs/events", query)
		if err != nil {
			return err
		}
		return out.emitList(res)

	case "client", "ap":
		if len(args) < 2 {
			return fmt.Errorf("%s needs a MAC address", cmd)
		}
		siteID, err := c.ResolveSite(ctx, fc.Site)
		if err != nil {
			return err
		}
		sub := map[string]string{"client": "clients", "ap": "aps"}[cmd]
		res, err := c.GetAll(ctx, "sites/"+siteID+"/"+sub+"/"+normalizeMAC(args[1]), nil)
		if err != nil {
			return err
		}
		return out.emitResult(res)

	case "get":
		if len(args) < 2 {
			return errors.New("get needs a path, e.g. get 'sites/{site}/clients'")
		}
		path := args[1]
		if strings.Contains(path, "{site}") {
			siteID, err := c.ResolveSite(ctx, fc.Site)
			if err != nil {
				return err
			}
			path = strings.ReplaceAll(path, "{site}", siteID)
		}
		res, err := c.GetAll(ctx, path, query)
		if err != nil {
			return err
		}
		return out.emitResult(res)

	default:
		flag.Usage()
		return fmt.Errorf("unknown command %q", cmd)
	}
}

// runWeb serves the commands backed by the controller's internal API.
func runWeb(ctx context.Context, args []string, fc fileConfig, query url.Values, since time.Duration, out *emitter) error {
	cmd := args[0]
	wc, err := omada.NewWeb(omada.WebConfig{
		Controller: fc.Controller,
		OmadacID:   fc.OmadacID,
		Username:   fc.Username,
		Password:   fc.Password,
		Insecure:   fc.insecure(),
	})
	if err != nil {
		return err
	}
	siteID, err := wc.ResolveSite(ctx, fc.Site)
	if err != nil {
		return err
	}

	var path string
	switch cmd {
	case "alerts":
		path = "sites/" + siteID + "/alerts"
		// The log endpoints take an explicit window in epoch milliseconds.
		now := time.Now()
		query.Set("filters.timeStart", strconv.FormatInt(now.Add(-since).UnixMilli(), 10))
		query.Set("filters.timeEnd", strconv.FormatInt(now.UnixMilli(), 10))

	case "dashboard":
		path = "sites/" + siteID + "/dashboard/overviewDiagram"

	case "web":
		if len(args) < 2 {
			return errors.New("web needs a path, e.g. web 'sites/{site}/eaps/AA-BB-CC-DD-EE-FF'")
		}
		path = strings.ReplaceAll(args[1], "{site}", siteID)
	}

	res, err := wc.GetAll(ctx, path, query)
	if err != nil {
		return err
	}
	if cmd == "alerts" {
		return out.emitList(res)
	}
	return out.emitResult(res)
}

// normalizeMAC accepts colon- or dash-separated MACs; the controller writes
// them as AA-BB-CC-DD-EE-FF.
func normalizeMAC(s string) string {
	return strings.ToUpper(strings.ReplaceAll(s, ":", "-"))
}

func isFlagSet(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

type emitter struct{ compact bool }

func (e *emitter) emit(v any) error {
	enc := json.NewEncoder(os.Stdout)
	if !e.compact {
		enc.SetIndent("", "  ")
	}
	return enc.Encode(v)
}

// emitResult prints whatever shape the endpoint returned: an array for a
// collection, the object itself for a single thing.
func (e *emitter) emitResult(res omada.Result) error {
	if !res.IsList {
		return e.emit(res.Raw)
	}
	return e.emitList(res)
}

// emitList always prints a JSON array, even for zero or one row. List
// commands must not change shape with the row count, or every consumer needs
// a special case for "exactly one client is connected".
func (e *emitter) emitList(res omada.Result) error {
	rows := res.Rows
	if !res.IsList {
		rows = []json.RawMessage{res.Raw}
	}
	if rows == nil {
		rows = []json.RawMessage{}
	}
	return e.emit(rows)
}
