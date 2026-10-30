package taskruntime

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestModelsJSONFragmentRoundTripAndIsolation(t *testing.T) {
	scope, id, input := fixture(t)
	raw := json.RawMessage(`{"providers":{"example-provider":{"baseUrl":"https://models.example.test","models":[{"id":"example-model"}]}}}`)
	input.Configuration.ModelsJSON = raw
	snap, err := Capture(scope, id, input)
	if err != nil {
		t.Fatal(err)
	}
	raw[30] = 'X'
	if bytes.Equal(snap.Configuration().ModelsJSON, raw) {
		t.Fatal("capture aliases caller models.json")
	}
	encoded, err := snap.Encode()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	before := append([]byte(nil), restored.Configuration().ModelsJSON...)
	read := restored.Configuration()
	read.ModelsJSON[0] = 'X'
	if !bytes.Equal(before, restored.Configuration().ModelsJSON) {
		t.Fatal("configuration read aliases stored models.json")
	}
}

func TestModelsJSONFragmentRejectsWrongProvider(t *testing.T) {
	for name, raw := range map[string]string{
		"wrong provider":     `{"providers":{"other":{"baseUrl":"https://models.example.test"}}}`,
		"empty providers":    `{"providers":{}}`,
		"multiple providers": `{"providers":{"example-provider":{},"other":{}}}`,
		"extra root":         `{"providers":{"example-provider":{}},"other":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			scope, id, input := fixture(t)
			input.Configuration.ModelsJSON = json.RawMessage(raw)
			if _, err := Capture(scope, id, input); err == nil {
				t.Fatal("invalid provider fragment accepted")
			}
		})
	}
}
