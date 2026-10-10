package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
)

// Bill scanning is optional: it is enabled only when ANTHROPIC_API_KEY is set.
// The extracted fields are a suggestion; the treasurer confirms before saving.

func ScanEnabled() bool { return os.Getenv("ANTHROPIC_API_KEY") != "" }

type ScannedBill struct {
	Date        string     `json:"date"`
	Vendor      string     `json:"vendor"`
	Description string     `json:"description"`
	Total       float64    `json:"total"`
	Category    string     `json:"category"`
	LineItems   []BillLine `json:"line_items"`
	Notes       string     `json:"notes"`
}

type BillLine struct {
	Item   string  `json:"item"`
	Amount float64 `json:"amount"`
}

var billSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"required":             []string{"date", "vendor", "description", "total", "category", "line_items", "notes"},
	"properties": map[string]any{
		"date":        map[string]any{"type": "string", "description": "Bill date as YYYY-MM-DD; empty string if unreadable"},
		"vendor":      map[string]any{"type": "string"},
		"description": map[string]any{"type": "string", "description": "Short particulars in the style of an expense register, e.g. 'Diesel for DG' or 'Housekeeping items'"},
		"total":       map[string]any{"type": "number", "description": "Grand total in rupees"},
		"category":    map[string]any{"type": "string"},
		"line_items": map[string]any{"type": "array", "items": map[string]any{
			"type": "object", "additionalProperties": false, "required": []string{"item", "amount"},
			"properties": map[string]any{"item": map[string]any{"type": "string"}, "amount": map[string]any{"type": "number"}},
		}},
		"notes": map[string]any{"type": "string", "description": "Anything uncertain or illegible"},
	},
}

func (a *App) ScanBill(ctx context.Context, data []byte) (*ScannedBill, error) {
	if !ScanEnabled() {
		return nil, fmt.Errorf("bill scanning is disabled (set ANTHROPIC_API_KEY to enable)")
	}
	cats, err := a.Categories()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, c := range cats {
		if c.InPnL {
			names = append(names, c.Name)
		}
	}
	mime := http.DetectContentType(data)
	b64 := base64.StdEncoding.EncodeToString(data)
	var media anthropic.BetaContentBlockParamUnion
	switch {
	case mime == "application/pdf":
		media = anthropic.NewBetaDocumentBlock(anthropic.BetaBase64PDFSourceParam{Data: b64})
	case mime == "image/jpeg" || mime == "image/png" || mime == "image/gif" || mime == "image/webp":
		media = anthropic.NewBetaImageBlock(anthropic.BetaBase64ImageSourceParam{Data: b64, MediaType: anthropic.BetaBase64ImageSourceMediaType(mime)})
	default:
		return nil, fmt.Errorf("unsupported file type %s (use JPEG, PNG, WebP or PDF)", mime)
	}

	prompt := fmt.Sprintf(`This is a bill or receipt for an apartment owners' association in Bengaluru, India.
Extract the bill date, vendor, grand total in rupees and line items.
Pick the category from this list: %s.
Today is %s; bills are usually recent, and Indian bills write dates day first (dd/mm/yyyy).`,
		strings.Join(names, ", "), Today().Format(DateFmt))

	client := anthropic.NewClient()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	resp, err := client.Beta.Messages.New(ctx, anthropic.BetaMessageNewParams{
		Model:     "claude-opus-5-5",
		MaxTokens: 16000,
		Betas:     []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01},
		Fallbacks: anthropic.BetaFallbacksParamUnion{OfDefault: constant.ValueOf[constant.Default]()},
		OutputConfig: anthropic.BetaOutputConfigParam{
			Effort: anthropic.BetaOutputConfigEffortLow,
			Format: anthropic.BetaJSONOutputFormatParam{Schema: billSchema},
		},
		Messages: []anthropic.BetaMessageParam{
			anthropic.NewBetaUserMessage(media, anthropic.NewBetaTextBlock(prompt)),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("scan failed: %w", err)
	}
	if resp.StopReason == anthropic.BetaStopReasonRefusal {
		return nil, fmt.Errorf("the model declined to read this file; please enter the bill manually")
	}
	var text string
	for _, b := range resp.Content {
		if t, ok := b.AsAny().(anthropic.BetaTextBlock); ok {
			text += t.Text
		}
	}
	var bill ScannedBill
	if err := json.Unmarshal([]byte(text), &bill); err != nil {
		return nil, fmt.Errorf("could not read scan result: %w", err)
	}
	return &bill, nil
}
