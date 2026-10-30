package modelconfig

import (
	"encoding/json"
	"errors"
	"regexp"
)

var environmentReference = regexp.MustCompile(`^\$[A-Za-z_][A-Za-z0-9_]*$`)

type invalidModelsJSON struct{ message string }

func (e *invalidModelsJSON) Error() string { return e.message }

func invalid(message string) error { return &invalidModelsJSON{message: message} }

func validationMessage(err error) (string, bool) {
	var target *invalidModelsJSON
	if errors.As(err, &target) {
		return target.message, true
	}
	return "", false
}

type headerFields struct {
	Headers map[string]json.RawMessage `json:"headers"`
}

type providerFields struct {
	APIKey         json.RawMessage            `json:"apiKey"`
	Headers        map[string]json.RawMessage `json:"headers"`
	Models         []headerFields             `json:"models"`
	ModelOverrides map[string]headerFields    `json:"modelOverrides"`
}

// validateModelsJSON rejects Pi's plaintext and command substitutions before
// user-controlled configuration can be persisted or returned to the editor.
func validateModelsJSON(text string) error {
	if !json.Valid([]byte(text)) {
		return invalid("models.json 必须是合法 JSON")
	}
	var document struct {
		Providers map[string]providerFields `json:"providers"`
	}
	if err := json.Unmarshal([]byte(text), &document); err != nil {
		return invalid("models.json 的 Provider 结构无效")
	}
	for _, provider := range document.Providers {
		if err := validateEnvironmentReference(provider.APIKey); err != nil {
			return err
		}
		if err := validateHeaders(provider.Headers); err != nil {
			return err
		}
		for _, model := range provider.Models {
			if err := validateHeaders(model.Headers); err != nil {
				return err
			}
		}
		for _, override := range provider.ModelOverrides {
			if err := validateHeaders(override.Headers); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateHeaders(headers map[string]json.RawMessage) error {
	for _, value := range headers {
		if err := validateEnvironmentReference(value); err != nil {
			return err
		}
	}
	return nil
}

func validateEnvironmentReference(raw json.RawMessage) error {
	if raw == nil {
		return nil
	}
	var value string
	if json.Unmarshal(raw, &value) != nil || !environmentReference.MatchString(value) {
		return invalid("apiKey 和 headers 只接受 $NAME 形式的环境变量引用，不接受明文或命令")
	}
	return nil
}
