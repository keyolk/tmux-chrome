// Package bridge talks to host/bridge.py over its Unix socket: one short
// connection per request, and one long-lived connection for the change feed.
//
// Errors fall in two kinds that callers must tell apart. An *AppError means
// Chrome answered and refused, so a write did not happen. Anything else — a
// dial failure, a timeout, a dropped connection, the bridge giving up waiting
// for Chrome — means the outcome is unknown: a write may or may not have
// landed. See Unknown.
package bridge

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

// DefaultSocket is where bridge.py listens; bin/tmux-chrome uses the same path.
const DefaultSocket = "/tmp/tmux-chrome-bridge.sock"

// bridgeTimeout is the text bridge.py sends when Chrome did not answer within
// its own 5s wait. The request reached Chrome's queue, so it is not a refusal.
const bridgeTimeout = "Timeout waiting for Chrome response"

// AppError is a refusal from the extension (or the bridge) after the request
// was read: the action did not happen.
type AppError struct{ Msg string }

func (e *AppError) Error() string { return e.Msg }

// ErrUnsupported means the running extension does not know the request type:
// it predates this client and needs a reload.
var ErrUnsupported = errors.New("extension does not support this request; reload it")

// Unknown reports whether err leaves the outcome of a write unknown.
func Unknown(err error) bool {
	if err == nil {
		return false
	}
	var app *AppError
	return !errors.As(err, &app) && !errors.Is(err, ErrUnsupported)
}

// Client is a value; the zero Client uses DefaultSocket and a 6s timeout.
type Client struct {
	Socket  string
	Timeout time.Duration
}

func (c Client) socket() string {
	if c.Socket != "" {
		return c.Socket
	}
	return DefaultSocket
}

func (c Client) timeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	// One second over bridge.py's wait for Chrome, so its own timeout reply
	// reaches us instead of our deadline cutting it off.
	return 6 * time.Second
}

var reqCounter atomic.Uint64

func requestID() string {
	return fmt.Sprintf("tui_%d_%d", os.Getpid(), reqCounter.Add(1))
}

type response struct {
	ID      string          `json:"id"`
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   string          `json:"error"`
}

// Do sends {"type": typ, ...params} and decodes the reply's data into out
// (out may be nil).
func (c Client) Do(ctx context.Context, typ string, params map[string]any, out any) error {
	msg := map[string]any{}
	for k, v := range params {
		msg[k] = v
	}
	msg["type"] = typ
	msg["id"] = requestID()
	line, err := json.Marshal(msg)
	if err != nil {
		return &AppError{Msg: "encode request: " + err.Error()}
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout())
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.socket())
	if err != nil {
		return fmt.Errorf("dial bridge: %w", err)
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(dl); err != nil {
			return fmt.Errorf("set bridge deadline: %w", err)
		}
	}
	if _, err := conn.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("send to bridge: %w", err)
	}
	raw, err := bufio.NewReaderSize(conn, 64*1024).ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("read from bridge: %w", err)
	}
	var resp response
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("decode bridge reply: %w", err)
	}
	if !resp.Success {
		return replyError(resp.Error)
	}
	if out == nil || len(resp.Data) == 0 {
		return nil
	}
	if err := json.Unmarshal(resp.Data, out); err != nil {
		return &AppError{Msg: fmt.Sprintf("decode %s reply: %v", typ, err)}
	}
	return nil
}

func replyError(msg string) error {
	switch {
	case msg == bridgeTimeout:
		return errors.New(msg)
	case strings.HasPrefix(msg, "Unknown message type"):
		return fmt.Errorf("%w (%s)", ErrUnsupported, msg)
	case msg == "":
		return &AppError{Msg: "request failed"}
	}
	return &AppError{Msg: msg}
}

// Alive reports whether something accepts connections on the socket. A
// socket file alone proves nothing: bridge.py leaves it behind when Chrome
// kills the host.
func (c Client) Alive() bool {
	conn, err := net.DialTimeout("unix", c.socket(), time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
