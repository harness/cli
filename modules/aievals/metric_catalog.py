import inspect
import json
import sys
from typing import get_type_hints

from ai_evals.runner.common import execution_scope_for_metric_kind
from harness_evals import catalog
from harness_evals.metrics import factory


def options_schema(metric_class):
    properties = {}
    required = {
        name for name, param in inspect.signature(metric_class).parameters.items()
        if name not in {"llm", "embedding", "provider", "threshold"}
        and param.default is inspect.Parameter.empty
        and param.kind in (param.POSITIONAL_OR_KEYWORD, param.KEYWORD_ONLY)
    }
    runtime_only = set(getattr(metric_class, "factory_reserved_options", ()))
    for klass in metric_class.__mro__:
        if klass.__module__ == "harness_evals.core.metric":
            continue
        init = klass.__dict__.get("__init__")
        if init is None:
            continue
        try:
            annotations = get_type_hints(init)
        except (NameError, TypeError):
            annotations = {}
        for name, param in inspect.signature(init).parameters.items():
            if name in {"self", "llm", "embedding", "provider", "threshold"} | runtime_only:
                continue
            if param.kind not in (param.POSITIONAL_OR_KEYWORD, param.KEYWORD_ONLY):
                continue
            if name in properties:
                continue
            prop = factory._infer_json_schema_type(param.default, annotations.get(name, param.annotation))
            if param.default is not inspect.Parameter.empty and param.default is not None:
                try:
                    json.dumps(param.default)
                    prop["default"] = param.default
                except TypeError:
                    pass
            properties.setdefault(name, prop)
    return {"type": "object", "properties": properties, "required": sorted(required), "additionalProperties": False}


def has_json_shape(schema):
    if "type" not in schema and "anyOf" not in schema:
        return False
    if schema.get("type") == "array" and not has_json_shape(schema.get("items", {})):
        return False
    return all(has_json_shape(part) for part in schema.get("anyOf", []))


def metric_catalog():
    registries = {
        "heuristic": factory._heuristic_registry(),
        "llm": factory._llm_metric_registry(),
        "embedding": factory._embedding_registry(),
    }
    entries = []
    for item in catalog():
        metric_type = next((key for key, registry in registries.items() if item.kind in registry), None)
        reason = ""
        if item.requires_provider:
            reason = "Requires a decision provider; the onboarding schema does not support decision metrics."
        elif metric_type is None:
            reason = "Not available through the onboarding runner's heuristic, LLM, or embedding factory."
        runtime_only = set(getattr(item.metric_class, "factory_reserved_options", ()))
        required_runtime = [
            name for name, param in inspect.signature(item.metric_class).parameters.items()
            if name in runtime_only and param.default is inspect.Parameter.empty
        ]
        if required_runtime:
            reason = "Requires runtime-only objects: " + ", ".join(required_runtime)
        schema = options_schema(item.metric_class)
        non_json = [name for name in schema["required"] if not has_json_shape(schema["properties"].get(name, {}))]
        if non_json and not reason:
            reason = "Requires constructor objects without a persisted JSON representation: " + ", ".join(non_json)
        entries.append({
            "kind": item.kind,
            "name": item.name,
            "dimension": getattr(item.dimension, "value", item.dimension),
            "category": item.category,
            "default_threshold": item.default_threshold,
            "requires_llm": item.requires_llm,
            "requires_embedding": item.requires_embedding,
            "requires_provider": item.requires_provider,
            "description": inspect.getdoc(item.metric_class) or item.description,
            "implementation": item.metric_class.__module__ + "." + item.metric_class.__qualname__,
            "execution_scope": execution_scope_for_metric_kind(item.kind).value,
            "options_schema": schema,
            "type": metric_type or "unsupported",
            "supported": not reason,
            "unsupported_reason": reason,
        })
    return entries


if len(sys.argv) == 1:
    print(json.dumps(metric_catalog()))