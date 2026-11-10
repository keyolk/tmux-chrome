package bridge

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"
)

// IdleLimit is how long a subscription may stay silent before the client
// calls it dead. bridge.py pings every 15s, so two missed pings and change.
const IdleLimit = 35 * time.Second

// Sub is an open change feed. Next blocks; Close unblocks it.
type Sub struct {
	conn net.Conn
	r    *bufio.Reader
}

// Subscribe opens the change feed. An old bridge.py does not know
// "subscribe" and forwards it to the extension, which refuses it: that comes
// back as ErrUnsupported.
func (c Client) Subscribe(ctx context.Context) (*Sub, error) {
	var d net.Dialer
	dctx, cancel := context.WithTimeout(ctx, c.timeout())
	defer cancel()
	conn, err := d.DialContext(dctx, "unix", c.socket())
	if err != nil {
		return nil, fmt.Errorf("dial bridge: %w", err)
	}
	line, err := json.Marshal(map[string]string{"type": "subscribe", "id": requestID()})
	if err != nil {
		conn.Close()
		return nil, err
	}
	if err := conn.SetDeadline(time.Now().Add(c.timeout())); err != nil {
		conn.Close()
		return nil, fmt.Errorf("set bridge deadline: %w", err)
	}
	if _, err := conn.Write(append(line, '\n')); err != nil {
		conn.Close()
		return nil, fmt.Errorf("send to bridge: %w", err)
	}
	s := &Sub{conn: conn, r: bufio.NewReader(conn)}
	raw, err := s.r.ReadBytes('\n')
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("read from bridge: %w", err)
	}
	ev, resp, err := decodeEvent(raw)
	switch {
	case err != nil:
		conn.Close()
		return nil, fmt.Errorf("decode bridge reply: %w", err)
	case ev.Event == "subscribed":
		return s, nil
	case resp.ID != "" || resp.Error != "":
		conn.Close()
		return nil, replyError(resp.Error)
	}
	conn.Close()
	return nil, errors.New("unexpected first line from bridge")
}

// Next returns the next "changed" event, skipping pings. Any error ends the
// feed; the caller reconnects.
func (s *Sub) Next() (Event, error) {
	for {
		if err := s.conn.SetReadDeadline(time.Now().Add(IdleLimit)); err != nil {
			return Event{}, err
		}
		raw, err := s.r.ReadBytes('\n')
		if err != nil {
			return Event{}, err
		}
		ev, _, err := decodeEvent(raw)
		if err != nil {
			return Event{}, fmt.Errorf("decode event: %w", err)
		}
		if ev.Event == "changed" {
			return ev, nil
		}
	}
}

func (s *Sub) Close() error { return s.conn.Close() }
