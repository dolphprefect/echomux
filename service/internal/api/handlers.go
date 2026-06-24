package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dolphprefect/echomux/internal/audio"
	"github.com/dolphprefect/echomux/internal/bluetooth"
	"nhooyr.io/websocket"
)

// SpawnFn builds an exec.Cmd for a pw-loopback process (not yet started).
type SpawnFn func(nodeName string, delayMS int) *exec.Cmd

type server struct {
	bt    bluetooth.Manager
	audio audio.Controller
	hub   *hub
	mux   *http.ServeMux

	mode       Mode
	name       string
	masterAddr string
	selfAddr   string // satellite only: public host:port reported in register message
	clientCtx  context.Context
	rtpPort    int

	nodes             *nodeRegistry
	heartbeatInterval time.Duration
	pongTimeout       time.Duration

	mu                sync.Mutex
	stateMu           sync.Mutex               // serialises saveState writes to disk
	speakers             map[string]*speakerState // MAC → active loopback
	delays               map[string]int           // MAC → delay_ms (persisted)
	volumes              map[string]int           // MAC → volume 0-100 (persisted)
	mutes                map[string]bool          // MAC → muted (persisted)
	knownSpeakers        map[string]bool          // MACs that have registered as BT A2DP sinks (persisted)
	connectedSpeakers    map[string]bool          // MACs that had live loopbacks at last save (persisted)
	watchdogRestarts  map[string]time.Time     // MAC → last watchdog-triggered restart time
	paused            bool                     // when true, tickRouter is a no-op
	stateFile         string
	spawn             SpawnFn
	debug             bool
	saveTimerMu       sync.Mutex
	saveTimer         *time.Timer              // debounces rapid saveState calls
	satPushCh         chan struct{}
	proxyTransport    *http.Transport
}

func (s *server) dbg(format string, args ...any) {
	if s.debug {
		log.Printf("[DBG] "+format, args...)
	}
}

// Option configures a server.
type Option func(*server)

func WithStateFile(path string) Option      { return func(s *server) { s.stateFile = path } }
func WithSpawn(fn SpawnFn) Option           { return func(s *server) { s.spawn = fn } }
func WithDebug(debug bool) Option           { return func(s *server) { s.debug = debug } }
func WithMode(m Mode) Option                { return func(s *server) { s.mode = m } }
func WithName(n string) Option              { return func(s *server) { s.name = n } }
func WithMasterAddr(addr string) Option     { return func(s *server) { s.masterAddr = addr } }
func WithRTPPort(port int) Option           { return func(s *server) { s.rtpPort = port } }
func WithSelfAddr(addr string) Option       { return func(s *server) { s.selfAddr = addr } }

// WithClientContext provides a context for the satellite client goroutine.
// The satellite client is only started when this option is set and mode is satellite.
// Pass the application's signal context from main; pass a test-cancellable context in tests.
func WithClientContext(ctx context.Context) Option { return func(s *server) { s.clientCtx = ctx } }

// WithHeartbeat overrides the default heartbeat intervals — intended for tests.
func WithHeartbeat(interval, pongTimeout time.Duration) Option {
	return func(s *server) {
		s.heartbeatInterval = interval
		s.pongTimeout = pongTimeout
	}
}

// WithKnownSpeakers pre-seeds the knownSpeakers set — intended for tests.
func WithKnownSpeakers(macs ...string) Option {
	return func(s *server) {
		for _, mac := range macs {
			s.knownSpeakers[mac] = true
		}
	}
}

