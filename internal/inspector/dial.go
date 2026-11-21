package inspector

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"

	"github.com/coder/websocket"
)

func dialInspector(ctx context.Context, endpoint string) (*websocket.Conn, error) {
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	ws, resp, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		return nil, fmt.Errorf("connect to WebKit inspector: %w", dialFailure(resp, err))
	}
	return ws, nil
}

// Only expose structured causes and fixed handshake diagnostics. The original
// error chain can contain the endpoint, proxy URLs, or values echoed by the peer.
func dialFailure(resp *http.Response, err error) error {
	if resp != nil {
		if resp.StatusCode != http.StatusSwitchingProtocols {
			return fmt.Errorf("WebSocket handshake returned HTTP %d, expected 101", resp.StatusCode)
		}
		return invalidUpgrade(err)
	}

	var dns *net.DNSError
	var network net.Error
	var errno syscall.Errno
	var cert *tls.CertificateVerificationError
	var unknownAuthority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var record tls.RecordHeaderError
	var alert tls.AlertError
	var request *url.Error
	switch {
	case errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		return context.DeadlineExceeded
	case errors.Is(err, http.ErrSchemeMismatch):
		return http.ErrSchemeMismatch
	case errors.As(err, &network) && network.Timeout():
		return errors.New("WebSocket connection timed out")
	case errors.As(err, &dns):
		if dns.IsNotFound {
			return errors.New("DNS lookup failed: no such host")
		}
		return errors.New("DNS lookup failed")
	case errors.As(err, &unknownAuthority):
		return errors.New("TLS certificate signed by unknown authority")
	case errors.As(err, &hostname):
		return errors.New("TLS certificate does not match endpoint hostname")
	case errors.As(err, &cert):
		return errors.New("TLS certificate verification failed")
	case errors.As(err, &record):
		return errors.New("invalid TLS handshake record")
	case errors.As(err, &alert):
		return alert
	case errors.As(err, &errno):
		return errno
	case errors.Is(err, io.ErrUnexpectedEOF):
		return io.ErrUnexpectedEOF
	case errors.Is(err, io.EOF):
		return io.EOF
	case errors.As(err, &request) && strings.HasPrefix(request.Err.Error(), "failed to parse Location header "):
		return errors.New("invalid WebSocket redirect Location header")
	default:
		return errors.New("WebSocket handshake request failed")
	}
}

func invalidUpgrade(err error) error {
	// coder/websocket does not expose typed errors for upgrade header violations.
	// Match only the known message prefix; never return peer-provided values.
	for cause := err; cause != nil; cause = errors.Unwrap(cause) {
		const prefix = "WebSocket protocol violation: "
		switch reason := cause.Error(); {
		case strings.HasPrefix(reason, prefix+"Connection header "):
			return errors.New("invalid WebSocket Connection header")
		case strings.HasPrefix(reason, prefix+"Upgrade header "):
			return errors.New("invalid WebSocket Upgrade header")
		case strings.HasPrefix(reason, prefix+"invalid Sec-WebSocket-Accept "):
			return errors.New("invalid WebSocket Sec-WebSocket-Accept header")
		case strings.HasPrefix(reason, prefix+"unexpected Sec-WebSocket-Protocol "):
			return errors.New("unexpected WebSocket Sec-WebSocket-Protocol header")
		case strings.HasPrefix(reason, "WebSocket protcol violation: unsupported extensions "):
			return errors.New("unsupported WebSocket extensions")
		}
	}
	return errors.New("invalid WebSocket upgrade response")
}
