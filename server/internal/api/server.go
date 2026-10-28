package api

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"

	"github.com/Anciank/agentTracer/server/internal/auth"
	"github.com/Anciank/agentTracer/server/internal/config"
	"github.com/Anciank/agentTracer/server/internal/ingest"
	"github.com/Anciank/agentTracer/server/internal/store"
	"github.com/Anciank/agentTracer/server/internal/traces"
	"github.com/Anciank/agentTracer/server/ui"
)

const CompatVersion = "0.9.0"
const ServerVersion = "0.2.0-go"

type Server struct {
	cfg   config.Config
	store *store.Store
	mux   *http.ServeMux
}

func New(cfg config.Config, st *store.Store) *Server {
	s := &Server{cfg: cfg, store: st, mux: http.NewServeMux()}
	s.routes()
	s.routesUI()
	return s
}

func (s *Server) Handler() http.Handler {
	h := http.Handler(s.mux)
	h = cors(s.cfg.CORSOrigins, h)
	h = auth.Middleware(s.cfg.APIKey)(h)
	return h
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /health", s.health)
	s.mux.HandleFunc("GET /info", s.info)
	s.mux.HandleFunc("GET /{$}", s.root)

	s.mux.HandleFunc("POST /runs", s.createRun)
	s.mux.HandleFunc("PATCH /runs/{id}", s.patchRun)
	s.mux.HandleFunc("DELETE /runs/{id}", s.deleteRun)
	s.mux.HandleFunc("POST /runs/batch", s.runsBatch)
	s.mux.HandleFunc("POST /runs/multipart", s.runsMultipart)
	s.mux.HandleFunc("POST /runs/query", s.runsQuery)
	s.mux.HandleFunc("GET /runs/{id}", s.getRun)
	s.mux.HandleFunc("GET /runs", s.queryRuns)
	s.mux.HandleFunc("GET /runs/stats", s.runStats)
	s.mux.HandleFunc("GET /runs/{id}/attachments/{name}", s.getAttachment)

	s.mux.HandleFunc("POST /sessions", s.createSession)
	s.mux.HandleFunc("GET /sessions", s.listSessions)
	s.mux.HandleFunc("GET /sessions/{id}", s.getSession)
	s.mux.HandleFunc("PATCH /sessions/{id}", s.patchSession)
	s.mux.HandleFunc("DELETE /sessions/{id}", s.deleteSession)
	s.mux.HandleFunc("GET /sessions/{id}/runs", s.sessionRuns)

	s.mux.HandleFunc("POST /feedback", s.createFeedback)
	s.mux.HandleFunc("GET /feedback", s.listFeedback)

	// M1 JSON (kept until /api/ui cutover). HTML navigations get the SPA shell.
	s.mux.HandleFunc("GET /ui/projects", s.uiProjects)
	s.mux.HandleFunc("GET /ui/traces", ui.HTMLOr(s.uiTraces))
	s.mux.HandleFunc("GET /ui/traces/{id}", ui.HTMLOr(s.uiTrace))
	ui.Mount(s.mux)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func readJSONBody(r *http.Request, max int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, max))
	if err != nil {
		return nil, err
	}
	return ingest.MaybeDecompress(body, r.Header.Get("Content-Encoding"))
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	// Do not leak table counts on the unauthenticated probe.
	writeJSON(w, 200, map[string]any{
		"status":         "ok",
		"implementation": "agenttracer-go",
		"version":        ServerVersion,
	})
}

func (s *Server) info(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"version":                 CompatVersion,
		"license_expiration_time": nil,
		"batch_ingest_config": map[string]any{
			"use_multipart_endpoint":    true,
			"size_limit":                100,
			"size_limit_bytes":          20_000_000,
			"scale_up_qsize_trigger":    1000,
			"scale_up_nthreads_limit":   16,
			"scale_down_nempty_trigger": 4,
		},
		// langsmith SDK checks instance_flags.zstd_compression_enabled before
		// enabling CompressedTraces; without it it warns and falls back.
		"instance_flags": map[string]any{
			"local_only":               true,
			"zstd_compression_enabled": true,
		},
	})
}

func (s *Server) root(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"service": "agenttracer", "version": ServerVersion})
}

