from unittest.mock import patch

from harness_evals.catalog import catalog
from harness_evals.metrics import factory

catalog_by_kind = {entry.kind: entry for entry in catalog()}

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
                if entry is None:
                    raise ValueError("unknown metric kind " + metric.kind)
                factory.build_metric(
                    metric_type=metric.metric_type,
                    config=config,
                    score_name=metric.score_name,
                    threshold=metric.threshold if metric.threshold is not None else 0.0,
                    allow_code_loading=False,
                    dimension=metric.dimension,
                )