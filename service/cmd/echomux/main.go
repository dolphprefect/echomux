package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/dolphprefect/echomux/internal/api"
	"github.com/dolphprefect/echomux/internal/audio"
	"github.com/dolphprefect/echomux/internal/bluetooth"
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func defaultStateFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "/var/lib/echomux/state.json"
	}
	return filepath.Join(home, ".local", "share", "echomux", "state.json")
}

func defaultName() string {
	if h, err := os.Hostname(); err == nil {
		return h
	}
	return ""
}

func main() {
	adapter    := flag.String("adapter",     envOr("ECHOMUX_ADAPTER",     "hci0"),            "Bluetooth adapter (e.g. hci0, hci1)")
	addr       := flag.String("addr",        envOr("ECHOMUX_ADDR",        ":56644"),           "HTTP/HTTPS listen address")
	debug      := flag.Bool("debug",         os.Getenv("ECHOMUX_DEBUG") != "",                "enable verbose request/BT/autorouter logging")
	stateFile  := flag.String("state-file",  envOr("ECHOMUX_STATE_FILE",  defaultStateFile()), "path to state JSON file")
	tlsCert    := flag.String("tls-cert",    envOr("ECHOMUX_TLS_CERT",    ""),                 "TLS certificate path (enables HTTPS)")
	tlsKey     := flag.String("tls-key",     envOr("ECHOMUX_TLS_KEY",     ""),                 "TLS private key path")
	modeStr    := flag.String("mode",        envOr("ECHOMUX_MODE",        "standalone"),  "operating mode: standalone | master | satellite")
	name       := flag.String("name",        envOr("ECHOMUX_NAME",        defaultName()), "node display name shown in the UI")
	masterAddr := flag.String("master-addr", envOr("ECHOMUX_MASTER_ADDR", ""),            "satellite only: host:port of the master echomux")
	selfAddr   := flag.String("self-addr",   envOr("ECHOMUX_SELF_ADDR",   ""),            "satellite only: public host:port reported to master for HTTP proxy (e.g. 192.168.1.10:56644)")
	rtpPort    := flag.Int("rtp-port",       9001,                                         "master only: UDP port for RTP unicast to satellites (must match satellite pipewire config)")
	flag.Parse()

	mode, err := api.ParseMode(*modeStr)
	if err != nil {
		log.Fatalf("echomux: %v", err)
	}
	if err := api.ValidateConfig(mode, *masterAddr); err != nil {
		log.Fatalf("echomux: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	btMgr, err := bluetooth.NewManager(*adapter)
	if err != nil {
		log.Fatalf("bluetooth: %v", err)
	}

	if err := os.MkdirAll(filepath.Dir(*stateFile), 0o755); err != nil {
		log.Fatalf("state dir: %v", err)
	}

	audioCtr := audio.NewController(audio.OSExecutor{})
	srv := api.NewServer(btMgr, audioCtr,
		api.WithStateFile(*stateFile),
		api.WithDebug(*debug),
		api.WithMode(mode),
		api.WithName(*name),
		api.WithMasterAddr(*masterAddr),
		api.WithSelfAddr(*selfAddr),
		api.WithRTPPort(*rtpPort),
		api.WithClientContext(ctx),
		api.WithShutdownContext(ctx),
	)

	httpSrv := &http.Server{Addr: *addr, Handler: srv}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		httpSrv.Shutdown(shutCtx)
	}()

	if *tlsCert != "" && *tlsKey != "" {
		log.Printf("echomux listening on %s (HTTPS, debug=%v)", *addr, *debug)
		if err := httpSrv.ListenAndServeTLS(*tlsCert, *tlsKey); err != nil && err != http.ErrServerClosed {
			log.Fatalf("https: %v", err)
		}
	} else {
		log.Printf("echomux listening on %s (debug=%v)", *addr, *debug)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http: %v", err)
		}
	}
}
