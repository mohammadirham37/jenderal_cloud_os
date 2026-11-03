// Package docker is a minimal Docker Engine API client used by the host
// agent. It speaks the versioned REST API over the local Unix socket and
// implements only the calls Jenderal Cloud OS needs.
package docker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MinAPIVersion is the oldest Engine API version supported (Docker 25+).
const MinAPIVersion = "1.44"

// ErrNotFound is returned for 404 responses.
var ErrNotFound = errors.New("docker: not found")

// ErrUnavailable is returned when the daemon cannot be reached.
var ErrUnavailable = errors.New("docker unavailable")

// APIError is a non-2xx Engine response.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("docker: HTTP %d: %s", e.Status, e.Message) }

// Client talks to the Docker Engine.
type Client struct {
	socket  string
	http    *http.Client
	mu      sync.Mutex
	version string
}

// New creates a client for the given socket path (e.g. /var/run/docker.sock).
func New(socket string) *Client {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
		DisableCompression: true,
		MaxIdleConns:       20,
		IdleConnTimeout:    30 * time.Second,
	}
	return &Client{socket: socket, http: &http.Client{Transport: tr}}
}

// Socket returns the socket path.
func (c *Client) Socket() string { return c.socket }

func versionLess(a, b string) bool {
	pa, pb := strings.SplitN(a, ".", 2), strings.SplitN(b, ".", 2)
	if len(pa) != 2 || len(pb) != 2 {
		return a < b
	}
	ma, _ := strconv.Atoi(pa[0])
	mb, _ := strconv.Atoi(pb[0])
	if ma != mb {
		return ma < mb
	}
	na, _ := strconv.Atoi(pa[1])
	nb, _ := strconv.Atoi(pb[1])
	return na < nb
}

