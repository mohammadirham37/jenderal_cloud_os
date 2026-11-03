// Package appproxy exposes web apps running inside workspace containers
// (e.g. JupyterLab) through an authenticated reverse proxy. It is served
// only on a dedicated origin (APP_PROXY_ORIGIN) so that scripts from
// untrusted workspace apps cannot read Jenderal Cloud OS API responses or CSRF
// tokens. Container ports are published on host loopback only.
package appproxy

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/itk-cloud/itk-cloud/internal/modules/auth"
	ws "github.com/itk-cloud/itk-cloud/internal/modules/workspaces"
	"github.com/itk-cloud/itk-cloud/internal/platform/config"
	"github.com/itk-cloud/itk-cloud/internal/platform/httpx"
)

// Handler proxies /api/v1/workspaces/{id}/proxy/{port}/...
type Handler struct {
	Svc *ws.Service
	Cfg *config.Config
}

// Enabled reports whether the app proxy is configured.
func (h *Handler) Enabled() bool { return h.Cfg.AppProxyOrigin != "" }

func (h *Handler) fromProxyOrigin(r *http.Request) bool {
	u, err := url.Parse(h.Cfg.AppProxyOrigin)
	if err != nil {
		return false
	}
	return strings.EqualFold(r.Host, u.Host)
}

// ServeHTTP implements the proxy.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) error {
	if !h.Enabled() {
		return httpx.Unavailable("The workspace app proxy is not configured on this server (APP_PROXY_ORIGIN).")
	}
	if !h.fromProxyOrigin(r) {
		return httpx.NotFound("Page")
	}
	ctx := r.Context()
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return httpx.NotFound("Workspace")
	}
	port, err := strconv.Atoi(r.PathValue("port"))
	if err != nil || port < 1024 || port > 65535 {
		return httpx.NotFound("Port")
	}
	wsp, g, err := ws.Authorize(ctx, h.Svc.Pool, r, id, ws.LevelEdit)
	if err != nil {
		return err
	}
	if g.Emergency {
		return httpx.Forbidden("Workspace apps can only be opened by the owner or editors.")
	}
	if wsp.Status != ws.StatusRunning {
		return httpx.Conflict("The workspace is not running.")
	}
	cs, err := ws.ListContainers(ctx, h.Svc.Pool, id)
	if err != nil {
		return err
	}
	var target *ws.Port
	var targetContainer uuid.UUID
	for _, c := range cs {
		if c.Status != "running" {
			continue
		}
		for i := range c.Ports {
			if c.Ports[i].Port == port && c.Ports[i].HostPort > 0 {
				target = &c.Ports[i]
				targetContainer = c.ID
				break
			}
		}
		if target != nil {
			break
		}
	}
	if target == nil {
		return httpx.NotFound("Published port")
	}
	ws.Touch(ctx, h.Svc.Pool, id)
	prefix := "/api/v1/workspaces/" + id.String() + "/proxy/" + strconv.Itoa(port)
	upstream := &url.URL{Scheme: "http", Host: "127.0.0.1:" + strconv.Itoa(target.HostPort)}
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(upstream)
			if !target.Proxy {
				// Apps not aware of the base path are served at "/".
				p := strings.TrimPrefix(pr.In.URL.Path, prefix)
				if p == "" {
					p = "/"
				}
				pr.Out.URL.Path = p
				pr.Out.URL.RawPath = ""
			} else {
				pr.Out.URL.Path = pr.In.URL.Path
			}
			pr.Out.Host = pr.In.Host
			stripPlatformCredentials(pr.Out)
			// Apps that support token auth (JupyterLab) only accept requests
			// carrying the per-container token added here after authorisation.
			pr.Out.Header.Set("Authorization", "token "+ws.AppToken(h.Cfg.SessionSecret, targetContainer))
			pr.SetXForwarded()
		},
		ModifyResponse: func(resp *http.Response) error {
			// Never let an app overwrite the platform session cookie.
			cookies := resp.Header.Values("Set-Cookie")
			resp.Header.Del("Set-Cookie")
			for _, c := range cookies {
				if !strings.HasPrefix(strings.TrimSpace(c), auth.CookieName+"=") {
					resp.Header.Add("Set-Cookie", c)
				}
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			httpx.WriteError(w, r, httpx.NewError(http.StatusBadGateway, "app_unreachable",
				"The application in the workspace is not responding on this port.").Wrap(err))
		},
	}
	rp.ServeHTTP(w, r)
	return nil
}

// stripPlatformCredentials removes the Jenderal Cloud OS session cookie and CSRF
// header so they are never forwarded to user workloads.
func stripPlatformCredentials(r *http.Request) {
	r.Header.Del(auth.CSRFHeader)
	r.Header.Del("Authorization")
	cookies := r.Cookies()
	r.Header.Del("Cookie")
	for _, c := range cookies {
		if c.Name != auth.CookieName {
			r.AddCookie(c)
		}
	}
}
