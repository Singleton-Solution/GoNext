package customfields

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidate_HappyPath(t *testing.T) {
	t.Parallel()
	group := FieldGroup{
		Schema: json.RawMessage(`{
			"type": "object",
			"required": ["price"],
			"properties": {
				"price": { "type": "number" },
				"sku":   { "type": "string" }
			}
		}`),
	}
	values := json.RawMessage(`{"price": 12.99, "sku": "abc-123"}`)
	if err := Validate(group, values); err != nil {
		t.Errorf("expected pass, got: %v", err)
	}
}

func TestValidate_MissingRequired(t *testing.T) {
	t.Parallel()
	group := FieldGroup{
		Schema: json.RawMessage(`{"type":"object","required":["price"],"properties":{"price":{"type":"number"}}}`),
	}
	values := json.RawMessage(`{}`)
	err := Validate(group, values)
	if err == nil {
		t.Fatal("expected fail, got pass")
	}
	if !strings.Contains(err.Error(), "price") {
		t.Errorf("error should name the missing field: %v", err)
	}
}

func TestValidate_WrongType(t *testing.T) {
	t.Parallel()
	group := FieldGroup{
		Schema: json.RawMessage(`{"type":"object","properties":{"price":{"type":"number"}}}`),
	}
	values := json.RawMessage(`{"price": "not a number"}`)
	err := Validate(group, values)
	if err == nil {
		t.Fatal("expected fail")
	}
}

func TestValidate_AdditionalPropertiesFalse(t *testing.T) {
	t.Parallel()
	group := FieldGroup{
		Schema: json.RawMessage(`{
			"type": "object",
			"additionalProperties": false,
			"properties": {"price": {"type": "number"}}
		}`),
	}
	values := json.RawMessage(`{"price": 1, "extra": "field"}`)
	err := Validate(group, values)
	if err == nil {
		t.Fatal("expected fail for unknown field")
	}
}

func TestValidate_Enum(t *testing.T) {
	t.Parallel()
	group := FieldGroup{
		Schema: json.RawMessage(`{
			"type":"object",
			"properties":{"size":{"type":"string","enum":["small","medium","large"]}}
		}`),
	}
	if err := Validate(group, json.RawMessage(`{"size":"medium"}`)); err != nil {
		t.Errorf("expected pass for enum match: %v", err)
	}
	if err := Validate(group, json.RawMessage(`{"size":"xl"}`)); err == nil {
		t.Errorf("expected fail for enum miss")
	}
}

func TestValidate_Integer(t *testing.T) {
	t.Parallel()
	group := FieldGroup{
		Schema: json.RawMessage(`{"type":"object","properties":{"count":{"type":"integer"}}}`),
	}
	if err := Validate(group, json.RawMessage(`{"count": 42}`)); err != nil {
		t.Errorf("expected pass for whole number: %v", err)
	}
	if err := Validate(group, json.RawMessage(`{"count": 3.5}`)); err == nil {
		t.Errorf("expected fail for fractional integer")
	}
}
