from unittest.mock import patch

from harness_evals.metrics import factory
from jsonschema import Draft202012Validator

catalog_by_kind = {entry["kind"]: entry for entry in metric_catalog()}

# Construction validates options without reading provider credentials or making requests.
with patch.object(factory, "build_llm_provider", return_value=object()), patch.object(
    factory, "build_embedding_provider", return_value=object()
):
    for agent in tree.agents.values():
        for evaluation in agent.evals.values():
            for metric in evaluation.metric_set.entries:
                if metric.module_path:
                    raise ValueError("onboarding does not load metric modules")
                config = {**metric.config, "kind": metric.kind}
                entry = catalog_by_kind.get(metric.kind)
                if entry is not None:
                    if not entry["supported"]:
                        raise ValueError(entry["unsupported_reason"])
                    options = dict(config.get("options") or {})
                    for key in ("criteria", "rubric", "prompt_instructions", "allowed_topics", "role_description"):
                        if key in config:
                            options[key] = config[key]
                    if next(Draft202012Validator(entry["options_schema"]).iter_errors(options), None):
                        raise ValueError("invalid or missing constructor options for metric " + metric.kind)
                factory.build_metric(
                    metric_type=metric.metric_type,
                    config=config,
                    score_name=metric.score_name,
                    threshold=metric.threshold if metric.threshold is not None else 0.0,
                    allow_code_loading=False,
                    dimension=metric.dimension,
                )