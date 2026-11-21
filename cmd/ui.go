package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/omniviewdev/wkperf/internal/webui"
)

func newUICmd() *cobra.Command {
	listen := "127.0.0.1:0"
	const maxUploadMiB = 256
	uploadMiB := 32
	var open bool
	cmd := &cobra.Command{
		Use:   "ui",
		Short: "Investigate one or two recordings in a local browser",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			addr, err := uiListenAddr(listen)
			if err != nil {
				return err
			}
			if uploadMiB < 1 || uploadMiB > maxUploadMiB {
				return fmt.Errorf("--max-upload-mib must be between 1 and %d", maxUploadMiB)
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
			if err != nil {
				return fmt.Errorf("listen for UI: %w", err)
			}
			defer listener.Close()
			host := listener.Addr().String()
			server := &http.Server{
				Handler:           webui.New(host, uploadMiB),
				ReadHeaderTimeout: 5 * time.Second,
				ReadTimeout:       2 * time.Minute,
				WriteTimeout:      3 * time.Minute,
				IdleTimeout:       30 * time.Second,
				MaxHeaderBytes:    16 << 10,
			}
			url := "http://" + host + "/"
			fmt.Fprintln(cmd.OutOrStdout(), url)
			if open {
				if err := openFile(url); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "could not open browser: %v; open the printed URL manually\n", err)
				}
			}
			stopServer := context.AfterFunc(ctx, func() { _ = server.Close() })
			defer stopServer()
			if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
				return fmt.Errorf("serve UI: %w", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&listen, "listen", listen, "loopback host:port (port 0 chooses a free port)")
	cmd.Flags().IntVar(&uploadMiB, "max-upload-mib", uploadMiB, "maximum size of each upload in MiB (1–256)")
	cmd.Flags().BoolVar(&open, "open", false, "open the printed URL in the default browser")
	return cmd
}

func uiListenAddr(raw string) (string, error) {
	host, port, err := net.SplitHostPort(raw)
	if err != nil {
		return "", fmt.Errorf("invalid --listen %q: use loopback host:port", raw)
	}
	if strings.EqualFold(host, "localhost") {
		host = "127.0.0.1"
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || ip.Zone() != "" || !ip.IsLoopback() {
		return "", fmt.Errorf("invalid --listen %q: host must be a loopback address or localhost", raw)
	}
	p, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return "", fmt.Errorf("invalid --listen %q: port must be between 0 and 65535", raw)
	}
	return net.JoinHostPort(ip.String(), strconv.FormatUint(p, 10)), nil
}
