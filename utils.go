package ago

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"time"
)

func schemaOf(t reflect.Type) json.RawMessage {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	props := map[string]any{}
	required := []string{}
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		parts := strings.Split(f.Tag.Get("json"), ",")
		name := parts[0]
		if name == "-" {
			continue
		}
		if name == "" {
			name = strings.ToLower(f.Name)
		}
		omitempty := len(parts) > 1 && strings.Contains(parts[1], "omitempty")
		props[name] = jsonProp(f.Type)
		if !omitempty {
			required = append(required, name)
		}
	}
	b, _ := json.Marshal(map[string]any{
		"type":       "object",
		"properties": props,
		"required":   required,
	})
	return b
}

func jsonProp(t reflect.Type) map[string]any {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		var schema map[string]any
		json.Unmarshal(schemaOf(t), &schema)
		return schema
	case reflect.Slice:
		return map[string]any{
			"type":  "array",
			"items": jsonProp(t.Elem()),
		}
	default:
		return map[string]any{"type": jsonKind(t.Kind())}
	}
}

func jsonKind(k reflect.Kind) string {
	switch k {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Bool:
		return "boolean"
	default:
		return "string"
	}
}

var httpClient = &http.Client{Timeout: 2 * time.Minute}

const maxRetries = 3

var retryBaseDelay = time.Second

type Resource string

type ResourceID string

func newID(r Resource) ResourceID {
	var b [8]byte
	rand.Read(b[:])
	return ResourceID(fmt.Sprintf("%s_%x", r, b[:]))
}

func httpPost(ctx context.Context, url string, headers map[string]string, body []byte) ([]byte, error) {
	var lastErr error
	for attempt := range maxRetries + 1 {
		if attempt > 0 {
			delay := retryBaseDelay * time.Duration(1<<(attempt-1))
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, data)
			continue
		}
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, data)
		}
		return data, nil
	}
	return nil, lastErr
}
