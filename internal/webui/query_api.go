package webui

import "net/http"

// apiQuery handles filtered event queries: /api/query?session=&tool=&verdict=&limit=
func (s *Server) apiQuery(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	writeJSON(w, s.Store.Query(
		q.Get("session"), q.Get("tool"), q.Get("verdict"),
		atoiDefault(q.Get("limit"), 100),
	))
}

func atoiDefault(s string, def int) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' { return def }
		n = n*10 + int(c-'0')
	}
	if n == 0 { return def }
	return n
}
