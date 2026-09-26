"""Private JSON-lines bridge from Violin to the upstream Laya SDK."""
import json
import os
import sys

from laya import Router
from laya.lang import analyse
import torch

revision = os.environ.get("VIOLIN_LAYA_CHECKPOINT_REVISION", "unknown")
device = "mps" if hasattr(torch.backends, "mps") and torch.backends.mps.is_available() else ("cuda" if torch.cuda.is_available() else "cpu")
router = Router(max_loaded=3, revision=revision, device=device)

def handle(request):
    state = request.get("state") or {}
    task = state.get("task", "") if isinstance(state, dict) else str(state)
    questions = {}
    descriptions = {
        "backend": {"agy": "Gemini AGY for broad coding and analysis", "qwen": "Qwen coding agent", "claude": "Claude coding agent"},
        "task_mode": {"inspect": "read-only inspection, explanation, review, or diagnosis", "implement": "create or modify code or files"},
        "risk": {"low": "routine, reversible work", "medium": "meaningful uncertainty or operational impact", "high": "security, credentials, destructive changes, or high consequence"},
        "timeout_policy": {"short": "small bounded task", "standard": "ordinary task", "long": "large or multi-step task"},
        "retry_policy": {"no_retry": "one attempt only", "retry_once": "one additional attempt for a bounded inspection"},
    }
    for q in request.get("questions", []):
        options = q.get("options", [])
        if not options:
            continue
        questions[q["id"]] = {
            "type": "choice",
            "instructions": q.get("prompt") or q["id"],
            "criteria": {value: descriptions.get(q["id"], {}).get(value, value) for value in options},
        }
    model = "typed-decisions" if analyse(task).get("is_english", False) else None
    prediction = router.predict({"task": task, "request": task}, questions, model=model)
    answers = []
    for q in request.get("questions", []):
        raw = prediction.get("answers", {}).get(q["id"], {})
        value = raw.get("choice")
        answers.append({"id": q["id"], "kind": q.get("kind", "choice"),
                        "value": value, "probabilities": raw.get("probabilities", {}),
                        "confidence": raw.get("confidence", 0), "fallback": False})
    # Decision projections are advisory. These typed answer probabilities are
    # derived from the actual per-option distribution, never model prose.
    by_id = {a["id"]: a for a in answers}
    selected = by_id.get("backend")
    decision = None
    if selected:
        probabilities = selected["probabilities"] or {}
        ordered = sorted(probabilities.items(), key=lambda item: item[1], reverse=True)
        conf = ordered[0][1] if ordered else 0.0
        margin = conf - (ordered[1][1] if len(ordered) > 1 else 0.0)
        def picked(key, fallback):
            return by_id.get(key, {}).get("value") or fallback
        mode = picked("task_mode", "inspect")
        risk = picked("risk", "low")
        timeout_class = picked("timeout_policy", "standard")
        retry = picked("retry_policy", "no_retry")
        timeout = {"short": 300, "standard": 900, "long": 3600}.get(timeout_class, 900)
        decision = {"backend_candidates": [item[0] for item in ordered] or [selected["value"]],
                    "task_mode": mode, "risk": risk, "timeout_hint_seconds": timeout,
                    "idle_timeout_enabled": True, "retry_hint": {"max_attempts": 2 if retry == "retry_once" and mode == "inspect" else 1},
                    "execution_target": "external", "cost_tier": "medium", "latency_tier": "medium",
                    "confidence": conf, "margin": margin,
                    "head_confidence": {key: (max(a["probabilities"].values(), default=0.0)) for key, a in by_id.items()},
                    "head_margin": {key: (sorted(a["probabilities"].values(), reverse=True)[0] - (sorted(a["probabilities"].values(), reverse=True)[1] if len(a["probabilities"]) > 1 else 0.0)) for key, a in by_id.items()},
                    "reason_codes": ["upstream_typed_choice"], "model_version": "laya-" + revision,
                    "fallback": False}
    return {"answers": answers, "decision": decision, "model_version": "laya-" + revision,
            "latency_ms": 0, "fallback": False}

for line in sys.stdin:
    try:
        print(json.dumps(handle(json.loads(line)), ensure_ascii=False), flush=True)
    except Exception as exc:
        print(json.dumps({"answers": [], "model_version": "laya-" + revision,
                          "fallback": True, "error": str(exc)}), flush=True)
