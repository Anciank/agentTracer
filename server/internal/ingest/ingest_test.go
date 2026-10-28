package ingest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
)

const boundary = "boundary123"

func encodeMultipart(parts []struct {
	name, ct string
	data     []byte
}) []byte {
	var out bytes.Buffer
	for _, p := range parts {
		fmt.Fprintf(&out, "--%s\r\n", boundary)
		fmt.Fprintf(&out, "Content-Disposition: form-data; name=\"%s\"\r\n", p.name)
		fmt.Fprintf(&out, "Content-Type: %s\r\n\r\n", p.ct)
		out.Write(p.data)
		out.WriteString("\r\n")
	}
	fmt.Fprintf(&out, "--%s--\r\n", boundary)
	return out.Bytes()
}

func TestMultipartFragmentedAndBinary(t *testing.T) {
	runID, traceID := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	main, _ := json.Marshal(map[string]any{"id": runID, "trace_id": traceID, "name": "n", "run_type": "llm"})
	inputs, _ := json.Marshal(map[string]any{"prompt": "hi"})
	errorJSON, _ := json.Marshal("boom")
	blob := make([]byte, 256)
	for i := range blob {
		blob[i] = byte(i)
	}
	body := encodeMultipart([]struct {
		name, ct string
		data     []byte
	}{
		{fmt.Sprintf("post.%s", runID), "application/json", main},
		{fmt.Sprintf("post.%s.inputs", runID), "application/json", inputs},
		{fmt.Sprintf("post.%s.error", runID), "application/json", errorJSON},
		{fmt.Sprintf("attachment.%s.chart", runID), "image/png", blob},
	})
	batch := BatchFromParts(ParseMultipart(body, boundary))
	if batch.Creates[runID]["name"] != "n" {
		t.Fatalf("name=%v", batch.Creates[runID]["name"])
	}
	in := batch.Creates[runID]["inputs"].(map[string]any)
	if in["prompt"] != "hi" {
		t.Fatalf("inputs=%v", in)
	}
	if batch.Creates[runID]["error"] != "boom" {
		t.Fatalf("error=%v", batch.Creates[runID]["error"])
	}
	if len(batch.Attachments) != 1 || batch.Attachments[0].Name != "chart" {
		t.Fatalf("attachments=%v", batch.Attachments)
	}
	if batch.Attachments[0].ContentType != "image/png" {
		t.Fatalf("ct=%s", batch.Attachments[0].ContentType)
	}
	if !bytes.Equal(batch.Attachments[0].Data, blob) {
		t.Fatal("blob mismatch")
	}
}

func TestMultipartCRLFInPayload(t *testing.T) {
	runID := "cccccccc-cccc-cccc-cccc-cccccccccccc"
	payload, _ := json.Marshal(map[string]any{"id": runID, "trace_id": "t", "name": "a\r\nb"})
	body := encodeMultipart([]struct {
		name, ct string
		data     []byte
	}{{fmt.Sprintf("post.%s", runID), "application/json", payload}})
	batch := BatchFromParts(ParseMultipart(body, boundary))
	if batch.Creates[runID]["name"] != "a\r\nb" {
		t.Fatalf("got %q", batch.Creates[runID]["name"])
	}
}

func TestAttachmentLengthParam(t *testing.T) {
	runID := "dddddddd-dddd-dddd-dddd-dddddddddddd"
	body := encodeMultipart([]struct {
		name, ct string
		data     []byte
	}{{fmt.Sprintf("attachment.%s.x", runID), "image/png; length=2", []byte{0, 1}}})
	batch := BatchFromParts(ParseMultipart(body, boundary))
	if batch.Attachments[0].ContentType != "image/png" {
		t.Fatalf("ct=%s", batch.Attachments[0].ContentType)
	}
}

func TestPostsPatchesFeedback(t *testing.T) {
	postID, patchID, fbID := "p1", "p2", "f1"
	body := encodeMultipart([]struct {
		name, ct string
		data     []byte
	}{
		{"post." + postID, "application/json", []byte(`{"id":"p1","name":"created"}`)},
		{"patch." + patchID, "application/json", []byte(`{"id":"p2","outputs":{"o":1}}`)},
		{"feedback." + fbID, "application/json", []byte(`{"id":"f1","key":"quality","score":1}`)},
	})
	batch := BatchFromParts(ParseMultipart(body, boundary))
	if len(batch.Creates) != 1 || len(batch.Updates) != 1 || len(batch.Feedback) != 1 {
		t.Fatalf("c=%d u=%d f=%d", len(batch.Creates), len(batch.Updates), len(batch.Feedback))
	}
}

func TestBatchFromJSON(t *testing.T) {
	batch := BatchFromJSON(map[string]any{
		"post":  []any{map[string]any{"id": "a", "name": "x"}, map[string]any{"name": "no-id"}},
		"patch": []any{map[string]any{"id": "b", "outputs": map[string]any{}}},
	})
	if len(batch.Creates) != 1 || batch.Creates["a"]["name"] != "x" {
		t.Fatalf("%v", batch.Creates)
	}
	if len(batch.Updates) != 1 {
		t.Fatalf("%v", batch.Updates)
	}
}

func TestZstdAndGzip(t *testing.T) {
	plain := []byte(`{"hello":"world"}`)
	var gzBuf bytes.Buffer
	zw := gzip.NewWriter(&gzBuf)
	_, _ = zw.Write(plain)
	_ = zw.Close()
	out, err := MaybeDecompress(gzBuf.Bytes(), "gzip")
	if err != nil || !bytes.Equal(out, plain) {
		t.Fatalf("gzip: %v %s", err, out)
	}
	enc, _ := zstd.NewWriter(nil)
	zst := enc.EncodeAll(plain, nil)
	_ = enc.Close()
	out, err = MaybeDecompress(zst, "zstd")
	if err != nil || !bytes.Equal(out, plain) {
		t.Fatalf("zstd: %v %s", err, out)
	}
}
