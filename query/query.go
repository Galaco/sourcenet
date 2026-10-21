// Package query implements blocking A2S server queries over UDP, as used by
// server browsers: server information, player lists, rules and ping.
//
// The challenge handshakes required by current servers (including the 2020
// A2S_INFO challenge) and split responses are handled transparently.
package query

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/galaco/sourcenet/connectionless"
)

// DefaultTimeout is the per-attempt timeout used when Client.Timeout is zero.
const DefaultTimeout = 3 * time.Second

// maxChallengeRounds limits how many challenges a server may send for one query.
const maxChallengeRounds = 3

// ErrUnexpectedResponse is returned when a server answers with a packet that
// does not belong to the query.
var ErrUnexpectedResponse = errors.New("query: unexpected response")

// Client performs server queries. The zero value is ready to use.
type Client struct {
	// Timeout is the time to wait for a response to each request. Zero means
	// DefaultTimeout.
	Timeout time.Duration
	// Retries is the number of times a request is resent after a timeout.
	Retries int
}

func (c *Client) timeout() time.Duration {
	if c.Timeout <= 0 {
		return DefaultTimeout
	}
	return c.Timeout
}

// Info queries server information (A2S_INFO).
func (c *Client) Info(ctx context.Context, addr string) (*connectionless.Info, error) {
	resp, err := c.withConn(ctx, addr, func(s *session) (connectionless.Packet, error) {
		req := &connectionless.InfoRequest{}
		for round := 0; round < maxChallengeRounds; round++ {
			resp, err := s.exchange(req.Marshal())
			if err != nil {
				return nil, err
			}
			ch, ok := resp.(*connectionless.QueryChallenge)
			if !ok {
				return resp, nil
			}
			req = &connectionless.InfoRequest{Challenge: ch.Challenge, HasChallenge: true}
		}
		return nil, errors.New("query: server kept sending challenges")
	})
	if err != nil {
		return nil, err
	}
	info, ok := resp.(*connectionless.Info)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnexpectedResponse, resp.ID())
	}
	return info, nil
}

// Players queries the player list (A2S_PLAYER).
func (c *Client) Players(ctx context.Context, addr string) ([]connectionless.Player, error) {
	resp, err := c.withConn(ctx, addr, func(s *session) (connectionless.Packet, error) {
		return s.challenged(func(ch uint32) []byte {
			return (&connectionless.PlayerRequest{Challenge: ch}).Marshal()
		})
	})
	if err != nil {
		return nil, err
	}
	players, ok := resp.(*connectionless.Players)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnexpectedResponse, resp.ID())
	}
	return players.Players, nil
}

// Rules queries the server's public convars (A2S_RULES).
func (c *Client) Rules(ctx context.Context, addr string) (*connectionless.Rules, error) {
	resp, err := c.withConn(ctx, addr, func(s *session) (connectionless.Packet, error) {
		return s.challenged(func(ch uint32) []byte {
			return (&connectionless.RulesRequest{Challenge: ch}).Marshal()
		})
	})
	if err != nil {
		return nil, err
	}
	rules, ok := resp.(*connectionless.Rules)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnexpectedResponse, resp.ID())
	}
	return rules, nil
}

// Ping measures the round trip time of an A2S_INFO request. The legacy
// A2A_PING packet is not answered by most current servers, so the first
// reply to an info request (a challenge or the info itself) is timed instead.
func (c *Client) Ping(ctx context.Context, addr string) (time.Duration, error) {
	var rtt time.Duration
	_, err := c.withConn(ctx, addr, func(s *session) (connectionless.Packet, error) {
		start := time.Now()
		resp, err := s.exchange((&connectionless.InfoRequest{}).Marshal())
		rtt = time.Since(start)
		return resp, err
	})
	return rtt, err
}

type session struct {
	ctx     context.Context
	conn    net.Conn
	timeout time.Duration
	retries int
	buf     []byte
}

func (c *Client) withConn(ctx context.Context, addr string, fn func(*session) (connectionless.Packet, error)) (connectionless.Packet, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp", addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() {
		conn.SetReadDeadline(time.Unix(1, 0))
	})
	defer stop()
	s := &session{ctx: ctx, conn: conn, timeout: c.timeout(), retries: c.Retries, buf: make([]byte, 65536)}
	resp, err := fn(s)
	if ctxErr := ctx.Err(); ctxErr != nil && err != nil {
		return nil, ctxErr
	}
	return resp, err
}

// challenged runs the A2S_PLAYER/A2S_RULES handshake: the request is sent
// with NoChallenge and repeated with the challenge the server returns.
func (s *session) challenged(build func(challenge uint32) []byte) (connectionless.Packet, error) {
	challenge := connectionless.NoChallenge
	for round := 0; round < maxChallengeRounds; round++ {
		resp, err := s.exchange(build(challenge))
		if err != nil {
			return nil, err
		}
		ch, ok := resp.(*connectionless.QueryChallenge)
		if !ok {
			return resp, nil
		}
		challenge = ch.Challenge
	}
	return nil, errors.New("query: server kept sending challenges")
}

// exchange sends req and waits for one complete response, resending after
// each timeout up to the configured number of retries.
func (s *session) exchange(req []byte) (connectionless.Packet, error) {
	var lastErr error
	for attempt := 0; attempt <= s.retries; attempt++ {
		if err := s.ctx.Err(); err != nil {
			return nil, err
		}
		if _, err := s.conn.Write(req); err != nil {
			return nil, err
		}
		resp, err := s.receive(time.Now().Add(s.timeout))
		if err == nil {
			return resp, nil
		}
		var ne net.Error
		if !errors.As(err, &ne) || !ne.Timeout() {
			return nil, err
		}
		lastErr = err
	}
	return nil, fmt.Errorf("query: no response after %d attempt(s): %w", s.retries+1, lastErr)
}

func (s *session) receive(deadline time.Time) (connectionless.Packet, error) {
	if d, ok := s.ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := s.conn.SetReadDeadline(deadline); err != nil {
		return nil, err
	}
	// Checked after setting the deadline so a cancellation racing with it is
	// not lost (the cancel hook resets the deadline to the past).
	if err := s.ctx.Err(); err != nil {
		return nil, err
	}
	var splits connectionless.QuerySplitReassembler
	for {
		n, err := s.conn.Read(s.buf)
		if err != nil {
			return nil, err
		}
		data := s.buf[:n]
		if connectionless.IsSplit(data) {
			full, done, err := splits.Add(data)
			if err != nil {
				return nil, err
			}
			if !done {
				continue
			}
			data = full
		}
		if !connectionless.IsConnectionless(data) {
			continue // not a query response
		}
		return connectionless.Parse(data)
	}
}
