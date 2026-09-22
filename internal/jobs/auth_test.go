package jobs

import (
	"strings"
	"testing"

	"github.com/film/violin/internal/config"
)

func TestCheckAuthReturnsStructuredRequirementForConfiguredAPI(t *testing.T) {
	err := checkAuth("qwen", config.Backend{Transport: "api", API: config.API{BaseURL: "https://example.test"}})
	required, ok := err.(AuthRequiredError)
	if !ok || required.Backend != "qwen" || required.Transport != "api" {
		t.Fatalf("error=%T %+v", err, err)
	}
	if required.Details()["status"] != "auth_required" {
		t.Fatalf("details=%v", required.Details())
	}
}

func TestCheckAuthLeavesCustomWorkerAuthenticationAlone(t *testing.T) {
	if err := checkAuth("qwen", config.Backend{Transport: "api", Command: []string{"custom-worker"}}); err != nil {
		t.Fatal(err)
	}
}

func TestAuthRequiredErrorDoesNotContainCredential(t *testing.T) {
	err := AuthRequiredError{Backend: "qwen", Transport: "api", Message: "configure API key"}
	if strings.Contains(err.Error(), "key=") {
		t.Fatal("credential leaked")
	}
}
