package agentproto

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"
)

// Paths served by the agent.
const (
	PathOps      = "/v1/ops"
	PathDownload = "/v1/files/download"
	PathUpload   = "/v1/files/upload"
	PathTerminal = "/v1/terminal"
)

// ErrUnavailable indicates the agent could not be reached.
var ErrUnavailable = errors.New("host agent unavailable")

// Call describes one agent operation.
type Call struct {
	Op         string
	RequestID  string
	ActorID    string
	ResourceID string
	Reason     string
	Payload    any
}

// Client talks to the agent over its Unix socket.
type Client struct {
	socket string
	secret []byte
	http   *http.Client
	stream *http.Client
}

// NewClient creates a client.
func NewClient(socketPath string, secret []byte) *Client {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
		MaxIdleConns:          10,
		IdleConnTimeout:       60 * time.Second,
		DisableCompression:    true,
		ResponseHeaderTimeout: 10 * time.Minute,
	}
	return &Client{
		socket: socketPath, secret: secret,
		http:   &http.Client{Transport: tr, Timeout: 10 * time.Minute},
		stream: &http.Client{Transport: tr},
	}
}

func nonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (c *Client) envelope(call Call) (Request, error) {
	payload, err := json.Marshal(call.Payload)
	if err != nil {
		return Request{}, err
	}
	if call.Payload == nil {
		payload = []byte("{}")
	}
	reqID := call.RequestID
	if reqID == "" {
		reqID = nonce()
	}
	return Request{ProtocolVersion: ProtocolVersion, Operation: call.Op, RequestID: reqID,
		ActorID: call.ActorID, ResourceID: call.ResourceID, Reason: call.Reason, Payload: payload}, nil
}

func (c *Client) sign(h http.Header, method, path, digest string) {
	ts := time.Now().Unix()
	n := nonce()
	h.Set(HeaderTimestamp, strconv.FormatInt(ts, 10))
	h.Set(HeaderNonce, n)
	h.Set(HeaderSignature, Sign(c.secret, method, path, ts, n, digest))
}

// Do performs an operation and decodes the response data into out (if non-nil).
func (c *Client) Do(ctx context.Context, call Call, out any) error {
	env, err := c.envelope(call)
	if err != nil {
		return err
	}
	body, err := json.Marshal(env)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://agent"+PathOps, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	c.sign(req.Header, http.MethodPost, PathOps, Digest(body))
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	return decodeResponse(resp, out)
}

func decodeResponse(resp *http.Response, out any) error {
	var r Response
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(&r); err != nil {
		return fmt.Errorf("agent: invalid response (HTTP %d): %w", resp.StatusCode, err)
	}
	if !r.OK {
		if r.Error == nil {
			return &Error{Code: ErrCodeInternal, Message: "unknown agent error"}
		}
		return r.Error
	}
	if out != nil && len(r.Data) > 0 {
		return json.Unmarshal(r.Data, out)
	}
	return nil
}

func (c *Client) opHeader(call Call) (string, error) {
	env, err := c.envelope(call)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(env)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Download streams a file. The caller must close the returned body.
func (c *Client) Download(ctx context.Context, call Call) (io.ReadCloser, int64, error) {
	op, err := c.opHeader(call)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://agent"+PathDownload, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set(HeaderOp, op)
	c.sign(req.Header, http.MethodGet, PathDownload, Digest([]byte(op)))
	resp, err := c.stream.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, 0, decodeResponse(resp, nil)
	}
	return resp.Body, resp.ContentLength, nil
}

// Upload streams body into a workspace file.
func (c *Client) Upload(ctx context.Context, call Call, body io.Reader) (*FileEntry, error) {
	op, err := c.opHeader(call)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://agent"+PathUpload, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set(HeaderOp, op)
	req.Header.Set("Content-Type", "application/octet-stream")
	c.sign(req.Header, http.MethodPost, PathUpload, Digest([]byte(op)))
	resp, err := c.stream.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	var fe FileEntry
	if err := decodeResponse(resp, &fe); err != nil {
		return nil, err
	}
	return &fe, nil
}

// Terminal opens a WebSocket terminal session on the agent.
func (c *Client) Terminal(ctx context.Context, call Call) (*websocket.Conn, error) {
	op, err := c.opHeader(call)
	if err != nil {
		return nil, err
	}
	h := http.Header{}
	h.Set(HeaderOp, op)
	c.sign(h, http.MethodGet, PathTerminal, Digest([]byte(op)))
	conn, resp, err := websocket.Dial(ctx, "ws://agent"+PathTerminal, &websocket.DialOptions{HTTPClient: c.stream, HTTPHeader: h})
	if err != nil {
		if resp != nil && resp.Body != nil {
			defer resp.Body.Close()
			if derr := decodeResponse(resp, nil); derr != nil {
				return nil, derr
			}
		}
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return conn, nil
}

// DecodeOpHeader parses a base64url request envelope from HeaderOp.
func DecodeOpHeader(v string) (*Request, error) {
	b, err := base64.RawURLEncoding.DecodeString(v)
	if err != nil || len(b) > 64<<10 {
		return nil, errors.New("invalid operation header")
	}
	var r Request
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return nil, errors.New("invalid operation header")
	}
	return &r, nil
}

// DecodePayload strictly decodes and validates the payload for r.Operation.
func DecodePayload(r *Request) (Payload, error) {
	ctor, ok := Operations[r.Operation]
	if !ok {
		return nil, fmt.Errorf("operation %q is not allowed", r.Operation)
	}
	p := ctor()
	raw := r.Payload
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(p); err != nil {
		return nil, fmt.Errorf("invalid payload: %v", err)
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return p, nil
}