func (s *Server) createRun(w http.ResponseWriter, r *http.Request) {
	body, err := readJSONBody(r, 32<<20)
	if err != nil {
		writeJSON(w, 400, map[string]any{"detail": err.Error()})
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		writeJSON(w, 422, map[string]any{"detail": err.Error()})
		return
	}
	if payload["id"] == nil {
		writeJSON(w, 422, map[string]any{"detail": "run id is required"})
		return
	}
	if err := s.store.UpsertRun(r.Context(), payload, "post"); err != nil {
		writeJSON(w, 500, map[string]any{"detail": err.Error()})
		return
	}
	writeJSON(w, 202, map[string]any{"id": payload["id"]})
}

func (s *Server) patchRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	body, err := readJSONBody(r, 32<<20)
	if err != nil {
		writeJSON(w, 400, map[string]any{"detail": err.Error()})
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		writeJSON(w, 422, map[string]any{"detail": err.Error()})
		return
	}
	payload["id"] = id
	if err := s.store.UpsertRun(r.Context(), payload, "patch"); err != nil {
		writeJSON(w, 500, map[string]any{"detail": err.Error()})
		return
	}
	writeJSON(w, 202, map[string]any{"id": id})
}

func (s *Server) deleteRun(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteRun(r.Context(), r.PathValue("id")); err != nil {
		writeJSON(w, 500, map[string]any{"detail": err.Error()})
		return
	}
	w.WriteHeader(204)
}

func (s *Server) getRun(w http.ResponseWriter, r *http.Request) {
	run, err := s.store.GetRun(r.Context(), r.PathValue("id"))
	if err != nil {
		writeJSON(w, 404, map[string]any{"detail": "not found"})
		return
	}
	writeJSON(w, 200, run)
}

func (s *Server) queryRuns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	opts := store.QueryOpts{Limit: 100}
	if t := q.Get("trace"); t != "" {
		opts.Trace = t
	}
	if q.Get("error") == "true" {
		opts.ErrorOnly = true
	}
	if lim := q.Get("limit"); lim != "" {
		if n, err := strconv.Atoi(lim); err == nil {
			opts.Limit = n
		}
	}
	if off := q.Get("offset"); off != "" {
		if n, err := strconv.Atoi(off); err == nil {
			opts.Offset = n
		}
	}
	runs, err := s.store.QueryRuns(r.Context(), opts)
	if err != nil {
		writeJSON(w, 500, map[string]any{"detail": err.Error()})
		return
	}
	writeJSON(w, 200, runs)
}

func (s *Server) runStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.store.Stats(r.Context()))
}