// negotiate pings the daemon and pins the API version.
func (c *Client) negotiate(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.version != "" {
		return c.version, nil
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/_ping", nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	resp.Body.Close()
	v := resp.Header.Get("API-Version")
	if v == "" {
		return "", errors.New("docker did not report an API version")
	}
	if versionLess(v, MinAPIVersion) {
		return "", fmt.Errorf("docker API %s is older than required %s", v, MinAPIVersion)
	}
	c.version = v
	return v, nil
}

// Reset forgets the negotiated version (e.g. after a daemon restart).
func (c *Client) Reset() {
	c.mu.Lock()
	c.version = ""
	c.mu.Unlock()
}

func (c *Client) url(v, path string, q url.Values) string {
	u := "http://docker/v" + v + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

func (c *Client) do(ctx context.Context, method, path string, q url.Values, body any) (*http.Response, error) {
	v, err := c.negotiate(ctx)
	if err != nil {
		return nil, err
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.url(v, path, q), rdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.Reset()
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, ErrNotFound
	}
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotModified {
		defer resp.Body.Close()
		var m struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&m)
		return nil, &APIError{Status: resp.StatusCode, Message: m.Message}
	}
	return resp, nil
}

func (c *Client) doJSON(ctx context.Context, method, path string, q url.Values, body, out any) error {
	resp, err := c.do(ctx, method, path, q, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(out)
}

// Version describes the daemon.
type Version struct {
	Version    string `json:"Version"`
	APIVersion string `json:"ApiVersion"`
}

// Version returns daemon version information.
func (c *Client) Version(ctx context.Context) (Version, error) {
	var v Version
	err := c.doJSON(ctx, http.MethodGet, "/version", nil, nil, &v)
	return v, err
}

// Info is a subset of /info.
type Info struct {
	Containers        int            `json:"Containers"`
	ContainersRunning int            `json:"ContainersRunning"`
	ContainersPaused  int            `json:"ContainersPaused"`
	ContainersStopped int            `json:"ContainersStopped"`
	Images            int            `json:"Images"`
	Runtimes          map[string]any `json:"Runtimes"`
	SecurityOptions   []string       `json:"SecurityOptions"`
	Driver            string         `json:"Driver"`
}

// Info returns daemon information.
func (c *Client) Info(ctx context.Context) (Info, error) {
	var i Info
	err := c.doJSON(ctx, http.MethodGet, "/info", nil, nil, &i)
	return i, err
}

// ImageInspect returns image metadata.
type ImageInspect struct {
	ID   string `json:"Id"`
	Size int64  `json:"Size"`
}

// InspectImage inspects an image by reference.
func (c *Client) InspectImage(ctx context.Context, ref string) (ImageInspect, error) {
	var ii ImageInspect
	err := c.doJSON(ctx, http.MethodGet, "/images/"+url.PathEscape(ref)+"/json", nil, nil, &ii)
	return ii, err
}

// PullImage pulls an image and waits for completion.
func (c *Client) PullImage(ctx context.Context, ref string) error {
	name, tag := splitRef(ref)
	q := url.Values{"fromImage": {name}}
	if tag != "" {
		q.Set("tag", tag)
	}
	resp, err := c.do(ctx, http.MethodPost, "/images/create", q, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	for {
		var msg struct {
			Error string `json:"error"`
		}
		if err := dec.Decode(&msg); err == io.EOF {
			return nil
		} else if err != nil {
			return err
		}
		if msg.Error != "" {
			return errors.New("pull failed: " + msg.Error)
		}
	}
}

// splitRef splits "repo:tag" or "repo@digest" (registry ports preserved).
func splitRef(ref string) (string, string) {
	if i := strings.Index(ref, "@"); i >= 0 {
		return ref[:i], ref[i+1:]
	}
	slash := strings.LastIndex(ref, "/")
	if i := strings.LastIndex(ref, ":"); i > slash {
		return ref[:i], ref[i+1:]
	}
	return ref, "latest"
}

// EnsureNetwork creates a bridge network if missing and returns its ID.
func (c *Client) EnsureNetwork(ctx context.Context, name string, labels map[string]string) (string, error) {
	var existing struct {
		ID string `json:"Id"`
	}
	err := c.doJSON(ctx, http.MethodGet, "/networks/"+url.PathEscape(name), nil, nil, &existing)
	if err == nil {
		return existing.ID, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return "", err
	}
	var created struct {
		ID string `json:"Id"`
	}
	err = c.doJSON(ctx, http.MethodPost, "/networks/create", nil, map[string]any{
		"Name": name, "Driver": "bridge", "CheckDuplicate": true, "Labels": labels,
		"Options": map[string]string{
			// Containers on the same workspace network may talk to each other;
			// different workspaces are isolated on separate bridges.
			"com.docker.network.bridge.enable_icc":           "true",
			"com.docker.network.bridge.enable_ip_masquerade": "true",
		},
	}, &created)
	if err != nil {
		var ae *APIError
		if errors.As(err, &ae) && ae.Status == http.StatusConflict {
			return c.EnsureNetwork(ctx, name, labels)
		}
		return "", err
	}
	return created.ID, nil
}

// RemoveNetwork deletes a network (ignores not found).
func (c *Client) RemoveNetwork(ctx context.Context, name string) error {
	err := c.doJSON(ctx, http.MethodDelete, "/networks/"+url.PathEscape(name), nil, nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// CreateContainer creates a container and returns its ID.
func (c *Client) CreateContainer(ctx context.Context, name string, cfg map[string]any) (string, error) {
	var out struct {
		ID       string   `json:"Id"`
		Warnings []string `json:"Warnings"`
	}
	err := c.doJSON(ctx, http.MethodPost, "/containers/create", url.Values{"name": {name}}, cfg, &out)
	return out.ID, err
}

// StartContainer starts a container (no-op if running).
func (c *Client) StartContainer(ctx context.Context, id string) error {
	return c.doJSON(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/start", nil, nil, nil)
}

// StopContainer stops a container with a grace period.
func (c *Client) StopContainer(ctx context.Context, id string, timeout int) error {
	return c.doJSON(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/stop", url.Values{"t": {strconv.Itoa(timeout)}}, nil, nil)
}

// RestartContainer restarts a container.
func (c *Client) RestartContainer(ctx context.Context, id string, timeout int) error {
	return c.doJSON(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/restart", url.Values{"t": {strconv.Itoa(timeout)}}, nil, nil)
}

// RemoveContainer force-removes a container and its anonymous volumes.
func (c *Client) RemoveContainer(ctx context.Context, id string) error {
	err := c.doJSON(ctx, http.MethodDelete, "/containers/"+url.PathEscape(id), url.Values{"force": {"true"}, "v": {"true"}}, nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// ContainerJSON is a subset of container inspect output.
type ContainerJSON struct {
	ID    string `json:"Id"`
	Name  string `json:"Name"`
	State struct {
		Status     string `json:"Status"`
		Running    bool   `json:"Running"`
		OOMKilled  bool   `json:"OOMKilled"`
		ExitCode   int    `json:"ExitCode"`
		Error      string `json:"Error"`
		StartedAt  string `json:"StartedAt"`
		FinishedAt string `json:"FinishedAt"`
	} `json:"State"`
	Config struct {
		Labels map[string]string `json:"Labels"`
		User   string            `json:"User"`
	} `json:"Config"`
	NetworkSettings struct {
		Ports map[string][]struct {
			HostIP   string `json:"HostIp"`
			HostPort string `json:"HostPort"`
		} `json:"Ports"`
	} `json:"NetworkSettings"`
}

// InspectContainer inspects a container.
func (c *Client) InspectContainer(ctx context.Context, id string) (ContainerJSON, error) {
	var cj ContainerJSON
	err := c.doJSON(ctx, http.MethodGet, "/containers/"+url.PathEscape(id)+"/json", nil, nil, &cj)
	return cj, err
}

// ContainerSummary is an entry of /containers/json.
type ContainerSummary struct {
	ID     string            `json:"Id"`
	State  string            `json:"State"`
	Labels map[string]string `json:"Labels"`
}

// ListContainers lists all containers having the label (key=value).
func (c *Client) ListContainers(ctx context.Context, label string) ([]ContainerSummary, error) {
	f, _ := json.Marshal(map[string][]string{"label": {label}})
	var out []ContainerSummary
	err := c.doJSON(ctx, http.MethodGet, "/containers/json", url.Values{"all": {"true"}, "filters": {string(f)}}, nil, &out)
	return out, err
}

// Logs returns the last tail lines of combined stdout/stderr.
func (c *Client) Logs(ctx context.Context, id string, tail int, timestamps bool) ([]string, bool, error) {
	q := url.Values{"stdout": {"true"}, "stderr": {"true"}, "tail": {strconv.Itoa(tail)}}
	if timestamps {
		q.Set("timestamps", "true")
	}
	resp, err := c.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(id)+"/logs", q, nil)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	const max = 4 << 20
	var buf bytes.Buffer
	truncated := false
	if resp.Header.Get("Content-Type") == "application/vnd.docker.raw-stream" {
		n, _ := io.Copy(&buf, io.LimitReader(resp.Body, max+1))
		truncated = n > max
	} else {
		truncated, err = demux(&buf, resp.Body, max)
		if err != nil {
			return nil, false, err
		}
	}
	text := strings.ToValidUTF8(buf.String(), "�")
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = []string{}
	}
	return lines, truncated, nil
}

// demux decodes Docker's multiplexed stdout/stderr stream.
func demux(dst *bytes.Buffer, r io.Reader, max int) (bool, error) {
	br := bufio.NewReader(r)
	hdr := make([]byte, 8)
	for {
		if _, err := io.ReadFull(br, hdr); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return false, nil
			}
			return false, err
		}
		size := int(binary.BigEndian.Uint32(hdr[4:]))
		if dst.Len()+size > max {
			_, _ = io.CopyN(dst, br, int64(max-dst.Len()))
			return true, nil
		}
		if _, err := io.CopyN(dst, br, int64(size)); err != nil {
			return false, err
		}
	}
}

// StatsJSON is a subset of the one-shot stats response.
type StatsJSON struct {
	CPUStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemUsage uint64 `json:"system_cpu_usage"`
		OnlineCPUs  uint32 `json:"online_cpus"`
	} `json:"cpu_stats"`
	PreCPUStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemUsage uint64 `json:"system_cpu_usage"`
	} `json:"precpu_stats"`
	MemoryStats struct {
		Usage uint64            `json:"usage"`
		Limit uint64            `json:"limit"`
		Stats map[string]uint64 `json:"stats"`
	} `json:"memory_stats"`
	PidsStats struct {
		Current uint64 `json:"current"`
	} `json:"pids_stats"`
	Networks map[string]struct {
		RxBytes uint64 `json:"rx_bytes"`
		TxBytes uint64 `json:"tx_bytes"`
	} `json:"networks"`
}

// Stats returns a one-shot stats sample (includes a precpu sample so CPU
// percentage can be computed).
func (c *Client) Stats(ctx context.Context, id string) (StatsJSON, error) {
	var s StatsJSON
	err := c.doJSON(ctx, http.MethodGet, "/containers/"+url.PathEscape(id)+"/stats", url.Values{"stream": {"false"}}, nil, &s)
	return s, err
}

// CreateExec creates an exec instance.
func (c *Client) CreateExec(ctx context.Context, id string, cfg map[string]any) (string, error) {
	var out struct {
		ID string `json:"Id"`
	}
	err := c.doJSON(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/exec", nil, cfg, &out)
	return out.ID, err
}

// ResizeExec resizes an exec TTY.
func (c *Client) ResizeExec(ctx context.Context, execID string, cols, rows int) error {
	return c.doJSON(ctx, http.MethodPost, "/exec/"+url.PathEscape(execID)+"/resize",
		url.Values{"w": {strconv.Itoa(cols)}, "h": {strconv.Itoa(rows)}}, nil, nil)
}

// InspectExec returns whether the exec is still running and its PID.
func (c *Client) InspectExec(ctx context.Context, execID string) (running bool, exitCode int, err error) {
	var out struct {
		Running  bool `json:"Running"`
		ExitCode int  `json:"ExitCode"`
	}
	err = c.doJSON(ctx, http.MethodGet, "/exec/"+url.PathEscape(execID)+"/json", nil, nil, &out)
	return out.Running, out.ExitCode, err
}

// StartExecTTY starts an exec with a TTY and returns the hijacked raw
// bidirectional connection.
func (c *Client) StartExecTTY(ctx context.Context, execID string) (net.Conn, *bufio.Reader, error) {
	v, err := c.negotiate(ctx)
	if err != nil {
		return nil, nil, err
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.socket)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	body := `{"Detach":false,"Tty":true}`
	req := "POST /v" + v + "/exec/" + url.PathEscape(execID) + "/start HTTP/1.1\r\n" +
		"Host: docker\r\nContent-Type: application/json\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n" +
		"Content-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n" + body
	if _, err := conn.Write([]byte(req)); err != nil {
		conn.Close()
		return nil, nil, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols && resp.StatusCode != http.StatusOK {
		conn.Close()
		return nil, nil, &APIError{Status: resp.StatusCode, Message: "exec start failed"}
	}
	return conn, br, nil
}
