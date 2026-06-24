package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"
)

// handleNodeProxy reverse-proxies requests under /nodes/{id}/... to the matching satellite node.
func (s *server) handleNodeProxy(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("id")
	if nodeID == "" {
		http.Error(w, "node id is required", http.StatusBadRequest)
		return
	}

	if s.nodes == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "only_master_mode_supports_proxy",
		})
		return
	}

	n := s.nodes.getNode(nodeID)
	if n == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "node_not_found",
			"node":  nodeID,
		})
		return
	}

	if !n.Online {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "node_offline",
			"node":  nodeID,
		})
		return
	}

	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = "http"
			req.URL.Host = n.Addr
			req.Host = n.Addr

			// Strip prefix `/nodes/{id}` (e.g. `/nodes/kitchen`).
			prefix := "/nodes/" + nodeID
			if strings.HasPrefix(req.URL.Path, prefix) {
				req.URL.Path = req.URL.Path[len(prefix):]
			}
			if req.URL.Path == "" {
				req.URL.Path = "/"
			}

			if req.URL.RawPath != "" && strings.HasPrefix(req.URL.RawPath, prefix) {
				req.URL.RawPath = req.URL.RawPath[len(prefix):]
			}
			if req.URL.RawPath == "" {
				req.URL.RawPath = ""
			}
		},
		Transport: s.proxyTransport,
		ModifyResponse: func(resp *http.Response) error {
			// Translate any 5xx error returned by the satellite into a 504 with JSON.
			if resp.StatusCode >= 500 {
				if resp.Body != nil {
					_ = resp.Body.Close()
				}
				bodyMap := map[string]string{
					"error": "bluetooth_subsystem",
					"node":  nodeID,
				}
				bodyBytes, _ := json.Marshal(bodyMap)
				resp.Body = io.NopCloser(bytes.NewReader(bodyBytes))
				resp.ContentLength = int64(len(bodyBytes))
				resp.Header.Set("Content-Type", "application/json")
				resp.Header.Set("Content-Length", strconv.Itoa(len(bodyBytes)))
				resp.StatusCode = http.StatusGatewayTimeout
				resp.Status = http.StatusText(http.StatusGatewayTimeout)
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			// Dial failure or timeout during proxy request -> 504.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusGatewayTimeout)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "bluetooth_subsystem",
				"node":  nodeID,
			})
		},
	}

	proxy.ServeHTTP(w, r)
}
