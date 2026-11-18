#!/usr/bin/env python3
"""Load the committed D4 onboarding/v1 candidate with D2's loader and translator.

Run this with the D2 worktree's Poetry environment via
scripts/check_d4_onboarding_v1.sh. That command fails when D2_ONBOARDING_ROOT
is absent. Normal D4 CI compares the golden fixture in Go and does not call
this script.
"""

from __future__ import annotations

import json
import sys
import tempfile
from pathlib import Path

import yaml

from ai_evals.onboarding.load import load_onboarding_tree
from ai_evals.onboarding.translate import materialized_json, translate

_METRIC_SET = """
identifier: quality
name: Quality
entries:
  - metric:
      identifier: exact-match
      name: Exact Match
      kind: exact_match
      type: heuristic
      dimension: correctness
    threshold: 1.0
    score_name: exact_match
"""

_EXPECTED_PRECOMPUTED_OUTPUT = {
    "scalar-input": 0,
    "list-input": ["one", 2, None],
    "object-input": {"output": "ready"},
    "null-input": None,
}


def main() -> None:
    if len(sys.argv) != 2:
        raise SystemExit("usage: check_d4_onboarding_v1.py <candidate.json>")
    candidate_path = Path(sys.argv[1])
    document = json.loads(candidate_path.read_text(encoding="utf-8"))
    dataset_cases = json.loads(
        candidate_path.with_name("onboarding_v1_dataset_cases.json").read_text(encoding="utf-8")
    )
    with tempfile.TemporaryDirectory() as tmp:
        root = Path(tmp)
        evals = root / ".harness" / "evals"
        agent_dir = evals / "agents" / "support"
        agent_dir.mkdir(parents=True)
        (evals / "datasets").mkdir()
        (evals / "metric-sets").mkdir()
        (agent_dir / "agent.yaml").write_text(yaml.safe_dump(document, sort_keys=False), encoding="utf-8")
        (evals / "datasets" / "answers.jsonl").write_text(
            "".join(json.dumps(row) + "\n" for row in dataset_cases),
            encoding="utf-8",
        )
        (evals / "metric-sets" / "quality.yaml").write_text(_METRIC_SET, encoding="utf-8")
        tree = load_onboarding_tree(evals)
        agent = tree.agents["support-agent"]
        if agent.schema_version != "onboarding/v1":
            raise SystemExit(f"schema_version {agent.schema_version!r}")
        if agent.candidate is None or agent.candidate.confidence != "high":
            raise SystemExit("candidate confidence was not preserved")
        evidence = agent.candidate.evidence[0]
        if evidence.source != "src/chat.py" or evidence.confidence != 0.9:
            raise SystemExit("evidence was not preserved")
        loaded_rows = agent.evals["answer-quality"].dataset.items
        if loaded_rows != dataset_cases:
            raise SystemExit(f"dataset rows changed in D2 loader: {loaded_rows!r}")
        translation = translate(tree, "support-agent", "local")
        items = translation.items_by_eval["answer-quality"]
        by_id = {item.id: item for item in items}
        if by_id["scalar-input"].input != "hello" or by_id["scalar-input"].expected is not None:
            raise SystemExit("scalar/null dataset values changed in D2 translator")
        if by_id["list-input"].input != [1, "two", False] or by_id["list-input"].expected != ["x", {"y": None}]:
            raise SystemExit("list dataset values changed in D2 translator")
        if by_id["object-input"].expected != {"answer": 42}:
            raise SystemExit("object expected value changed in D2 translator")
        if by_id["null-input"].input is not None:
            raise SystemExit("null input changed in D2 translator")
        for item_id, expected in _EXPECTED_PRECOMPUTED_OUTPUT.items():
            if by_id[item_id].precomputed_output != expected:
                raise SystemExit(f"{item_id} precomputed_output changed in D2 translator")
        managed_yaml = yaml.safe_load(translation.managed_eval_yaml["answer-quality"])
        serialized = materialized_json(translation)
        materialized = json.loads(serialized)
        round_tripped_yaml = yaml.safe_load(materialized["managed_eval_yaml"]["answer-quality"])
        for projection_name, projection in (
            ("managed single-eval YAML", managed_yaml),
            ("materialized managed single-eval YAML", round_tripped_yaml),
        ):
            managed_items = {item["id"]: item for item in projection["dataset"]["items"]}
            for item_id, expected in _EXPECTED_PRECOMPUTED_OUTPUT.items():
                item = managed_items.get(item_id)
                if item is None or "precomputed_output" not in item:
                    raise SystemExit(f"{projection_name} dropped {item_id} precomputed_output")
                if item["precomputed_output"] != expected:
                    raise SystemExit(f"{projection_name} changed {item_id} precomputed_output")
        nested = agent.request_template["nested"]["value"]
        if nested["flag"] is not True or nested["count"] != 2 or nested["empty"] is not None:
            raise SystemExit(f"nested literal was retyped: {nested!r}")
        if nested["items"] != [1, "task-name", False] or nested["child"]["ok"] is not True:
            raise SystemExit(f"nested collection was retyped: {nested!r}")
        if not isinstance(nested["child"], dict):
            raise SystemExit("nested child was stringified")
        plan = translate(tree, "support-agent", "local").request_plan.as_dict()
        rendered = plan["request_template"]["nested"]
        if rendered["flag"] is not True or rendered["count"] != 2 or rendered["empty"] is not None:
            raise SystemExit(f"translated literal was retyped: {rendered!r}")
        if rendered["deskLamp"] != "desk-lamp" or rendered["child"]["keyboard"] != "keys":
            raise SystemExit(f"ordinary literal was rejected: {rendered!r}")
        if plan["request_template"]["message"] != "{{input.items.0}}":
            raise SystemExit(f"input selector rendered as {plan['request_template']['message']!r}")
        if plan["query_params"]["q"] != "hello world" or plan["query_params"]["flag"] is not True:
            raise SystemExit(f"query literal was retyped: {plan['query_params']!r}")
        if plan["query_params"]["empty"] is not None or plan["query_params"]["region"] != "{{env.REGION}}":
            raise SystemExit(f"query env was retyped: {plan['query_params']!r}")
        if plan["auth_headers"]["Authorization"] != "{{env.SUPPORT_TOKEN}}":
            raise SystemExit("auth header did not stay a named secret reference")
        if plan["auth_headers"]["X-Request-Id"] != "{{env.REQUEST_ID}}":
            raise SystemExit("auth header did not stay an environment name")
        # desk-lamp stays in the plan. These sentinels are not substrings of it.
        for forbidden in ("super-secret", "pat.", "resolved-secret", "sk-abc"):
            if forbidden in serialized:
                raise SystemExit(f"serialized plan contained {forbidden!r}")
        if "SUPPORT_TOKEN" not in serialized or "account.supportToken" not in serialized:
            raise SystemExit("named secret reference was dropped")
    print("onboarding/v1 candidate loaded")


if __name__ == "__main__":
    main()
