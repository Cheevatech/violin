package laya

import "testing"

func TestFallbackEngineReturnsTypedAnswers(t *testing.T) {
	result, err := (FallbackEngine{ModelVersion: "fallback"}).Evaluate(Request{Questions: []Question{{ID: "backend", Kind: Choice, Options: []string{"agy", "qwen"}, Fallback: "qwen"}, {ID: "safe", Kind: Noul}}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Fallback || len(result.Answers) != 2 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.Answers[0].Value != "qwen" {
		t.Fatalf("unexpected choice: %+v", result.Answers[0])
	}
}

func TestFallbackEngineRejectsNonEnglishProtocol(t *testing.T) {
	_, err := (FallbackEngine{}).Evaluate(Request{Language: "th", Questions: []Question{{ID: "backend", Kind: Choice, Options: []string{"qwen"}}}})
	if err == nil {
		t.Fatal("expected non-English protocol rejection")
	}
}
