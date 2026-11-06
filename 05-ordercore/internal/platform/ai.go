package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// AI endpoint is an operator-controlled internal service, never supplied by API callers.
func AI(ctx context.Context, kind string, input map[string]any) (string, string, error) {
	endpoint := os.Getenv("AI_URL")
	if endpoint == "" {
		if kind == "rag" {
			return "Найдены релевантные фрагменты; см. sources. Генерация ответа отключена.", "retrieval_only", nil
		}
		return "Спасибо за обращение. Оператор проверит сведения и подготовит решение.", "template", nil
	}
	body, _ := json.Marshal(input)
	req, e := http.NewRequestWithContext(ctx, "POST", endpoint+"/"+kind, bytes.NewReader(body))
	if e != nil {
		return "", "", e
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+os.Getenv("AI_SERVICE_TOKEN"))
	resp, e := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if e != nil {
		return "", "", Fail(502, "AI service unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", "", Fail(502, fmt.Sprintf("AI service returned %d", resp.StatusCode))
	}
	var out struct {
		Answer string `json:"answer"`
		Mode   string `json:"mode"`
	}
	if e = json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&out); e != nil {
		return "", "", e
	}
	if out.Answer == "" {
		return "", "", Fail(502, "empty AI response")
	}
	return out.Answer, out.Mode, nil
}
