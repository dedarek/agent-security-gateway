package webui

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// RegisterExplorerProxy mounts the Semantica Explorer (interactive graph UI +
// its FastAPI backend) under /explorer/* on the ASG console, so operators get
// ONE unified interface. The proxy injects the explorer API key automatically
// — the operator never handles it.
func RegisterExplorerProxy(mux *http.ServeMux, target string, apiKey string) {
	if target == "" {
		return
	}
	u, err := url.Parse(target)
	if err != nil {
		return
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	inject := func(req *http.Request) {
		if apiKey != "" {
			req.Header.Set("X-API-Key", apiKey)
		}
	}

	// Static SPA + assets live at explorer root.
	mux.HandleFunc("/explorer", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/explorer/", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/explorer/", func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/explorer")
		if r.URL.Path == "" {
			r.URL.Path = "/"
		}
		inject(r)
		proxy.ServeHTTP(w, r)
	})

	// Graph/decision/lineage APIs: same paths the explorer SPA fetches.
	graphPrefixes := []string{
		"/api/graph/", "/api/decisions", "/api/analytics",
		"/api/temporal", "/api/sparql", "/api/ontology",
		"/api/vocabulary", "/api/enrich", "/api/export_import",
		"/api/annotations", "/api/provenance",
	}
	for _, prefix := range graphPrefixes {
		mux.HandleFunc(prefix, func(w http.ResponseWriter, r *http.Request) {
			inject(r)
			proxy.ServeHTTP(w, r)
		})
	}
}
