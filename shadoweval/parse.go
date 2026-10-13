package shadoweval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// Action is the subset of a decision action used for shadow scoring.
type Action struct {
	Action     string  `json:"action"`
	Symbol     string  `json:"symbol"`
	Price      float64 `json:"price"`
	Confidence float64 `json:"confidence"`
	Reasoning  string  `json:"reasoning"`
	OrderID    int64   `json:"order_id"`
	Success    *bool   `json:"success"`
	Error      string  `json:"error"`
}

// Parsed is one shadow row derived from a decision action.
type Parsed struct {
	Index         int
	Symbol        string
	Action        string
	Direction     string // long | short | flat | close
	Source        string // ai | jev
	Status        string // executed | rejected | vetoed | wait | hold | ...
	Confidence    *float64
	Probability   *float64
	Regime        string
	Executed      bool
	DecisionPrice *float64
}

var (
	jevVetoRe   = regexp.MustCompile(`Jev signal (open_long|open_short) vetoed[^\n]*?probability ([0-9.]+)`)
	regimeRe    = regexp.MustCompile(`regime=([a-z_]+)`)
	jevSystemRe = regexp.MustCompile(`^Jev `)
)

// PromptHash returns a short stable hash of a system prompt.
func PromptHash(prompt string) string {
	if strings.TrimSpace(prompt) == "" {
		return ""
	}
	h := sha256.Sum256([]byte(prompt))
	return hex.EncodeToString(h[:])[:12]
}

func parseActions(raw string) []Action {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return nil
	}
	var out []Action
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

// ParseRecord converts a decision record's executed actions (decisions column)
// and model output (decision_json) into shadow rows. The executed list is
// authoritative for success; decision_json fills reasoning/confidence gaps.
func ParseRecord(decisionsCol, decisionJSON string) []Parsed {
	exec := parseActions(decisionsCol)
	model := parseActions(decisionJSON)
	base := exec
	if len(base) == 0 {
		base = model
	}
	byKey := map[string]Action{}
	for _, a := range model {
		byKey[a.Symbol+"|"+a.Action] = a
	}
	var rows []Parsed
	for i, a := range base {
		if a.Symbol == "" || a.Action == "" {
			continue
		}
		if m, ok := byKey[a.Symbol+"|"+a.Action]; ok {
			if a.Reasoning == "" {
				a.Reasoning = m.Reasoning
			}
			if a.Confidence == 0 {
				a.Confidence = m.Confidence
			}
		}
		p := Parsed{Index: i, Symbol: strings.ToUpper(a.Symbol), Action: a.Action, Source: "ai"}
		if a.Confidence > 0 {
			c := a.Confidence
			p.Confidence = &c
		}
		if a.Price > 0 {
			v := a.Price
			p.DecisionPrice = &v
		}
		if jevSystemRe.MatchString(a.Reasoning) || strings.Contains(a.Reasoning, "Jev signal") {
			p.Source = "jev"
		}
		if m := regimeRe.FindStringSubmatch(a.Reasoning); m != nil {
			p.Regime = m[1]
		}
		success := a.Success == nil || *a.Success
		switch {
		case strings.HasPrefix(a.Action, "open_"):
			p.Direction = strings.TrimPrefix(a.Action, "open_")
			if success && a.Error == "" && a.Success != nil {
				p.Executed, p.Status = true, "executed"
			} else if a.Success == nil {
				p.Status = "unknown"
			} else {
				p.Status = "rejected"
			}
		case strings.HasPrefix(a.Action, "close_"):
			p.Direction = "close"
			p.Executed = success && a.Error == "" && a.Success != nil
			p.Status = map[bool]string{true: "executed", false: "rejected"}[p.Executed]
		default:
			p.Direction = "flat"
			p.Status = a.Action
		}
		if m := jevVetoRe.FindStringSubmatch(a.Reasoning); m != nil {
			p.Source = "jev"
			p.Direction = strings.TrimPrefix(m[1], "open_")
			p.Status = "vetoed"
			p.Executed = false
			if v, err := strconv.ParseFloat(m[2], 64); err == nil {
				p.Probability = &v
			}
		}
		rows = append(rows, p)
	}
	return rows
}