func (s *Server) runsQuery(w http.ResponseWriter, r *http.Request) {
	body, err := readJSONBody(r, 4<<20)
	if err != nil {
		writeJSON(w, 400, map[string]any{"detail": err.Error()})
		return
	}
	var payload map[string]any
	if len(body) > 0 {
		if err := json.Unmarshal(body, &payload); err != nil {
			writeJSON(w, 422, map[string]any{"detail": err.Error()})
			return
		}
	}
	if payload == nil {
		payload = map[string]any{}
	}
	opts := store.QueryOpts{Limit: 100}
	if v, ok := payload["limit"]; ok {
		opts.Limit = asIntAny(v)
	}
	if v, ok := payload["offset"]; ok {
		opts.Offset = asIntAny(v)
	}
	if t, ok := payload["trace"].(string); ok && t != "" {
		opts.Trace = t
	}
	if t, ok := payload["parent_run"].(string); ok && t != "" {
		opts.ParentRun = t
	}
	if t, ok := payload["run_type"].(string); ok && t != "" {
		opts.RunType = t
	}
	if t, ok := payload["name"].(string); ok && t != "" {
		opts.Name = t
	}
	if payload["error"] == true {
		opts.ErrorOnly = true
	}
	if v, ok := payload["is_root"]; ok {
		switch b := v.(type) {
		case bool:
			opts.IsRoot = &b
		}
	}
	if st, ok := payload["start_time"].(string); ok && st != "" {
		if t := parseTimeAny(st); t != nil {
			opts.StartAfter = t
		}
	}
	// session: string | []string of UUIDs
	switch sess := payload["session"].(type) {
	case string:
		if sess != "" {
			opts.SessionIDs = []string{sess}
		}
	case []any:
		for _, x := range sess {
			if s, ok := x.(string); ok && s != "" {
				opts.SessionIDs = append(opts.SessionIDs, s)
			}
		}
	}
	switch ids := payload["id"].(type) {
	case string:
		opts.IDs = []string{ids}
	case []any:
		for _, x := range ids {
			if s, ok := x.(string); ok {
				opts.IDs = append(opts.IDs, s)
			}
		}
	}
	runs, err := s.store.QueryRuns(r.Context(), opts)
	if err != nil {
		writeJSON(w, 500, map[string]any{"detail": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"runs": runs, "cursors": map[string]any{"next": nil}})
}

func (s *Server) runsBatch(w http.ResponseWriter, r *http.Request) {
	body, err := readJSONBody(r, 32<<20)
	if err != nil {
		writeJSON(w, 400, map[string]any{"detail": err.Error()})
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		writeJSON(w, 422, map[string]any{"detail": err.Error()})
		return
	}
	batch := ingest.BatchFromJSON(payload)
	result := s.store.PersistBatch(r.Context(), batch)
	writeJSON(w, 202, result)
}

func (s *Server) runsMultipart(w http.ResponseWriter, r *http.Request) {
	body, err := readJSONBody(r, 64<<20)
	if err != nil {
		writeJSON(w, 400, map[string]any{"detail": err.Error()})
		return
	}
	boundary := ingest.BoundaryFromContentType(r.Header.Get("Content-Type"))
	if boundary == "" {
		writeJSON(w, 400, map[string]any{"detail": "missing multipart boundary"})
		return
	}
	parts := ingest.ParseMultipart(body, boundary)
	batch := ingest.BatchFromParts(parts)
	_ = s.store.PersistBatch(r.Context(), batch)
	// LangSmith-local / SDK parity: empty 204
	w.WriteHeader(204)
}

func (s *Server) getAttachment(w http.ResponseWriter, r *http.Request) {
	data, ct, err := s.store.GetAttachment(r.Context(), r.PathValue("id"), r.PathValue("name"))
	if err != nil {
		writeJSON(w, 404, map[string]any{"detail": "not found"})
		return
	}
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(200)
	_, _ = w.Write(data)
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	body, err := readJSONBody(r, 1<<20)
	if err != nil {
		writeJSON(w, 400, map[string]any{"detail": err.Error()})
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		writeJSON(w, 422, map[string]any{"detail": err.Error()})
		return
	}
	sess, err := s.store.CreateSession(r.Context(), payload)
	if err != nil {
		writeJSON(w, 500, map[string]any{"detail": err.Error()})
		return
	}
	writeJSON(w, 200, sess)
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	limit := 100
	if lim := r.URL.Query().Get("limit"); lim != "" {
		if n, err := strconv.Atoi(lim); err == nil {
			limit = n
		}
	}
	list, err := s.store.ListSessions(r.Context(), limit, name)
	if err != nil {
		writeJSON(w, 500, map[string]any{"detail": err.Error()})
		return
	}
	writeJSON(w, 200, list)
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	sess, err := s.store.GetSession(r.Context(), r.PathValue("id"))
	if err != nil {
		writeJSON(w, 404, map[string]any{"detail": "not found"})
		return
	}
	writeJSON(w, 200, sess)
}

func (s *Server) patchSession(w http.ResponseWriter, r *http.Request) {
	body, err := readJSONBody(r, 1<<20)
	if err != nil {
		writeJSON(w, 400, map[string]any{"detail": err.Error()})
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		writeJSON(w, 422, map[string]any{"detail": err.Error()})
		return
	}
	sess, err := s.store.UpdateSession(r.Context(), r.PathValue("id"), payload)
	if err != nil {
		writeJSON(w, 404, map[string]any{"detail": "not found"})
		return
	}
	writeJSON(w, 200, sess)
}

func (s *Server) deleteSession(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteSession(r.Context(), r.PathValue("id")); err != nil {
		writeJSON(w, 500, map[string]any{"detail": err.Error()})
		return
	}
	w.WriteHeader(204)
}

func (s *Server) sessionRuns(w http.ResponseWriter, r *http.Request) {
	limit, offset := 100, 0
	if lim := r.URL.Query().Get("limit"); lim != "" {
		if n, err := strconv.Atoi(lim); err == nil {
			limit = n
		}
	}
	if off := r.URL.Query().Get("offset"); off != "" {
		if n, err := strconv.Atoi(off); err == nil {
			offset = n
		}
	}
	runs, err := s.store.QueryRuns(r.Context(), store.QueryOpts{
		SessionIDs: []string{r.PathValue("id")}, Limit: limit, Offset: offset,
	})
	if err != nil {
		writeJSON(w, 500, map[string]any{"detail": err.Error()})
		return
	}
	writeJSON(w, 200, runs)
}

func (s *Server) createFeedback(w http.ResponseWriter, r *http.Request) {
	body, err := readJSONBody(r, 4<<20)
	if err != nil {
		writeJSON(w, 400, map[string]any{"detail": err.Error()})
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		writeJSON(w, 422, map[string]any{"detail": err.Error()})
		return
	}
	fb, err := s.store.CreateFeedbackFull(r.Context(), payload)
	if err != nil {
		writeJSON(w, 500, map[string]any{"detail": err.Error()})
		return
	}
	writeJSON(w, 200, fb)
}

func (s *Server) listFeedback(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListFeedback(r.Context(), r.URL.Query().Get("run_id"), r.URL.Query().Get("key"))
	if err != nil {
		writeJSON(w, 500, map[string]any{"detail": err.Error()})
		return
	}
	writeJSON(w, 200, list)
}

func (s *Server) uiProjects(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListSessions(r.Context(), 200, "")
	if err != nil {
		writeJSON(w, 500, map[string]any{"detail": err.Error()})
		return
	}
	names := []string{}
	seen := map[string]bool{}
	for _, s := range list {
		n, _ := s["name"].(string)
		if n != "" && !seen[n] {
			names = append(names, n)
			seen[n] = true
		}
	}
	writeJSON(w, 200, names)
}

func (s *Server) uiTraces(w http.ResponseWriter, r *http.Request) {
	session := r.URL.Query().Get("session")
	limit := 100
	if lim := r.URL.Query().Get("limit"); lim != "" {
		if n, err := strconv.Atoi(lim); err == nil {
			limit = n
		}
	}
	var sessionIDs []string
	if session != "" {
		matches, err := s.store.ListSessions(r.Context(), 100, session)
		if err != nil {
			writeJSON(w, 500, map[string]any{"detail": err.Error()})
			return
		}
		if len(matches) == 0 {
			writeJSON(w, 200, []any{})
			return
		}
		for _, m := range matches {
			sessionIDs = append(sessionIDs, m["id"].(string))
		}
	}
	traceIDs, err := s.store.ListTraceIDs(r.Context(), sessionIDs, limit)
	if err != nil {
		writeJSON(w, 500, map[string]any{"detail": err.Error()})
		return
	}
	if len(traceIDs) == 0 {
		writeJSON(w, 200, []any{})
		return
	}
	runs, err := s.store.QueryRuns(r.Context(), store.QueryOpts{Traces: traceIDs, Unlimited: true})
	if err != nil {
		writeJSON(w, 500, map[string]any{"detail": err.Error()})
		return
	}
	writeJSON(w, 200, traces.TraceSummaries(runs))
}

func (s *Server) uiTrace(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	runs, err := s.store.QueryRuns(r.Context(), store.QueryOpts{Trace: id, Unlimited: true})
	if err != nil {
		writeJSON(w, 500, map[string]any{"detail": err.Error()})
		return
	}
	detail := traces.TraceDetail(id, runs)
	if detail == nil {
		writeJSON(w, 404, map[string]any{"detail": "trace not found"})
		return
	}
	writeJSON(w, 200, detail)
}

func cors(origins []string, next http.Handler) http.Handler {
	allowAll := len(origins) == 0 || (len(origins) == 1 && origins[0] == "*")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if allowAll {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		} else if origin != "" {
			for _, o := range origins {
				if o == origin {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					break
				}
			}
		}
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Encoding, x-api-key, Authorization")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func LogRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}
