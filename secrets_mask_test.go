package config_test

import (
	"testing"

	"go.dagstack.dev/config"
)

func TestIsSecretField_Suffix(t *testing.T) {
	cases := []string{"api_key", "db_password", "auth_token", "service_credentials"}
	for _, name := range cases {
		if !config.IsSecretField(name) {
			t.Errorf("IsSecretField(%q) = false, want true", name)
		}
	}
}

func TestIsSecretField_CaseInsensitive(t *testing.T) {
	for _, name := range []string{"APIKEY", "Password", "TOKEN"} {
		if !config.IsSecretField(name) {
			t.Errorf("IsSecretField(%q) = false, want true", name)
		}
	}
}

func TestIsSecretField_NonSecret(t *testing.T) {
	for _, name := range []string{"host", "port", "name", "pool_size", "url"} {
		if config.IsSecretField(name) {
			t.Errorf("IsSecretField(%q) = true, want false", name)
		}
	}
}

func TestMaskValue_ReplacesNonEmpty(t *testing.T) {
	got := config.MaskValue("api_key", "sk-abc123")
	if got != config.MaskedPlaceholder {
		t.Errorf("MaskValue = %v, want %v", got, config.MaskedPlaceholder)
	}
}

func TestMaskValue_PreservesEmpty(t *testing.T) {
	if got := config.MaskValue("api_key", ""); got != "" {
		t.Errorf("MaskValue empty = %v, want empty string", got)
	}
	if got := config.MaskValue("api_key", nil); got != nil {
		t.Errorf("MaskValue nil = %v, want nil", got)
	}
}

func TestMaskValue_NonSecretPassthrough(t *testing.T) {
	if got := config.MaskValue("host", "prod.example.com"); got != "prod.example.com" {
		t.Errorf("MaskValue non-secret modified: %v", got)
	}
}
