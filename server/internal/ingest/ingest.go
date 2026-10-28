package ingest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
)

type RawPart struct {
	Name    string
	Headers map[string]string
	Data    []byte
}

func (p RawPart) ContentType() string {
	ct := p.Headers["content-type"]
	if ct == "" {
		return ""
	}
	return strings.TrimSpace(strings.Split(ct, ";")[0])
}

type Attachment struct {
	RunID       string
	Name        string
	ContentType string
	Data        []byte
}

type Batch struct {
	Creates     map[string]map[string]any
	Updates     map[string]map[string]any
	Feedback    []map[string]any
	Attachments []Attachment
}

func NewBatch() *Batch {
	return &Batch{
		Creates:  map[string]map[string]any{},
		Updates:  map[string]map[string]any{},
		Feedback: []map[string]any{},
	}
}

func (b *Batch) TotalRuns() int { return len(b.Creates) + len(b.Updates) }

func ParseMultipart(body []byte, boundary string) []RawPart {
	delimiter := []byte("\r\n--" + boundary)
	segments := bytes.Split(append([]byte("\r\n"), body...), delimiter)
	var parts []RawPart
	for _, segment := range segments[1:] {
		if bytes.HasPrefix(segment, []byte("--")) {
			break
		}
		if bytes.HasPrefix(segment, []byte("\r\n")) {
			segment = segment[2:]
		}
		idx := bytes.Index(segment, []byte("\r\n\r\n"))
		if idx < 0 {
			continue
		}
		headerBlock, data := segment[:idx], segment[idx+4:]
		headers := map[string]string{}
		for _, line := range bytes.Split(headerBlock, []byte("\r\n")) {
			colon := bytes.IndexByte(line, ':')
			if colon < 0 {
				continue
			}
			key := strings.ToLower(strings.TrimSpace(string(line[:colon])))
			val := strings.TrimSpace(string(line[colon+1:]))
			headers[key] = val
		}
		name, _ := parseDisposition(headers["content-disposition"])
		if name == "" {
			continue
		}
		parts = append(parts, RawPart{Name: name, Headers: headers, Data: data})
	}
	return parts
}

func parseDisposition(value string) (name, filename string) {
	for _, token := range strings.Split(value, ";") {
		token = strings.TrimSpace(token)
		lower := strings.ToLower(token)
		if strings.HasPrefix(lower, "name=") {
			name = strings.Trim(token[5:], `"`)
		} else if strings.HasPrefix(lower, "filename=") {
			filename = strings.Trim(token[9:], `"`)
		}
	}
	return
}

func MaybeDecompress(body []byte, contentEncoding string) ([]byte, error) {
	if contentEncoding == "" {
		return body, nil
	}
	encodings := strings.Split(strings.ToLower(contentEncoding), ",")
	for i := range encodings {
		encodings[i] = strings.TrimSpace(encodings[i])
	}
	out := body
	for _, enc := range encodings {
		switch enc {
		case "zstd":
			dec, err := zstd.NewReader(nil)
			if err != nil {
				return nil, err
			}
			defer dec.Close()
			out, err = dec.DecodeAll(out, nil)
			if err != nil {
				return nil, fmt.Errorf("zstd decompress: %w", err)
			}
		case "gzip", "x-gzip":
			r, err := gzip.NewReader(bytes.NewReader(out))
			if err != nil {
				return nil, fmt.Errorf("gzip decompress: %w", err)
			}
			var buf bytes.Buffer
			if _, err := buf.ReadFrom(r); err != nil {
				_ = r.Close()
				return nil, err
			}
			_ = r.Close()
			out = buf.Bytes()
		}
	}
	return out, nil
}

func decodeJSON(data []byte) any {
	if len(data) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return nil
	}
	return v
}

func BatchFromParts(parts []RawPart) *Batch {
	batch := NewBatch()
	for _, part := range parts {
		name := part.Name
		if strings.HasPrefix(name, "attachment.") {
			rest := name[len("attachment."):]
			runID, attName, ok := strings.Cut(rest, ".")
			if ok && runID != "" && attName != "" {
				batch.Attachments = append(batch.Attachments, Attachment{
					RunID: runID, Name: attName, ContentType: part.ContentType(), Data: part.Data,
				})
			}
			continue
		}
		if strings.HasPrefix(name, "feedback.") {
			if payload, ok := decodeJSON(part.Data).(map[string]any); ok {
				batch.Feedback = append(batch.Feedback, payload)
			}
			continue
		}
		op, rest, ok := strings.Cut(name, ".")
		if !ok || (op != "post" && op != "patch") || rest == "" {
			continue
		}
		runID, subField, _ := strings.Cut(rest, ".")
		payload := decodeJSON(part.Data)
		if payload == nil {
			continue
		}
		target := batch.Creates
		if op == "patch" {
			target = batch.Updates
		}
		entry, ok := target[runID]
		if !ok {
			entry = map[string]any{}
			target[runID] = entry
		}
		if subField != "" {
			entry[subField] = payload
		} else if m, ok := payload.(map[string]any); ok {
			for k, v := range m {
				entry[k] = v
			}
		}
	}
	return batch
}

func BatchFromJSON(body map[string]any) *Batch {
	batch := NewBatch()
	for _, pair := range []struct {
		op     string
		target map[string]map[string]any
	}{
		{"post", batch.Creates},
		{"patch", batch.Updates},
	} {
		raw := body[pair.op]
		var payloads []any
		switch v := raw.(type) {
		case nil:
			continue
		case []any:
			payloads = v
		case map[string]any:
			payloads = []any{v}
		default:
			continue
		}
		for _, item := range payloads {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			runID := fmt.Sprint(m["id"])
			if runID == "" || runID == "<nil>" {
				continue
			}
			entry, ok := pair.target[runID]
			if !ok {
				entry = map[string]any{}
				pair.target[runID] = entry
			}
			for k, v := range m {
				entry[k] = v
			}
		}
	}
	return batch
}

func BoundaryFromContentType(ct string) string {
	for _, chunk := range strings.Split(ct, ";") {
		chunk = strings.TrimSpace(chunk)
		if strings.HasPrefix(strings.ToLower(chunk), "boundary=") {
			return strings.Trim(chunk[len("boundary="):], `"`)
		}
	}
	return ""
}
