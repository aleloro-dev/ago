package ago

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
)

func schemaOf(t reflect.Type) json.RawMessage {
	props := map[string]any{}
	required := []string{}
	for i := range t.NumField() {
		f := t.Field(i)
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		if name == "" {
			name = strings.ToLower(f.Name)
		}
		props[name] = jsonProp(f.Type)
		required = append(required, name)
	}
	b, _ := json.Marshal(map[string]any{
		"type":       "object",
		"properties": props,
		"required":   required,
	})
	return b
}

func jsonProp(t reflect.Type) map[string]any {
	if t.Kind() == reflect.Slice {
		return map[string]any{
			"type":  "array",
			"items": map[string]any{"type": jsonKind(t.Elem().Kind())},
		}
	}
	return map[string]any{"type": jsonKind(t.Kind())}
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

func httpPost(url string, headers map[string]string, body []byte) ([]byte, error) {
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, data)
	}
	return data, nil
}
