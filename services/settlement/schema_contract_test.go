package main

// Avro schema pins: the events this service consumes (TradeExecuted) and
// publishes (SettlementFailed) are validated against their contracts in
// ../../libs/schemas/*.avsc. Guards against struct/Avro drift (a known past
// failure mode — see commit "align SettlementFailed struct with Avro schema").
// Dependency-free: walks the .avsc JSON directly. Unknown JSON keys are
// ignored — additive fields are safe under Avro field resolution — but
// required fields must be present with the right JSON type.

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"testing"
)

func loadAvsc(t *testing.T, name string) any {
	t.Helper()
	path := fmt.Sprintf("../../libs/schemas/%s.avsc", name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var schema any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return schema
}

func collectAvroDefs(schema any, defs map[string]any) {
	switch s := schema.(type) {
	case map[string]any:
		if n, ok := s["name"].(string); ok {
			if typ, _ := s["type"].(string); typ == "record" || typ == "enum" {
				defs[n] = schema
			}
		}
		for _, v := range s {
			collectAvroDefs(v, defs)
		}
	case []any:
		for _, v := range s {
			collectAvroDefs(v, defs)
		}
	}
}

func validateAvro(v any, schema any, defs map[string]any, path string) error {
	switch s := schema.(type) {
	case string:
		return validateAvroNamed(v, s, defs, path)
	case []any: // union
		var errs []error
		for _, branch := range s {
			if err := validateAvro(v, branch, defs, path); err == nil {
				return nil
			} else {
				errs = append(errs, err)
			}
		}
		return fmt.Errorf("%s: no union branch matches %v: %v", path, v, errs)
	case map[string]any:
		typ, _ := s["type"].(string)
		switch typ {
		case "record":
			obj, ok := v.(map[string]any)
			if !ok {
				return fmt.Errorf("%s: expected object, got %v", path, v)
			}
			for _, f := range s["fields"].([]any) {
				field := f.(map[string]any)
				fname := field["name"].(string)
				fv, present := obj[fname]
				if !present {
					if _, hasDefault := field["default"]; hasDefault {
						continue // Avro default applies
					}
					return fmt.Errorf("%s: missing required field %s", path, fname)
				}
				if err := validateAvro(fv, field["type"], defs, path+"."+fname); err != nil {
					return err
				}
			}
			return nil
		case "enum":
			str, ok := v.(string)
			if !ok {
				return fmt.Errorf("%s: expected enum string, got %v", path, v)
			}
			for _, sym := range s["symbols"].([]any) {
				if sym == str {
					return nil
				}
			}
			return fmt.Errorf("%s: %q not in enum symbols %v", path, str, s["symbols"])
		case "array":
			arr, ok := v.([]any)
			if !ok {
				return fmt.Errorf("%s: expected array, got %v", path, v)
			}
			for i, item := range arr {
				if err := validateAvro(item, s["items"], defs, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
			return nil
		default:
			return fmt.Errorf("%s: unsupported schema type %q", path, typ)
		}
	default:
		return fmt.Errorf("%s: bad schema node %v", path, schema)
	}
}

func validateAvroNamed(v any, typ string, defs map[string]any, path string) error {
	var ok bool
	switch typ {
	case "string":
		_, ok = v.(string)
	case "long":
		f, isNum := v.(float64)
		ok = isNum && f == math.Trunc(f) && math.Abs(f) < 9.3e18
	case "int":
		f, isNum := v.(float64)
		ok = isNum && f == math.Trunc(f) && f >= -2147483648 && f <= 2147483647
	case "double", "float":
		_, ok = v.(float64)
	case "boolean":
		_, ok = v.(bool)
	case "null":
		ok = v == nil
	default: // named reference (record/enum defined elsewhere in the schema)
		ref, found := defs[typ]
		if !found {
			return fmt.Errorf("%s: unknown type %q", path, typ)
		}
		return validateAvro(v, ref, defs, path)
	}
	if !ok {
		return fmt.Errorf("%s: expected %s, got %v", path, typ, v)
	}
	return nil
}

// pinToAvro marshals event and validates the JSON against its .avsc contract.
func pinToAvro(t *testing.T, schemaName string, event any) {
	t.Helper()
	schema := loadAvsc(t, schemaName)
	defs := map[string]any{}
	collectAvroDefs(schema, defs)
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	if err := validateAvro(v, schema, defs, schemaName); err != nil {
		t.Fatalf("%s drift: %v\nevent: %s", schemaName, err, raw)
	}
}

// Consumed from the `trades` topic — the struct we json.Unmarshal into must
// match the producer's contract field-for-field.
func TestTradeExecutedMatchesAvro(t *testing.T) {
	pinToAvro(t, "TradeExecuted", TradeExecuted{
		EventID:          "e1",
		TradeID:          "t1",
		Symbol:           "H100:us-east-1",
		PriceCents:       500,
		Quantity:         10,
		AggressorSide:    AggressorBuy,
		MakerOrderID:     "m1",
		TakerOrderID:     "t2",
		MakerUserID:      "u1",
		TakerUserID:      "u2",
		OccurredAtUnixMs: 1,
	})
}

func TestSettlementFailedMatchesAvro(t *testing.T) {
	pinToAvro(t, "SettlementFailed", SettlementFailed{
		EventID:          "e2",
		TradeID:          "t1",
		Symbol:           "H100:us-east-1",
		BuyerID:          "u1",
		SellerID:         "u2",
		TotalCost:        5000,
		Reason:           "INSUFFICIENT_FUNDS",
		OccurredAtUnixMs: 2,
	})
}

// Emitted to `node-jobs` on every job lifecycle transition.
func TestJobEventMatchesAvro(t *testing.T) {
	pinToAvro(t, "JobEvent", JobEvent{
		EventID:          "e3",
		JobID:            "j1",
		TradeID:          "t1",
		NodeID:           "n1",
		Symbol:           "H100:us-east-1",
		BuyerID:          "u1",
		SellerID:         "u2",
		Status:           "ASSIGNED",
		OccurredAtUnixMs: 3,
	})
	pinToAvro(t, "JobEvent", JobEvent{
		EventID:          "e4",
		JobID:            "j1",
		TradeID:          "t1",
		NodeID:           "n1",
		Symbol:           "H100:us-east-1",
		BuyerID:          "u1",
		SellerID:         "u2",
		Status:           "FAILED",
		Reason:           "EXECUTION_ERROR",
		OccurredAtUnixMs: 4,
	})
}
