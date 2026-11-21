package admin

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

const PanelPath = "/cpa-ext"

func Panel(upstream *url.URL, extension http.Handler) http.Handler {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(upstream)
			r.Out.Host = r.In.Host
			// The extension session is scoped to the path and should never reach CPA.
			r.Out.Header.Del("Cookie")
		},
	}
	embedded := http.StripPrefix(PanelPath, extension)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case path == "/":
			http.Redirect(w, r, "/management.html", http.StatusFound)
		case path == PanelPath:
			http.Redirect(w, r, PanelPath+"/", http.StatusPermanentRedirect)
		case strings.HasPrefix(path, PanelPath+"/"):
			embedded.ServeHTTP(w, r)
		case path == "/management.html",
			strings.HasPrefix(path, "/v8/management/"),
			strings.HasPrefix(path, "/v0/resource/"),
			strings.HasPrefix(path, "/v0/extension/"),
			strings.HasPrefix(path, "/oauth/"),
			strings.HasSuffix(path, "/callback") && strings.Count(path, "/") == 2:
			proxy.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	})
}
