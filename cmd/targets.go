package cmd

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// WebKitGTK/WPE serve an HTML frontend, not Chromium's /json/list API.
// Split at row boundaries before matching the upstream Inspect action.
var inspectAction = regexp.MustCompile(`(?s)<div class="targetname">([^<]*)</div>.*?<input type="button" value="Inspect" onclick="window\.open\('Main\.html\?ws=' \+ window\.location\.host \+ '(/socket/[0-9]+/[0-9]+/([A-Za-z]+))'`)

func newTargetsCmd() *cobra.Command {
	return &cobra.Command{Use: "targets INSPECTOR_HTTP_URL", Short: "List page WebSocket endpoints from a WebKitGTK/WPE inspector HTTP server", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		u, err := url.Parse(args[0])
		if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("expected an http(s) inspector listing URL without credentials, query, or fragment")
		}
		req, err := http.NewRequestWithContext(cmd.Context(), http.MethodGet, u.String(), nil)
		if err != nil {
			return err
		}
		client := http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("inspector listing: %s", resp.Status)
		}
		const maxBytes = 4 << 20
		data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
		if err != nil {
			return err
		}
		if len(data) > maxBytes {
			return fmt.Errorf("inspector listing exceeds %d bytes", maxBytes)
		}
		page := string(data)
		if !strings.Contains(page, "Inspectable targets") {
			return fmt.Errorf("response is not a WebKit inspector target listing")
		}
		type target struct {
			Name     string `json:"name"`
			Endpoint string `json:"endpoint"`
		}
		targets := []target{}
		scheme := "ws"
		if u.Scheme == "https" {
			scheme = "wss"
		}
		_, rows, _ := strings.Cut(page, "<tr>")
		for entry := range strings.SplitSeq(rows, "<tr>") {
			row, _, closed := strings.Cut(entry, "</tr>")
			if !closed {
				continue
			}
			m := inspectAction.FindStringSubmatch(row)
			if m == nil || m[3] != "WebPage" {
				continue
			}
			ws := url.URL{Scheme: scheme, Host: u.Host, Path: m[2]}
			targets = append(targets, target{html.UnescapeString(m[1]), ws.String()})
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(targets)
	}}
}
