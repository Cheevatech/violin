package credentials

import (
	"context"
	"errors"
	"testing"
)

func TestLookupPrefersEnvironment(t *testing.T) {
	called := false
	store := Store{Env: map[string]string{"TOKEN": "from-env"}, Keychain: func(context.Context, string) (string, error) {
		called = true
		return "from-keychain", nil
	}}
	value, err := store.Lookup(context.Background(), "TOKEN", "violin/test")
	if err != nil || value != "from-env" || called {
		t.Fatalf("value=%q err=%v keychain_called=%v", value, err, called)
	}
}

func TestLookupUsesKeychainWhenEnvironmentMissing(t *testing.T) {
	store := Store{Env: map[string]string{}, Keychain: func(context.Context, string) (string, error) {
		return "from-keychain", nil
	}}
	value, err := store.Lookup(context.Background(), "TOKEN", "violin/test")
	if err != nil || value != "from-keychain" {
		t.Fatalf("value=%q err=%v", value, err)
	}
}

func TestLookupDoesNotReturnSecretInMissingError(t *testing.T) {
	store := Store{Env: map[string]string{"TOKEN": ""}, Keychain: func(context.Context, string) (string, error) {
		return "", errors.New("missing")
	}}
	_, err := store.Lookup(context.Background(), "TOKEN", "violin/test")
	if err == nil || err.Error() != "credential not found: violin/test" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSetAndRemoveUseKeychainWithoutReadingEnvironment(t *testing.T) {
	var savedService, savedValue string
	removed := ""
	store := Store{
		SetKeychain: func(_ context.Context, service, value string) error {
			savedService, savedValue = service, value
			return nil
		},
		DeleteKeychain: func(_ context.Context, service string) error { removed = service; return nil },
	}
	if err := store.Set(context.Background(), "violin/qwen/api-key", "secret"); err != nil {
		t.Fatal(err)
	}
	if savedService != "violin/qwen/api-key" || savedValue != "secret" {
		t.Fatalf("service=%q value=%q", savedService, savedValue)
	}
	if err := store.Remove(context.Background(), savedService); err != nil || removed != savedService {
		t.Fatalf("removed=%q err=%v", removed, err)
	}
}