func NewServer(bt bluetooth.Manager, audio audio.Controller, opts ...Option) http.Handler {
	s := &server{
		bt:                bt,
		audio:             audio,
		hub:               newHub(),
		speakers:          make(map[string]*speakerState),
		delays:            make(map[string]int),
		volumes:           make(map[string]int),
		mutes:             make(map[string]bool),
		knownSpeakers:     make(map[string]bool),
		connectedSpeakers: make(map[string]bool),
		watchdogRestarts:  make(map[string]time.Time),
		spawn:             defaultSpawn,
		mode:              ModeStandalone,
		rtpPort:           9001,
		heartbeatInterval: 10 * time.Second,
		pongTimeout:       5 * time.Second,
		satPushCh:         make(chan struct{}, 1),
		proxyTransport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   2,
			IdleConnTimeout:       30 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			ResponseHeaderTimeout: 5 * time.Second,
		},
	}
	for _, o := range opts {
		o(s)
	}
	// Resolve hostname as a last-resort name — covers callers that omit WithName
	// (e.g. tests and programmatic use). main.go passes defaultName() via WithName,
	// so this branch fires only when WithName is absent or explicitly set to "".
	if s.name == "" {
		if h, err := os.Hostname(); err == nil {
			s.name = h
		}
	}
	if s.mode == ModeMaster {
		s.nodes = newNodeRegistry()
		if err := s.audio.CleanOrphanRTPModules(context.Background(), s.rtpPort); err != nil {
			log.Printf("failed to clean orphan RTP modules: %v", err)
		}
	}
	if s.stateFile != "" {
		s.loadState()
	}
	s.mux = http.NewServeMux()
	s.routes()
	go s.hub.RunEventForwarder(context.Background(), bt.Events())
	s.startAutoRouter(context.Background(), 2*time.Second)
	if s.mode == ModeSatellite && s.masterAddr != "" && s.clientCtx != nil {
		go s.runSatelliteClient(s.clientCtx)
	}
	go s.startupConnect(context.Background())
	return s.mux
}

func defaultSpawn(nodeName string, delayMS int) *exec.Cmd {
	delayS := float64(delayMS) / 1000.0
	cmd := exec.Command("pw-loopback",
		"--capture", "main-mix-source",
		"--playback", nodeName,
		"--latency", "200",
		"--delay", fmt.Sprintf("%.3f", delayS),
	)
	cmd.Env = append(os.Environ(), "PIPEWIRE_RUNTIME_DIR=/run/pipewire")
	return cmd
}

type savedState struct {
	Delays             map[string]int  `json:"delays"`
	Volumes            map[string]int  `json:"volumes"`
	Mutes              map[string]bool `json:"mutes"`
	KnownSpeakers      map[string]bool `json:"known_speakers"`
	ConnectedSpeakers  map[string]bool `json:"connected_speakers"`
}

func (s *server) loadState() {
	data, err := os.ReadFile(s.stateFile)
	if err != nil {
		return
	}
	var st savedState
	if err := json.Unmarshal(data, &st); err != nil {
		return
	}
	if st.Delays != nil {
		s.delays = st.Delays
	}
	if st.Volumes != nil {
		s.volumes = st.Volumes
	}
	if st.Mutes != nil {
		s.mutes = st.Mutes
	}
	if st.KnownSpeakers != nil {
		s.knownSpeakers = st.KnownSpeakers
	}
	if st.ConnectedSpeakers != nil {
		s.connectedSpeakers = st.ConnectedSpeakers
	}
}

func (s *server) saveState() {
	if s.stateFile == "" {
		return
	}
	s.mu.Lock()
	// Deep-copy maps before releasing the lock: json.Marshal iterates them
	// and concurrent writes (handleVolume, etc.) would cause a data race.
	connected := make(map[string]bool, len(s.speakers))
	for mac, sp := range s.speakers {
		if !speakerDead(sp) {
			connected[mac] = true
		}
	}
	st := savedState{
		Delays:            copyIntMap(s.delays),
		Volumes:           copyIntMap(s.volumes),
		Mutes:             copyBoolMap(s.mutes),
		KnownSpeakers:     copyBoolMap(s.knownSpeakers),
		ConnectedSpeakers: connected,
	}
	s.mu.Unlock()
	data, err := json.Marshal(st)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(s.stateFile), 0755)
	// Atomic write: write to a temp file then rename so readers never see a partial file.
	// stateMu serialises concurrent goroutine saves so no goroutine overwrites a fresher snapshot.
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	tmp := s.stateFile + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return
	}
	_ = os.Rename(tmp, s.stateFile)
}

