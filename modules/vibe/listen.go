// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package vibe

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/harness/cli/pkg/auth"
	"github.com/harness/cli/pkg/cmdctx"
)

const defaultListenAddr = "127.0.0.1:17373"

func runListenBridge(ctx *cmdctx.Ctx, a *auth.ResolvedAuth) error {
	addr := os.Getenv("VIBE_LAUNCH_LISTEN_ADDR")
	if addr == "" {
		addr = defaultListenAddr
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		setCORS(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("/launch", func(w http.ResponseWriter, r *http.Request) {
		setCORS(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var req launchRequest
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		if req.Host == "" {
			req.Host = "cursor"
		}
		if req.AppID == "" || req.ExecutionID == "" {
			http.Error(w, "appId and executionId are required", http.StatusBadRequest)
			return
		}
		fmt.Printf("Launch request app=%s execution=%s host=%s\n", req.AppID, req.ExecutionID, req.Host)
		if err := runLaunch(a, req, false); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
			fmt.Fprintf(os.Stderr, "launch failed: %v\n", err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	server := &http.Server{Addr: addr, Handler: mux}
	fmt.Printf("Vibe Fix in IDE bridge listening on http://%s\n", addr)
	fmt.Println("POST /launch  { appId, executionId, host }")
	fmt.Println("GET  /health")
	fmt.Println("CORS allowlist: http://localhost:4200")

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()
	waitCtx := ctx.Context
	if waitCtx == nil {
		waitCtx = context.Background()
	}
	select {
	case <-waitCtx.Done():
		_ = server.Close()
		return waitCtx.Err()
	case sig := <-stop:
		_ = server.Close()
		fmt.Printf("Stopped (%s).\n", sig)
		return nil
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

func setCORS(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if originAllowed(origin) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
	}
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

func originAllowed(origin string) bool {
	if origin == "" {
		return false
	}
	allowed := []string{"http://localhost:4200", "http://127.0.0.1:4200"}
	if extra := os.Getenv("VIBE_LISTEN_CORS_ORIGIN"); extra != "" {
		allowed = append(allowed, extra)
	}
	for _, a := range allowed {
		if strings.EqualFold(origin, a) {
			return true
		}
	}
	return false
}
