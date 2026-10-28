package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/klauspost/compress/zstd"
)

// TestPatchBeforePostSeparateRequests: patch lands first; later create with
// end_time:null must not wipe end_time/outputs/status.
func TestPatchBeforePostSeparateRequests(t *testing.T) {
	srv, key := testServer(t)
	h := srv.Handler()
	runID := uuid.NewString()
	traceID := runID
	end := time.Now().UTC().Format(time.RFC3339Nano)
	start := time.Now().UTC().Add(-time.Second).Format(time.RFC3339Nano)

	patchBody, _ := json.Marshal(map[string]any{
		"id": runID, "trace_id": traceID, "name": "chat", "run_type": "chain",
		"session_name": "patch-order", "start_time": start, "end_time": end,
		"outputs":      map[string]any{"messages": []any{"你好"}},
		"dotted_order": "20260101T000000000000Z" + runID[:8],
	})
	req := httptest.NewRequest("POST", "/runs/multipart", bytes.NewReader(encodeMP([]struct {
		name, ct string
		data     []byte
	}{
		{"patch." + runID, "application/json", patchBody},
		{"patch." + runID + ".outputs", "application/json", []byte(`{"messages":["你好"]}`)},
	})))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=boundary123")
	req.Header.Set("X-Api-Key", key)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 204 {
		t.Fatalf("patch multipart %d %s", rr.Code, rr.Body.String())
	}

	// Create with explicit nulls (langsmith serialize_run_dict shape)
	postBody, _ := json.Marshal(map[string]any{
		"id": runID, "trace_id": traceID, "name": "chat", "run_type": "chain",
		"session_name": "patch-order", "start_time": start, "end_time": nil,
		"dotted_order": "20260101T000000000000Z" + runID[:8],
		"error":        nil,
	})
	req = httptest.NewRequest("POST", "/runs/multipart", bytes.NewReader(encodeMP([]struct {
		name, ct string
		data     []byte
	}{
		{"post." + runID, "application/json", postBody},
		{"post." + runID + ".inputs", "application/json", []byte(`{"x":1}`)},
		{"post." + runID + ".outputs", "application/json", []byte(`{}`)},
	})))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=boundary123")
	req.Header.Set("X-Api-Key", key)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 204 {
		t.Fatalf("post multipart %d %s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest("GET", "/api/ui/runs/"+runID, nil)
	req.Header.Set("X-Api-Key", key)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("get run %d %s", rr.Code, rr.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["end_time"] == nil || got["end_time"] == "" {
		t.Fatalf("end_time wiped after late create: %#v", got["end_time"])
	}
	if fmt.Sprint(got["status"]) != "success" {
		t.Fatalf("status=%v want success", got["status"])
	}
	outs, _ := got["outputs"].(map[string]any)
	if outs == nil || outs["messages"] == nil {
		t.Fatalf("outputs wiped: %#v", got["outputs"])
	}
}

func TestPostPatchSameMultipartBothOrders(t *testing.T) {
	srv, key := testServer(t)
	h := srv.Handler()
	for _, order := range []string{"post-then-patch", "patch-then-post"} {
		t.Run(order, func(t *testing.T) {
			runID := uuid.NewString()
			traceID := runID
			end := time.Now().UTC().Format(time.RFC3339Nano)
			start := time.Now().UTC().Add(-time.Second).Format(time.RFC3339Nano)
			postBody, _ := json.Marshal(map[string]any{
				"id": runID, "trace_id": traceID, "name": "chat", "run_type": "chain",
				"session_name": "same-mp", "start_time": start, "end_time": nil,
				"dotted_order": "20260101T000000000000Z" + runID[:8],
			})
			patchBody, _ := json.Marshal(map[string]any{
				"id": runID, "trace_id": traceID, "end_time": end,
				"dotted_order": "20260101T000000000000Z" + runID[:8],
			})
			parts := []struct {
				name, ct string
				data     []byte
			}{
				{"post." + runID, "application/json", postBody},
				{"post." + runID + ".inputs", "application/json", []byte(`{"q":"你好"}`)},
				{"patch." + runID, "application/json", patchBody},
				{"patch." + runID + ".outputs", "application/json", []byte(`{"messages":["ok"]}`)},
			}
			if order == "patch-then-post" {
				parts = []struct {
					name, ct string
					data     []byte
				}{
					{"patch." + runID, "application/json", patchBody},
					{"patch." + runID + ".outputs", "application/json", []byte(`{"messages":["ok"]}`)},
					{"post." + runID, "application/json", postBody},
					{"post." + runID + ".inputs", "application/json", []byte(`{"q":"你好"}`)},
				}
			}
			req := httptest.NewRequest("POST", "/runs/multipart", bytes.NewReader(encodeMP(parts)))
			req.Header.Set("Content-Type", "multipart/form-data; boundary=boundary123")
			req.Header.Set("X-Api-Key", key)
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != 204 {
				t.Fatalf("multipart %d %s", rr.Code, rr.Body.String())
			}
			req = httptest.NewRequest("GET", "/api/ui/runs/"+runID, nil)
			req.Header.Set("X-Api-Key", key)
			rr = httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			var got map[string]any
			_ = json.Unmarshal(rr.Body.Bytes(), &got)
			if got["end_time"] == nil || got["end_time"] == "" {
				t.Fatalf("%s: end_time missing: %#v", order, got)
			}
			if fmt.Sprint(got["status"]) != "success" {
				t.Fatalf("%s: status=%v", order, got["status"])
			}
			outs, _ := got["outputs"].(map[string]any)
			if outs == nil || outs["messages"] == nil {
				t.Fatalf("%s: outputs=%#v", order, got["outputs"])
			}
		})
	}
}

func TestPostPatchSameBatchJSON(t *testing.T) {
	srv, key := testServer(t)
	h := srv.Handler()
	runID := uuid.NewString()
	traceID := runID
	end := time.Now().UTC().Format(time.RFC3339Nano)
	start := time.Now().UTC().Add(-time.Second).Format(time.RFC3339Nano)
	body, _ := json.Marshal(map[string]any{
		"post": []map[string]any{{
			"id": runID, "trace_id": traceID, "name": "chat", "run_type": "chain",
			"session_name": "batch-order", "start_time": start, "end_time": nil,
			"inputs": map[string]any{"q": 1}, "dotted_order": "20260101T000000000000Z" + runID[:8],
		}},
		"patch": []map[string]any{{
			"id": runID, "trace_id": traceID, "end_time": end,
			"outputs":      map[string]any{"messages": []any{"done"}},
			"dotted_order": "20260101T000000000000Z" + runID[:8],
		}},
	})
	req := httptest.NewRequest("POST", "/runs/batch", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", key)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 202 {
		t.Fatalf("batch %d %s", rr.Code, rr.Body.String())
	}
	req = httptest.NewRequest("GET", "/api/ui/runs/"+runID, nil)
	req.Header.Set("X-Api-Key", key)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var got map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if got["end_time"] == nil {
		t.Fatalf("batch end_time wiped: %#v", got)
	}
	if fmt.Sprint(got["status"]) != "success" {
		t.Fatalf("status=%v", got["status"])
	}
}

func TestInfoAdvertisesZstdCompression(t *testing.T) {
	srv, key := testServer(t)
	h := srv.Handler()
	req := httptest.NewRequest("GET", "/info", nil)
	req.Header.Set("X-Api-Key", key)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("info %d", rr.Code)
	}
	var info map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &info)
	flags, _ := info["instance_flags"].(map[string]any)
	if flags == nil || flags["zstd_compression_enabled"] != true {
		t.Fatalf("instance_flags.zstd_compression_enabled missing: %#v", info["instance_flags"])
	}
	bic, _ := info["batch_ingest_config"].(map[string]any)
	if bic == nil || bic["use_multipart_endpoint"] != true {
		t.Fatalf("batch_ingest_config.use_multipart_endpoint missing: %#v", bic)
	}
}

// TestZstdMultipartAsSDKBuilds: Content-Encoding: zstd over multipart like
// langsmith CompressedTraces / _send_compressed_multipart_req.
func TestZstdMultipartAsSDKBuilds(t *testing.T) {
	srv, key := testServer(t)
	h := srv.Handler()
	runID := uuid.NewString()
	traceID := runID
	end := time.Now().UTC().Format(time.RFC3339Nano)
	start := time.Now().UTC().Add(-time.Second).Format(time.RFC3339Nano)
	postBody, _ := json.Marshal(map[string]any{
		"id": runID, "trace_id": traceID, "name": "chat", "run_type": "chain",
		"session_name": "zstd-sdk", "start_time": start, "end_time": nil,
		"dotted_order": "20260101T000000000000Z" + runID[:8],
	})
	patchBody, _ := json.Marshal(map[string]any{
		"id": runID, "end_time": end, "trace_id": traceID,
		"dotted_order": "20260101T000000000000Z" + runID[:8],
	})
	raw := encodeMP([]struct {
		name, ct string
		data     []byte
	}{
		{"post." + runID, "application/json", postBody},
		{"post." + runID + ".inputs", "application/json", []byte(`{"messages":[{"content":"压缩测试"}]}`)},
		{"patch." + runID, "application/json", patchBody},
		{"patch." + runID + ".outputs", "application/json", []byte(`{"messages":[{"content":"ok"}]}`)},
	})
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedFastest))
	if err != nil {
		t.Fatal(err)
	}
	zst := enc.EncodeAll(raw, nil)
	_ = enc.Close()

	req := httptest.NewRequest("POST", "/runs/multipart", bytes.NewReader(zst))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=boundary123")
	req.Header.Set("Content-Encoding", "zstd")
	req.Header.Set("X-Api-Key", key)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 204 {
		t.Fatalf("zstd multipart %d %s", rr.Code, rr.Body.String())
	}
	req = httptest.NewRequest("GET", "/api/ui/runs/"+runID, nil)
	req.Header.Set("X-Api-Key", key)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var got map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if got["end_time"] == nil {
		t.Fatalf("zstd sdk body lost end_time: %#v", got)
	}
	if fmt.Sprint(got["status"]) != "success" {
		t.Fatalf("status=%v", got["status"])
	}
}