// saveStateDebounced schedules a state write, coalescing rapid successive
// calls (e.g. from a volume slider drag) into a single write ~200 ms after
// the last change. This replaces naked go s.saveState() calls. Uses its own
// mutex so it is safe to call from within tickRouter (which holds s.mu).
func (s *server) saveStateDebounced() {
	s.saveTimerMu.Lock()
	if s.saveTimer != nil {
		s.saveTimer.Stop()
	}
	s.saveTimer = time.AfterFunc(200*time.Millisecond, s.saveState)
	s.saveTimerMu.Unlock()
}

func (s *server) triggerSatellitePush() {
	if s.mode != ModeSatellite {
		return
	}
	select {
	case s.satPushCh <- struct{}{}:
	default:
	}
}

func copyIntMap(m map[string]int) map[string]int {
	cp := make(map[string]int, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

func copyBoolMap(m map[string]bool) map[string]bool {
	cp := make(map[string]bool, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.ResponseWriter.(http.Hijacker).Hijack()
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *server) routes() {
	mux := s.mux
	mux.Handle("/", staticHandler())
	mux.HandleFunc("GET /nodes", s.handleNodes)
	mux.HandleFunc("/nodes/{id}/", s.handleNodeProxy)
	mux.HandleFunc("GET /devices", s.handleGetDevices)
	mux.HandleFunc("POST /scan", s.handleScan)
	mux.HandleFunc("POST /devices/{mac}/connect", s.handleConnect)
	mux.HandleFunc("POST /devices/{mac}/disconnect", s.handleDisconnect)
	mux.HandleFunc("POST /devices/{mac}/pair", s.handlePair)
	mux.HandleFunc("DELETE /devices/{mac}", s.handleForget)
	mux.HandleFunc("PUT /devices/{mac}/volume", s.handleVolume)
	mux.HandleFunc("PUT /devices/{mac}/mute", s.handleMute)
	mux.HandleFunc("PUT /devices/{mac}/delay", s.handleDelay)
	mux.HandleFunc("POST /playback/pause", s.handlePause)
	mux.HandleFunc("POST /playback/resume", s.handleResume)
	mux.HandleFunc("POST /playback/restart", s.handleRestart)
	mux.HandleFunc("GET /input", s.handleGetInput)
	mux.HandleFunc("POST /input/discover", s.handleInputDiscover)
	mux.HandleFunc("GET /stream", s.handleStream)
	mux.HandleFunc("/events", s.handleEvents)

	if s.debug {
		// Replace mux with a logging wrapper so every request is traced.
		original := s.mux
		logging := http.NewServeMux()
		logging.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			sw := &statusWriter{ResponseWriter: w, status: 200}
			start := time.Now()
			original.ServeHTTP(sw, r)
			log.Printf("[HTTP] %s %s → %d (%s)", r.Method, r.URL.Path, sw.status, time.Since(start).Round(time.Millisecond))
		})
		s.mux = logging
	}
}

func (s *server) mac(r *http.Request) string {
	return r.PathValue("mac")
}

func (s *server) nodeIDForMAC(ctx context.Context, mac string) (int, error) {
	nodes, err := s.audio.Nodes(ctx)
	if err != nil {
		return -1, err
	}
	for _, n := range nodes {
		if n.MAC == mac {
			return n.ID, nil
		}
	}
	return -1, nil
}


type deviceInfo struct {
	bluetooth.Device
	NodeID  string `json:"node_id,omitempty"`
	DelayMs int    `json:"delay_ms"`
	Volume  int    `json:"volume"`
	Muted   bool   `json:"muted"`
	Playing bool   `json:"playing"`
}

func (s *server) handleGetDevices(w http.ResponseWriter, r *http.Request) {
	devs, err := s.bt.Devices(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.mu.Lock()
	var out []deviceInfo
	var pendingIdxs []int // indices into out[] where volume is unresolved
	for _, d := range devs {
		if s.knownSpeakers[d.MAC] {
			vol, hasVol := s.volumes[d.MAC]
			if !hasVol {
				pendingIdxs = append(pendingIdxs, len(out))
			}
			out = append(out, deviceInfo{
				Device:  d,
				DelayMs: s.delays[d.MAC],
				Volume:  vol,
				Muted:   s.mutes[d.MAC],
				Playing: !speakerDead(s.speakers[d.MAC]),
			})
		}
	}
	s.mu.Unlock()

	// Resolve actual PW volumes for devices with no stored volume.
	if len(pendingIdxs) > 0 {
		nodes, err := s.audio.Nodes(r.Context())
		if err != nil {
			log.Printf("GET /devices: Nodes() error: %v", err)
		}
		nodeByMAC := make(map[string]int, len(nodes))
		for _, n := range nodes {
			nodeByMAC[n.MAC] = n.ID
		}
		for _, idx := range pendingIdxs {
			mac := out[idx].MAC
			if nodeID, ok := nodeByMAC[mac]; ok {
				if v, err := s.audio.GetVolume(r.Context(), nodeID); err == nil && v >= 0 {
					out[idx].Volume = v
					continue
				}
			}
			out[idx].Volume = -1 // PipeWire node not yet created
		}
	}
	// In master mode, stamp local devices with the master's node_id and append
	// satellite devices from the in-memory registry cache.
	if s.mode == ModeMaster {
		masterNodeID := slugify(s.name)
		for i := range out {
			out[i].NodeID = masterNodeID
		}
		if s.nodes != nil {
			for _, n := range s.nodes.list() {
				if !n.Online || n.Devices == nil {
					continue
				}
				for _, d := range n.Devices {
					d.NodeID = n.ID
					out = append(out, d)
				}
			}
		}
	}
	if out == nil {
		out = []deviceInfo{}
	}
	for _, d := range out {
		s.dbg("GET /devices: %s name=%q connected=%v playing=%v vol=%d muted=%v",
			d.MAC, d.Name, d.Connected, d.Playing, d.Volume, d.Muted)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func (s *server) handleScan(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TimeoutSec int `json:"timeout_sec"`
	}
	body.TimeoutSec = 10
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.TimeoutSec <= 0 {
		body.TimeoutSec = 10
	}

	// Fully disconnect active BT speakers so the radio is free during discovery.
	// Classic BT inquiry and A2DP share the same radio and interfere when both run.
	s.mu.Lock()
	s.paused = true
	s.pauseAllLoopbacks()
	activeMacs := make([]string, 0, len(s.speakers))
	for mac := range s.speakers {
		activeMacs = append(activeMacs, mac)
	}
	s.mu.Unlock()

	for _, mac := range activeMacs {
		_ = s.bt.Disconnect(r.Context(), mac)
	}

	// Auto-unpause if the client disconnects before calling POST /playback/resume.
	// Normally the client manages pause/resume, but if the tab closes or the
	// network drops mid-scan the service would be stuck paused indefinitely.
	stop := context.AfterFunc(r.Context(), func() {
		s.mu.Lock()
		if s.paused {
			s.dbg("scan: client disconnected, auto-unpausing")
			s.paused = false
		}
		s.mu.Unlock()
	})
	defer stop()

	// Flush 200 + headers before the scan so reverse proxies with a short
	// ResponseHeaderTimeout (e.g. the master's node proxy) don't time out
	// waiting for headers while the scan is running.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	s.dbg("scan: starting %ds scan (paused loopbacks for %v)", body.TimeoutSec, activeMacs)
	scanErr := s.bt.Scan(r.Context(), time.Duration(body.TimeoutSec)*time.Second)
	s.dbg("scan: finished, err=%v", scanErr)

	// Do not reconnect or unpause here. The client manages the full
	// pause/resume lifecycle via POST /playback/resume (called when the
	// scan sheet closes). This avoids races between the reconnect goroutine
	// and any subsequent pair/connect calls the client may make.

	if scanErr != nil {
		// Unpause immediately on error: the context.AfterFunc callback only fires on
		// client disconnect, not on a scan failure while the connection is still open.
		s.mu.Lock()
		s.paused = false
		s.mu.Unlock()
		json.NewEncoder(w).Encode([]any{})
		return
	}
	devs, err := s.bt.Devices(r.Context())
	if err != nil {
		s.mu.Lock()
		s.paused = false
		s.mu.Unlock()
		json.NewEncoder(w).Encode([]any{})
		return
	}
	json.NewEncoder(w).Encode(devs)
}

func (s *server) handleConnect(w http.ResponseWriter, r *http.Request) {
	mac := s.mac(r)
	ctx := r.Context()

	// Retry up to 3 times: classic BT paging can fail under radio load when
	// multiple A2DP streams are already active (TDM slot contention).
	const maxAttempts = 3
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		s.dbg("connect %s: BT Connect attempt %d/%d", mac, attempt, maxAttempts)
		err := s.bt.Connect(ctx, mac)
		if err == nil {
			lastErr = nil
			break
		}
		// "Already connected" is success: just ensure the loopback is started.
		if strings.Contains(err.Error(), "AlreadyConnected") || strings.Contains(err.Error(), "Already Connected") {
			s.dbg("connect %s: already connected", mac)
			lastErr = nil
			break
		}
		lastErr = err
		s.dbg("connect %s: attempt %d failed: %v", mac, attempt, err)
		// DeviceNotFound is permanent; everything else (page timeout, radio busy) is transient.
		var nf *bluetooth.DeviceNotFoundError
		if errors.As(err, &nf) {
			break
		}
		if attempt < maxAttempts {
			select {
			case <-ctx.Done():
				http.Error(w, "request cancelled", http.StatusServiceUnavailable)
				return
			case <-time.After(2 * time.Second):
			}
		}
	}

	if lastErr != nil {
		s.dbg("connect %s: all attempts failed: %v", mac, lastErr)
		var nf *bluetooth.DeviceNotFoundError
		if errors.As(lastErr, &nf) {
			http.Error(w, lastErr.Error(), http.StatusNotFound)
			return
		}
		http.Error(w, lastErr.Error(), http.StatusInternalServerError)
		return
	}

	s.dbg("connect %s: BT connected, waiting for PW node", mac)
	// Always broadcast connected here — bt.Connect() only emits the event when
	// d.Connect() returns nil, so the "AlreadyConnected" success path is silent.
	go s.hub.broadcast(context.Background(), map[string]string{"type": "connected", "mac": mac})
	go func() {
		time.Sleep(2 * time.Second)
		for i := 0; i < 60; i++ {
			s.tickRouter(context.Background())
			time.Sleep(500 * time.Millisecond)
			s.mu.Lock()
			_, running := s.speakers[mac]
			s.mu.Unlock()
			if running {
				s.dbg("connect %s: loopback running after %d polls", mac, i+1)
				break
			}
		}
	}()

	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handlePause(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.paused = true
	s.pauseAllLoopbacks()
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleResume(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.paused = false
	s.mu.Unlock()
	s.tickRouter(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

// handleRestart kills all running loopbacks (without disconnecting BT) and lets
// tickRouter restart them on the next tick. Fixes silent/stuck loopbacks.
func (s *server) handleRestart(w http.ResponseWriter, r *http.Request) {
	s.dbg("restart: killing all loopbacks for forced restart")
	s.mu.Lock()
	for mac, sp := range s.speakers {
		if sp.cmd != nil && sp.cmd.Process != nil {
			s.dbg("restart: killing loopback for %s (pid=%d)", mac, sp.cmd.Process.Pid)
			_ = sp.cmd.Process.Kill()
		}
	}
	// Clear watchdog cooldowns: manual restart is the user's signal that
	// something is stuck, so the watchdog should act immediately after.
	s.watchdogRestarts = make(map[string]time.Time)
	s.mu.Unlock()
	// Kill any orphan pw-loopback processes not tracked by s.speakers (e.g. left
	// over from a crashed previous run that somehow persisted into this session).
	// This enforces the invariant: exactly one loopback per connected speaker.
	killOrphanLoopbacks()
	// tickRouter will detect dead processes and restart them within 2 seconds.
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	mac := s.mac(r)

	s.mu.Lock()
	s.stopLoopback(mac)
	s.mu.Unlock()

	if err := s.bt.Disconnect(r.Context(), mac); err != nil {
		var nf *bluetooth.DeviceNotFoundError
		if errors.As(err, &nf) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handlePair(w http.ResponseWriter, r *http.Request) {
	mac := s.mac(r)
	s.dbg("pair %s: start", mac)
	err := s.bt.Pair(r.Context(), mac)
	if err != nil {
		var nf *bluetooth.DeviceNotFoundError
		if errors.As(err, &nf) {
			// BlueZ clears non-paired devices from its cache when discovery stops.
			// Re-run a brief scan so BlueZ re-discovers the device, then retry.
			s.dbg("pair %s: not in BlueZ, re-scanning 6s to rediscover", mac)
			_ = s.bt.Scan(r.Context(), 6*time.Second)
			err = s.bt.Pair(r.Context(), mac)
			s.dbg("pair %s: retry after re-scan: %v", mac, err)
		}
	}
	if err != nil {
		// BlueZ returns "Already Exists" when the device is already paired;
		// treat this as success so a re-add after forget+reconnect flows cleanly.
		if strings.Contains(err.Error(), "Already Exists") || strings.Contains(err.Error(), "AlreadyExists") {
			s.dbg("pair %s: already paired, treating as success", mac)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var nf *bluetooth.DeviceNotFoundError
		if errors.As(err, &nf) {
			s.dbg("pair %s: device not found after re-scan", mac)
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		s.dbg("pair %s: error: %v", mac, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.dbg("pair %s: success", mac)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleForget(w http.ResponseWriter, r *http.Request) {
	mac := s.mac(r)

	// Remove from local state first so /devices stops returning this speaker.
	s.mu.Lock()
	s.stopLoopback(mac)
	delete(s.knownSpeakers, mac)
	delete(s.delays, mac)
	delete(s.volumes, mac)
	delete(s.mutes, mac)
	delete(s.watchdogRestarts, mac)
	s.mu.Unlock()
	s.saveStateDebounced()
	s.triggerSatellitePush()

	// Best-effort disconnect before removing from BlueZ.
	_ = s.bt.Disconnect(r.Context(), mac)

	// RemoveDevice unpairs and deletes from BlueZ. Treat DeviceNotFoundError as
	// success — the device was already gone from BlueZ (e.g. factory reset).
	if err := s.bt.Forget(r.Context(), mac); err != nil {
		var nf *bluetooth.DeviceNotFoundError
		if !errors.As(err, &nf) {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleVolume(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Level int `json:"level"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if body.Level < 0 || body.Level > 100 {
		http.Error(w, "level must be 0–100", http.StatusBadRequest)
		return
	}
	mac := s.mac(r)
	ctx := r.Context()
	nodeID, err := s.nodeIDForMAC(ctx, mac)
	if err != nil {
		s.dbg("handleVolume %s: nodeIDForMAC error: %v", mac, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if nodeID < 0 {
		s.dbg("handleVolume %s: PW node not found", mac)
		http.Error(w, "device not found", http.StatusNotFound)
		return
	}
	if s.debug {
		if before, gerr := s.audio.GetVolume(ctx, nodeID); gerr == nil {
			s.dbg("handleVolume %s: PW vol BEFORE: %d (node=%d)", mac, before, nodeID)
		}
	}
	s.dbg("handleVolume %s: SetVolume(nodeID=%d, level=%d)", mac, nodeID, body.Level)
	if err := s.audio.SetVolume(ctx, nodeID, body.Level); err != nil {
		s.dbg("handleVolume %s: SetVolume error: %v", mac, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if s.debug {
		if after, gerr := s.audio.GetVolume(ctx, nodeID); gerr == nil {
			s.dbg("handleVolume %s: PW vol AFTER: %d (match=%v)", mac, after, after == body.Level)
		}
	}
	s.mu.Lock()
	s.volumes[mac] = body.Level
	s.mu.Unlock()
	s.saveStateDebounced()
	s.triggerSatellitePush()
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleMute(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Muted bool `json:"muted"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	mac := s.mac(r)
	ctx := r.Context()
	nodeID, err := s.nodeIDForMAC(ctx, mac)
	if err != nil {
		s.dbg("handleMute %s: nodeIDForMAC error: %v", mac, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if nodeID < 0 {
		s.dbg("handleMute %s: PW node not found", mac)
		http.Error(w, "device not found", http.StatusNotFound)
		return
	}
	if s.debug {
		if before, gerr := s.audio.GetVolume(ctx, nodeID); gerr == nil {
			s.dbg("handleMute %s: PW vol/mute BEFORE: vol=%d (node=%d)", mac, before, nodeID)
		}
	}
	s.dbg("handleMute %s: SetMute(nodeID=%d, muted=%v)", mac, nodeID, body.Muted)
	if err := s.audio.SetMute(ctx, nodeID, body.Muted); err != nil {
		s.dbg("handleMute %s: SetMute error: %v", mac, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if s.debug {
		if after, gerr := s.audio.GetVolume(ctx, nodeID); gerr == nil {
			s.dbg("handleMute %s: PW vol/mute AFTER: vol=%d (muted confirmed via string check)", mac, after)
		}
	}
	s.mu.Lock()
	s.mutes[mac] = body.Muted
	s.mu.Unlock()
	s.saveStateDebounced()
	s.triggerSatellitePush()
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleDelay(w http.ResponseWriter, r *http.Request) {
	mac := s.mac(r)
	var body struct {
		Ms int `json:"ms"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if body.Ms < 0 || body.Ms > 2000 {
		http.Error(w, "ms must be 0–2000", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	prevDelay := s.delays[mac]
	s.delays[mac] = body.Ms
	// Restart loopback with updated delay if one is running.
	if sp, ok := s.speakers[mac]; ok {
		s.dbg("handleDelay %s: delay %dms→%dms, killing loopback pid=%d (%s)", mac, prevDelay, body.Ms, sp.cmd.Process.Pid, sp.nodeName)
		nodeName := sp.nodeName
		s.stopLoopback(mac)
		s.startLoopback(mac, nodeName)
		if newSp, ok2 := s.speakers[mac]; ok2 {
			s.dbg("handleDelay %s: new loopback pid=%d cmd=%v", mac, newSp.cmd.Process.Pid, newSp.cmd.Args)
		}
	} else {
		s.dbg("handleDelay %s: delay stored to %dms (no live loopback to restart)", mac, body.Ms)
	}
	s.mu.Unlock()

	s.saveStateDebounced()
	s.triggerSatellitePush()
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleGetInput(w http.ResponseWriter, r *http.Request) {
	sources, err := s.audio.Sources(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sources)
}

func (s *server) handleInputDiscover(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled    bool `json:"enabled"`
		TimeoutSec int  `json:"timeout_sec"`
	}
	body.Enabled = true
	body.TimeoutSec = 60
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.TimeoutSec <= 0 {
		body.TimeoutSec = 60
	}
	timeout := time.Duration(body.TimeoutSec) * time.Second
	if err := s.bt.SetDiscoverable(r.Context(), body.Enabled, timeout); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleStream(w http.ResponseWriter, r *http.Request) {
	sources, err := s.audio.Sources(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	resp := map[string]any{"active": false}
	for _, src := range sources {
		if src.Name == "rtp-source" {
			resp["active"] = true
			resp["source_node_id"] = src.ID
			break
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *server) handleEvents(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	s.hub.add(c)
	defer s.hub.remove(c)
	<-r.Context().Done()
	c.CloseNow()
}
